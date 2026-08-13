package repairjson

import (
	"context"
	"fmt"
	"strings"

	"code.byted.org/gopkg/logs/v2"
	"github.com/cloudwego/eino/compose"

	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/tools"
)

// RepairJSONMiddleware 修复 LLM 返回的无效 JSON 工具调用参数
// 处理 Claude 等模型常见的 JSON 问题：Infinity、NaN、未转义控制字符等
// Override WrapToolCall，作为洋葱链的最外层，确保所有中间件看到的都是合法 JSON
type RepairJSONMiddleware struct {
	middleware.BaseMiddleware
}

// New 创建 JSON 修复中间件
func New() *RepairJSONMiddleware {
	return &RepairJSONMiddleware{}
}

func (m *RepairJSONMiddleware) Name() string {
	return "repair_json"
}

// WrapToolCall 在工具调用前修复 JSON 参数（覆盖全部 4 种端点）
func (m *RepairJSONMiddleware) WrapToolCall() compose.ToolMiddleware {
	return middleware.WrapAllToolInputs(func(ctx context.Context, input *compose.ToolInput) (string, bool) {
		repaired, err := tools.RepairJSONWithError(input.Arguments)
		if err == nil {
			input.Arguments = repaired
			return "", false
		}
		logs.CtxWarn(ctx, "[RepairJSONMiddleware] tool input is invalid json: tool=%s call_id=%s arg_len=%d arg_preview=%q err=%v",
			input.Name, input.CallID, len(input.Arguments), truncateRepairJSONPreview(input.Arguments), err)
		return fmt.Sprintf("[Error] input is invalid json: %v", err), true
	})
}

func truncateRepairJSONPreview(input string) string {
	input = strings.TrimSpace(input)
	if len(input) <= 256 {
		return input
	}
	return input[:256] + "...(truncated)"
}
