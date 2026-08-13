package middleware

import (
	"context"
	"strings"

	"eino-cli/deepagent/core/types"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// BasePromptMiddlewareName 标识需要排在所有 system prompt 最前面的身份 prompt。
const BasePromptMiddlewareName = "base_prompt"

type MiddlewareChain struct {
	middlewares []Middleware
}

func NewMiddlewareChain(middlewares ...Middleware) *MiddlewareChain {
	return &MiddlewareChain{middlewares: middlewares}
}

func (c *MiddlewareChain) Add(m Middleware) {
	c.middlewares = append(c.middlewares, m)
}

func (c *MiddlewareChain) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	var tools []tool.BaseTool
	for _, m := range c.middlewares {
		middlewareTools, err := m.Tools(ctx)
		if err != nil {
			return nil, err
		}
		tools = append(tools, middlewareTools...)
	}
	return tools, nil
}

func (c *MiddlewareChain) BeforeAgent(ctx context.Context) error {
	for _, m := range c.middlewares {
		if err := m.BeforeAgent(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (c *MiddlewareChain) ModifyModelRequest(ctx context.Context, initialContext []*schema.Message, messages []*schema.Message, state *types.GraphState) ([]*schema.Message, error) {
	var err error
	for _, m := range c.middlewares {
		messages, err = m.ModifyModelRequest(ctx, initialContext, messages, state)
		if err != nil {
			return nil, err
		}
	}
	return messages, nil
}

func (c *MiddlewareChain) BuildPrompts(ctx context.Context) ([]*schema.Message, error) {
	var (
		basePrompts        []string
		otherSystemPrompts []string
		nonSystemMessages  []*schema.Message
	)

	for _, middleware := range c.middlewares {
		messages, err := middleware.BuildInitialContext(ctx)
		if err != nil {
			return nil, err
		}

		for _, message := range messages {
			if message == nil {
				continue
			}
			if message.Role != schema.System {
				nonSystemMessages = append(nonSystemMessages, message)
				continue
			}
			if message.Content == "" {
				continue
			}
			if middleware.Name() == BasePromptMiddlewareName {
				basePrompts = append(basePrompts, message.Content)
				continue
			}
			otherSystemPrompts = append(otherSystemPrompts, message.Content)
		}
	}

	systemPrompts := make([]string, 0, len(basePrompts)+len(otherSystemPrompts))
	systemPrompts = append(systemPrompts, basePrompts...)
	systemPrompts = append(systemPrompts, otherSystemPrompts...)

	var prompts []*schema.Message
	if len(systemPrompts) > 0 {
		prompts = append(prompts, schema.SystemMessage(strings.Join(systemPrompts, "\n\n")))
	}

	return append(prompts, nonSystemMessages...), nil
}

func (c *MiddlewareChain) ModifyModelResponse(ctx context.Context, modelResp *schema.Message, state *types.GraphState) (*schema.Message, error) {
	var err error
	for _, m := range c.middlewares {
		modelResp, err = m.ModifyModelResponse(ctx, modelResp, state)
		if err != nil {
			return nil, err
		}
	}
	return modelResp, nil
}

func (c *MiddlewareChain) ModifyModelStreamResponse(ctx context.Context, modelResp *schema.StreamReader[*schema.Message], state *types.GraphState) (*schema.StreamReader[*schema.Message], error) {
	var err error
	for _, m := range c.middlewares {
		modelResp, err = m.ModifyModelStreamResponse(ctx, modelResp, state)
		if err != nil {
			return nil, err
		}
	}
	return modelResp, nil
}

func (c *MiddlewareChain) BuildStateHandlers() map[string]types.RunTimeStateful {
	handlers := make(map[string]types.RunTimeStateful)
	for _, m := range c.middlewares {
		if handler := m.BuildStateHandler(); handler != nil {
			handlers[m.Name()] = handler
		}
	}
	return handlers
}

func (c *MiddlewareChain) ToolCallMiddlewares() []compose.ToolMiddleware {
	var middlewares []compose.ToolMiddleware
	for _, m := range c.middlewares {
		toolMiddleware := m.WrapToolCall()
		if toolMiddleware.Invokable != nil || toolMiddleware.Streamable != nil ||
			toolMiddleware.EnhancedInvokable != nil || toolMiddleware.EnhancedStreamable != nil {
			middlewares = append(middlewares, toolMiddleware)
		}
	}
	return middlewares
}
