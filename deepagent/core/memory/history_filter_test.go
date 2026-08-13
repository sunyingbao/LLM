package memory

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/constant"
	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func TestSerializeHistoryForStage1RetainsUserAssistantAndValuableToolResults(t *testing.T) {
	out, updated, err := SerializeHistoryForStage1(context.Background(), []*agentthread.HistoryRecord{
		historyMessage("thread-1", "turn-1", 1, schema.UserMessage("remember concrete requirements"), 10),
		historyMessage("thread-1", "turn-1", 2, schema.AssistantMessage("I will inspect the code first.", nil), 11),
		historyMessage("thread-1", "turn-1", 3, schema.ToolMessage(`{"path":"docs/features/memory/plan.md"}`, "call-1", schema.WithToolName("read_file")), 12),
	}, HistoryFilterConfig{})
	require.NoError(t, err)
	require.Equal(t, time.Unix(12, 0), updated)

	items := decodeSerializedHistory(t, out)
	require.Len(t, items, 3)
	require.Equal(t, "user", items[0].Role)
	require.Equal(t, "remember concrete requirements", items[0].Content)
	require.Equal(t, "assistant", items[1].Role)
	require.Equal(t, "tool", items[2].Role)
	require.Contains(t, items[2].Content, "docs/features/memory")
}

func TestSerializeHistoryForStage1SkipsSystemDeveloperControlEmptyToolAndCompact(t *testing.T) {
	out, updated, err := SerializeHistoryForStage1(context.Background(), []*agentthread.HistoryRecord{
		historyMessage("thread-1", "turn-1", 1, schema.SystemMessage("internal prompt"), 10),
		historyMessage("thread-1", "turn-1", 2, &schema.Message{Role: schema.RoleType("developer"), Content: "developer note"}, 11),
		historyMessage("thread-1", "turn-1", 3, schema.ToolMessage(`{"plan":[{"step":"x"}]}`, "call-1", schema.WithToolName(constant.ToolUpdatePlan)), 12),
		historyMessage("thread-1", "turn-1", 4, schema.ToolMessage("   ", "call-2", schema.WithToolName("read_file")), 13),
		{
			Type:      agentthread.HistoryRecordCompact,
			ThreadID:  "thread-1",
			TurnID:    "turn-1",
			MessageID: 5,
			Message:   schema.AssistantMessage("compact summary should not enter stage1 evidence", nil),
			CreateAt:  14,
		},
		historyMessage("thread-1", "turn-1", 6, schema.UserMessage("real user signal"), 15),
	}, HistoryFilterConfig{})
	require.NoError(t, err)
	require.Equal(t, time.Unix(15, 0), updated)

	items := decodeSerializedHistory(t, out)
	require.Len(t, items, 1)
	require.Equal(t, "user", items[0].Role)
	require.Equal(t, "real user signal", items[0].Content)
	require.NotContains(t, out, "internal prompt")
	require.NotContains(t, out, "developer note")
	require.NotContains(t, out, constant.ToolUpdatePlan)
	require.NotContains(t, out, "compact summary")
}

func TestSerializeHistoryForStage1BoundsRecordsAndContentBytes(t *testing.T) {
	out, updated, err := SerializeHistoryForStage1(context.Background(), []*agentthread.HistoryRecord{
		historyMessage("thread-1", "turn-1", 1, schema.UserMessage("first"), 10),
		historyMessage("thread-1", "turn-1", 2, schema.AssistantMessage("second message with long tail", nil), 11),
		historyMessage("thread-1", "turn-1", 3, schema.UserMessage("third should be dropped by record cap"), 12),
	}, HistoryFilterConfig{MaxRecords: 2, MaxContentBytes: 12})
	require.NoError(t, err)
	require.Equal(t, time.Unix(12, 0), updated)

	items := decodeSerializedHistory(t, out)
	require.Len(t, items, 2)
	require.Equal(t, "first", items[0].Content)
	require.Equal(t, "second ", items[1].Content)
	require.LessOrEqual(t, totalSerializedContentBytes(items), 12)
	require.NotContains(t, out, "third should be dropped")
}

func TestSerializeHistoryForStage1KeepsScanningUpdatedTimeAfterContentBudget(t *testing.T) {
	out, updated, err := SerializeHistoryForStage1(context.Background(), []*agentthread.HistoryRecord{
		historyMessage("thread-1", "turn-1", 1, schema.UserMessage(strings.Repeat("a", 50)), 10),
		historyMessage("thread-1", "turn-1", 2, schema.AssistantMessage("later record after content budget", nil), 99),
	}, HistoryFilterConfig{MaxRecords: 20, MaxContentBytes: 8})
	require.NoError(t, err)
	require.Equal(t, time.Unix(99, 0), updated)

	items := decodeSerializedHistory(t, out)
	require.Len(t, items, 1)
	require.Equal(t, strings.Repeat("a", 8), items[0].Content)
	require.NotContains(t, out, "later record after content budget")
}

func decodeSerializedHistory(t *testing.T, raw string) []serializedHistoryMessage {
	t.Helper()
	var items []serializedHistoryMessage
	require.NoError(t, sonic.UnmarshalString(raw, &items))
	return items
}

func totalSerializedContentBytes(items []serializedHistoryMessage) int {
	var total int
	for _, item := range items {
		total += len([]byte(item.Content))
	}
	return total
}

func historyMessage(threadID, turnID string, messageID int64, msg *schema.Message, createAt int64) *agentthread.HistoryRecord {
	return &agentthread.HistoryRecord{
		Type:      agentthread.HistoryRecordMessage,
		ThreadID:  threadID,
		TurnID:    turnID,
		MessageID: messageID,
		Message:   msg,
		CreateAt:  createAt,
	}
}

