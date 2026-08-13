package baseprompt

import (
	"context"

	"eino-cli/deepagent/core/middleware"
	"github.com/cloudwego/eino/schema"
)

// BasePromptMiddlewareName 是 BasePromptMiddleware 的名称常量，
// MiddlewareChain.BuildPrompts 依赖此名称将身份 prompt 排在合并后的 system message 最前面。
const BasePromptMiddlewareName = middleware.BasePromptMiddlewareName

// BasePromptMiddleware 注入 base system prompt（身份/角色定义）。
// BuildInitialContext 返回的 SystemMessage 会被 MiddlewareChain 合并到统一的 system message 中，
// 并通过 Name() == BasePromptMiddlewareName 保证排在最前面。
type BasePromptMiddleware struct {
	middleware.BaseMiddleware
	prompt string
}

// New 创建 base prompt 中间件
func New(prompt string) *BasePromptMiddleware {
	return &BasePromptMiddleware{prompt: prompt}
}

func (m *BasePromptMiddleware) Name() string { return BasePromptMiddlewareName }

func (m *BasePromptMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	if m.prompt != "" {
		return []*schema.Message{schema.SystemMessage(m.prompt)}, nil
	}
	return nil, nil
}
