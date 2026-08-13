package inprocess

import (
	"context"

	"eino-cli/deepagent/worker"
)

// ThreadFactory creates one thread-scoped runtime for the given persisted
// thread state.
//
// The worker argument is the host that is currently creating the runtime. It
// lets runtime-level collaboration tools spawn and communicate with sibling
// threads without requiring the factory to keep a long-lived back reference to
// the worker.
type ThreadFactory func(ctx context.Context, state *ThreadState, worker *Worker) (agentworker.AgentThread, error)
