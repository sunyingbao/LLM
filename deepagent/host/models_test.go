package host

import (
	"context"
	"testing"

	"eino-cli/deepagent/backend/config"
	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/stretchr/testify/require"
)

func TestParseReasoningEffort(t *testing.T) {
	require.Equal(t, openaimodel.ReasoningEffortLevelLow, parseReasoningEffort(" low "))
	require.Equal(t, openaimodel.ReasoningEffortLevelMedium, parseReasoningEffort("MEDIUM"))
	require.Equal(t, openaimodel.ReasoningEffortLevelHigh, parseReasoningEffort("high"))
	require.Equal(t, openaimodel.ReasoningEffortLevel(""), parseReasoningEffort("unknown"))
}

func TestBuildChatModels(t *testing.T) {
	ctx := context.Background()
	_, err := buildChatModel(ctx, &config.ModelConfig{Provider: "unsupported"})
	require.ErrorContains(t, err, "unsupported model provider")

	for _, modelConfig := range []*config.ModelConfig{
		{Provider: "openai", APIKey: "test", Model: "gpt-test", BaseURL: "http://127.0.0.1:1/v1", ReasoningEffort: "low"},
		{Provider: "openai", APIKey: "test", Model: "gpt-test", BaseURL: "https://example.com/crawl"},
		{Provider: "kimi", APIKey: "test", Model: "invalid-name", BaseURL: ""},
		{Provider: "moonshot", APIKey: "test", Model: "moonshot-v1-8k", BaseURL: "http://127.0.0.1:1/v1"},
		{Provider: "claude", APIKey: "test", Model: "claude-test", BaseURL: "http://127.0.0.1:1", TimeoutSeconds: 1},
		{Provider: "anthropic", APIKey: "test", Model: "claude-test", SupportsThinking: true},
		{Provider: "anthropic", APIKey: "test", Model: "claude-test", SupportsThinking: true, ThinkingBudgetTokens: 1024},
	} {
		chatModel, buildErr := buildChatModel(ctx, modelConfig)
		require.NoError(t, buildErr)
		require.NotNil(t, chatModel)
	}

	_, err = buildChatModel(ctx, &config.ModelConfig{
		Provider: "openai",
		APIKey:   "test",
		Model:    "gpt-test",
		BaseURL:  "://invalid/crawl",
	})
	require.ErrorContains(t, err, "build modelhub client")
}

func TestBuildSummaryAndToolCallingModel(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{
		DefaultModel: "invalid",
		Models: map[string]*config.ModelConfig{
			"invalid": {Provider: "unsupported"},
		},
	}
	require.Nil(t, buildSummaryChatModel(ctx, cfg))

	cfg.Models["valid"] = &config.ModelConfig{Provider: "openai", APIKey: "test", Model: "gpt-test", BaseURL: "http://127.0.0.1:1/v1"}
	cfg.DefaultModel = " valid "
	require.NotNil(t, buildSummaryChatModel(ctx, cfg))

	chatModel, err := BuildToolCallingChatModel(ctx, cfg.Models["valid"])
	require.NoError(t, err)
	require.NotNil(t, chatModel)
	_, err = BuildToolCallingChatModel(ctx, cfg.Models["invalid"])
	require.ErrorContains(t, err, "unsupported model provider")
}
