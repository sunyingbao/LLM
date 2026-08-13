package deepagents

import (
	"context"
	"fmt"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/tracing"
	"eino-cli/deepagent/core/types"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func (a *DeepAgent) newRunContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, "deep_agent", a)
}

// prepareRun 执行 Run/Stream 共享的初始化逻辑：
// BeforeAgent hook、callback 注入、checkpoint 选项、resume 上下文。
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

	var resumeMode bool
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

	stream = schema.StreamReaderWithConvert(stream, func(msg *schema.Message) (*schema.Message, error) {
		return msg, nil
	}, schema.WithErrWrapper(func(err error) error {
		if _, ok := compose.ExtractInterruptInfo(err); ok {
			_ = a.graphState.Save(ctx, runOpt.CheckpointID)
		}
		return err
	}))
	return stream, nil
}
