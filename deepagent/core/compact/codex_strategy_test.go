package compact

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/utils"
	"eino-cli/deepagent/mock/mock_model"
	serialiser "eino-cli/deepagent/serialiser"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestCodexStrategy_CompactAndResume(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockModel := mock_model.NewMockToolCallingChatModel(ctrl)

	var capturedReq []*schema.Message
	expectedSummary := "deterministic summary"
	mockModel.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			capturedReq = req
			return schema.AssistantMessage(expectedSummary, nil), nil
		}).Times(1)

	// keptLimit=2 tokens; each 8-char message == 2 tokens
	strat := NewCodexStrategy(mockModel, 1, 2, nil)

	// Mixed-role history with controlled token counts (len/4)
	sys := schema.SystemMessage("SYSMSG12")         // 8 chars => 2 tokens
	as1 := schema.AssistantMessage("ASSTMSG1", nil) // 8 chars => 2 tokens
	u1 := schema.UserMessage("USERAAA1")            // 8 chars => 2 tokens
	as2 := schema.AssistantMessage("ASSTMSG2", nil) // 8 chars => 2 tokens
	u2 := schema.UserMessage("USERBBB2")            // 8 chars => 2 tokens

	current := []*agentthread.Message{sys, as1, u1, as2, u2}

	t.Run("CompactStructure", func(t *testing.T) {
		res, err := strat.Compact(ctx, current)
		require.NoError(t, err)
		require.NotNil(t, res)

		// Assert LLM request: system summarization policy + current + user summary request.
		require.Len(t, capturedReq, len(current)+2)
		assert.Equal(t, schema.System, capturedReq[0].Role)
		assert.Equal(t, summarizationPrompt, capturedReq[0].Content)
		for i := 0; i < len(current); i++ {
			assert.Equal(t, current[i].Role, capturedReq[i+1].Role)
			assert.Equal(t, current[i].Content, capturedReq[i+1].Content)
		}
		last := capturedReq[len(capturedReq)-1]
		assert.Equal(t, schema.User, last.Role)
		assert.Equal(t, summarizationUserPrompt, last.Content)

		// Rebuilt should be one compact summary message. Recent user messages are
		// embedded in the summary content instead of emitted as consecutive user
		// messages, because some model providers reject that shape.
		require.Len(t, res.Rebuilt, 1)
		assert.Equal(t, schema.User, res.Rebuilt[0].Role)

		// Summary message assertions
		summary := res.Rebuilt[0]
		assert.Equal(t, schema.User, summary.Role)
		assert.Contains(t, summary.Content, summarizationPrefix+"\n\n"+expectedSummary)
		assert.Contains(t, summary.Content, "Recent user messages preserved before compaction:")
		assert.Contains(t, summary.Content, "USERBBB2")

		// CompactRecord assertions
		require.NotNil(t, res.Compact)
		assert.Equal(t, "codex", res.Compact.CompactStrategyID)
		require.NotNil(t, res.Compact.Summary)
		assert.Equal(t, summary.Content, res.Compact.Summary.Content)

		// Payload JSON assertions
		var payload struct {
			OriginalTokens int               `json:"original_tokens"`
			NewTokens      int               `json:"new_tokens"`
			KeptUserMsgs   []*schema.Message `json:"kept_user_msgs"`
		}
		err = json.Unmarshal([]byte(res.Compact.CompactStrategyPayload), &payload)
		require.NoError(t, err)

		expectedOriginal := utils.SimpleTokenCounter(current)
		expectedNew := utils.SimpleTokenCounter([]*schema.Message{u2})
		assert.Equal(t, expectedOriginal, payload.OriginalTokens)
		assert.Equal(t, expectedNew, payload.NewTokens)
		require.Len(t, payload.KeptUserMsgs, 1)
		assert.Equal(t, schema.User, payload.KeptUserMsgs[0].Role)
		assert.Equal(t, "USERBBB2", payload.KeptUserMsgs[0].Content)

		// Subtest depends on compact output for resume
		t.Run("ResumeOrder", func(t *testing.T) {
			post1 := schema.AssistantMessage("ASSTMSG3", nil) // 8 chars
			post2 := schema.UserMessage("USERCCC3")           // 8 chars
			post := []*agentthread.Message{post1, post2}

			rr, err := strat.Resume(ctx, res.Compact, post)
			require.NoError(t, err)
			require.NotNil(t, rr)
			require.Len(t, rr.Rebuilt, 3)

			// [summary, post...]
			assert.Equal(t, schema.User, rr.Rebuilt[0].Role)
			assert.Contains(t, rr.Rebuilt[0].Content, "USERBBB2")

			assert.Equal(t, schema.Assistant, rr.Rebuilt[1].Role)
			assert.Equal(t, "ASSTMSG3", rr.Rebuilt[1].Content)

			assert.Equal(t, schema.User, rr.Rebuilt[2].Role)
			assert.Equal(t, "USERCCC3", rr.Rebuilt[2].Content)
		})
	})
}

