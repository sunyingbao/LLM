package agentthread

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// 简易内存历史存储用于测试
type memStore struct{ recs []*HistoryRecord }

func (s *memStore) Append(ctx context.Context, rec *HistoryRecord) error {
	if rec != nil && rec.MessageID == 0 {
		rec.MessageID = int64(len(s.recs) + 1)
	}
	if rec != nil && rec.Seq == 0 {
		rec.Seq = int64(len(s.recs) + 1)
	}
	s.recs = append(s.recs, rec)
	return nil
}
func (s *memStore) List(ctx context.Context, q ListQuery) ([]*HistoryRecord, error) {
	return s.recs, nil
}

type errStore struct {
	err error
}

func (s errStore) Append(context.Context, *HistoryRecord) error {
	return s.err
}

func (s errStore) List(context.Context, ListQuery) ([]*HistoryRecord, error) {
	return nil, s.err
}

type recordingContextStateStore struct {
	states []ContextState
	err    error
}

func (s *recordingContextStateStore) Save(ctx context.Context, state ContextState) error {
	_ = ctx
	s.states = append(s.states, state)
	return s.err
}

func TestMemoryContextManager_AddHistoryReturnsPersistError(t *testing.T) {
	want := errors.New("persist failed")
	cm := NewMemoryContextManager("t1", errStore{err: want}, nil, nil)

	err := cm.AddHistory(context.Background(), "r1", schema.UserMessage("hello"))
	if !errors.Is(err, want) {
		t.Fatalf("AddHistory() error = %v, want %v", err, want)
	}
	if got := len(snapshotMessages(cm)); got != 0 {
		t.Fatalf("messages len = %d, want 0 when persist fails", got)
	}
}

func TestMemoryContextManager_HistoryRecordUniqueKeyProvider(t *testing.T) {
	ms := &memStore{}
	cm := NewMemoryContextManager("t1", ms, nil, nil,
		WithHistoryRecordUniqueKeyProvider(func(ctx context.Context, threadID, turnID string, msg *schema.Message) string {
			return threadID + ":" + turnID + ":" + msg.Content
		}),
	)

	if err := cm.AddHistory(context.Background(), "r1", schema.UserMessage("hello")); err != nil {
		t.Fatalf("AddHistory() error = %v", err)
	}
	if len(ms.recs) != 1 {
		t.Fatalf("persisted records = %d, want 1", len(ms.recs))
	}
	if ms.recs[0].UniqueKey != "t1:r1:hello" {
		t.Fatalf("UniqueKey = %q, want provider value", ms.recs[0].UniqueKey)
	}
}

func TestMemoryContextManager_HistoryRecordDefaultUniqueKeyAndCreateAtMS(t *testing.T) {
	ms := &memStore{}
	cm := NewMemoryContextManager("t1", ms, nil, nil)

	if err := cm.AddHistory(context.Background(), "r1", schema.UserMessage("hello")); err != nil {
		t.Fatalf("AddHistory() error = %v", err)
	}
	if len(ms.recs) != 1 {
		t.Fatalf("persisted records = %d, want 1", len(ms.recs))
	}
	if ms.recs[0].UniqueKey == "" {
		t.Fatal("UniqueKey is empty")
	}
	if ms.recs[0].CreateAtMS <= 0 {
		t.Fatalf("CreateAtMS = %d, want positive millis", ms.recs[0].CreateAtMS)
	}
}

