package agentthread

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/checkpointer"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/mock/mock_model"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/mock/gomock"
)

func TestMemoryContextManagerCompactionStoreAndReloadShape(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryHistoryRolloutStore()
	strategy := newCompactionProbeStrategy(100, "summary")
	cm := NewMemoryContextManager("thread-compact-shape", store, strategy, compactionTestContentLengthCounter)

	if err := cm.AddHistory(ctx, "r1", schema.UserMessage("seed")); err != nil {
		t.Fatalf("add user history: %v", err)
	}
	if strategy.CallCount() != 0 {
		t.Fatalf("compact should not trigger below limit")
	}

	longAssistant := schema.AssistantMessage(strings.Repeat("long ", 30), nil)
	if err := cm.AddHistory(ctx, "r1", longAssistant); err != nil {
		t.Fatalf("add assistant history: %v", err)
	}
	if strategy.CallCount() != 0 {
		t.Fatalf("compact should wait for pre-sampling boundary, calls=%d", strategy.CallCount())
	}
	if !cm.CompactNeeded(ctx) {
		t.Fatalf("compact needed = false, want true")
	}
	if _, err := cm.Compact(ctx, "r1"); err != nil {
		t.Fatalf("compact before model request: %v", err)
	}
	if strategy.CallCount() != 1 {
		t.Fatalf("compact calls = %d, want 1", strategy.CallCount())
	}

	history := cm.History(ctx)
	if !compactionTestContentsEqual(history, []string{"summary"}) {
		t.Fatalf("in-memory history after compact = %v, want [summary]", compactionTestMessageContents(history))
	}

	records := compactionTestListRecords(t, ctx, store, "thread-compact-shape")
	if got := compactionTestRecordTypeCount(records, HistoryRecordMessage); got != 2 {
		t.Fatalf("message records = %d, want 2", got)
	}
	if got := compactionTestRecordTypeCount(records, HistoryRecordCompact); got != 1 {
		t.Fatalf("compact records = %d, want 1", got)
	}
	if !compactionTestRecordsContain(records, "seed") || !compactionTestRecordsContain(records, longAssistant.Content) {
		t.Fatalf("pre-compact records should remain in rollout store, got %v", compactionTestRecordContents(records))
	}

	if err := cm.AddHistory(ctx, "r2", schema.UserMessage("post compact")); err != nil {
		t.Fatalf("add post-compact history: %v", err)
	}
	if strategy.CallCount() != 1 {
		t.Fatalf("compact should not retrigger for small post message, calls=%d", strategy.CallCount())
	}

	reloaded := NewMemoryContextManager("thread-compact-shape", store, strategy, compactionTestContentLengthCounter)
	if err := reloaded.ReloadHistory(ctx); err != nil {
		t.Fatalf("reload history: %v", err)
	}
	if !compactionTestContentsEqual(reloaded.History(ctx), []string{"summary", "post compact"}) {
		t.Fatalf("reloaded history = %v, want [summary post compact]", compactionTestMessageContents(reloaded.History(ctx)))
	}
	if got := strategy.ResumeCallCount(); got != 1 {
		t.Fatalf("resume calls = %d, want 1", got)
	}
}

