package inprocess

import "context"

import "eino-cli/deepagent/worker"

// ThreadStateStore persists and lists the local worker's durable thread state.
//
// It stores durable thread facts and resume-critical state, but it does not
// store live runtime objects such as channels, goroutines, or current
// active/idle execution state.
//
// Implementations may store state in memory, SQL, KV, or delegate to a
// business-owned service. The SDK does not assume any particular backend.
type ThreadStateStore interface {
	CreateThread(ctx context.Context, spec CreateThreadSpec) (*ThreadState, error)
	GetThread(ctx context.Context, threadID string) (*ThreadState, error)
	ListThreads(ctx context.Context, opts ListThreadsOptions) ([]*ThreadState, error)
	ListThreadsBySession(ctx context.Context, sessionID string, opts ListThreadsOptions) ([]*ThreadState, error)
	UpdateThread(ctx context.Context, threadID string, patch UpdateThreadStatePatch) (*ThreadState, error)
}

// EventStore persists and replays local worker events.
type EventStore interface {
	AppendEvent(ctx context.Context, ev *agentworker.Event) error
	ListEvents(ctx context.Context, threadID string, opts ListEventsOptions) ([]*agentworker.Event, error)
}
