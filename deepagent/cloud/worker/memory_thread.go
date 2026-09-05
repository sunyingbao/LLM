//go:build !windows

package worker

import (
	"context"
	agentworker "eino-cli/deepagent/worker"
	"strings"
	"sync"
)

type staleMemoryConsolidationThread struct {
	threadID string
	reason   string
	output   chan agentworker.ThreadOutputItem

	mu     sync.Mutex
	closed bool
	yield  bool
}

func newStaleMemoryConsolidationThread(threadID string, reason string) agentworker.AgentThread {
	return &staleMemoryConsolidationThread{
		threadID: threadID,
		reason:   reason,
		output:   make(chan agentworker.ThreadOutputItem, 1),
	}
}

func (t *staleMemoryConsolidationThread) Init(context.Context) (*agentworker.ThreadOutput, error) {
	return &agentworker.ThreadOutput{Items: t.output}, nil
}

func (t *staleMemoryConsolidationThread) PostMessage(ctx context.Context, msg *agentworker.Message) (*agentworker.PostMessageResult, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, agentworker.ErrThreadClosed
	}
	if t.yield {
		t.mu.Unlock()
		return nil, nil
	}
	t.yield = true
	t.mu.Unlock()

	go t.emitYield(context.WithoutCancel(ctx))
	return nil, nil
}

func (t *staleMemoryConsolidationThread) Interrupt(context.Context, agentworker.ThreadInterruptRequest) error {
	return nil
}

func (t *staleMemoryConsolidationThread) ActiveTurn() *agentworker.ActiveTurn {
	return nil
}

func (t *staleMemoryConsolidationThread) Close(context.Context) error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return nil
}

func (t *staleMemoryConsolidationThread) emitYield(ctx context.Context) {
	reason := strings.TrimSpace(t.reason)
	if reason == "" {
		reason = "stale memory consolidation thread"
	}
	select {
	case t.output <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: reason}}:
	case <-ctx.Done():
	}
}
