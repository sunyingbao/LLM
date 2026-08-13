package patchtoolcalls

import (
	"fmt"

	"github.com/cloudwego/eino/schema"

	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
)

// PatchToolCallsMiddleware 修补悬挂的工具调用
// 当消息历史中存在 AI 消息的 tool_calls 没有对应的 ToolMessage 响应时，
// 该中间件会自动添加合成的 ToolMessage 以保持消息历史的一致性
type PatchToolCallsMiddleware struct {
	middleware.BaseMiddleware
}

// New 创建修补工具调用中间件
func New() *PatchToolCallsMiddleware {
	return &PatchToolCallsMiddleware{}
}

func (m *PatchToolCallsMiddleware) Name() string {
	return constant.MiddlewarePatchToolCalls
}

// NOTE: Tools, BeforeAgent, WrapModelCall, WrapModelStream, WrapToolCall
// all use BaseMiddleware defaults (no-op / passthrough).
// The actual patching is done in graph_builder.go's patchDanglingToolCallsPhase.

// PatchDanglingToolCalls 修补消息列表中的悬挂工具调用
// 对于每个有 tool_calls 的 AI 消息，检查是否有对应的 ToolMessage
// 如果没有，添加一个合成的取消消息
func PatchDanglingToolCalls(messages []*schema.Message) []*schema.Message {
	if len(messages) == 0 {
		return messages
	}

	patched := make([]*schema.Message, 0, len(messages))

	for i, msg := range messages {
		patched = append(patched, msg)

		// 只处理 AI 消息中的 tool_calls
		if msg.Role != schema.Assistant || len(msg.ToolCalls) == 0 {
			continue
		}

		// 检查每个 tool_call 是否有对应的 ToolMessage
		for _, tc := range msg.ToolCalls {
			hasResponse := false

			// 在后续消息中查找对应的 ToolMessage
			for _, followMsg := range messages[i+1:] {
				if followMsg.Role == schema.Tool && followMsg.ToolCallID == tc.ID {
					hasResponse = true
					break
				}
			}

			// 如果没有响应，添加合成的取消消息
			if !hasResponse {
				cancelMsg := fmt.Sprintf(
					constant.MsgToolCallCancelled,
					tc.Function.Name,
					tc.ID,
				)
				patched = append(patched, &schema.Message{
					Role:       schema.Tool,
					ToolCallID: tc.ID,
					Content:    cancelMsg,
				})
			}
		}
	}

	return patched
}

// PatchSingleMessage 修补单条消息的工具调用
// 用于实时消息处理场景
func PatchSingleMessage(msg *schema.Message, existingResponses map[string]bool) []*schema.Message {
	result := []*schema.Message{msg}

	if msg.Role != schema.Assistant || len(msg.ToolCalls) == 0 {
		return result
	}

	for _, tc := range msg.ToolCalls {
		if existingResponses != nil && existingResponses[tc.ID] {
			continue
		}

		cancelMsg := fmt.Sprintf(
			constant.MsgToolCallCancelled,
			tc.Function.Name,
			tc.ID,
		)
		result = append(result, &schema.Message{
			Role:       schema.Tool,
			ToolCallID: tc.ID,
			Content:    cancelMsg,
		})
	}

	return result
}
