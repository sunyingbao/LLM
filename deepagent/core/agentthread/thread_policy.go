package agentthread

import (
	"context"

	deepagents "eino-cli/deepagent/core"
)

// threadReactLoopPolicy adapts thread-local pending-input state to the
// generic DeepAgent react-loop branch policy.
type threadReactLoopPolicy struct {
	commitEndIfNoPending func() bool
}

func (t *DeepAgentThread) newReactLoopPolicy() deepagents.ReactLoopBranchPolicy {
	return &threadReactLoopPolicy{commitEndIfNoPending: t.commitEndIfNoPending}
}

func (p *threadReactLoopPolicy) AfterModel(ctx context.Context, input deepagents.ReactLoopAfterModelInput) (deepagents.ReactLoopBranchDecision, error) {
	_ = ctx
	if input.Default != deepagents.ReactLoopBranchToEnd {
		return deepagents.ReactLoopBranchDefault, nil
	}
	if p.commitEndIfNoPending() {
		return deepagents.ReactLoopBranchDefault, nil
	}
	return deepagents.ReactLoopBranchToExecutor, nil
}

func (p *threadReactLoopPolicy) AfterTools(ctx context.Context, input deepagents.ReactLoopAfterToolsInput) (deepagents.ReactLoopBranchDecision, error) {
	_ = ctx
	_ = input
	return deepagents.ReactLoopBranchDefault, nil
}