func TestSerializeHistoryForStage1ReturnsJSONForNoRetainedMessages(t *testing.T) {
	out, updated, err := SerializeHistoryForStage1(context.Background(), []*agentthread.HistoryRecord{
		historyMessage("thread-1", "turn-1", 1, schema.SystemMessage("system only"), 10),
	}, HistoryFilterConfig{})
	require.NoError(t, err)
	require.Equal(t, time.Unix(10, 0), updated)
	require.Equal(t, "[]", strings.TrimSpace(out))
}

func TestBuildStage1HistoryInputUsesBoundedHeadTailReads(t *testing.T) {
	ctx := context.Background()
	store := &recordingHistoryStore{}
	for i := int64(1); i <= 10; i++ {
		require.NoError(t, store.Append(ctx, historyMessage("thread-1", "turn-1", i, schema.UserMessage("message-"+strconv.FormatInt(i, 10)), 100+i)))
	}

	input, err := buildStage1HistoryInput(ctx, store, RunStage1Request{
		SourceThreadID: "thread-1",
		SourceTurnID:   "turn-1",
	}, Stage1HistoryInputConfig{
		HeadRecords:    2,
		TailRecords:    3,
		MaxInputTokens: 1000,
	})
	require.NoError(t, err)

	require.Len(t, store.queries, 2)
	require.Equal(t, agentthread.ListOrderASC, store.queries[0].Order)
	require.Equal(t, 2, store.queries[0].Limit)
	require.Equal(t, agentthread.ListOrderDESC, store.queries[1].Order)
	require.Equal(t, 3, store.queries[1].Limit)

	items := decodeSerializedHistory(t, input.Contents)
	require.Len(t, items, 5)
	require.Equal(t, []int64{1, 2, 8, 9, 10}, serializedMessageIDs(items))
	require.Contains(t, input.Contents, "message-1")
	require.Contains(t, input.Contents, "message-10")
	require.NotContains(t, input.Contents, "message-5")
	require.Equal(t, time.Unix(110, 0), input.SourceUpdatedAt)
}

func TestBuildStage1HistoryInputAppliesFilterAndTokenBudgets(t *testing.T) {
	ctx := context.Background()
	store := &recordingHistoryStore{}
	require.NoError(t, store.Append(ctx, historyMessage("thread-1", "", 1, schema.UserMessage("# AGENTS.md instructions for /repo\n\n<INSTRUCTIONS>\nnoise\n</INSTRUCTIONS>"), 100)))
	require.NoError(t, store.Append(ctx, historyMessage("thread-1", "", 2, schema.UserMessage("<skill>\nbody\n</skill>"), 101)))
	require.NoError(t, store.Append(ctx, historyMessage("thread-1", "", 3, schema.UserMessage("business-cache should be filtered by upper layer"), 102)))
	require.NoError(t, store.Append(ctx, historyMessage("thread-1", "", 4, schema.ToolMessage(strings.Repeat("t", 200), "tool-call", schema.WithToolName("read_file")), 103)))
	require.NoError(t, store.Append(ctx, historyMessage("thread-1", "", 5, schema.UserMessage("final stable lesson should stay"), 104)))

	input, err := buildStage1HistoryInput(ctx, store, RunStage1Request{SourceThreadID: "thread-1"}, Stage1HistoryInputConfig{
		HeadRecords:      5,
		TailRecords:      5,
		MaxInputTokens:   80,
		MaxMessageTokens: 4,
		KeepRecord: func(rec *agentthread.HistoryRecord) bool {
			return !strings.Contains(rec.Message.Content, "business-cache")
		},
	})
	require.NoError(t, err)

	require.NotContains(t, input.Contents, "AGENTS.md")
	require.NotContains(t, input.Contents, "<skill>")
	require.NotContains(t, input.Contents, "business-cache")
	require.NotContains(t, input.Contents, strings.Repeat("t", 20))
	require.Contains(t, input.Contents, "final stable")
	require.LessOrEqual(t, input.EstimatedTokens, 80)
	items := decodeSerializedHistory(t, input.Contents)
	for _, item := range items {
		require.LessOrEqual(t, estimateStage1Tokens(item.Content), 4)
	}
}

func serializedMessageIDs(items []serializedHistoryMessage) []int64 {
	out := make([]int64, 0, len(items))
	for _, item := range items {
		out = append(out, item.MessageID)
	}
	return out
}

type recordingHistoryStore struct {
	records []*agentthread.HistoryRecord
	queries []agentthread.ListQuery
}

func (s *recordingHistoryStore) Append(_ context.Context, rec *agentthread.HistoryRecord) error {
	if rec != nil && rec.Seq == 0 {
		rec.Seq = int64(len(s.records) + 1)
	}
	s.records = append(s.records, rec)
	return nil
}

func (s *recordingHistoryStore) List(_ context.Context, q agentthread.ListQuery) ([]*agentthread.HistoryRecord, error) {
	s.queries = append(s.queries, q)
	var out []*agentthread.HistoryRecord
	for _, rec := range s.records {
		if q.ThreadID != "" && rec.ThreadID != q.ThreadID {
			continue
		}
		if q.TurnID != "" && rec.TurnID != q.TurnID {
			continue
		}
		if q.Order == agentthread.ListOrderDESC && q.BeforeID != nil && rec.OrderSeq() >= *q.BeforeID {
			continue
		}
		if q.Order != agentthread.ListOrderDESC && q.AfterID != nil && rec.OrderSeq() <= *q.AfterID {
			continue
		}
		out = append(out, rec)
	}
	if q.Order == agentthread.ListOrderDESC {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}