func TestCodexStrategy_CompactRejectsEmptySummary(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockModel := mock_model.NewMockToolCallingChatModel(ctrl)
	mockModel.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(schema.AssistantMessage("  \n\t", nil), nil).
		Times(1)

	strat := NewCodexStrategy(mockModel, 1, 2, nil)
	res, err := strat.Compact(ctx, []*agentthread.Message{schema.UserMessage("hello")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty summary")
	assert.Nil(t, res)
}

func TestCodexStrategyPromptAppend(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockModel := mock_model.NewMockToolCallingChatModel(ctrl)
	var capturedReq []*schema.Message
	mockModel.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			capturedReq = req
			return schema.AssistantMessage("summary", nil), nil
		}).Times(1)

	strat := NewCodexStrategy(mockModel, 1, 2, nil, WithPromptAppend("Preserve unresolved verification risks."))
	_, err := strat.Compact(ctx, []*agentthread.Message{schema.UserMessage("hello")})
	require.NoError(t, err)
	require.NotEmpty(t, capturedReq)
	assert.Contains(t, capturedReq[0].Content, summarizationPrompt)
	assert.Contains(t, capturedReq[0].Content, "Preserve unresolved verification risks.")
}

func TestCodexStrategyCompactSkipsPriorSummaryAsRecentUserMessage(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockModel := mock_model.NewMockToolCallingChatModel(ctrl)
	mockModel.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(schema.AssistantMessage("second summary", nil), nil).
		Times(1)

	strat := NewCodexStrategy(mockModel, 1, 100, nil)
	priorSummary := schema.UserMessage(summarizationPrefix + "\n\nfirst summary")
	current := []*agentthread.Message{
		schema.UserMessage("original request"),
		priorSummary,
		schema.UserMessage("latest request"),
	}

	res, err := strat.Compact(ctx, current)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Contains(t, res.Rebuilt[0].Content, "original request")
	assert.Contains(t, res.Rebuilt[0].Content, "latest request")
	assert.NotContains(t, res.Rebuilt[0].Content, "first summary")

	var payload codexStrategyPayload
	require.NoError(t, json.Unmarshal([]byte(res.Compact.CompactStrategyPayload), &payload))
	require.Len(t, payload.KeptUserMsgs, 2)
	assert.Equal(t, "original request", payload.KeptUserMsgs[0].Content)
	assert.Equal(t, "latest request", payload.KeptUserMsgs[1].Content)
}

func TestCodexStrategyRepeatedCompactThroughContextManagerSkipsPriorSummary(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockModel := mock_model.NewMockToolCallingChatModel(ctrl)
	var capturedReqs [][]*schema.Message
	call := 0
	mockModel.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			capturedReqs = append(capturedReqs, req)
			call++
			switch call {
			case 1:
				return schema.AssistantMessage("first compact summary", nil), nil
			case 2:
				return schema.AssistantMessage("second compact summary", nil), nil
			default:
				t.Fatalf("unexpected compact model call %d", call)
				return nil, nil
			}
		}).Times(2)

	store := newCompactTestHistoryStore()
	strat := NewCodexStrategy(
		mockModel,
		1,
		1000,
		nil,
		WithPromptAppend("Preserve active plan state."),
	)
	cm := agentthread.NewMemoryContextManager("thread-repeat-compact", store, strat, nil)
	latestRequest := "latest request\n- keep this bullet\nand this continuation line"

	require.NoError(t, cm.AddHistory(ctx, "r1", schema.UserMessage("first request")))
	_, err := cm.Compact(ctx, "r1")
	require.NoError(t, err)
	require.Len(t, cm.History(ctx), 1)
	assert.Contains(t, cm.History(ctx)[0].Content, "first compact summary")

	require.NoError(t, cm.AddHistory(ctx, "r2", schema.UserMessage(latestRequest)))

	reloaded := agentthread.NewMemoryContextManager("thread-repeat-compact", store, strat, nil)
	require.NoError(t, reloaded.ReloadHistory(ctx))
	require.Len(t, reloaded.History(ctx), 1)
	assert.Contains(t, reloaded.History(ctx)[0].Content, "first compact summary")
	embedded := embeddedPostCompactUserMessages(reloaded.History(ctx)[0])
	require.Len(t, embedded, 1)
	assert.Equal(t, latestRequest, embedded[0].Content)

	_, err = reloaded.Compact(ctx, "r2")
	require.NoError(t, err)

	require.Len(t, capturedReqs, 2)
	secondReq := capturedReqs[1]
	require.NotEmpty(t, secondReq)
	assert.Contains(t, secondReq[0].Content, "Preserve active plan state.")
	assert.Contains(t, flattenMessageContent(secondReq), "first compact summary")
	assert.Contains(t, flattenMessageContent(secondReq), "latest request")

	history := reloaded.History(ctx)
	require.Len(t, history, 1)
	assert.Contains(t, history[0].Content, "second compact summary")
	assert.Contains(t, history[0].Content, latestRequest)
	assert.NotContains(t, history[0].Content, "first compact summary")

	records, err := store.List(ctx, agentthread.ListQuery{
		ThreadID: "thread-repeat-compact",
		Order:    agentthread.ListOrderASC,
	})
	require.NoError(t, err)
	var latestPayload codexStrategyPayload
	for _, rec := range records {
		if rec == nil || rec.Type != agentthread.HistoryRecordCompact || rec.Ext == nil {
			continue
		}
		require.NoError(t, json.Unmarshal([]byte(rec.Ext.CompactStrategyPayload), &latestPayload))
	}
	require.Len(t, latestPayload.KeptUserMsgs, 1)
	assert.Equal(t, latestRequest, latestPayload.KeptUserMsgs[0].Content)
}

