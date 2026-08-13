package agentthread

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func TestTokenUsageTrackerUsesModelUsageAsAuthoritativeBaseline(t *testing.T) {
	tracker := NewTokenUsageTracker(1000, nil)

	tracker.AddLocalMessages([]*schema.Message{schema.UserMessage("12345678")})
	require.Equal(t, ContextUsageSourceEstimated, tracker.Current().Source)
	require.Equal(t, int64(2), tracker.Current().CurrentTotal)

	tracker.RecordModelUsage(context.Background(), &model.TokenUsage{
		PromptTokens:     70,
		CompletionTokens: 30,
		TotalTokens:      100,
	})
	snapshot := tracker.Current()
	require.Equal(t, ContextUsageSourceModelUsage, snapshot.Source)
	require.Equal(t, int64(100), snapshot.CurrentTotal)
	require.Equal(t, int64(100), snapshot.LastModelTotal)

	tracker.AddLocalMessages([]*schema.Message{
		schema.AssistantMessage("assistant output already counted by model usage", nil),
		schema.UserMessage("1234"),
	})
	snapshot = tracker.Current()
	require.Equal(t, int64(101), snapshot.CurrentTotal)
	require.Equal(t, int64(1), snapshot.EstimatedAfterLastModel)
}

func TestTokenUsageTrackerCurrentTotalDoesNotDropBelowEstimate(t *testing.T) {
	tracker := NewTokenUsageTracker(1000, nil)

	tracker.AddLocalMessages([]*schema.Message{schema.UserMessage(strings.Repeat("x", 400))})
	require.Equal(t, int64(100), tracker.Current().CurrentTotal)

	tracker.RecordModelUsage(context.Background(), &model.TokenUsage{TotalTokens: 60})
	snapshot := tracker.Current()
	require.Equal(t, ContextUsageSourceEstimated, snapshot.Source)
	require.Equal(t, int64(100), snapshot.CurrentTotal)
	require.Equal(t, int64(60), snapshot.LastModelTotal)
}

func TestTokenUsageTrackerRecomputeFallsBackToEstimate(t *testing.T) {
	tracker := NewTokenUsageTracker(1000, nil)
	tracker.RecordModelUsage(context.Background(), &model.TokenUsage{TotalTokens: 100})

	tracker.Recompute(context.Background(), []*schema.Message{schema.UserMessage("12345678")})
	snapshot := tracker.Current()
	require.Equal(t, ContextUsageSourceEstimated, snapshot.Source)
	require.Equal(t, int64(2), snapshot.CurrentTotal)
	require.Equal(t, int64(0), snapshot.LastModelTotal)
}

func TestMemoryContextManagerCompactsBeforeModelRequestWithModelUsageLimit(t *testing.T) {
	strategy := &limitCompactionStrategy{limit: 50}
	cm := NewMemoryContextManager("t1", nil, strategy, nil)

	require.NoError(t, cm.AddHistory(context.Background(), "r1", schema.UserMessage("12345678")))
	require.Equal(t, 0, strategy.calls)

	cm.RecordModelUsage(context.Background(), &model.TokenUsage{TotalTokens: 60})
	require.NoError(t, cm.AddHistory(context.Background(), "r1", schema.UserMessage("1234")))
	require.Equal(t, 0, strategy.calls)

	require.NoError(t, cm.AddHistory(context.Background(), "r1", schema.AssistantMessage("assistant done", nil)))
	require.Equal(t, 0, strategy.calls)

	require.True(t, cm.CompactNeeded(context.Background()))
	_, err := cm.Compact(context.Background(), "r1")
	require.NoError(t, err)
	require.Equal(t, 1, strategy.calls)
	require.Equal(t, ContextUsageSourceEstimated, cm.ContextUsage().Source)
	require.Less(t, cm.ContextUsage().CurrentTotal, int64(50))
}

