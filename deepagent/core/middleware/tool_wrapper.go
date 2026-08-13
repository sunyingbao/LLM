package middleware

import (
	"context"

	"eino-cli/deepagent/core/internal/toolerrors"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// WrapAllToolInputs 对四种工具端点统一应用输入修改函数，并允许 middleware 直接返回结果。
func WrapAllToolInputs(fn func(ctx context.Context, input *compose.ToolInput) (result string, handled bool)) compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if result, handled := fn(ctx, input); handled {
					ctx = emitSyntheticToolStart(ctx, input)
					emitSyntheticToolEnd(ctx, input.CallID, result)
					return &compose.ToolOutput{Result: result}, nil
				}
				output, err := next(ctx, input)
				if err != nil && toolerrors.ShouldReturnAsResult(err) {
					result := toolerrors.Result(err)
					emitSyntheticToolEnd(ctx, input.CallID, result)
					return &compose.ToolOutput{Result: result}, nil
				}
				return output, err
			}
		},
		Streamable: func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
				if result, handled := fn(ctx, input); handled {
					ctx = emitSyntheticToolStart(ctx, input)
					return &compose.StreamToolOutput{Result: syntheticStreamToolResult(ctx, input.CallID, result)}, nil
				}
				output, err := next(ctx, input)
				if err != nil && toolerrors.ShouldReturnAsResult(err) {
					return &compose.StreamToolOutput{Result: syntheticStreamToolResult(ctx, input.CallID, toolerrors.Result(err))}, nil
				}
				return output, err
			}
		},
		EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
				if result, handled := fn(ctx, input); handled {
					ctx = emitSyntheticToolStart(ctx, input)
					emitSyntheticEnhancedToolEnd(ctx, input.CallID, result)
					return &compose.EnhancedInvokableToolOutput{Result: textToolResult(result)}, nil
				}
				output, err := next(ctx, input)
				if err != nil && toolerrors.ShouldReturnAsResult(err) {
					result := toolerrors.Result(err)
					emitSyntheticEnhancedToolEnd(ctx, input.CallID, result)
					return &compose.EnhancedInvokableToolOutput{Result: textToolResult(result)}, nil
				}
				return output, err
			}
		},
		EnhancedStreamable: func(next compose.EnhancedStreamableToolEndpoint) compose.EnhancedStreamableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedStreamableToolOutput, error) {
				if result, handled := fn(ctx, input); handled {
					ctx = emitSyntheticToolStart(ctx, input)
					return &compose.EnhancedStreamableToolOutput{Result: syntheticEnhancedStreamToolResult(ctx, input.CallID, result)}, nil
				}
				output, err := next(ctx, input)
				if err != nil && toolerrors.ShouldReturnAsResult(err) {
					return &compose.EnhancedStreamableToolOutput{Result: syntheticEnhancedStreamToolResult(ctx, input.CallID, toolerrors.Result(err))}, nil
				}
				return output, err
			}
		},
	}
}

func emitSyntheticToolStart(ctx context.Context, input *compose.ToolInput) context.Context {
	if input == nil {
		return ctx
	}
	return callbacks.OnStart(ctx, &tool.CallbackInput{
		ArgumentsInJSON: input.Arguments,
		Extra:           toolCallbackExtra(input.CallID),
	})
}

func emitSyntheticToolEnd(ctx context.Context, callID string, result string) {
	_ = callbacks.OnEnd(ctx, &tool.CallbackOutput{Response: result, Extra: toolCallbackExtra(callID)})
}

func emitSyntheticEnhancedToolEnd(ctx context.Context, callID string, result string) {
	_ = callbacks.OnEnd(ctx, &tool.CallbackOutput{ToolOutput: textToolResult(result), Extra: toolCallbackExtra(callID)})
}

func syntheticStreamToolResult(ctx context.Context, callID string, result string) *schema.StreamReader[string] {
	stream := schema.StreamReaderFromArray([]string{result})
	callbackStream := schema.StreamReaderWithConvert(stream, func(chunk string) (*tool.CallbackOutput, error) {
		return &tool.CallbackOutput{Response: chunk, Extra: toolCallbackExtra(callID)}, nil
	})
	_, observed := callbacks.OnEndWithStreamOutput(ctx, callbackStream)
	return schema.StreamReaderWithConvert(observed, func(chunk *tool.CallbackOutput) (string, error) {
		if chunk == nil {
			return "", nil
		}
		return chunk.Response, nil
	})
}

func syntheticEnhancedStreamToolResult(ctx context.Context, callID string, result string) *schema.StreamReader[*schema.ToolResult] {
	stream := schema.StreamReaderFromArray([]*schema.ToolResult{textToolResult(result)})
	callbackStream := schema.StreamReaderWithConvert(stream, func(chunk *schema.ToolResult) (*tool.CallbackOutput, error) {
		return &tool.CallbackOutput{ToolOutput: chunk, Extra: toolCallbackExtra(callID)}, nil
	})
	_, observed := callbacks.OnEndWithStreamOutput(ctx, callbackStream)
	return schema.StreamReaderWithConvert(observed, func(chunk *tool.CallbackOutput) (*schema.ToolResult, error) {
		if chunk == nil {
			return nil, nil
		}
		return chunk.ToolOutput, nil
	})
}

func toolCallbackExtra(callID string) map[string]any {
	if callID == "" {
		return nil
	}
	return map[string]any{"tool_call_id": callID}
}

func textToolResult(text string) *schema.ToolResult {
	return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: text}}}
}
