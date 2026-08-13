package agentthread

import (
	"context"

	"eino-cli/deepagent/core/graph"
)

// TurnRunnerStartRequest describes a turn runner that is about to start. It is
// passed to OnTurnRunnerStartFunc only after DeepAgentThread decides a run can
// start and the turn ID is final. It is not delivered when input is queued into
// an already active turn.
type TurnRunnerStartRequest struct {
	ThreadID string
	TurnID   string
	Trigger  TurnRunnerConfigTrigger

	Input     *Message
	InputMeta any
	Resume    *ResumeTurnConfigRequest
}

// OnTurnRunnerStartFunc derives the run context for one turn runner. It is
// called after the turn ID is final and before the TurnRunnerConfig resolver,
// runner.Init and RunTurn, so middleware and resolvers observe the returned
// context. Returning nil keeps the original context.
//
// The hook runs under the thread mutex with the same constraints as the
// TurnRunnerConfig resolver: it must be fast, must not block on IO, and must not
// call back into the same DeepAgentThread.
type OnTurnRunnerStartFunc func(ctx context.Context, req TurnRunnerStartRequest) context.Context

// WithTurnRunnerStartHook installs an OnTurnRunnerStartFunc for a single
// SubmitInput call. It is ignored when the input is queued into an active turn.
func WithTurnRunnerStartHook(f OnTurnRunnerStartFunc) SubmitInputOption {
	return func(opts *SubmitInputOptions) {
		opts.OnTurnRunnerStart = f
	}
}

// applyTurnRunnerStart invokes the hook (if any) and returns the run context for
// this turn. A nil hook or nil return keeps the original context.
func applyTurnRunnerStart(ctx context.Context, hook OnTurnRunnerStartFunc, req TurnRunnerStartRequest) context.Context {
	if hook == nil {
		return ctx
	}
	req.Input = graph.CopyMessage(req.Input)
	req.Resume = copyResumeTurnConfigRequest(req.Resume)
	if runCtx := hook(ctx, req); runCtx != nil {
		return runCtx
	}
	return ctx
}
