package agentthread

import (
	"context"

	"eino-cli/deepagent/core/graph"
)

type TurnStartRequest struct {
	ThreadID  string
	TurnID    string
	Input     *Message
	InputMeta any
	Resume    *ResumeTurnOptions
}

type TurnConfigProvider func(ctx context.Context, request TurnStartRequest) (*TurnConfig, error)

type OnTurnStartFunc func(ctx context.Context, request TurnStartRequest) context.Context

func applyTurnStart(ctx context.Context, onStart OnTurnStartFunc, request TurnStartRequest) (runCtx context.Context) {
	if onStart == nil {
		return ctx
	}
	request.Input = graph.CopyMessage(request.Input)
	request.Resume = copyResumeTurnOptions(request.Resume)
	runCtx = onStart(ctx, request)
	if runCtx == nil {
		return ctx
	}
	return runCtx
}

func WithTurnStartHook(onStart OnTurnStartFunc) (option SubmitInputOption) {
	option = func(opts *submitInputOptions) {
		opts.OnTurnStart = onStart
	}
	return option
}
