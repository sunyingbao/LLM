package videoagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultModelHubAttempts = 3

type ImageXConfig struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	ServiceID string `json:"service_id"`
}

type ModelHubImageConfig struct {
	GenURL   string       `json:"gen_url"`
	EditURL  string       `json:"edit_url,omitempty"`
	APIKeys  []string     `json:"api_keys"`
	Model    string       `json:"model"`
	Width    int          `json:"width,omitempty"`
	Height   int          `json:"height,omitempty"`
	Attempts int          `json:"attempts,omitempty"`
	ImageX   ImageXConfig `json:"imagex"`
}

type ImageBinaryUploader interface {
	UploadImage(context.Context, []byte) (string, error)
}

type ModelHubImageClient struct {
	config   ModelHubImageConfig
	client   *http.Client
	uploader ImageBinaryUploader
}

func NewModelHubImageClient(config ModelHubImageConfig, client *http.Client, uploader ImageBinaryUploader) (*ModelHubImageClient, error) {
	config.GenURL = strings.TrimSpace(config.GenURL)
	config.EditURL = strings.TrimSpace(config.EditURL)
	config.APIKeys = nonEmptyStrings(config.APIKeys)
	if config.GenURL == "" || len(config.APIKeys) == 0 || uploader == nil {
		return nil, fmt.Errorf("model hub gen url, api keys and image uploader are required")
	}
	if config.Model == "" {
		config.Model = "gemini-3-pro-image-preview"
	}
	if config.Width == 0 {
		config.Width = 1024
	}
	if config.Height == 0 {
		config.Height = 1536
	}
	if config.Attempts <= 0 {
		config.Attempts = defaultModelHubAttempts
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &ModelHubImageClient{config: config, client: client, uploader: uploader}, nil
}

func (client *ModelHubImageClient) SubmitImage(ctx context.Context, request ImageRequest) (SubmittedJob, error) {
	requestURL := client.config.GenURL
	if len(request.ImageURLs) > 0 {
		if client.config.EditURL == "" {
			return SubmittedJob{}, fmt.Errorf("model hub edit url is empty")
		}
		requestURL = client.config.EditURL
	}
	body, err := json.Marshal(client.buildRequest(request))
	if err != nil {
		return SubmittedJob{}, err
	}

	var lastErr error
	for attempt := 0; attempt < client.config.Attempts; attempt++ {
		for _, apiKey := range client.config.APIKeys {
			image, callErr := client.generate(ctx, strings.ReplaceAll(requestURL, "{{api_key}}", url.QueryEscape(apiKey)), body)
			if callErr != nil {
				lastErr = callErr
				continue
			}
			uri, uploadErr := client.uploader.UploadImage(ctx, image)
			if uploadErr != nil {
				lastErr = uploadErr
				continue
			}
			if uri == "" {
				lastErr = fmt.Errorf("image uploader returned an empty uri")
				continue
			}
			return SubmittedJob{Provider: "model_hub", Status: &JobStatus{State: JobSucceeded, URI: uri}}, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("model hub image generation failed")
	}
	return SubmittedJob{}, lastErr
}

func (client *ModelHubImageClient) buildRequest(request ImageRequest) any {
	contents := []map[string]any{{"type": "text", "text": "请生成一张图片。提示词：" + request.Prompt}}
	if len(request.ImageURLs) > 0 {
		contents[0]["text"] = "请基于我提供的图片，生成一张图片。提示词：" + request.Prompt
		for _, imageURL := range nonEmptyStrings(request.ImageURLs) {
			contents = append(contents, map[string]any{"type": "image_url", "image_url": map[string]string{"url": imageURL}})
		}
	}
	return map[string]any{
		"stream":              false,
		"model":               firstNonEmpty(request.Model, client.config.Model),
		"max_tokens":          4096,
		"messages":            []any{map[string]any{"role": "user", "content": contents}},
		"response_modalities": []string{"TEXT", "IMAGE"},
		"image_config": map[string]any{
			"aspectRatio":        imageAspectRatio(firstPositive(request.Width, client.config.Width), firstPositive(request.Height, client.config.Height)),
			"imageSize":          "2K",
			"imageOutputOptions": map[string]string{"mimeType": "image/png"},
		},
	}
}

func (client *ModelHubImageClient) generate(ctx context.Context, requestURL string, body []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("model hub returned %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	return decodeGeminiImage(payload)
}

func (*ModelHubImageClient) GetImage(context.Context, string) (JobStatus, error) {
	return JobStatus{}, fmt.Errorf("model hub image generation is synchronous")
}

func (*ModelHubImageClient) FindImageBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, ErrSubmitReconciliationUnsupported
}

func decodeGeminiImage(payload []byte) ([]byte, error) {
	var response struct {
		Choices []struct {
			Message struct {
				Contents []struct {
					Type       string `json:"type"`
					InlineData *struct {
						Data string `json:"data"`
					} `json:"inline_data"`
				} `json:"multimodal_contents"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, err
	}
	for _, choice := range response.Choices {
		for _, content := range choice.Message.Contents {
			if content.Type != "inline_data" || content.InlineData == nil || content.InlineData.Data == "" {
				continue
			}
			image, err := base64.StdEncoding.DecodeString(content.InlineData.Data)
			if err != nil {
				return nil, err
			}
			return image, nil
		}
	}
	return nil, fmt.Errorf("gemini image response is empty")
}

func imageAspectRatio(width, height int) string {
	if width > height {
		return "16:9"
	}
	if height > width {
		return "9:16"
	}
	return "1:1"
}

func nonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

var _ ImageClient = (*ModelHubImageClient)(nil)