func TestMemoryContextManager_RecordModelUsageSavesOptionalContextState(t *testing.T) {
	store := &recordingContextStateStore{}
	cm := NewMemoryContextManager("t1", &memStore{}, nil, nil,
		WithContextUsageModelName("seed"),
		WithContextWindow(128000),
		WithTokenUsageTrackerOpts(WithTrackerStateStore(store)),
	)

	cm.RecordModelUsage(context.Background(), &model.TokenUsage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
	})

	if len(store.states) != 1 {
		t.Fatalf("saved states = %d, want 1", len(store.states))
	}
	got := store.states[0]
	if got.ThreadID != "t1" {
		t.Fatalf("state identity = %+v", got)
	}
	if got.Usage.CurrentTotal != 15 || got.Usage.Source != ContextUsageSourceModelUsage {
		t.Fatalf("usage = %+v, want model usage total 15", got.Usage)
	}
	if got.Usage.ContextWindow != 128000 {
		t.Fatalf("usage.ContextWindow = %d, want 128000", got.Usage.ContextWindow)
	}
	if got.UpdatedAtMS <= 0 {
		t.Fatalf("UpdatedAtMS = %d, want positive millis", got.UpdatedAtMS)
	}
}

// 自定义消息ID提供器注入测试
func TestMemoryContextManager_MessageIDProvider(t *testing.T) {
	ctx := context.Background()
	ms := &memStore{}
	cm := NewMemoryContextManager("t1", ms, nil, nil)

	// 外部提供器：从 1000 开始递增
	var base int64 = 1000
	cm.SetMessageIDProvider(func(ctx context.Context, threadID, turnID string) int64 {
		base++
		return base - 1
	})

	// 持久化两条消息，期望 ID 为 1000、1001
	if err := cm.persistMessage(ctx, "r1", &Message{Role: "user", Content: "a"}); err != nil {
		t.Fatalf("persist msg1 err: %v", err)
	}
	if err := cm.persistMessage(ctx, "r1", &Message{Role: "assistant", Content: "b"}); err != nil {
		t.Fatalf("persist msg2 err: %v", err)
	}

	if len(ms.recs) < 2 {
		t.Fatalf("expected >=2 records, got %d", len(ms.recs))
	}
	if ms.recs[0].MessageID != 1000 || ms.recs[1].MessageID != 1001 {
		t.Fatalf("unexpected messageIDs: %d, %d", ms.recs[0].MessageID, ms.recs[1].MessageID)
	}

	// 关闭提供器后，由 store 侧补齐 ID。
	cm.SetMessageIDProvider(nil)
	ms2 := &memStore{}
	cm.rs = ms2
	if err := cm.persistMessage(ctx, "r1", &Message{Role: "user", Content: "c"}); err != nil {
		t.Fatalf("persist msg3 err: %v", err)
	}
	if ms2.recs[0].MessageID != 1 {
		t.Fatalf("store-generated id expected 1, got %d", ms2.recs[0].MessageID)
	}
}