func TestMemoryContextManagerCompactAllowsModelUsageCallbackDuringStrategy(t *testing.T) {
	ctx := context.Background()
	strategy := &recordUsageDuringCompactStrategy{}
	cm := NewMemoryContextManager("thread-compact-reentrant-usage", nil, strategy, compactionTestContentLengthCounter)
	strategy.cm = cm

	if err := cm.AddHistory(ctx, "r1", schema.UserMessage("seed")); err != nil {
		t.Fatalf("add history: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		payload, err := cm.Compact(ctx, "r1")
		if err == nil && payload == nil {
			err = fmt.Errorf("compact returned nil payload")
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("compact returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("compact deadlocked when strategy recorded model usage")
	}

	if !compactionTestContentsEqual(cm.History(ctx), []string{"summary"}) {
		t.Fatalf("history after compact = %v, want [summary]", compactionTestMessageContents(cm.History(ctx)))
	}
}

func TestMemoryContextManagerCompactDoesNotOverwriteConcurrentHistoryChange(t *testing.T) {
	ctx := context.Background()
	strategy := &blockingCompactionStrategy{
		started:          make(chan struct{}),
		release:          make(chan struct{}),
		recordUsageTotal: 9999,
	}
	cm := NewMemoryContextManager("thread-compact-conflict", nil, strategy, compactionTestContentLengthCounter)
	strategy.cm = cm

	if err := cm.AddHistory(ctx, "r1", schema.UserMessage("seed")); err != nil {
		t.Fatalf("add seed history: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		payload, err := cm.Compact(ctx, "r1")
		if err == nil && payload != nil {
			err = fmt.Errorf("compact committed stale snapshot")
		}
		done <- err
	}()

	select {
	case <-strategy.started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("compact strategy did not start")
	}

	if err := cm.AddHistory(ctx, "r1", schema.UserMessage("during compact")); err != nil {
		t.Fatalf("add concurrent history: %v", err)
	}
	close(strategy.release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("compact returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("compact did not return after strategy release")
	}

	if !compactionTestContentsEqual(cm.History(ctx), []string{"seed", "during compact"}) {
		t.Fatalf("history after stale compact = %v, want original history plus concurrent message", compactionTestMessageContents(cm.History(ctx)))
	}
	usage := cm.ContextUsage()
	if usage.Source != ContextUsageSourceEstimated || usage.CurrentTotal != int64(len("seed")+len("during compact")) {
		t.Fatalf("usage after stale compact = %+v, want recomputed estimate for current history", usage)
	}
}

func TestMemoryContextManagerCompactPreservesModelUsageAfterConcurrentHistoryChange(t *testing.T) {
	ctx := context.Background()
	strategy := &blockingCompactionStrategy{
		started:          make(chan struct{}),
		release:          make(chan struct{}),
		recordUsageTotal: 9999,
		limit:            50,
	}
	cm := NewMemoryContextManager("thread-compact-conflict-model-usage", nil, strategy, compactionTestContentLengthCounter)
	strategy.cm = cm

	if err := cm.AddHistory(ctx, "r1", schema.UserMessage("seed")); err != nil {
		t.Fatalf("add seed history: %v", err)
	}
	cm.RecordModelUsage(ctx, &model.TokenUsage{PromptTokens: 40, CompletionTokens: 20, TotalTokens: 60})
	if !cm.CompactNeeded(ctx) {
		t.Fatalf("compact should be pending from provider model usage")
	}

	done := make(chan error, 1)
	go func() {
		payload, err := cm.Compact(ctx, "r1")
		if err == nil && payload != nil {
			err = fmt.Errorf("compact committed stale snapshot")
		}
		done <- err
	}()

	select {
	case <-strategy.started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("compact strategy did not start")
	}

	if err := cm.AddHistory(ctx, "r1", schema.UserMessage("more")); err != nil {
		t.Fatalf("add concurrent history: %v", err)
	}
	close(strategy.release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("compact returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("compact did not return after strategy release")
	}

	usage := cm.ContextUsage()
	if usage.Source != ContextUsageSourceModelUsage || usage.CurrentTotal < 60 {
		t.Fatalf("usage after stale compact = %+v, want preserved provider usage baseline", usage)
	}
	if !cm.CompactNeeded(ctx) {
		t.Fatalf("compact should still be pending after stale compact")
	}
}

func TestMemoryContextManagerCompactRestoresUsageWhenStrategyDoesNotCommit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		strategy func(cm *MemoryContextManager) CompactionStrategy
		store    HistoryRolloutStore
	}{
		{
			name: "skip",
			strategy: func(cm *MemoryContextManager) CompactionStrategy {
				return &recordUsageThenNoCommitStrategy{cm: cm, limit: 1000}
			},
		},
		{
			name: "error",
			strategy: func(cm *MemoryContextManager) CompactionStrategy {
				return &recordUsageThenNoCommitStrategy{cm: cm, limit: 1000, err: fmt.Errorf("compact failed")}
			},
		},
		{
			name:  "persist_fail",
			store: compactAppendFailStore{},
			strategy: func(cm *MemoryContextManager) CompactionStrategy {
				return &recordUsageDuringCompactStrategy{cm: cm, limit: 1000}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cm := NewMemoryContextManager("thread-compact-no-commit-"+tc.name, tc.store, nil, compactionTestContentLengthCounter)
			cm.strategy = tc.strategy(cm)
			if err := cm.AddHistory(ctx, "r1", schema.UserMessage("seed")); err != nil {
				t.Fatalf("add seed history: %v", err)
			}
			cm.RecordModelUsage(ctx, &model.TokenUsage{PromptTokens: 40, CompletionTokens: 20, TotalTokens: 60})
			if err := cm.AddHistory(ctx, "r1", schema.UserMessage("more")); err != nil {
				t.Fatalf("add post-model history: %v", err)
			}
			before := cm.ContextUsage()

			_, _ = cm.Compact(ctx, "r1")

			if after := cm.ContextUsage(); after != before {
				t.Fatalf("usage after uncommitted compact = %+v, want %+v", after, before)
			}
			if cm.CompactNeeded(ctx) {
				t.Fatalf("compact pending should be restored after uncommitted compact")
			}
			if !compactionTestContentsEqual(cm.History(ctx), []string{"seed", "more"}) {
				t.Fatalf("history after uncommitted compact = %v, want original history", compactionTestMessageContents(cm.History(ctx)))
			}
		})
	}
}

func TestAgentThreadCompactionContinuesWithRebuiltHistory(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	strategy := newCompactionProbeStrategy(100, "summary")
	var streamCall int32
	longAssistant := strings.Repeat("long ", 30)
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			callIndex := atomic.AddInt32(&streamCall, 1)
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()
			switch callIndex {
			case 1:
				if !compactionTestHasContent(input, "seed") {
					t.Fatalf("first model input should contain original user message, got %v", compactionTestMessageContents(input))
				}
				sw.Send(schema.AssistantMessage(longAssistant, nil), nil)
			case 2:
				contents := compactionTestMessageContents(input)
				if !compactionTestHasContent(input, "summary") || !compactionTestHasContent(input, "next") {
					t.Fatalf("second model input should contain compact summary and new user, got %v", contents)
				}
				if compactionTestHasContent(input, "seed") || compactionTestHasContent(input, longAssistant) {
					t.Fatalf("second model input leaked pre-compact messages: %v", contents)
				}
				sw.Send(schema.AssistantMessage("ok", nil), nil)
			default:
				t.Fatalf("unexpected model stream call %d with input %v", callIndex, compactionTestMessageContents(input))
			}
			return sr, nil
		},
	).AnyTimes()

	store := NewInMemoryHistoryRolloutStore()
	thread := NewDefault("thread-compact-continue", &TurnRunnerConfig{
		ChatModel:       cm,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, make(chan Event, 128), DefaultThreadOptions{
		HistoryStore:       store,
		CompactionStrategy: strategy,
		TokenCounter:       compactionTestContentLengthCounter,
	})
	if err := thread.Init(ctx); err != nil {
		t.Fatalf("init thread: %v", err)
	}
	if err := runUserInput(ctx, thread, "r1", "seed"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if strategy.CallCount() != 0 {
		t.Fatalf("compact should wait for next pre-sampling boundary, calls=%d", strategy.CallCount())
	}

	if err := runUserInput(ctx, thread, "r2", "next"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if strategy.CallCount() != 1 {
		t.Fatalf("compact calls after second pre-sampling boundary = %d, want 1", strategy.CallCount())
	}
	if got := atomic.LoadInt32(&streamCall); got != 2 {
		t.Fatalf("model stream calls = %d, want 2", got)
	}
}

func TestAgentThreadCompactionBeforeApproveResume(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	strategy := newCompactionProbeStrategy(100, "summary")
	longAssistant := strings.Repeat("long ", 30)
	var streamCall int32
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			callIndex := atomic.AddInt32(&streamCall, 1)
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()
			switch callIndex {
			case 1:
				sw.Send(schema.AssistantMessage(longAssistant, nil), nil)
			case 2:
				contents := compactionTestMessageContents(input)
				if !compactionTestHasContent(input, "summary") || !compactionTestHasContent(input, "needs approval") {
					t.Fatalf("approval turn should start from compacted history, got %v", contents)
				}
				if compactionTestHasContent(input, "seed") || compactionTestHasContent(input, longAssistant) {
					t.Fatalf("approval turn leaked pre-compact messages: %v", contents)
				}
				sw.Send(schema.AssistantMessage("call approval", []schema.ToolCall{{
					ID:   "approval_call",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "approval_tool",
						Arguments: `{"task":"approve"}`,
					},
				}}), nil)
			case 3:
				contents := compactionTestMessageContents(input)
				if !compactionTestHasContent(input, "summary") {
					t.Fatalf("resume model input should retain compact summary, got %v", contents)
				}
				if compactionTestHasContent(input, "seed") || compactionTestHasContent(input, longAssistant) {
					t.Fatalf("resume model input leaked pre-compact messages: %v", contents)
				}
				if !compactionTestHasToolCallID(input, "approval_call") {
					t.Fatalf("resume model input should contain approved tool result, got %v", contents)
				}
				sw.Send(schema.AssistantMessage("done", nil), nil)
			default:
				t.Fatalf("unexpected model stream call %d with input %v", callIndex, compactionTestMessageContents(input))
			}
			return sr, nil
		},
	).AnyTimes()

	approvedBase := &countingToolResult{name: "approval_tool", result: `{"approved":true}`}
	approvalTool := deeptools.NewInvokableApprovableTool(approvedBase, func(context.Context, *deeptools.ApprovalInfo) bool {
		return true
	})

	bus := make(chan Event, 256)
	store := NewInMemoryHistoryRolloutStore()
	thread := NewDefault("thread-compact-resume", &TurnRunnerConfig{
		ChatModel:       cm,
		Tools:           []tool.BaseTool{approvalTool},
		CheckpointStore: checkpointer.NewInMemoryStore(),
		HITLConfig: &deepagents.HITLConfig{
			NeedApproveTools: map[string]deeptools.NeedApproval{
				"approval_tool": func(context.Context, *deeptools.ApprovalInfo) bool { return true },
			},
		},
	}, bus, DefaultThreadOptions{
		HistoryStore:       store,
		CompactionStrategy: strategy,
		TokenCounter:       compactionTestContentLengthCounter,
	})
	if err := thread.Init(ctx); err != nil {
		t.Fatalf("init thread: %v", err)
	}

	if err := runUserInput(ctx, thread, "r1", "seed"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if strategy.CallCount() != 0 {
		t.Fatalf("compact should wait for approval turn pre-sampling boundary, calls=%d", strategy.CallCount())
	}

	collector := &eventCollector{}
	go collector.collect(bus)

	runErrCh := runUserInputAsync(ctx, thread, "r2", "needs approval")
	ev, ok := collector.waitFor(t, EventApproveRequested, 3*time.Second)
	if !ok {
		t.Fatalf("missing approve_requested event")
	}
	payload := ev.Payload.(ApprovalRequiredPayload)
	resume := map[string]any{payload.InterruptID: &deeptools.ApprovalResult{Approved: true}}
	if _, err := thread.ResumeTurn(ctx, "r2", ResumeTurnOptions{
		CheckpointID:       payload.CheckpointID,
		ResumeInterruptIDs: []string{payload.InterruptID},
		ResumeData:         resume,
	}); err != nil {
		t.Fatalf("resume turn: %v", err)
	}
	if err := <-runErrCh; err != nil {
		t.Fatalf("initial interrupted run should complete after resume: %v", err)
	}
	waitUntilEventfully(t, 3*time.Second, func() bool {
		return approvedBase.RunCount() == 1 && hasEventType(collector.snapshot(), EventTurnEnd)
	}, "resume did not finish")
	if got := atomic.LoadInt32(&streamCall); got != 3 {
		t.Fatalf("model stream calls = %d, want 3", got)
	}
	if strategy.CallCount() != 1 {
		t.Fatalf("compact calls after approval turn pre-sampling boundary = %d, want 1", strategy.CallCount())
	}

	reloaded := NewMemoryContextManager("thread-compact-resume", store, strategy, compactionTestContentLengthCounter)
	if err := reloaded.ReloadHistory(ctx); err != nil {
		t.Fatalf("reload after resume: %v", err)
	}
	reloadedContents := compactionTestMessageContents(reloaded.History(ctx))
	if !compactionTestHasContent(reloaded.History(ctx), "summary") || compactionTestContainsString(reloadedContents, "seed") || compactionTestContainsString(reloadedContents, longAssistant) {
		t.Fatalf("reloaded history after resume has wrong shape: %v", reloadedContents)
	}
}

func TestIntegrationAgentThreadCompactionWithRealModel(t *testing.T) {
	if os.Getenv("DEEPAGENT_MODEL_INTEGRATION") != "1" {
		t.Skip("set DEEPAGENT_MODEL_INTEGRATION=1 to run real model integration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	chatModel := newIntegrationArkModel(t)
	strategy := newCompactionProbeStrategy(80, "real-smoke-summary")
	store := NewInMemoryHistoryRolloutStore()
	threadID := "thread-real-compact"
	eventBus := make(chan Event, 4096)
	go func() {
		for range eventBus {
		}
	}()
	thread := NewDefault(threadID, &TurnRunnerConfig{
		ChatModel:       chatModel,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, eventBus, DefaultThreadOptions{
		HistoryStore:       store,
		CompactionStrategy: strategy,
		TokenCounter:       compactionTestContentLengthCounter,
	})
	if err := thread.Init(ctx); err != nil {
		t.Fatalf("init thread: %v", err)
	}

	if err := runUserInput(ctx, thread, "r1", "请用不少于120个汉字概述 Go context 的作用。"); err != nil {
		t.Fatalf("first real-model run: %v", err)
	}
	if strategy.CallCount() == 0 {
		t.Fatalf("expected real-model first run to trigger compaction")
	}
	if got := compactionTestRecordTypeCount(compactionTestListRecords(t, ctx, store, threadID), HistoryRecordCompact); got == 0 {
		t.Fatalf("expected compact record after real-model first run")
	}

	if err := runUserInput(ctx, thread, "r2", "只回复 OK"); err != nil {
		t.Fatalf("second real-model run after compaction: %v", err)
	}

	reloaded := NewMemoryContextManager(threadID, store, strategy, compactionTestContentLengthCounter)
	if err := reloaded.ReloadHistory(ctx); err != nil {
		t.Fatalf("reload real-model compacted history: %v", err)
	}
	if !compactionTestHasContent(reloaded.History(ctx), "real-smoke-summary") {
		t.Fatalf("reloaded real-model history missing compact summary: %v", compactionTestMessageContents(reloaded.History(ctx)))
	}
}

type compactionProbeStrategy struct {
	mu             sync.Mutex
	limit          int64
	summaryContent string
	compactInputs  [][]string
	resumePosts    [][]string
}

type recordUsageDuringCompactStrategy struct {
	cm    *MemoryContextManager
	limit int64
}

func (s *recordUsageDuringCompactStrategy) ID() string {
	return "record_usage_during_compact"
}

func (s *recordUsageDuringCompactStrategy) AutoCompactTokenLimit() int64 {
	return s.limit
}

func (s *recordUsageDuringCompactStrategy) Compact(ctx context.Context, current []*Message) (*CompactionResult, error) {
	_ = current
	s.cm.RecordModelUsage(ctx, &model.TokenUsage{TotalTokens: 1})
	summary := schema.UserMessage("summary")
	return &CompactionResult{
		Compact: &CompactRecord{
			Summary:           summary,
			CompactStrategyID: s.ID(),
		},
		Rebuilt: []*Message{summary},
	}, nil
}

func (s *recordUsageDuringCompactStrategy) Resume(ctx context.Context, compact *CompactRecord, postCompactMessages []*Message) (*ResumeResult, error) {
	_ = ctx
	return &ResumeResult{Rebuilt: append([]*Message{compact.Summary}, postCompactMessages...)}, nil
}

type blockingCompactionStrategy struct {
	cm               *MemoryContextManager
	recordUsageTotal int
	limit            int64
	started          chan struct{}
	release          chan struct{}
}

func (s *blockingCompactionStrategy) ID() string {
	return "blocking_compact"
}

func (s *blockingCompactionStrategy) AutoCompactTokenLimit() int64 {
	return s.limit
}

func (s *blockingCompactionStrategy) Compact(ctx context.Context, current []*Message) (*CompactionResult, error) {
	_ = current
	close(s.started)
	if s.cm != nil && s.recordUsageTotal > 0 {
		s.cm.RecordModelUsage(ctx, &model.TokenUsage{TotalTokens: s.recordUsageTotal})
	}
	<-s.release
	summary := schema.UserMessage("summary")
	return &CompactionResult{
		Compact: &CompactRecord{
			Summary:           summary,
			CompactStrategyID: s.ID(),
		},
		Rebuilt: []*Message{summary},
	}, nil
}

func (s *blockingCompactionStrategy) Resume(ctx context.Context, compact *CompactRecord, postCompactMessages []*Message) (*ResumeResult, error) {
	_ = ctx
	return &ResumeResult{Rebuilt: append([]*Message{compact.Summary}, postCompactMessages...)}, nil
}

type recordUsageThenNoCommitStrategy struct {
	cm    *MemoryContextManager
	limit int64
	err   error
}

func (s *recordUsageThenNoCommitStrategy) ID() string {
	return "record_usage_then_no_commit"
}

func (s *recordUsageThenNoCommitStrategy) AutoCompactTokenLimit() int64 {
	return s.limit
}

func (s *recordUsageThenNoCommitStrategy) Compact(ctx context.Context, current []*Message) (*CompactionResult, error) {
	_ = current
	s.cm.RecordModelUsage(ctx, &model.TokenUsage{TotalTokens: 9999})
	if s.err != nil {
		return nil, s.err
	}
	return nil, nil
}

func (s *recordUsageThenNoCommitStrategy) Resume(ctx context.Context, compact *CompactRecord, postCompactMessages []*Message) (*ResumeResult, error) {
	_ = ctx
	return &ResumeResult{Rebuilt: append([]*Message{compact.Summary}, postCompactMessages...)}, nil
}

type compactAppendFailStore struct{}

func (compactAppendFailStore) Append(ctx context.Context, rec *HistoryRecord) error {
	_ = ctx
	if rec != nil && rec.Type == HistoryRecordCompact {
		return fmt.Errorf("append compact failed")
	}
	return nil
}

func (compactAppendFailStore) List(ctx context.Context, q ListQuery) ([]*HistoryRecord, error) {
	_ = ctx
	_ = q
	return nil, nil
}

func newCompactionProbeStrategy(limit int64, summaryContent string) *compactionProbeStrategy {
	return &compactionProbeStrategy{limit: limit, summaryContent: summaryContent}
}

func (s *compactionProbeStrategy) ID() string {
	return "compaction_probe"
}

func (s *compactionProbeStrategy) AutoCompactTokenLimit() int64 {
	return s.limit
}

func (s *compactionProbeStrategy) Compact(ctx context.Context, current []*Message) (*CompactionResult, error) {
	_ = ctx
	s.mu.Lock()
	s.compactInputs = append(s.compactInputs, compactionTestMessageContents(current))
	s.mu.Unlock()

	summary := schema.UserMessage(s.summaryContent)
	return &CompactionResult{
		Compact: &CompactRecord{
			Summary:                summary,
			CompactStrategyID:      s.ID(),
			CompactStrategyPayload: "probe",
		},
		Rebuilt: []*Message{summary},
	}, nil
}

func (s *compactionProbeStrategy) Resume(ctx context.Context, compact *CompactRecord, postCompactMessages []*Message) (*ResumeResult, error) {
	_ = ctx
	s.mu.Lock()
	s.resumePosts = append(s.resumePosts, compactionTestMessageContents(postCompactMessages))
	s.mu.Unlock()
	rebuilt := append([]*Message{compact.Summary}, postCompactMessages...)
	return &ResumeResult{Rebuilt: rebuilt}, nil
}

func (s *compactionProbeStrategy) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.compactInputs)
}

func (s *compactionProbeStrategy) ResumeCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.resumePosts)
}

func compactionTestContentLengthCounter(messages []*schema.Message) int {
	total := 0
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		total += len(msg.Content)
	}
	return total
}

func compactionTestListRecords(t testing.TB, ctx context.Context, store HistoryRolloutStore, threadID string) []*HistoryRecord {
	t.Helper()
	records, err := store.List(ctx, ListQuery{ThreadID: threadID, Order: ListOrderASC})
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	return records
}

func compactionTestRecordTypeCount(records []*HistoryRecord, typ HistoryRecordType) int {
	count := 0
	for _, record := range records {
		if record != nil && record.Type == typ {
			count++
		}
	}
	return count
}

func compactionTestRecordsContain(records []*HistoryRecord, content string) bool {
	for _, record := range records {
		if record != nil && record.Message != nil && record.Message.Content == content {
			return true
		}
	}
	return false
}

func compactionTestHasContent(messages []*schema.Message, content string) bool {
	for _, msg := range messages {
		if msg != nil && msg.Content == content {
			return true
		}
	}
	return false
}

func compactionTestHasToolCallID(messages []*schema.Message, callID string) bool {
	for _, msg := range messages {
		if msg != nil && msg.ToolCallID == callID {
			return true
		}
	}
	return false
}

func compactionTestContentsEqual(messages []*schema.Message, want []string) bool {
	got := compactionTestMessageContents(messages)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func compactionTestContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func compactionTestMessageContents(messages []*schema.Message) []string {
	contents := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			contents = append(contents, "<nil>")
			continue
		}
		contents = append(contents, msg.Content)
	}
	return contents
}

func compactionTestRecordContents(records []*HistoryRecord) []string {
	contents := make([]string, 0, len(records))
	for _, record := range records {
		if record == nil || record.Message == nil {
			contents = append(contents, "<nil>")
			continue
		}
		contents = append(contents, record.Message.Content)
	}
	return contents
}
