package middleware

import (
	"context"

	"eino-cli/deepagent/core/types"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// Middleware 定义统一的中间件接口。
type Middleware interface {
	Name() string
	Tools(ctx context.Context) ([]tool.BaseTool, error)
	BeforeAgent(ctx context.Context) error
	ModifyModelRequest(ctx context.Context, initialContext []*schema.Message, userInputOrToolRes []*schema.Message, state *types.GraphState) ([]*schema.Message, error)
	ModifyModelResponse(ctx context.Context, modelResp *schema.Message, state *types.GraphState) (*schema.Message, error)
	ModifyModelStreamResponse(ctx context.Context, modelResp *schema.StreamReader[*schema.Message], state *types.GraphState) (*schema.StreamReader[*schema.Message], error)
	BuildInitialContext(ctx context.Context) ([]*schema.Message, error)
	WrapToolCall() compose.ToolMiddleware
	BuildStateHandler() types.RunTimeStateful
}

// BaseMiddleware 提供 Middleware 的默认空实现。
type BaseMiddleware struct{}

func (b *BaseMiddleware) Name() string { return "" }

func (b *BaseMiddleware) Tools(ctx context.Context) ([]tool.BaseTool, error) { return nil, nil }

func (b *BaseMiddleware) BeforeAgent(ctx context.Context) error { return nil }

func (b *BaseMiddleware) ModifyModelRequest(ctx context.Context, initialContext []*schema.Message, messages []*schema.Message, state *types.GraphState) ([]*schema.Message, error) {
	return messages, nil
}

func (b *BaseMiddleware) ModifyModelResponse(ctx context.Context, modelResp *schema.Message, state *types.GraphState) (*schema.Message, error) {
	return modelResp, nil
}

func (b *BaseMiddleware) ModifyModelStreamResponse(ctx context.Context, modelResp *schema.StreamReader[*schema.Message], state *types.GraphState) (*schema.StreamReader[*schema.Message], error) {
	return modelResp, nil
}

func (b *BaseMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	return nil, nil
}

func (b *BaseMiddleware) WrapToolCall() compose.ToolMiddleware {
	return compose.ToolMiddleware{}
}

func (b *BaseMiddleware) BuildStateHandler() types.RunTimeStateful {
	return nil
}

type GraphInterruptHandle func(opts ...compose.GraphInterruptOption)
