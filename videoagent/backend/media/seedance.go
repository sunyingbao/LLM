package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SeedanceConfig struct {
	BaseURL    string `json:"base_url"`
	APIKey     string `json:"api_key"`
	Model      string `json:"model"`
	Ratio      string `json:"ratio"`
	Resolution string `json:"resolution"`
	Duration   int    `json:"duration"`
}

type SeedanceClient struct {
	config         SeedanceConfig
	client         *http.Client
	importer       VideoImporter
	mediaResolvers MediaURLResolvers
}

type MediaURLResolvers struct {
	Image MediaURLResolver
	Audio MediaURLResolver
	Video MediaURLResolver
}

func NewSeedanceClient(config SeedanceConfig, client *http.Client) (*SeedanceClient, error) {
	return NewSeedanceClientWithImporter(config, client, nil)
}

func NewSeedanceClientWithImporter(config SeedanceConfig, client *http.Client, importer VideoImporter) (*SeedanceClient, error) {
	return NewSeedanceClientWithMediaResolvers(config, client, importer, MediaURLResolvers{})
}

func NewSeedanceClientWithMediaResolver(config SeedanceConfig, client *http.Client, importer VideoImporter, mediaResolver MediaURLResolver) (*SeedanceClient, error) {
	return NewSeedanceClientWithMediaResolvers(config, client, importer, MediaURLResolvers{
		Image: mediaResolver, Audio: mediaResolver, Video: mediaResolver,
	})
}

func NewSeedanceClientWithMediaResolvers(config SeedanceConfig, client *http.Client, importer VideoImporter, mediaResolvers MediaURLResolvers) (*SeedanceClient, error) {
	if strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("seedance base_url, api_key and model are required")
	}
	if _, err := url.ParseRequestURI(config.BaseURL); err != nil {
		return nil, err
	}
	if config.Ratio == "" {
		config.Ratio = "9:16"
	}
	if config.Resolution == "" {
		config.Resolution = "720p"
	}
	if config.Duration == 0 {
		config.Duration = 5
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &SeedanceClient{config: config, client: client, importer: importer, mediaResolvers: mediaResolvers}, nil
}

func (client *SeedanceClient) SubmitPreview(ctx context.Context, request VideoRequest) (SubmittedJob, error) {
	content, err := client.seedanceContent(ctx, request)
	if err != nil {
		return SubmittedJob{}, err
	}
	body := seedanceSubmitRequest{
		Model:                 firstNonEmpty(request.Model, client.config.Model),
		Content:               content,
		ExecutionExpiresAfter: 3600,
		Duration:              firstPositive(request.Duration, client.config.Duration),
		Ratio:                 firstNonEmpty(request.AspectRatio, client.config.Ratio),
		Resolution:            client.config.Resolution,
	}
	var response struct {
		ID    string         `json:"id"`
		Error *seedanceError `json:"error"`
	}
	if err := client.call(ctx, http.MethodPost, "", body, &response); err != nil {
		return SubmittedJob{}, err
	}
	if response.Error != nil {
		return SubmittedJob{}, fmt.Errorf("seedance submit failed: %s: %s", response.Error.Code, response.Error.Message)
	}
	if response.ID == "" {
		return SubmittedJob{}, fmt.Errorf("seedance submit returned an empty task id")
	}
	return SubmittedJob{Provider: "seedance", JobID: response.ID}, nil
}

func (client *SeedanceClient) GetPreview(ctx context.Context, jobID string) (JobStatus, error) {
	var response seedanceStatusResponse
	if err := client.call(ctx, http.MethodGet, "/"+url.PathEscape(jobID), nil, &response); err != nil {
		return JobStatus{}, err
	}
	switch response.Status {
	case "queued", "running":
		return JobStatus{State: JobPending}, nil
	case "succeeded":
		if response.Content == nil || response.Content.VideoURL == "" {
			return JobStatus{}, fmt.Errorf("seedance task succeeded without video_url")
		}
		if client.importer == nil {
			return JobStatus{State: JobSucceeded, URL: response.Content.VideoURL}, nil
		}
		video, err := client.importer.ImportVideo(ctx, jobID, response.Content.VideoURL)
		if err != nil {
			return JobStatus{}, err
		}
		return JobStatus{State: JobSucceeded, URI: video.URI, URL: video.URL}, nil
	case "failed", "cancelled", "expired":
		message := response.Status
		if response.Error != nil && response.Error.Message != "" {
			message = response.Error.Message
		}
		return JobStatus{State: JobFailed, Message: message}, nil
	default:
		return JobStatus{}, fmt.Errorf("unknown seedance task status: %s", response.Status)
	}
}

func (*SeedanceClient) FindPreviewBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, ErrSubmitReconciliationUnsupported
}

func (client *SeedanceClient) call(ctx context.Context, method, suffix string, body, output any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(client.config.BaseURL, "/")+suffix, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.config.APIKey)
	response, err := client.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("seedance returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(response.Body).Decode(output)
}