func TestMemoryContextManagerCompactsBeforeFollowupSamplingAfterToolResult(t *testing.T) {
	strategy := &limitCompactionStrategy{limit: 50}
	cm := NewMemoryContextManager("t1", nil, strategy, nil)

	require.NoError(t, cm.AddHistory(context.Background(), "r1", schema.UserMessage("run a tool")))
	cm.RecordModelUsage(context.Background(), &model.TokenUsage{TotalTokens: 60})
	require.NoError(t, cm.AddHistory(context.Background(), "r1", schema.AssistantMessage("call tool", []schema.ToolCall{{
		ID: "call_1",
		Function: schema.FunctionCall{
			Name:      "echo",
			Arguments: `{"value":"hello"}`,
		},
	}})))
	require.Equal(t, 0, strategy.calls)

	require.NoError(t, cm.AddHistory(context.Background(), "r1", schema.ToolMessage("hello", "call_1", schema.WithToolName("echo"))))
	require.Equal(t, 0, strategy.calls)

	require.True(t, cm.CompactNeeded(context.Background()))
	_, err := cm.Compact(context.Background(), "r1")
	require.NoError(t, err)
	require.Equal(t, 1, strategy.calls)
	require.Equal(t, []string{"summary"}, compactionTestMessageContents(cm.History(context.Background())))
	require.Contains(t, strategy.lastInput, "call tool")
	require.Contains(t, strategy.lastInput, "hello")
}

type limitCompactionStrategy struct {
	limit     int64
	calls     int
	lastInput []string
}

func (s *limitCompactionStrategy) ID() string {
	return "limit_test"
}

func (s *limitCompactionStrategy) AutoCompactTokenLimit() int64 {
	return s.limit
}

func (s *limitCompactionStrategy) Compact(ctx context.Context, current []*Message) (*CompactionResult, error) {
	_ = ctx
	s.calls++
	s.lastInput = compactionTestMessageContents(current)
	summary := schema.UserMessage("summary")
	return &CompactionResult{
		Compact: &CompactRecord{
			Summary:           summary,
			CompactStrategyID: s.ID(),
		},
		Rebuilt: []*Message{summary},
	}, nil
}

func (s *limitCompactionStrategy) Resume(ctx context.Context, compact *CompactRecord, postCompactMessages []*Message) (*ResumeResult, error) {
	_ = ctx
	rebuilt := append([]*Message{compact.Summary}, postCompactMessages...)
	return &ResumeResult{Rebuilt: rebuilt}, nil
}

func TestTokenUsageTrackerRecordModelUsageSavesState(t *testing.T) {
	store := &recordingContextStateStore{}
	tracker := NewTokenUsageTracker(128000, nil,
		WithTrackerThreadID("t1"),
		WithTrackerStateStore(store),
	)

	tracker.RecordModelUsage(context.Background(), &model.TokenUsage{
		PromptTokens:     50,
		CompletionTokens: 20,
		TotalTokens:      70,
	})

	require.Len(t, store.states, 1)
	got := store.states[0]
	require.Equal(t, "t1", got.ThreadID)
	require.Equal(t, int64(70), got.Usage.CurrentTotal)
	require.Equal(t, int64(128000), got.Usage.ContextWindow)
	require.Equal(t, ContextUsageSourceModelUsage, got.Usage.Source)
	require.Greater(t, got.UpdatedAtMS, int64(0))
}

func TestTokenUsageTrackerRecomputeSavesState(t *testing.T) {
	store := &recordingContextStateStore{}
	tracker := NewTokenUsageTracker(128000, nil,
		WithTrackerThreadID("t1"),
		WithTrackerStateStore(store),
	)

	tracker.Recompute(context.Background(), []*schema.Message{schema.UserMessage("12345678")})

	require.Len(t, store.states, 1)
	got := store.states[0]
	require.Equal(t, "t1", got.ThreadID)
	require.Equal(t, int64(2), got.Usage.CurrentTotal)
	require.Equal(t, ContextUsageSourceEstimated, got.Usage.Source)
}

func TestTokenUsageTrackerNoStoreNoSave(t *testing.T) {
	// No store registered — should not panic.
	tracker := NewTokenUsageTracker(128000, nil)
	tracker.RecordModelUsage(context.Background(), &model.TokenUsage{TotalTokens: 50})
	tracker.Recompute(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	// If we reach here without panic, the test passes.
}
