// Package tools 提供工具包装器和辅助函数
package tools

import (
	"context"

	"eino-cli/deepagent/core/internal/toolerrors"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ToolRequestPreprocess 工具请求预处理函数
// 在工具调用前执行，可用于修复 JSON 等
type ToolRequestPreprocess func(ctx context.Context, baseTool tool.InvokableTool, toolArguments string) (string, error)

// ToolResponsePostprocess 工具响应后处理函数
// 在工具调用后执行，可用于格式化输出等
type ToolResponsePostprocess func(ctx context.Context, baseTool tool.InvokableTool, toolResponse, toolArguments string) (string, error)

// Mask 决定一个工具是否对外暴露。
// 返回 true 表示保留该工具，返回 false 表示将其从最终工具集合中过滤掉。
type Mask func(ctx context.Context, info *schema.ToolInfo) bool

// ToolInfoRewriter 重写工具的 ToolInfo（name/desc 等）。
// 接收原始 ToolInfo，返回修改后的 ToolInfo。
// 若不需要修改，直接返回原始 info。
type ToolInfoRewriter func(ctx context.Context, info *schema.ToolInfo) *schema.ToolInfo

// CombineMasks 将多个 mask 组合为一个。
// 只有当所有非 nil mask 都返回 true 时，工具才会被保留。
func CombineMasks(masks ...Mask) Mask {
	return func(ctx context.Context, info *schema.ToolInfo) bool {
		for _, mask := range masks {
			if mask != nil && !mask(ctx, info) {
				return false
			}
		}
		return true
	}
}

// WrapTool 包装后的工具
type WrapTool struct {
	baseTool     tool.InvokableTool
	preprocess   []ToolRequestPreprocess
	postprocess  []ToolResponsePostprocess
	infoRewriter ToolInfoRewriter
}

type wrapperCallbacksDisabledKey struct{}

func WithWrapperCallbacksDisabled(ctx context.Context) context.Context {
	return context.WithValue(ctx, wrapperCallbacksDisabledKey{}, true)
}

func wrapperCallbacksDisabled(ctx context.Context) bool {
	disabled, _ := ctx.Value(wrapperCallbacksDisabledKey{}).(bool)
	return disabled
}

// NewWrapTool 创建工具包装器
func NewWrapTool(
	baseTool tool.InvokableTool,
	preprocess []ToolRequestPreprocess,
	postprocess []ToolResponsePostprocess,
) *WrapTool {
	return &WrapTool{
		baseTool:    baseTool,
		preprocess:  preprocess,
		postprocess: postprocess,
	}
}

// Info 返回工具信息
func (w *WrapTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := w.baseTool.Info(ctx)
	if err != nil {
		return nil, err
	}
	if w.infoRewriter != nil {
		info = w.infoRewriter(ctx, info)
	}
	return info, nil
}

func (w *WrapTool) IsCallbacksEnabled() bool {
	return true
}

// InvokableRun 执行工具。
// 模型生成的工具入参错误会以字符串形式返回，让 LLM 有机会修正；
// 运行时控制流错误和工具内部错误需要继续向上传播。
func (w *WrapTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	if wrapperCallbacksDisabled(ctx) {
		return w.invokableRun(ctx, argumentsInJSON, opts...)
	}

	ctx = callbacks.OnStart(ctx, &tool.CallbackInput{ArgumentsInJSON: argumentsInJSON})
	result, err := w.invokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		callbacks.OnError(ctx, err)
		return "", err
	}
	callbacks.OnEnd(ctx, &tool.CallbackOutput{Response: result})
	return result, nil
}

