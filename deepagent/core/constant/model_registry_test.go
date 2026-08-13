package constant

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLookupModelContextWindow_ExactPrefix(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, 200000, LookupModelContextWindow(ctx, "claude-3-opus-20240229"))
	assert.Equal(t, 200000, LookupModelContextWindow(ctx, "claude-4-sonnet-20250514"))
	assert.Equal(t, 400000, LookupModelContextWindow(ctx, "gpt-5-turbo"))
	assert.Equal(t, 128000, LookupModelContextWindow(ctx, "gpt-4o-mini"))
	assert.Equal(t, 128000, LookupModelContextWindow(ctx, "gpt-4-turbo-preview"))
	assert.Equal(t, 64000, LookupModelContextWindow(ctx, "deepseek-chat"))
	assert.Equal(t, 1048000, LookupModelContextWindow(ctx, "gemini-2-pro"))
	assert.Equal(t, 131072, LookupModelContextWindow(ctx, "qwen-2.5-72b"))
	assert.Equal(t, 131072, LookupModelContextWindow(ctx, "llama-3.1-405b"))
	assert.Equal(t, 200000, LookupModelContextWindow(ctx, "o3-mini"))
	assert.Equal(t, 200000, LookupModelContextWindow(ctx, "o4-mini"))
}

func TestLookupModelContextWindow_LongestPrefixMatch(t *testing.T) {
	ctx := context.Background()
	// "claude-opus-4" 比 "claude-4" 更长，应优先匹配
	result := LookupModelContextWindow(ctx, "claude-opus-4-20250514")
	assert.Equal(t, 200000, result)

	// "claude-sonnet-4" 比 "claude-4" 更长
	result = LookupModelContextWindow(ctx, "claude-sonnet-4-20250514")
	assert.Equal(t, 200000, result)

	// "gpt-4.1" 比 "gpt-4" 前缀不同，gpt-4.1 匹配
	result = LookupModelContextWindow(ctx, "gpt-4.1-nano")
	assert.Equal(t, 1047576, result)

	// "gpt-4o" 不应匹配 "gpt-4-turbo"
	result = LookupModelContextWindow(ctx, "gpt-4o-2024")
	assert.Equal(t, 128000, result)
}

func TestLookupModelContextWindow_NoMatch(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, DefaultModelContextWindow, LookupModelContextWindow(ctx, "unknown-model"))
	assert.Equal(t, DefaultModelContextWindow, LookupModelContextWindow(ctx, ""))
	assert.Equal(t, DefaultModelContextWindow, LookupModelContextWindow(ctx, "mistral-7b"))
}

func TestLookupModelContextWindow_ExactKeyMatch(t *testing.T) {
	ctx := context.Background()
	// 模型名称正好等于注册表 key
	assert.Equal(t, 200000, LookupModelContextWindow(ctx, "claude-4"))
	assert.Equal(t, 64000, LookupModelContextWindow(ctx, "deepseek"))
	assert.Equal(t, 200000, LookupModelContextWindow(ctx, "o3"))
}

func TestLookupModelContextWindow_PrefixNotSubstring(t *testing.T) {
	ctx := context.Background()
	// "gpt-4" 不是注册表 key，但 "gpt-4o" 是
	// "gpt-4-something" 不应匹配 "gpt-4o" 或 "gpt-4.1"
	// 应匹配 "gpt-4-turbo" 如果 HasPrefix 成立
	result := LookupModelContextWindow(ctx, "gpt-4-turbo-2024")
	assert.Equal(t, 128000, result)

	// "gpt-4-base" 不匹配 "gpt-4o" 也不匹配 "gpt-4-turbo" 也不匹配 "gpt-4.1"
	result = LookupModelContextWindow(ctx, "gpt-4-base")
	assert.Equal(t, DefaultModelContextWindow, result)
}