func (client *SeedanceClient) seedanceContent(ctx context.Context, request VideoRequest) ([]seedanceContentItem, error) {
	content := []seedanceContentItem{{Type: "text", Text: videoPrompt(request)}}
	seen := make(map[string]bool)
	for index, imageURL := range request.ImageURLs {
		imageURL, err := resolveMediaURL(ctx, imageURL, client.mediaResolvers.Image)
		if err != nil {
			return nil, err
		}
		if imageURL == "" || seen[imageURL] {
			continue
		}
		seen[imageURL] = true
		role := "reference_image"
		if request.Strategy == PreviewStrategyI2V && index == 0 {
			role = "first_frame"
		}
		content = append(content, seedanceContentItem{Type: "image_url", ImageURL: &seedanceMediaURL{URL: imageURL}, Role: role})
	}
	for _, audioURL := range request.AudioURLs {
		audioURL, err := resolveMediaURL(ctx, audioURL, client.mediaResolvers.Audio)
		if err != nil {
			return nil, err
		}
		if audioURL == "" || seen[audioURL] {
			continue
		}
		seen[audioURL] = true
		content = append(content, seedanceContentItem{Type: "audio_url", AudioURL: &seedanceMediaURL{URL: audioURL}, Role: "reference_audio"})
	}
	for _, videoURL := range request.VideoURLs {
		videoURL, err := resolveMediaURL(ctx, videoURL, client.mediaResolvers.Video)
		if err != nil {
			return nil, err
		}
		if videoURL == "" || seen[videoURL] {
			continue
		}
		seen[videoURL] = true
		content = append(content, seedanceContentItem{Type: "video_url", VideoURL: &seedanceMediaURL{URL: videoURL}, Role: "reference_video"})
	}
	for _, artifact := range request.Inputs {
		var resolver MediaURLResolver
		switch artifact.Kind {
		case "competition_reference_image", "character_reference_image":
			resolver = client.mediaResolvers.Image
		case "voice_preview":
			resolver = client.mediaResolvers.Audio
		default:
			continue
		}
		mediaURL, err := resolveMediaURL(ctx, artifactMediaURL(artifact), resolver)
		if err != nil {
			return nil, err
		}
		if mediaURL == "" || seen[mediaURL] {
			continue
		}
		seen[mediaURL] = true
		switch artifact.Kind {
		case "competition_reference_image", "character_reference_image":
			content = append(content, seedanceContentItem{Type: "image_url", ImageURL: &seedanceMediaURL{URL: mediaURL}, Role: "reference_image"})
		case "voice_preview":
			content = append(content, seedanceContentItem{Type: "audio_url", AudioURL: &seedanceMediaURL{URL: mediaURL}, Role: "reference_audio"})
		}
	}
	return content, nil
}

func resolveMediaURL(ctx context.Context, reference string, resolver MediaURLResolver) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", nil
	}
	parsed, err := url.Parse(reference)
	if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		return reference, nil
	}
	if resolver == nil {
		return "", fmt.Errorf("seedance media is not publicly accessible: %s", reference)
	}
	resolved, err := resolver.ResolveURL(ctx, reference)
	if err != nil {
		return "", err
	}
	parsed, err = url.Parse(resolved)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("media resolver returned an invalid url for %s", reference)
	}
	return resolved, nil
}

func videoPrompt(request VideoRequest) string {
	if strings.TrimSpace(request.Prompt) != "" {
		return request.Prompt
	}
	if request.ClipScript == nil {
		return ""
	}
	parts := make([]string, 0, len(request.ClipScript.Scenes))
	for _, scene := range request.ClipScript.Scenes {
		if scene.Visual != "" {
			parts = append(parts, scene.Visual)
		}
	}
	return strings.Join(parts, "\n")
}

func artifactMediaURL(artifact Artifact) string {
	return firstArtifactValue(artifact, "url", "preview_audio_url", "audio_url")
}

func firstPositive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

type seedanceSubmitRequest struct {
	Model                 string                `json:"model"`
	Content               []seedanceContentItem `json:"content"`
	ExecutionExpiresAfter int                   `json:"execution_expires_after,omitempty"`
	Duration              int                   `json:"duration,omitempty"`
	Ratio                 string                `json:"ratio,omitempty"`
	Resolution            string                `json:"resolution,omitempty"`
}

type seedanceContentItem struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	ImageURL *seedanceMediaURL `json:"image_url,omitempty"`
	AudioURL *seedanceMediaURL `json:"audio_url,omitempty"`
	VideoURL *seedanceMediaURL `json:"video_url,omitempty"`
	Role     string            `json:"role,omitempty"`
}

type seedanceMediaURL struct {
	URL string `json:"url"`
}

type seedanceError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type seedanceStatusResponse struct {
	Status  string `json:"status"`
	Content *struct {
		VideoURL string `json:"video_url"`
	} `json:"content,omitempty"`
	Error *seedanceError `json:"error,omitempty"`
}

var _ PreviewClient = (*SeedanceClient)(nil)