func (w *WrapTool) invokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// 预处理
	processedArgs := argumentsInJSON
	for _, pre := range w.preprocess {
		var err error
		processedArgs, err = pre(ctx, w.baseTool, processedArgs)
		if err != nil {
			// 不返回 error，将错误信息作为字符串返回，避免图停止运行
			return "[Error] Tool preprocess failed: " + err.Error(), nil
		}
	}

	// 执行工具
	result, err := w.baseTool.InvokableRun(ctx, processedArgs, opts...)
	if err != nil {
		if toolerrors.ShouldReturnAsResult(err) {
			return toolerrors.Result(err), nil
		}
		return "", err
	}

	// 后处理
	processedResult := result
	for _, post := range w.postprocess {
		var err error
		processedResult, err = post(ctx, w.baseTool, processedResult, processedArgs)
		if err != nil {
			// 不返回 error，将错误信息作为字符串返回，避免图停止运行
			return "[Error] Tool postprocess failed: " + err.Error(), nil
		}
	}

	return processedResult, nil
}

// StreamableWrapTool wraps streamable-only tools so local argument errors can be
// returned as tool output before Eino's default callback layer records them as errors.
type StreamableWrapTool struct {
	baseTool     tool.StreamableTool
	infoRewriter ToolInfoRewriter
}

func (w *StreamableWrapTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := w.baseTool.Info(ctx)
	if err != nil {
		return nil, err
	}
	if w.infoRewriter != nil {
		info = w.infoRewriter(ctx, info)
	}
	return info, nil
}

func (w *StreamableWrapTool) IsCallbacksEnabled() bool {
	return true
}

func (w *StreamableWrapTool) StreamableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (*schema.StreamReader[string], error) {
	if wrapperCallbacksDisabled(ctx) {
		return w.baseTool.StreamableRun(ctx, argumentsInJSON, opts...)
	}
	ctx = callbacks.OnStart(ctx, &tool.CallbackInput{ArgumentsInJSON: argumentsInJSON})

	result, err := w.baseTool.StreamableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		if toolerrors.ShouldReturnAsResult(err) {
			return observeStringToolStream(ctx, schema.StreamReaderFromArray([]string{toolerrors.Result(err)})), nil
		}
		callbacks.OnError(ctx, err)
		return nil, err
	}
	return observeStringToolStream(ctx, result), nil
}

func observeStringToolStream(ctx context.Context, stream *schema.StreamReader[string]) *schema.StreamReader[string] {
	if stream == nil {
		return nil
	}
	cbStream := schema.StreamReaderWithConvert(stream, func(chunk string) (*tool.CallbackOutput, error) {
		return &tool.CallbackOutput{Response: chunk}, nil
	})
	_, observed := callbacks.OnEndWithStreamOutput(ctx, cbStream)
	return schema.StreamReaderWithConvert(observed, func(chunk *tool.CallbackOutput) (string, error) {
		if chunk == nil {
			return "", nil
		}
		return chunk.Response, nil
	})
}

type EnhancedInvokableWrapTool struct {
	baseTool     tool.EnhancedInvokableTool
	infoRewriter ToolInfoRewriter
}

func (w *EnhancedInvokableWrapTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := w.baseTool.Info(ctx)
	if err != nil {
		return nil, err
	}
	if w.infoRewriter != nil {
		info = w.infoRewriter(ctx, info)
	}
	return info, nil
}

func (w *EnhancedInvokableWrapTool) IsCallbacksEnabled() bool {
	return true
}

func (w *EnhancedInvokableWrapTool) InvokableRun(ctx context.Context, toolArgument *schema.ToolArgument, opts ...tool.Option) (*schema.ToolResult, error) {
	if wrapperCallbacksDisabled(ctx) {
		return w.baseTool.InvokableRun(ctx, toolArgument, opts...)
	}
	ctx = callbacks.OnStart(ctx, toolArgument)

	result, err := w.baseTool.InvokableRun(ctx, toolArgument, opts...)
	if err != nil {
		if toolerrors.ShouldReturnAsResult(err) {
			result = textToolResult(toolerrors.Result(err))
			callbacks.OnEnd(ctx, result)
			return result, nil
		}
		callbacks.OnError(ctx, err)
		return nil, err
	}
	callbacks.OnEnd(ctx, result)
	return result, nil
}

type EnhancedStreamableWrapTool struct {
	baseTool     tool.EnhancedStreamableTool
	infoRewriter ToolInfoRewriter
}

func (w *EnhancedStreamableWrapTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := w.baseTool.Info(ctx)
	if err != nil {
		return nil, err
	}
	if w.infoRewriter != nil {
		info = w.infoRewriter(ctx, info)
	}
	return info, nil
}

func (w *EnhancedStreamableWrapTool) IsCallbacksEnabled() bool {
	return true
}

func (w *EnhancedStreamableWrapTool) StreamableRun(ctx context.Context, toolArgument *schema.ToolArgument, opts ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
	if wrapperCallbacksDisabled(ctx) {
		return w.baseTool.StreamableRun(ctx, toolArgument, opts...)
	}
	ctx = callbacks.OnStart(ctx, toolArgument)

	result, err := w.baseTool.StreamableRun(ctx, toolArgument, opts...)
	if err != nil {
		if toolerrors.ShouldReturnAsResult(err) {
			return observeEnhancedToolStream(ctx, schema.StreamReaderFromArray([]*schema.ToolResult{textToolResult(toolerrors.Result(err))})), nil
		}
		callbacks.OnError(ctx, err)
		return nil, err
	}
	return observeEnhancedToolStream(ctx, result), nil
}

func observeEnhancedToolStream(ctx context.Context, stream *schema.StreamReader[*schema.ToolResult]) *schema.StreamReader[*schema.ToolResult] {
	if stream == nil {
		return nil
	}
	_, observed := callbacks.OnEndWithStreamOutput(ctx, stream)
	return observed
}

func textToolResult(text string) *schema.ToolResult {
	return &schema.ToolResult{
		Parts: []schema.ToolOutputPart{{
			Type: schema.ToolPartTypeText,
			Text: text,
		}},
	}
}

type invokableStreamableWrapTool struct {
	*WrapTool
	*StreamableWrapTool
}

func (w *invokableStreamableWrapTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.WrapTool.Info(ctx)
}

func (w *invokableStreamableWrapTool) IsCallbacksEnabled() bool {
	return true
}

type invokableEnhancedStreamableWrapTool struct {
	*WrapTool
	*EnhancedStreamableWrapTool
}

func (w *invokableEnhancedStreamableWrapTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.WrapTool.Info(ctx)
}

func (w *invokableEnhancedStreamableWrapTool) IsCallbacksEnabled() bool {
	return true
}

type enhancedInvokableStreamableWrapTool struct {
	*EnhancedInvokableWrapTool
	*StreamableWrapTool
}

func (w *enhancedInvokableStreamableWrapTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.EnhancedInvokableWrapTool.Info(ctx)
}

func (w *enhancedInvokableStreamableWrapTool) IsCallbacksEnabled() bool {
	return true
}

type enhancedInvokableEnhancedStreamableWrapTool struct {
	*EnhancedInvokableWrapTool
	*EnhancedStreamableWrapTool
}

func (w *enhancedInvokableEnhancedStreamableWrapTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.EnhancedInvokableWrapTool.Info(ctx)
}

func (w *enhancedInvokableEnhancedStreamableWrapTool) IsCallbacksEnabled() bool {
	return true
}

// ToolRequestRepairJSON 预处理器：修复 JSON
func ToolRequestRepairJSON(ctx context.Context, baseTool tool.InvokableTool, toolArguments string) (string, error) {
	return RepairJSONWithError(toolArguments)
}

// WrapToolsConfig 包装工具的配置
type WrapToolsConfig struct {
	Preprocess   []ToolRequestPreprocess
	Postprocess  []ToolResponsePostprocess
	InfoRewriter ToolInfoRewriter
}

// InfoOnlyWrapTool 只包装 Info 的轻量 wrapper，用于非 InvokableTool 的 BaseTool
type InfoOnlyWrapTool struct {
	baseTool     tool.BaseTool
	infoRewriter ToolInfoRewriter
}

func (w *InfoOnlyWrapTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := w.baseTool.Info(ctx)
	if err != nil {
		return nil, err
	}
	if w.infoRewriter != nil {
		info = w.infoRewriter(ctx, info)
	}
	return info, nil
}

