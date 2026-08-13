package utils

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// CountToolContentTokens 统计单条消息中工具相关内容的 token 数
// 包括 ToolCall.Arguments 和 Tool 角色消息的 Content
func CountToolContentTokens(msg *schema.Message) int {
	total := 0
	for _, tc := range msg.ToolCalls {
		total += len(tc.Function.Arguments) / 4
	}
	if msg.Role == schema.Tool {
		total += len(msg.Content) / 4
	}
	return total
}

// SimpleTokenCounter 简单的令牌计数器（字符数 / 4）
func SimpleTokenCounter(messages []*schema.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content) / 4
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Name) / 4
			total += len(tc.Function.Arguments) / 4
		}
	}
	return total
}

// EstimateTokens 估算字符串的 token 数（len / 4）
func EstimateTokens(s string) int {
	return len(s) / 4
}

// EstimateToolDefinitionsTokens 估算所有工具定义的 token 数
func EstimateToolDefinitionsTokens(ctx context.Context, allTools []tool.BaseTool) int {
	total := 0
	for _, t := range allTools {
		info, err := t.Info(ctx)
		if err != nil {
			continue
		}
		data, err := json.Marshal(info)
		if err != nil {
			continue
		}
		total += len(data) / 4
	}
	return total
}
