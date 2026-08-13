package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/model"
)

type Stage1ExtractFunc func(ctx context.Context, input Stage1ExtractInput) (Stage1ExtractResult, error)

func NewModelStage1ExtractFunc(m model.ToolCallingChatModel) Stage1ExtractFunc {
	return func(ctx context.Context, input Stage1ExtractInput) (Stage1ExtractResult, error) {
		if m == nil {
			return Stage1ExtractResult{}, fmt.Errorf("memory stage1 extractor: model is nil")
		}
		resp, err := m.Generate(ctx, BuildStage1PromptMessages(input))
		if err != nil {
			return Stage1ExtractResult{}, err
		}
		if resp == nil {
			return Stage1ExtractResult{}, fmt.Errorf("memory stage1 extractor: model returned nil response")
		}
		return parseStage1ExtractResult(resp.Content)
	}
}

func parseStage1ExtractResult(content string) (Stage1ExtractResult, error) {
	var out struct {
		RawMemory      string `json:"raw_memory"`
		RolloutSummary string `json:"rollout_summary"`
		RolloutSlug    string `json:"rollout_slug"`
	}
	if err := sonic.UnmarshalString(extractJSONObject(content), &out); err != nil {
		return Stage1ExtractResult{}, fmt.Errorf("memory stage1 extractor: parse json: %w", err)
	}
	return Stage1ExtractResult{
		RawMemory:      strings.TrimSpace(out.RawMemory),
		RolloutSummary: strings.TrimSpace(out.RolloutSummary),
		RolloutSlug:    strings.TrimSpace(out.RolloutSlug),
	}, nil
}

func extractJSONObject(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end >= start {
		return content[start : end+1]
	}
	return content
}