// TestMemoryContextManager_ModifyModelStreamResponse_ForwardsChunksAndPersistsMergedMessage
// 主要验证:
// 1. ModifyModelStreamResponse 会把模型流的每个 chunk 原样透传给调用方。
// 2. 流结束后，它会把完整 assistant message 合并后写入内存上下文和 rollout store。
//
// 验证思路:
// - 构造一个分块 assistant stream，content 和 tool call arguments 都拆成两段；
// - 先读取返回的 output stream，确认 chunk 数量、顺序和内容都未被改写；
// - 再等待后台 merge 完成，检查 CurrentWindow 和 memStore 中都只落了一条完整消息。
func TestMemoryContextManager_ModifyModelStreamResponse_ForwardsChunksAndPersistsMergedMessage(t *testing.T) {
	ctx := context.Background()
	ms := &memStore{}
	cm := NewMemoryContextManager("t1", ms, nil, nil)
	mw := &ctxMngMiddleware{core: cm, turnID: "r1"}

	idx := 0
	inputChunks := []*schema.Message{
		{
			Role:    schema.Assistant,
			Content: "hel",
			ToolCalls: []schema.ToolCall{{
				ID:    "call_1",
				Index: &idx,
				Function: schema.FunctionCall{
					Name:      "echo",
					Arguments: `{"value":"wo`,
				},
			}},
		},
		{
			Role:    schema.Assistant,
			Content: "lo",
			ToolCalls: []schema.ToolCall{{
				ID:    "call_1",
				Index: &idx,
				Function: schema.FunctionCall{
					Arguments: `rld"}`,
				},
			}},
		},
	}

	out, err := mw.ModifyModelStreamResponse(ctx, schema.StreamReaderFromArray(inputChunks), nil)
	if err != nil {
		t.Fatalf("ModifyModelStreamResponse err: %v", err)
	}

	gotChunks, err := collectMessageChunks(out)
	if err != nil {
		t.Fatalf("collectMessageChunks err: %v", err)
	}

	if len(gotChunks) != len(inputChunks) {
		t.Fatalf("expected %d chunks, got %d", len(inputChunks), len(gotChunks))
	}
	for i := range inputChunks {
		if gotChunks[i].Content != inputChunks[i].Content {
			t.Fatalf("chunk %d content mismatch: got %q want %q", i, gotChunks[i].Content, inputChunks[i].Content)
		}
		if len(gotChunks[i].ToolCalls) != len(inputChunks[i].ToolCalls) {
			t.Fatalf("chunk %d tool call count mismatch: got %d want %d", i, len(gotChunks[i].ToolCalls), len(inputChunks[i].ToolCalls))
		}
		if len(gotChunks[i].ToolCalls) > 0 && gotChunks[i].ToolCalls[0].Function.Arguments != inputChunks[i].ToolCalls[0].Function.Arguments {
			t.Fatalf("chunk %d tool args mismatch: got %q want %q", i, gotChunks[i].ToolCalls[0].Function.Arguments, inputChunks[i].ToolCalls[0].Function.Arguments)
		}
	}

	waitUntil(t, func() bool {
		return len(snapshotMessages(cm)) == 1 && len(ms.recs) == 1
	})

	window := snapshotMessages(cm)
	if len(window) != 1 {
		t.Fatalf("expected 1 merged message in current window, got %d", len(window))
	}
	merged := window[0]
	if merged.Content != "hello" {
		t.Fatalf("unexpected merged content: %q", merged.Content)
	}
	if len(merged.ToolCalls) != 1 {
		t.Fatalf("expected 1 merged tool call, got %d", len(merged.ToolCalls))
	}
	if merged.ToolCalls[0].Function.Name != "echo" {
		t.Fatalf("unexpected tool name: %q", merged.ToolCalls[0].Function.Name)
	}
	if merged.ToolCalls[0].Function.Arguments != `{"value":"world"}` {
		t.Fatalf("unexpected merged tool args: %q", merged.ToolCalls[0].Function.Arguments)
	}

	if len(ms.recs) != 1 {
		t.Fatalf("expected 1 persisted record, got %d", len(ms.recs))
	}
	if ms.recs[0].Type != HistoryRecordMessage {
		t.Fatalf("unexpected record type: %s", ms.recs[0].Type)
	}
	if ms.recs[0].ThreadID != "t1" || ms.recs[0].TurnID != "r1" {
		t.Fatalf("unexpected record identity: thread=%s turn=%s", ms.recs[0].ThreadID, ms.recs[0].TurnID)
	}
	if ms.recs[0].Message == nil || ms.recs[0].Message.Content != "hello" {
		t.Fatalf("unexpected persisted message: %+v", ms.recs[0].Message)
	}
	if len(ms.recs[0].Message.ToolCalls) != 1 || ms.recs[0].Message.ToolCalls[0].Function.Arguments != `{"value":"world"}` {
		t.Fatalf("unexpected persisted tool calls: %+v", ms.recs[0].Message.ToolCalls)
	}
}

