package main

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/ark"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	arkmodel "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

type kimiTransport struct {
	logID            string
	defaultTransport http.RoundTripper
}

func (t *kimiTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("X-TT-LOGID", t.logID)
	return t.defaultTransport.RoundTrip(req)
}

func asciiURL(b ...byte) string {

	return string(b)
}

func createChatModel(ctx context.Context) (model.ToolCallingChatModel, error) {
	if apiKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")); apiKey != "" {
		return openai.NewChatModel(ctx, openAIModelConfigFromEnv())
	}

	if apiKey := os.Getenv("OPENROUTER_API_KEY"); apiKey != "" {
		modelName := os.Getenv("OPENROUTER_MODEL")
		if modelName == "" {
			modelName = "anthropic/claude-sonnet-4.5"
		}
		cfg := &openai.ChatModelConfig{
			BaseURL: asciiURL(104, 116, 116, 112, 115, 58, 47, 47, 111, 112, 101, 110, 114, 111, 117, 116, 101, 114, 46, 97, 105, 47, 97, 112, 105, 47, 118, 49),
			APIKey:  apiKey,
			Model:   modelName,
		}
		applyOpenAIReasoningConfig(cfg, false)
		return openai.NewChatModel(ctx, cfg)
	}

	if apiKey := os.Getenv("KIMI_API_KEY"); apiKey != "" {
		modelName := os.Getenv("KIMI_MODEL")
		if modelName == "" {
			modelName = "kimi-k2.5"
		}
		baseURL := os.Getenv("KIMI_BASE_URL")
		if baseURL == "" {
			baseURL = asciiURL(104, 116, 116, 112, 115, 58, 47, 47, 115, 101, 97, 114, 99, 104, 46, 98, 121, 116, 101, 100, 97, 110, 99, 101, 46, 110, 101, 116, 47, 103, 112, 116, 47, 111, 112, 101, 110, 97, 112, 105, 47, 111, 110, 108, 105, 110, 101, 47, 118, 50, 47, 99, 114, 97, 119, 108)
		}
		logID := os.Getenv("KIMI_LOG_ID")
		if logID == "" {
			logID = "kimi-deepagent"
		}
		cfg := &openai.ChatModelConfig{
			BaseURL:    baseURL,
			APIKey:     apiKey,
			Model:      modelName,
			ByAzure:    true,
			APIVersion: "2024-02-15-preview",
			HTTPClient: &http.Client{Transport: &kimiTransport{logID: logID, defaultTransport: http.DefaultTransport}},
		}
		applyOpenAIReasoningConfig(cfg, true)
		return openai.NewChatModel(ctx, cfg)
	}

	cfg := &ark.ChatModelConfig{
		Model:  os.Getenv("ARK_MODEL"),
		APIKey: os.Getenv("ARK_API_KEY"),
	}
	applyArkReasoningConfig(cfg)
	return ark.NewChatModel(ctx, cfg)
}

func openAIModelConfigFromEnv() *openai.ChatModelConfig {
	modelName := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	if modelName == "" {
		modelName = "gpt-4o"
	}
	cfg := &openai.ChatModelConfig{
		APIKey: strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")),
		Model:  modelName,
	}
	if baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")); baseURL != "" {
		cfg.BaseURL = baseURL
	}
	applyOpenAIReasoningConfig(cfg, false)
	return cfg
}

func applyArkReasoningConfig(cfg *ark.ChatModelConfig) {
	if cfg == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DEEPAGENT_THINKING"))) {
	case "enabled", "enable", "on", "true", "1":
		cfg.Thinking = &arkmodel.Thinking{Type: arkmodel.ThinkingTypeEnabled}
	case "auto":
		cfg.Thinking = &arkmodel.Thinking{Type: arkmodel.ThinkingTypeAuto}
	case "disabled", "disable", "off", "false", "0":
		cfg.Thinking = &arkmodel.Thinking{Type: arkmodel.ThinkingTypeDisabled}
	}
	if effort, ok := arkReasoningEffortFromEnv(); ok {
		cfg.ReasoningEffort = &effort
	}
}

func applyOpenAIReasoningConfig(cfg *openai.ChatModelConfig, defaultDisableThinking bool) {
	if cfg == nil {
		return
	}
	thinking := strings.ToLower(strings.TrimSpace(os.Getenv("DEEPAGENT_THINKING")))
	if thinking == "" && defaultDisableThinking {
		thinking = "disabled"
	}
	switch thinking {
	case "enabled", "enable", "on", "true", "1":
		setOpenAIExtraField(cfg, "thinking", map[string]any{"type": "enabled"})
	case "auto":
		setOpenAIExtraField(cfg, "thinking", map[string]any{"type": "auto"})
	case "disabled", "disable", "off", "false", "0":
		setOpenAIExtraField(cfg, "thinking", map[string]any{"type": "disabled"})
	}
	if effort, ok := openAIReasoningEffortFromEnv(); ok {
		cfg.ReasoningEffort = effort
	}
}

func setOpenAIExtraField(cfg *openai.ChatModelConfig, key string, value any) {
	if cfg.ExtraFields == nil {
		cfg.ExtraFields = make(map[string]any)
	}
	cfg.ExtraFields[key] = value
}

func arkReasoningEffortFromEnv() (arkmodel.ReasoningEffort, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DEEPAGENT_REASONING_EFFORT"))) {
	case "minimal":
		return arkmodel.ReasoningEffortMinimal, true
	case "low":
		return arkmodel.ReasoningEffortLow, true
	case "medium":
		return arkmodel.ReasoningEffortMedium, true
	case "high":
		return arkmodel.ReasoningEffortHigh, true
	default:
		return "", false
	}
}

func openAIReasoningEffortFromEnv() (openai.ReasoningEffortLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DEEPAGENT_REASONING_EFFORT"))) {
	case "low":
		return openai.ReasoningEffortLevelLow, true
	case "medium":
		return openai.ReasoningEffortLevelMedium, true
	case "high":
		return openai.ReasoningEffortLevelHigh, true
	default:
		return "", false
	}
}
