package chatmodel

import (
	"context"
	"fmt"
	"net/http"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/backend/modelhub"
	"eino-cli/deepagent/cloud/worker/bootstrap/config"
	"github.com/cloudwego/eino-ext/components/model/ark"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	arkruntime_model "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

type Builder interface {
	Build(ctx context.Context, modelConfig config.ModelConfig) (model.ToolCallingChatModel, error)
}

type BuilderImpl struct{}

func NewBuilder() Builder {
	return &BuilderImpl{}
}
func BuildAll(ctx context.Context, models map[string]config.ModelConfig) (map[string]model.ToolCallingChatModel, error) {
	builder := NewBuilder()
	out := make(map[string]model.ToolCallingChatModel, len(models))
	for id, modelCfg := range models {
		chatModel, err := builder.Build(ctx, modelCfg)
		if err != nil {
			return nil, fmt.Errorf("build model %s: %w", id, err)
		}
		out[id] = chatModel
	}
	return out, nil
}

func (b *BuilderImpl) Build(ctx context.Context, modelConfig config.ModelConfig) (model.ToolCallingChatModel, error) {
	if modelConfig.ModelName == "" {
		return nil, fmt.Errorf("model_name is required")
	}
	if modelConfig.SDKType == "" {
		return nil, fmt.Errorf("sdk_type is required for model: %s", modelConfig.ModelName)
	}
	if modelConfig.ModelEndpointID == "" {
		return nil, fmt.Errorf("model_endpoint_id is required for model: %s", modelConfig.ModelName)
	}

	logs.CtxInfo(ctx, "[chatmodel] build model: name=%s sdk_type=%s endpoint_id=%s base_url=%s thinking=%s",
		modelConfig.ModelName, modelConfig.SDKType, modelConfig.ModelEndpointID, modelConfig.ModelBaseURL, thinkingConfigSummary(modelConfig.Thinking))

	switch modelConfig.SDKType {
	case config.SDKTypeArk:
		return buildArkModel(ctx, modelConfig)
	case config.SDKTypeOpenAI:
		return buildOpenAIModel(ctx, modelConfig)
	case config.SDKTypeKimi:
		return buildKimiModel(ctx, modelConfig)
	default:
		return nil, fmt.Errorf("unsupported sdk_type: %s", modelConfig.SDKType)
	}
}

func thinkingConfigSummary(thinking *config.ThinkingType) string {
	if thinking == nil {
		return "unset"
	}
	return string(*thinking)
}

func buildArkModel(ctx context.Context, modelConfig config.ModelConfig) (model.ToolCallingChatModel, error) {
	cfg := &ark.ChatModelConfig{
		BaseURL:          modelConfig.ModelBaseURL,
		APIKey:           modelConfig.ModelAPIKey,
		Model:            modelConfig.ModelEndpointID,
		MaxTokens:        intPtr(modelConfig.MaxTokens),
		Temperature:      modelConfig.Temperature,
		TopP:             modelConfig.TopP,
		FrequencyPenalty: modelConfig.FrequencyPenalty,
	}
	if modelConfig.Thinking != nil {
		cfg.Thinking = &arkruntime_model.Thinking{
			Type: arkruntime_model.ThinkingType(*modelConfig.Thinking),
		}
	}
	return ark.NewChatModel(ctx, cfg)
}

func buildOpenAIModel(ctx context.Context, modelConfig config.ModelConfig) (chatModel model.ToolCallingChatModel, err error) {
	cfg := &openai.ChatModelConfig{
		BaseURL:          modelConfig.ModelBaseURL,
		APIKey:           modelConfig.ModelAPIKey,
		Model:            modelConfig.ModelEndpointID,
		ByAzure:          !modelConfig.DisableByAzure,
		APIVersion:       modelConfig.APIVersion,
		MaxTokens:        intPtr(modelConfig.MaxTokens),
		Temperature:      modelConfig.Temperature,
		TopP:             modelConfig.TopP,
		FrequencyPenalty: modelConfig.FrequencyPenalty,
	}
	if modelhub.IsEndpoint(cfg.BaseURL) {
		cfg.HTTPClient, err = modelhub.NewHTTPClient(cfg.BaseURL, cfg.APIKey, 0)
		if err != nil {
			return nil, fmt.Errorf("build modelhub client: %w", err)
		}
	}
	if modelConfig.Thinking != nil {
		cfg.ExtraFields = map[string]any{
			"thinking": map[string]any{
				"type": string(*modelConfig.Thinking),
			},
		}
	}
	return openai.NewChatModel(ctx, cfg)
}

func buildKimiModel(ctx context.Context, modelConfig config.ModelConfig) (model.ToolCallingChatModel, error) {
	cfg := &openai.ChatModelConfig{
		BaseURL:             modelConfig.ModelBaseURL,
		APIKey:              modelConfig.ModelAPIKey,
		Model:               modelConfig.ModelEndpointID,
		ByAzure:             true,
		APIVersion:          valueOrDefault(modelConfig.APIVersion, "2024-02-15-preview"),
		MaxCompletionTokens: intPtr(modelConfig.MaxTokens),
		HTTPClient: &http.Client{Transport: &kimiTransport{
			logID:            valueOrDefault(modelConfig.LogID, "kimi-deepagent-worker"),
			defaultTransport: http.DefaultTransport,
		}},
	}
	if modelConfig.Thinking != nil {
		cfg.ExtraFields = map[string]any{
			"thinking": map[string]any{
				"type": string(*modelConfig.Thinking),
			},
		}
	}
	return openai.NewChatModel(ctx, cfg)
}

type kimiTransport struct {
	logID            string
	defaultTransport http.RoundTripper
}

func (t *kimiTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("X-TT-LOGID", t.logID)
	return t.defaultTransport.RoundTrip(req)
}

func intPtr(v int32) *int {
	if v <= 0 {
		return nil
	}
	i := int(v)
	return &i
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
