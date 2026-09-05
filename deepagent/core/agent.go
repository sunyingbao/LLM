// Package deepagents 提供基于中间件架构的深度代理实现
package deepagents

import (
	"context"
	"fmt"
	"sync"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/tracing"
	"eino-cli/deepagent/core/types"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// DeepAgent 实现了基于中间件架构的深度代理。
type DeepAgent struct {
	runnable              compose.Runnable[[]*schema.Message, *schema.Message]
	middlewareChain       *middleware.MiddlewareChain
	backend               backends.Backend
	callbacks             []callbacks.Handler
	graphState            *types.GraphState
	graphInterruptMu      sync.Mutex
	graphInterruptHandle  middleware.GraphInterruptHandle
	graphInterruptUsed    bool
	pendingGraphInterrupt []compose.GraphInterruptOption
	depth                 int
}

// GetGraphRunnable 返回编译完成的 Eino Runnable。
func (a *DeepAgent) GetGraphRunnable() compose.Runnable[[]*schema.Message, *schema.Message] {
	return a.runnable
}

// GraphState 返回 Agent 的持久化图状态。
func (a *DeepAgent) GraphState() *types.GraphState {
	return a.graphState
}

// Depth 返回当前 Agent 的嵌套深度。
func (a *DeepAgent) Depth() int {
	return a.depth
}

// Name 返回 Agent 对应的图名称。
func (a *DeepAgent) Name() string {
	return constant.GraphName
}

func (a *DeepAgent) newRunContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, "deep_agent", a)
}

// prepareRun 执行 Run/Stream 共享的初始化逻辑：BeforeAgent hook、callback、checkpoint 与 resume 上下文。
func (a *DeepAgent) prepareRun(ctx context.Context, opts []RunOptionFunc) (context.Context, *RunOptions, error) {
	runOpt := applyRunOptions(opts)
	runCtx, interrupt := compose.WithGraphInterrupt(ctx)
	a.setGraphInterruptHandle(interrupt)
	ctx = runCtx
	if err := a.middlewareChain.BeforeAgent(ctx); err != nil {
		return ctx, nil, fmt.Errorf("middleware BeforeAgent failed: %w", err)
	}
	if len(a.callbacks) > 0 {
		runOpt.composeOpts = append(runOpt.composeOpts, compose.WithCallbacks(a.callbacks...))
	}
	if runOpt.CheckpointID != "" {
		runOpt.composeOpts = append(runOpt.composeOpts, compose.WithCheckPointID(runOpt.CheckpointID))
	}
	if runOpt.WriteToCheckpointID != "" {
		runOpt.composeOpts = append(runOpt.composeOpts, compose.WithWriteToCheckPointID(runOpt.WriteToCheckpointID))
	}
	if runOpt.ForceNewRun {
		runOpt.composeOpts = append(runOpt.composeOpts, compose.WithForceNewRun())
	}
	resumeMode := false
	if len(runOpt.ResumeData) > 0 {
		ctx = compose.BatchResumeWithData(ctx, runOpt.ResumeData)
		resumeMode = true
	} else if len(runOpt.ResumeInterruptIDs) > 0 {
		ctx = compose.Resume(ctx, runOpt.ResumeInterruptIDs...)
		resumeMode = true
	}
	if resumeMode {
		if err := a.graphState.Resume(ctx, runOpt.CheckpointID); err != nil {
			logs.CtxWarn(ctx, "[DeepAgent::prepareRun] resume graph state failed: %v", err)
		}
	}
	runCtx = types.NewStateContext(ctx, a.graphState)
	runCtx = a.newRunContext(runCtx)
	return runCtx, runOpt, nil
}