func TestCodexStrategyResumeSanitizesPostCompactMessages(t *testing.T) {
	strat := NewCodexStrategy(nil, 1, 2, nil)
	summary := schema.UserMessage(summarizationPrefix + "\n\nsummary")
	payload := serialiser.ToString(&codexStrategyPayload{
		KeptUserMsgs: []*schema.Message{schema.UserMessage("original request")},
	})
	compact := &agentthread.CompactRecord{
		Summary:                summary,
		CompactStrategyID:      strat.ID(),
		CompactStrategyPayload: payload,
	}

	post := []*schema.Message{
		schema.ToolMessage("orphan result", "orphan_call"),
		schema.UserMessage("next request\n- with bullet\nand continuation"),
	}

	rr, err := strat.Resume(context.Background(), compact, post)
	require.NoError(t, err)
	require.NotNil(t, rr)
	require.Len(t, rr.Rebuilt, 1)
	assert.Equal(t, schema.User, rr.Rebuilt[0].Role)
	assert.Contains(t, rr.Rebuilt[0].Content, "summary")
	assert.Contains(t, rr.Rebuilt[0].Content, "original request")
	embedded := embeddedPostCompactUserMessages(rr.Rebuilt[0])
	require.Len(t, embedded, 1)
	assert.Equal(t, "next request\n- with bullet\nand continuation", embedded[0].Content)
	assert.NotContains(t, rr.Rebuilt[0].Content, "orphan result")
}

func TestCodexStrategyPostCompactMarkerRequiresValidTailBlock(t *testing.T) {
	content := summarizationPrefix + "\n\nsummary mentions " + postCompactUserMessagesBeginMarker + " in normal prose"

	merged := appendPostCompactUserMessages(content, []*schema.Message{schema.UserMessage("first post message")})
	assert.Contains(t, merged, "in normal prose")
	embedded := embeddedPostCompactUserMessages(schema.UserMessage(merged))
	require.Len(t, embedded, 1)
	assert.Equal(t, "first post message", embedded[0].Content)

	merged = appendPostCompactUserMessages(merged, []*schema.Message{schema.UserMessage("second post message")})
	assert.Contains(t, merged, "in normal prose")
	embedded = embeddedPostCompactUserMessages(schema.UserMessage(merged))
	require.Len(t, embedded, 2)
	assert.Equal(t, "first post message", embedded[0].Content)
	assert.Equal(t, "second post message", embedded[1].Content)
}

func flattenMessageContent(messages []*schema.Message) string {
	out := ""
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		out += "\n" + msg.Content
	}
	return out
}

type compactTestHistoryStore struct {
	mu      sync.RWMutex
	records map[string][]*agentthread.HistoryRecord
}

func newCompactTestHistoryStore() *compactTestHistoryStore {
	return &compactTestHistoryStore{records: make(map[string][]*agentthread.HistoryRecord)}
}

func (s *compactTestHistoryStore) Append(_ context.Context, rec *agentthread.HistoryRecord) error {
	if rec == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec.MessageID == 0 {
		rec.MessageID = int64(len(s.records[rec.ThreadID]) + 1)
	}
	if rec.Seq == 0 {
		rec.Seq = int64(len(s.records[rec.ThreadID]) + 1)
	}
	s.records[rec.ThreadID] = append(s.records[rec.ThreadID], rec)
	return nil
}

func (s *compactTestHistoryStore) List(_ context.Context, q agentthread.ListQuery) ([]*agentthread.HistoryRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := s.records[q.ThreadID]
	if len(records) == 0 {
		return nil, nil
	}
	out := make([]*agentthread.HistoryRecord, 0, len(records))
	if q.Order == agentthread.ListOrderDESC {
		for i := len(records) - 1; i >= 0; i-- {
			rec := records[i]
			if rec == nil {
				continue
			}
			if q.TurnID != "" && rec.TurnID != q.TurnID {
				continue
			}
			if q.BeforeID != nil && rec.OrderSeq() >= *q.BeforeID {
				continue
			}
			out = append(out, rec)
			if q.Limit > 0 && len(out) >= q.Limit {
				break
			}
		}
		return out, nil
	}
	for _, rec := range records {
		if rec == nil {
			continue
		}
		if q.TurnID != "" && rec.TurnID != q.TurnID {
			continue
		}
		if q.AfterID != nil && rec.OrderSeq() <= *q.AfterID {
			continue
		}
		out = append(out, rec)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}
