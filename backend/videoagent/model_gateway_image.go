package videoagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type ModelGatewayImageConfig struct {
	Model     string `json:"model"`
	TaskQueue string `json:"task_queue"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

type ModelGatewayImageClient struct {
	config  ModelGatewayImageConfig
	gateway ModelGateway
}

func NewModelGatewayImageClient(config ModelGatewayImageConfig, gateway ModelGateway) (*ModelGatewayImageClient, error) {
	if gateway == nil {
		return nil, fmt.Errorf("model gateway is nil")
	}
	if config.Model == "" {
		return nil, fmt.Errorf("image model is empty")
	}
	if config.TaskQueue == "" {
		config.TaskQueue = "jichuang_agent"
	}
	if config.Width == 0 {
		config.Width = 1024
	}
	if config.Height == 0 {
		config.Height = 1536
	}
	return &ModelGatewayImageClient{config: config, gateway: gateway}, nil
}

func (client *ModelGatewayImageClient) SubmitImage(ctx context.Context, request ImageRequest) (SubmittedJob, error) {
	model := firstNonEmpty(request.Model, client.config.Model)
	width := firstPositive(request.Width, client.config.Width)
	height := firstPositive(request.Height, client.config.Height)
	payload, err := json.Marshal(struct {
		Prompt    string `json:"prompt"`
		IsRewrite bool   `json:"is_rewrite"`
		Size      string `json:"size"`
	}{
		Prompt: request.Prompt,
		Size:   fmt.Sprintf("%dx%d", width, height),
	})
	if err != nil {
		return SubmittedJob{}, err
	}
	jobID, err := client.gateway.CreateTask(ctx, ModelTaskRequest{
		Input: payload, Model: model, TaskQueue: client.config.TaskQueue,
		Extra: map[string]string{"submit_key": request.SubmitKey},
	})
	if err != nil {
		return SubmittedJob{}, err
	}
	if jobID == "" {
		return SubmittedJob{}, fmt.Errorf("model gateway returned an empty task id")
	}
	return SubmittedJob{Provider: "model_gateway", JobID: jobID}, nil
}

func (client *ModelGatewayImageClient) GetImage(ctx context.Context, jobID string) (JobStatus, error) {
	result, err := client.gateway.GetTask(ctx, jobID)
	if err != nil {
		return JobStatus{}, err
	}
	switch result.Code {
	case -1001, -1002:
		return JobStatus{State: JobPending}, nil
	case -1000:
		return JobStatus{State: JobFailed, Message: firstNonEmpty(result.BizMessage, result.Status)}, nil
	case 0:
		image, err := decodeImageResult(result.Result)
		if err != nil {
			return JobStatus{}, err
		}
		if strings.HasPrefix(image, "tos://") {
			return JobStatus{State: JobSucceeded, URI: image}, nil
		}
		return JobStatus{State: JobSucceeded, URL: image}, nil
	default:
		return JobStatus{}, fmt.Errorf("unknown model gateway task code: %d", result.Code)
	}
}

func (*ModelGatewayImageClient) FindImageBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, ErrSubmitReconciliationUnsupported
}

func decodeImageResult(payload []byte) (string, error) {
	var result struct {
		Image     string            `json:"image"`
		ImageURLs []json.RawMessage `json:"image_urls"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return "", err
	}
	if result.Image != "" {
		return result.Image, nil
	}
	for _, raw := range result.ImageURLs {
		var value string
		if json.Unmarshal(raw, &value) == nil && value != "" {
			return value, nil
		}
		var item struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(raw, &item) == nil && item.URL != "" {
			return item.URL, nil
		}
	}
	return "", fmt.Errorf("model gateway image result is empty")
}

var _ ImageClient = (*ModelGatewayImageClient)(nil)