// WrapToolsWithConfig 使用配置批量包装工具
func WrapToolsWithConfig(
	toolList []tool.BaseTool,
	cfg *WrapToolsConfig,
) []tool.BaseTool {
	wrapped := make([]tool.BaseTool, len(toolList))
	for i, t := range toolList {
		invokable, hasInvokable := t.(tool.InvokableTool)
		streamable, hasStreamable := t.(tool.StreamableTool)
		enhancedInvokable, hasEnhancedInvokable := t.(tool.EnhancedInvokableTool)
		enhancedStreamable, hasEnhancedStreamable := t.(tool.EnhancedStreamableTool)

		switch {
		case hasInvokable && hasStreamable:
			wrapped[i] = &invokableStreamableWrapTool{
				WrapTool: &WrapTool{
					baseTool:     invokable,
					preprocess:   cfg.Preprocess,
					postprocess:  cfg.Postprocess,
					infoRewriter: cfg.InfoRewriter,
				},
				StreamableWrapTool: &StreamableWrapTool{
					baseTool:     streamable,
					infoRewriter: cfg.InfoRewriter,
				},
			}
		case hasInvokable && hasEnhancedStreamable:
			wrapped[i] = &invokableEnhancedStreamableWrapTool{
				WrapTool: &WrapTool{
					baseTool:     invokable,
					preprocess:   cfg.Preprocess,
					postprocess:  cfg.Postprocess,
					infoRewriter: cfg.InfoRewriter,
				},
				EnhancedStreamableWrapTool: &EnhancedStreamableWrapTool{
					baseTool:     enhancedStreamable,
					infoRewriter: cfg.InfoRewriter,
				},
			}
		case hasEnhancedInvokable && hasStreamable:
			wrapped[i] = &enhancedInvokableStreamableWrapTool{
				EnhancedInvokableWrapTool: &EnhancedInvokableWrapTool{
					baseTool:     enhancedInvokable,
					infoRewriter: cfg.InfoRewriter,
				},
				StreamableWrapTool: &StreamableWrapTool{
					baseTool:     streamable,
					infoRewriter: cfg.InfoRewriter,
				},
			}
		case hasEnhancedInvokable && hasEnhancedStreamable:
			wrapped[i] = &enhancedInvokableEnhancedStreamableWrapTool{
				EnhancedInvokableWrapTool: &EnhancedInvokableWrapTool{
					baseTool:     enhancedInvokable,
					infoRewriter: cfg.InfoRewriter,
				},
				EnhancedStreamableWrapTool: &EnhancedStreamableWrapTool{
					baseTool:     enhancedStreamable,
					infoRewriter: cfg.InfoRewriter,
				},
			}
		case hasInvokable:
			wrapped[i] = &WrapTool{
				baseTool:     invokable,
				preprocess:   cfg.Preprocess,
				postprocess:  cfg.Postprocess,
				infoRewriter: cfg.InfoRewriter,
			}
		case hasEnhancedInvokable:
			wrapped[i] = &EnhancedInvokableWrapTool{
				baseTool:     enhancedInvokable,
				infoRewriter: cfg.InfoRewriter,
			}
		case hasStreamable:
			wrapped[i] = &StreamableWrapTool{
				baseTool:     streamable,
				infoRewriter: cfg.InfoRewriter,
			}
		case hasEnhancedStreamable:
			wrapped[i] = &EnhancedStreamableWrapTool{
				baseTool:     enhancedStreamable,
				infoRewriter: cfg.InfoRewriter,
			}
		case cfg.InfoRewriter != nil:
			wrapped[i] = &InfoOnlyWrapTool{
				baseTool:     t,
				infoRewriter: cfg.InfoRewriter,
			}
		default:
			wrapped[i] = t
		}
	}
	return wrapped
}

// Deprecated: Use WrapToolsWithConfig instead.
func WrapTools(
	tools []tool.BaseTool,
	preprocess []ToolRequestPreprocess,
	postprocess []ToolResponsePostprocess,
) []tool.BaseTool {
	return WrapToolsWithConfig(tools, &WrapToolsConfig{
		Preprocess:  preprocess,
		Postprocess: postprocess,
	})
}
