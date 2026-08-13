//go:build !windows

package worker

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"eino-cli/deepagent/core/memory"
	"eino-cli/deepagent/worker"
)

func (b *threadBuilder) prepareMemoryConsolidationResources(ctx context.Context, spec threadSpec) (threadResources, error) {
	if b.deps.HistoryStore == nil {
		return threadResources{}, fmt.Errorf("cloudagent: memory consolidation history store provider is required")
	}
	if b.deps.CheckpointStore == nil {
		return threadResources{}, fmt.Errorf("cloudagent: memory consolidation checkpoint store provider is required")
	}
	history, err := b.deps.HistoryStore(ctx, spec.ThreadID)
	if err != nil {
		return threadResources{}, fmt.Errorf("init memory history store: %w", err)
	}
	checkpoint, err := b.deps.CheckpointStore(ctx, spec.ThreadID)
	if err != nil {
		return threadResources{}, fmt.Errorf("init memory checkpoint store: %w", err)
	}
	return threadResources{
		EventBus:   nil,
		WorkDir:    "",
		History:    history,
		Checkpoint: checkpoint,
	}, nil
}

func (b *threadBuilder) newMemoryConsolidationAgentThread(ctx context.Context, spec threadSpec, resources threadResources, turnProfile ResolvedTurnProfile) (agentworker.AgentThread, error) {
	modelProfile := turnProfile.Model
	metadata := spec.Info.GetMetadata()
	parsedMetadata, err := memory.ParseStage2Metadata(metadata)
	if err != nil {
		return newStaleMemoryConsolidationThread(spec.ThreadID, fmt.Sprintf("parse memory stage2 metadata: %v", err)), nil
	}
	if err := b.deps.MemoryStore.ValidateStage2Thread(ctx, memory.ValidateStage2ThreadRequest{
		UserID:         parsedMetadata.UserID,
		OwnershipToken: parsedMetadata.OwnershipToken,
		ThreadID:       spec.ThreadID,
		ValidatedAt:    time.Now(),
	}); err != nil {
		return newStaleMemoryConsolidationThread(spec.ThreadID, fmt.Sprintf("authenticate memory stage2 thread: %v", err)), nil
	}
	return memory.NewConsolidationAgentThread(memory.ConsolidationAgentThreadConfig{
		ThreadID:        spec.ThreadID,
		Metadata:        metadata,
		ChatModel:       modelProfile.ChatModel,
		HistoryStore:    resources.History,
		CheckpointStore: resources.Checkpoint,
		Callbacks:       turnProfile.Capabilities.Callbacks,
		EventIDProvider: b.eventID,
		Store:           b.deps.MemoryStore,
		Workspace:       b.deps.MemoryWorkspace.ForUser(parsedMetadata.UserID),
		LeaseTTL:        b.stage2LeaseTTL(),
		RetryDelay:      b.stage2RetryDelay(),
	})
}

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