// Run 同步执行 Agent。
func (a *DeepAgent) Run(ctx context.Context, messages []*schema.Message, opts ...RunOptionFunc) (*schema.Message, error) {
	ctx, runOpt, err := a.prepareRun(ctx, opts)
	if err != nil {
		return nil, err
	}
	logger := tracing.NewLoggerFromContext(ctx, "DeepAgent")
	timer := logger.Start(tracing.StageAgentRun, fmt.Sprintf("messages=%d", len(messages)))
	result, err := a.runnable.Invoke(ctx, messages, runOpt.composeOpts...)
	if err != nil {
		if _, ok := compose.ExtractInterruptInfo(err); ok {
			_ = a.graphState.Save(ctx, runOpt.CheckpointID)
			return nil, err
		}
		timer.Error(err, tracing.SourceGraph)
		return result, tracing.WrapError(err, tracing.StageAgentRun, tracing.SourceGraph, tracing.GetRequestID(ctx))
	}
	timer.Success(fmt.Sprintf("content_length=%d | tool_calls=%d", len(result.Content), len(result.ToolCalls)))
	return result, nil
}

// Stream 流式执行 Agent。
func (a *DeepAgent) Stream(ctx context.Context, messages []*schema.Message, opts ...RunOptionFunc) (*schema.StreamReader[*schema.Message], error) {
	ctx, runOpt, err := a.prepareRun(ctx, opts)
	if err != nil {
		return nil, err
	}
	logger := tracing.NewLoggerFromContext(ctx, "DeepAgent")
	stream, err := a.runnable.Stream(ctx, messages, runOpt.composeOpts...)
	if err != nil {
		if _, ok := compose.ExtractInterruptInfo(err); ok {
			_ = a.graphState.Save(ctx, runOpt.CheckpointID)
			return nil, err
		}
		logger.Error(tracing.StageAgentStream, err, tracing.SourceGraph)
		return nil, tracing.WrapError(err, tracing.StageAgentStream, tracing.SourceGraph, tracing.GetRequestID(ctx))
	}
	stream = schema.StreamReaderWithConvert(stream, func(msg *schema.Message) (*schema.Message, error) { return msg, nil }, schema.WithErrWrapper(func(err error) error {
		if _, ok := compose.ExtractInterruptInfo(err); ok {
			_ = a.graphState.Save(ctx, runOpt.CheckpointID)
		}
		return err
	}))
	return stream, nil
}

func (a *DeepAgent) setGraphInterruptHandle(handle middleware.GraphInterruptHandle) {
	if a == nil {
		return
	}
	var pending []compose.GraphInterruptOption
	a.graphInterruptMu.Lock()
	a.graphInterruptHandle = handle
	a.graphInterruptUsed = false
	if handle != nil && a.pendingGraphInterrupt != nil {
		pending = a.pendingGraphInterrupt
		a.pendingGraphInterrupt = nil
		a.graphInterruptUsed = true
	}
	a.graphInterruptMu.Unlock()
	if handle != nil && pending != nil {
		handle(pending...)
	}
}

// Interrupt 中断当前正在执行的 Agent 图；若图尚未启动，则暂存中断请求。
func (a *DeepAgent) Interrupt(opts ...compose.GraphInterruptOption) (accepted bool) {
	if a == nil {
		return false
	}
	// nil 表示没有请求；非 nil 的空切片表示无参中断。
	copiedOpts := append(make([]compose.GraphInterruptOption, 0, len(opts)), opts...)
	var handle middleware.GraphInterruptHandle
	a.graphInterruptMu.Lock()
	if a.graphInterruptHandle == nil {
		a.pendingGraphInterrupt = copiedOpts
		a.graphInterruptMu.Unlock()
		return true
	}
	if a.graphInterruptUsed {
		a.graphInterruptMu.Unlock()
		return true
	}
	handle = a.graphInterruptHandle
	a.graphInterruptUsed = true
	a.graphInterruptMu.Unlock()
	handle(copiedOpts...)
	return true
}

// Close 目前无需清理，预留接口供 subAgentRunnerAdapter 实现 SubAgentRunner。
func (a *DeepAgent) Close(ctx context.Context) error { return nil }
