package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"eino-cli/videoagent/backend/model"
	"eino-cli/videoagent/backend/planning"
)

type RemoteConfig struct {
	BaseURL        string                `json:"base_url"`
	APIKey         string                `json:"api_key"`
	CallbackSecret string                `json:"callback_secret"`
	Endpoints      map[string]string     `json:"endpoints"`
	Seedance       *SeedanceConfig       `json:"seedance,omitempty"`
	PromptTTS      *PromptTTSConfig      `json:"prompt_tts,omitempty"`
	ImageArk       *ArkImageConfig       `json:"image_ark,omitempty"`
	ClipMix        *ClipMixConfig        `json:"clipmix,omitempty"`
	VideoStorage   *VideoStorageConfig   `json:"video_storage,omitempty"`
	FinalVideo     *MetaFinalVideoConfig `json:"finalvideo,omitempty"`
	MediaURL       *MediaURLConfig       `json:"media_url,omitempty"`
	ImageMediaURL  *MediaURLConfig       `json:"image_media_url,omitempty"`
	AudioMediaURL  *MediaURLConfig       `json:"audio_media_url,omitempty"`
	VideoMediaURL  *MediaURLConfig       `json:"video_media_url,omitempty"`
}

// NewRemoteClients creates production adapters without depending on Mega workflow APIs.
func NewRemoteClients(config RemoteConfig, videoImportCache VideoImportCache) (Clients, error) {
	if err := validateRemoteConfig(config); err != nil {
		return Clients{}, err
	}
	if config.Seedance != nil && config.FinalVideo != nil && config.VideoStorage == nil {
		return Clients{}, fmt.Errorf("video storage is required between seedance preview and meta finalvideo")
	}
	httpClient := &http.Client{Timeout: 2 * time.Minute}
	transport := newRemoteTransport(config, httpClient)
	clients := remoteFallbackClients(transport, config.Endpoints)
	var preview PreviewClient
	var finalVideo FinalVideoClient
	if clients.Video != nil {
		preview = clients.Video
		finalVideo = clients.Video
	}
	mediaResolvers, err := buildMediaURLResolvers(config)
	if err != nil {
		return Clients{}, err
	}
	if config.Seedance != nil {
		var importer VideoImporter
		if config.VideoStorage != nil {
			uploader, err := NewBytedanceVideoUploader(*config.VideoStorage)
			if err != nil {
				return Clients{}, err
			}
			importer, err = NewHTTPVideoImporter(uploader, nil, config.VideoStorage.MaxBytes, videoImportCache)
			if err != nil {
				return Clients{}, err
			}
		}
		preview, err = NewSeedanceClientWithMediaResolvers(*config.Seedance, httpClient, importer, mediaResolvers)
		if err != nil {
			return Clients{}, err
		}
	}
	if config.FinalVideo != nil {
		renderer, err := NewBytedanceVideoRenderer(config.FinalVideo.BizID)
		if err != nil {
			return Clients{}, err
		}
		finalVideo, err = NewMetaFinalVideoClient(*config.FinalVideo, renderer)
		if err != nil {
			return Clients{}, err
		}
	}
	if preview != nil || finalVideo != nil {
		clients.Video, err = CombineVideoClients(preview, finalVideo)
		if err != nil {
			return Clients{}, err
		}
	}
	if config.PromptTTS != nil {
		matx, err := NewBytedanceMatxClient()
		if err != nil {
			return Clients{}, err
		}
		var importer AudioImporter
		if config.PromptTTS.Storage != nil {
			uploader, err := NewBytedanceAudioUploader(*config.PromptTTS.Storage)
			if err != nil {
				return Clients{}, err
			}
			importer, err = NewHTTPAudioImporter(uploader, nil, config.PromptTTS.Storage.MaxBytes)
			if err != nil {
				return Clients{}, err
			}
		}
		clients.TTS, err = NewPromptTTSClientWithImporter(*config.PromptTTS, matx, importer)
		if err != nil {
			return Clients{}, err
		}
	}
	if config.ImageArk != nil {
		clients.Image, err = NewArkImageClient(*config.ImageArk)
		if err != nil {
			return Clients{}, err
		}
	}
	if config.ClipMix != nil {
		gateway, gatewayErr := model.NewBytedanceModelGateway()
		if gatewayErr != nil {
			return Clients{}, gatewayErr
		}
		clients.PreviewPlanner, err = planning.NewClipMixPlanner(gateway, config.ClipMix.Model)
		if err != nil {
			return Clients{}, err
		}
	}
	clients.Image = withImageURL(clients.Image, mediaResolvers.Image)
	return clients, nil
}

