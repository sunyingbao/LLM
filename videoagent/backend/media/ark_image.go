package media

import (
	"context"
	"fmt"
	"strings"

	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
)

// ArkImageConfig 配置 Ark 图片模型的直连参数。
type ArkImageConfig struct {
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	Model          string `json:"model"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	ResponseFormat string `json:"response_format,omitempty"`
}

// ArkImageClient 通过 Ark HTTPS 接口直接生成图片，不依赖内部服务发现。
type ArkImageClient struct {
	config ArkImageConfig
	client *arkruntime.Client
}

// NewArkImageClient 创建 Ark 图片客户端。
func NewArkImageClient(config ArkImageConfig) (*ArkImageClient, error) {
	if strings.TrimSpace(config.BaseURL) == "" {
		return nil, fmt.Errorf("ark image base url is empty")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("ark image api key is empty")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("ark image model is empty")
	}
	if config.Width <= 0 {
		config.Width = 1024
	}
	if config.Height <= 0 {
		config.Height = 1536
	}
	if config.ResponseFormat == "" {
		config.ResponseFormat = model.GenerateImagesResponseFormatURL
	}
	if config.ResponseFormat != model.GenerateImagesResponseFormatURL && config.ResponseFormat != model.GenerateImagesResponseFormatBase64 {
		return nil, fmt.Errorf("unsupported ark image response format: %s", config.ResponseFormat)
	}
	client := arkruntime.NewClientWithApiKey(
		config.APIKey,
		arkruntime.WithBaseUrl(config.BaseURL),
	)
	return &ArkImageClient{config: config, client: client}, nil
}

// SubmitImage 调用 Ark 图片接口并把同步结果转换成统一的已完成任务。
func (client *ArkImageClient) SubmitImage(ctx context.Context, request ImageRequest) (SubmittedJob, error) {
	if client == nil || client.client == nil {
		return SubmittedJob{}, fmt.Errorf("ark image client is nil")
	}
	modelKey := firstNonEmpty(request.Model, client.config.Model)
	width := firstPositive(request.Width, client.config.Width)
	height := firstPositive(request.Height, client.config.Height)
	imageRequest := model.GenerateImagesRequest{
		Model:          modelKey,
		Prompt:         request.Prompt,
		ResponseFormat: volcengine.String(client.config.ResponseFormat),
		Size:           volcengine.String(fmt.Sprintf("%dx%d", width, height)),
		Watermark:      volcengine.Bool(false),
	}
	if len(request.ImageURLs) == 1 {
		imageRequest.Image = request.ImageURLs[0]
	} else if len(request.ImageURLs) > 1 {
		imageRequest.Image = request.ImageURLs
	}
	response, err := client.client.GenerateImages(ctx, imageRequest)
	if err != nil {
		return SubmittedJob{}, err
	}
	if response.Error != nil {
		return SubmittedJob{}, fmt.Errorf("ark image generation failed: %s", response.Error.Message)
	}
	if len(response.Data) == 0 || response.Data[0] == nil {
		return SubmittedJob{}, fmt.Errorf("ark image response is empty")
	}
	status := JobStatus{State: JobSucceeded}
	if response.Data[0].Url != nil {
		status.URL = *response.Data[0].Url
	}
	if response.Data[0].B64Json != nil {
		status.URI = "data:image;base64," + *response.Data[0].B64Json
	}
	if status.URL == "" && status.URI == "" {
		return SubmittedJob{}, fmt.Errorf("ark image response has no url or data")
	}
	return SubmittedJob{Provider: "ark", JobID: request.SubmitKey, Status: &status}, nil
}

// GetImage 不支持轮询，因为 Ark GenerateImages 已经返回最终图片。
func (*ArkImageClient) GetImage(context.Context, string) (JobStatus, error) {
	return JobStatus{}, fmt.Errorf("ark image generation does not support polling")
}

// FindImageBySubmitKey 不依赖内部任务存储，因此不支持提交后补偿查询。
func (*ArkImageClient) FindImageBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, ErrSubmitReconciliationUnsupported
}

var _ ImageClient = (*ArkImageClient)(nil)
