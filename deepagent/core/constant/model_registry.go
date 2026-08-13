package constant

import (
	"context"
	"strings"

	"code.byted.org/gopkg/logs/v2"
)

// ModelContextWindow 模型上下文窗口注册表
// key: 模型名称前缀, value: 上下文窗口大小（token 数）
var ModelContextWindow = map[string]int{
	"claude-4":        200000,
	"claude-opus-4":   200000,
	"claude-sonnet-4": 200000,
	"claude-3":        200000,
	"gpt-5":           400000,
	"gpt-4.1":         1047576,
	"gpt-4o":          128000,
	"gpt-4-turbo":     128000,
	"o3":              200000,
	"o4":              200000,
	"deepseek":        64000,
	"gemini-2":        1048000,
	"qwen":            131072,
	"llama":           131072,
	"kimi-k2":         256000,
	"DeepSeek-V3.2":   164000,
}

// DefaultModelContextWindow 默认上下文窗口（匹配不到注册表时使用）
const DefaultModelContextWindow = 128000

// LookupModelContextWindow 根据模型名称查找上下文窗口大小
// 优先前缀最长匹配，无匹配返回 DefaultModelContextWindow
func LookupModelContextWindow(ctx context.Context, modelName string) int {
	bestMatch := ""
	bestValue := 0

	for prefix, value := range ModelContextWindow {
		if strings.HasPrefix(modelName, prefix) && len(prefix) > len(bestMatch) {
			bestMatch = prefix
			bestValue = value
		}
	}

	if bestMatch != "" {
		logs.CtxInfo(ctx, "[LookupModelContextWindow] model_name: %s, matched_prefix: %s, context_window: %d",
			modelName, bestMatch, bestValue)
		return bestValue
	}
	logs.CtxInfo(ctx, "[LookupModelContextWindow] model_name: %s, no match, using default: %d",
		modelName, DefaultModelContextWindow)
	return DefaultModelContextWindow
}