// TestMemoryContextManager_ModifyModelStreamResponse_NilInput
// 主要验证:
// 1. nil stream 输入会被直接原样返回。
// 2. 这种空输入不会污染内存上下文，也不会写 rollout store。
//
// 验证思路:
// - 传入 nil stream 调用 ModifyModelStreamResponse；
// - 检查返回值仍为 nil；
// - 再检查 CurrentWindow 和 memStore 均保持为空。
func TestMemoryContextManager_ModifyModelStreamResponse_NilInput(t *testing.T) {
	ctx := context.Background()
	ms := &memStore{}
	cm := NewMemoryContextManager("t1", ms, nil, nil)
	mw := &ctxMngMiddleware{core: cm, turnID: "r1"}

	out, err := mw.ModifyModelStreamResponse(ctx, nil, nil)
	if err != nil {
		t.Fatalf("ModifyModelStreamResponse err: %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil output stream, got %+v", out)
	}
	if got := len(snapshotMessages(cm)); got != 0 {
		t.Fatalf("expected empty current window, got %d", got)
	}
	if len(ms.recs) != 0 {
		t.Fatalf("expected no persisted records, got %d", len(ms.recs))
	}
}

type testPendingInputDrainer struct {
	msgs []*schema.Message
}

func (d *testPendingInputDrainer) DrainInput(ctx context.Context) []*schema.Message {
	_ = ctx
	return d.msgs
}

func TestCtxMngMiddleware_ModifyModelRequest_AppendsDrainedInputAfterGraphInput(t *testing.T) {
	ctx := context.Background()
	cm := NewSimpleTestContextManager()
	mw := &ctxMngMiddleware{
		core:   cm,
		turnID: "r1",
		drainer: &testPendingInputDrainer{
			msgs: []*schema.Message{schema.UserMessage("pending input")},
		},
	}

	got, err := mw.ModifyModelRequest(ctx, []*schema.Message{schema.SystemMessage("system")}, []*schema.Message{schema.ToolMessage("tool result", "call-1")}, nil)
	if err != nil {
		t.Fatalf("ModifyModelRequest() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("messages len = %d, want 3: %+v", len(got), got)
	}
	if got[0].Role != schema.System || got[0].Content != "system" {
		t.Fatalf("unexpected initial context: %+v", got[0])
	}
	if got[1].Role != schema.Tool || got[1].Content != "tool result" {
		t.Fatalf("graph input should be before drained input, got: %+v", got[1])
	}
	if got[2].Role != schema.User || got[2].Content != "pending input" {
		t.Fatalf("drained input should be last, got: %+v", got[2])
	}
}

func TestCtxMngMiddleware_ModifyModelRequestPatchesDanglingToolCallsInRequestOnly(t *testing.T) {
	ctx := context.Background()
	ms := &memStore{}
	cm := NewMemoryContextManager("t1", ms, nil, nil)
	mw := &ctxMngMiddleware{core: cm, turnID: "r1"}
	assistant := schema.AssistantMessage("call tool", []schema.ToolCall{{
		ID:   "call_1",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "missing_tool",
			Arguments: `{"value":"x"}`,
		},
	}})
	if err := cm.AddHistory(ctx, "r0", schema.UserMessage("run it"), assistant); err != nil {
		t.Fatalf("AddHistory() error = %v", err)
	}

	got, err := mw.ModifyModelRequest(ctx, nil, []*schema.Message{schema.UserMessage("continue")}, nil)
	if err != nil {
		t.Fatalf("ModifyModelRequest() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("messages len = %d, want 4: %+v", len(got), got)
	}
	if got[2].Role != schema.Tool || got[2].ToolCallID != "call_1" {
		t.Fatalf("dangling tool call was not patched in request view: %+v", got)
	}

	persisted := snapshotMessages(cm)
	if len(persisted) != 3 {
		t.Fatalf("persisted history len = %d, want 3: %+v", len(persisted), persisted)
	}
	for _, msg := range persisted {
		if msg.Role == schema.Tool && msg.ToolCallID == "call_1" {
			t.Fatalf("patch should not be persisted into history: %+v", persisted)
		}
	}
}

func collectMessageChunks(reader *schema.StreamReader[*schema.Message]) ([]*schema.Message, error) {
	defer reader.Close()

	var chunks []*schema.Message
	for {
		chunk, err := reader.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return chunks, nil
			}
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("condition not satisfied before timeout")
}

func snapshotMessages(cm *MemoryContextManager) []*schema.Message {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	out := make([]*schema.Message, len(cm.messages))
	copy(out, cm.messages)
	return out
}