// ValidateCanvasRemoteConfig checks that a remote Canvas run can execute every built-in media node.
func ValidateCanvasRemoteConfig(config RemoteConfig) error {
	if err := validateRemoteConfig(config); err != nil {
		return err
	}
	missing := make([]string, 0, 6)
	if config.ImageArk == nil && !hasEndpoints(config.Endpoints, "image_submit", "image_status", "image_find") {
		missing = append(missing, "image")
	}
	if config.PromptTTS == nil && !hasEndpoints(config.Endpoints, "tts_submit", "tts_status", "tts_find") {
		missing = append(missing, "tts")
	}
	if config.Seedance == nil && !hasEndpoints(config.Endpoints, "preview_submit", "preview_status", "preview_find") {
		missing = append(missing, "preview")
	}
	if config.FinalVideo == nil && !hasEndpoints(config.Endpoints, "finalvideo_submit", "finalvideo_status", "finalvideo_find") {
		missing = append(missing, "finalvideo")
	}
	if strings.TrimSpace(config.Endpoints["image_audit"]) == "" {
		missing = append(missing, "image_audit")
	}
	if strings.TrimSpace(config.Endpoints["prompt_shield"]) == "" {
		missing = append(missing, "prompt_shield")
	}
	if len(missing) > 0 {
		return fmt.Errorf("remote canvas config is incomplete: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func validateRemoteConfig(config RemoteConfig) error {
	payload, err := json.Marshal(config)
	if err != nil {
		return err
	}
	value := strings.ToLower(string(payload))
	if strings.Contains(value, "replace-with-") || strings.Contains(value, ".example.com") {
		return fmt.Errorf("remote config contains placeholder values")
	}
	if len(config.Endpoints) > 0 && invalidRemoteCredential(config.CallbackSecret) {
		return fmt.Errorf("callback_secret is required when remote endpoints are configured")
	}
	return nil
}

func invalidRemoteCredential(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "" || strings.Contains(value, "replace-with-")
}

func buildMediaURLResolvers(config RemoteConfig) (MediaURLResolvers, error) {
	resolvers := MediaURLResolvers{}
	for _, item := range []struct {
		config   *MediaURLConfig
		fallback *MediaURLConfig
		target   *MediaURLResolver
	}{
		{config.ImageMediaURL, config.MediaURL, &resolvers.Image},
		{config.AudioMediaURL, config.MediaURL, &resolvers.Audio},
		{config.VideoMediaURL, config.MediaURL, &resolvers.Video},
	} {
		selected := item.config
		if selected == nil {
			selected = item.fallback
		}
		if selected == nil {
			continue
		}
		resolver, err := NewBytedanceMediaURLResolver(*selected)
		if err != nil {
			return MediaURLResolvers{}, err
		}
		*item.target = resolver
	}
	return resolvers, nil
}

func newRemoteTransport(config RemoteConfig, client *http.Client) *remoteTransport {
	if strings.TrimSpace(config.BaseURL) == "" && len(config.Endpoints) == 0 {
		return nil
	}
	return &remoteTransport{
		baseURL:   strings.TrimRight(config.BaseURL, "/"),
		apiKey:    config.APIKey,
		endpoints: config.Endpoints,
		client:    client,
	}
}

func remoteFallbackClients(transport *remoteTransport, endpoints map[string]string) Clients {
	if transport == nil {
		return Clients{}
	}
	var clients Clients
	if hasEndpoints(endpoints, "requirement", "clipscript", "competition_plan", "tts_plan", "character_plan") {
		clients.Planner = transport
	}
	if hasEndpoints(endpoints, "image_submit", "image_status", "image_find") {
		clients.Image = transport
	}
	if hasEndpoints(endpoints, "tts_submit", "tts_status", "tts_find") {
		clients.TTS = transport
	}
	if hasEndpoints(endpoints, "preview_submit", "preview_status", "preview_find", "finalvideo_submit", "finalvideo_status", "finalvideo_find") {
		clients.Video = transport
	}
	if hasEndpoints(endpoints, "image_audit") {
		clients.Audit = transport
	}
	if hasEndpoints(endpoints, "prompt_shield") {
		clients.Shield = transport
	}
	return clients
}

func hasEndpoints(endpoints map[string]string, names ...string) bool {
	for _, name := range names {
		if strings.TrimSpace(endpoints[name]) == "" {
			return false
		}
	}
	return true
}

type remoteTransport struct {
	baseURL   string
	apiKey    string
	endpoints map[string]string
	client    *http.Client
}

func (remote *remoteTransport) AnalyzeRequirement(ctx context.Context, input RunInput) (Requirement, error) {
	return callRemote[Requirement](ctx, remote, "requirement", input)
}

func (remote *remoteTransport) CreateClipScript(ctx context.Context, requirement Requirement, input RunInput) (ClipScript, error) {
	return callRemote[ClipScript](ctx, remote, "clipscript", struct {
		Requirement Requirement `json:"requirement"`
		Input       RunInput    `json:"input"`
	}{requirement, input})
}

func (remote *remoteTransport) PlanCompetition(ctx context.Context, input ClipScript, runInput RunInput) ([]ResourcePlan, error) {
	return callRemote[[]ResourcePlan](ctx, remote, "competition_plan", struct {
		ClipScript ClipScript `json:"clipscript"`
		Input      RunInput   `json:"input"`
	}{input, runInput})
}

func (remote *remoteTransport) PlanTTS(ctx context.Context, input ClipScript) ([]ResourcePlan, error) {
	return callRemote[[]ResourcePlan](ctx, remote, "tts_plan", input)
}

func (remote *remoteTransport) PlanCharacterReferences(ctx context.Context, input ClipScript, runInput RunInput) ([]ResourcePlan, error) {
	return callRemote[[]ResourcePlan](ctx, remote, "character_plan", struct {
		ClipScript ClipScript `json:"clipscript"`
		Input      RunInput   `json:"input"`
	}{input, runInput})
}

func (remote *remoteTransport) SubmitImage(ctx context.Context, input ImageRequest) (SubmittedJob, error) {
	return callRemote[SubmittedJob](ctx, remote, "image_submit", input)
}

func (remote *remoteTransport) GetImage(ctx context.Context, jobID string) (JobStatus, error) {
	return callRemote[JobStatus](ctx, remote, "image_status", map[string]string{"job_id": jobID})
}

func (remote *remoteTransport) CancelImage(ctx context.Context, jobID string) error {
	return remote.call(ctx, "image_cancel", map[string]string{"job_id": jobID}, nil)
}

func (remote *remoteTransport) FindImageBySubmitKey(ctx context.Context, key string) (SubmittedJob, bool, error) {
	return remote.find(ctx, "image_find", key)
}

func (remote *remoteTransport) SubmitTTS(ctx context.Context, input TTSRequest) (SubmittedJob, error) {
	return callRemote[SubmittedJob](ctx, remote, "tts_submit", input)
}

func (remote *remoteTransport) GetTTS(ctx context.Context, jobID string) (JobStatus, error) {
	return callRemote[JobStatus](ctx, remote, "tts_status", map[string]string{"job_id": jobID})
}

func (remote *remoteTransport) CancelTTS(ctx context.Context, jobID string) error {
	return remote.call(ctx, "tts_cancel", map[string]string{"job_id": jobID}, nil)
}

func (remote *remoteTransport) FindTTSBySubmitKey(ctx context.Context, key string) (SubmittedJob, bool, error) {
	return remote.find(ctx, "tts_find", key)
}

func (remote *remoteTransport) SubmitPreview(ctx context.Context, input VideoRequest) (SubmittedJob, error) {
	return callRemote[SubmittedJob](ctx, remote, "preview_submit", input)
}

func (remote *remoteTransport) GetPreview(ctx context.Context, jobID string) (JobStatus, error) {
	return callRemote[JobStatus](ctx, remote, "preview_status", map[string]string{"job_id": jobID})
}

func (remote *remoteTransport) CancelVideo(ctx context.Context, jobID string) error {
	return remote.call(ctx, "video_cancel", map[string]string{"job_id": jobID}, nil)
}

func (remote *remoteTransport) FindPreviewBySubmitKey(ctx context.Context, key string) (SubmittedJob, bool, error) {
	return remote.find(ctx, "preview_find", key)
}

func (remote *remoteTransport) SubmitFinalVideo(ctx context.Context, input VideoRequest) (SubmittedJob, error) {
	return callRemote[SubmittedJob](ctx, remote, "finalvideo_submit", input)
}

func (remote *remoteTransport) GetFinalVideo(ctx context.Context, jobID string) (JobStatus, error) {
	return callRemote[JobStatus](ctx, remote, "finalvideo_status", map[string]string{"job_id": jobID})
}

func (remote *remoteTransport) FindFinalVideoBySubmitKey(ctx context.Context, key string) (SubmittedJob, bool, error) {
	return remote.find(ctx, "finalvideo_find", key)
}

func (remote *remoteTransport) CheckImage(ctx context.Context, uri string) error {
	return remote.checkModeration(ctx, "image_audit", map[string]string{"uri": uri}, true)
}

func (remote *remoteTransport) CheckPrompt(ctx context.Context, prompt string) error {
	return remote.checkModeration(ctx, "prompt_shield", map[string]string{"prompt": prompt}, false)
}

type moderationResponse struct {
	Pass        *bool  `json:"pass,omitempty"`
	Decision    any    `json:"decision,omitempty"`
	Status      string `json:"status,omitempty"`
	AuditResult *struct {
		Status string `json:"status,omitempty"`
	} `json:"audit_result,omitempty"`
}

func (remote *remoteTransport) checkModeration(ctx context.Context, endpoint string, input any, allowMark bool) error {
	var response moderationResponse
	if err := remote.call(ctx, endpoint, input, &response); err != nil {
		return err
	}
	if response.Pass != nil {
		if *response.Pass {
			return nil
		}
		return fmt.Errorf("%s rejected content", endpoint)
	}
	decision := response.Status
	if decision == "" && response.AuditResult != nil {
		decision = response.AuditResult.Status
	}
	if decision == "" {
		switch value := response.Decision.(type) {
		case string:
			decision = value
		case map[string]any:
			decision, _ = value["type"].(string)
		}
	}
	switch strings.ToUpper(strings.TrimSpace(decision)) {
	case "PASS", "ALLOW", "ALLOWED":
		return nil
	case "MARK":
		if allowMark {
			return nil
		}
	case "BLOCK", "REJECT", "REJECTED", "DENY", "DENIED":
		return fmt.Errorf("%s rejected content", endpoint)
	}
	return fmt.Errorf("%s returned unknown moderation decision: %q", endpoint, decision)
}

func (remote *remoteTransport) find(ctx context.Context, name, key string) (SubmittedJob, bool, error) {
	var output struct {
		Found bool `json:"found"`
		SubmittedJob
	}
	err := remote.call(ctx, name, map[string]string{"submit_key": key}, &output)
	return output.SubmittedJob, output.Found, err
}

func callRemote[T any](ctx context.Context, remote *remoteTransport, name string, input any) (output T, err error) {
	err = remote.call(ctx, name, input, &output)
	return
}

func (remote *remoteTransport) call(ctx context.Context, name string, input, output any) error {
	endpoint := remote.endpoints[name]
	if endpoint == "" {
		return fmt.Errorf("remote endpoint is not configured: %s", name)
	}
	requestURL := endpoint
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		if remote.baseURL == "" {
			return fmt.Errorf("remote base url is empty for endpoint: %s", name)
		}
		requestURL = remote.baseURL + endpoint
	}
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if remote.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+remote.apiKey)
	}
	response, err := remote.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("remote %s returned %s: %s", name, response.Status, strings.TrimSpace(string(message)))
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(output)
}
