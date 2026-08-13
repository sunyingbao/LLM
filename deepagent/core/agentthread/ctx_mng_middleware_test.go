package agentthread

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type recordingContextManager struct {
	mu sync.Mutex

	messages          []*schema.Message
	addErr            error
	compactErr        error
	compactCalls      []int
	forceCompactCalls []int
	forceCompactTurn []string
	usage             ContextUsageSnapshot
}

func (c *recordingContextManager) ReloadHistory(ctx context.Context) error {
	_ = ctx
	return nil
}

func (c *recordingContextManager) AddHistory(ctx context.Context, turnID string, msg ...*schema.Message) error {
	_, _ = ctx, turnID
	if c.addErr != nil {
		return c.addErr
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, msg...)
	return nil
}

func (c *recordingContextManager) History(ctx context.Context) []*schema.Message {
	_ = ctx

	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*schema.Message, len(c.messages))
	copy(out, c.messages)
	return out
}

func (c *recordingContextManager) ContextUsage() ContextUsageSnapshot {
	return c.usage
}

func (c *recordingContextManager) RecordModelUsage(ctx context.Context, usage *model.TokenUsage) {
	_, _ = ctx, usage
}

func (c *recordingContextManager) Compact(ctx context.Context, turnID string) (*ContextCompactedPayload, error) {
	return c.recordCompact(ctx, turnID, true)
}

func (c *recordingContextManager) CompactNeeded(ctx context.Context) bool {
	_ = ctx
	return true
}

func (c *recordingContextManager) recordCompact(ctx context.Context, turnID string, force bool) (*ContextCompactedPayload, error) {
	_ = ctx

	c.mu.Lock()
	defer c.mu.Unlock()
	if force {
		c.forceCompactCalls = append(c.forceCompactCalls, len(c.messages))
		c.forceCompactTurn = append(c.forceCompactTurn, turnID)
	} else {
		c.compactCalls = append(c.compactCalls, len(c.messages))
	}
	return &ContextCompactedPayload{}, c.compactErr
}

func (c *recordingContextManager) messageCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

func TestCtxMngMiddleware_ModifyModelRequestCompactsBeforeUserInput(t *testing.T) {
	ctx := context.Background()
	tc := &recordingContextManager{
		messages: []*schema.Message{schema.UserMessage("old")},
	}
	mw := &ctxMngMiddleware{core: tc, turnID: "r1"}

	got, err := mw.ModifyModelRequest(ctx, []*schema.Message{schema.SystemMessage("system")}, []*schema.Message{schema.UserMessage("new")}, nil)
	if err != nil {
		t.Fatalf("ModifyModelRequest() error = %v", err)
	}
	if len(tc.forceCompactCalls) != 1 || tc.forceCompactCalls[0] != 1 {
		t.Fatalf("compact should run before appending user input, calls=%v", tc.forceCompactCalls)
	}
	if len(got) != 3 {
		t.Fatalf("request message count = %d, want 3: %+v", len(got), got)
	}
	if got[0].Role != schema.System || got[1].Content != "old" || got[2].Content != "new" {
		t.Fatalf("unexpected request messages: %+v", got)
	}
}

func TestCtxMngMiddleware_EmitsContextCompactStartedBeforeCompacted(t *testing.T) {
	ctx := context.Background()
	tc := &recordingContextManager{
		messages: []*schema.Message{schema.UserMessage("old")},
		usage: ContextUsageSnapshot{
			ContextWindow: 100,
			CurrentTotal:  80,
		},
	}
	var events []Event
	mw := &ctxMngMiddleware{
		core:   tc,
		turnID: "r1",
		emit: func(ctx context.Context, typ EventType, payload any) {
			_ = ctx
			events = append(events, Event{Type: typ, Payload: payload})
		},
	}

	if _, err := mw.ModifyModelRequest(ctx, nil, []*schema.Message{schema.UserMessage("new")}, nil); err != nil {
		t.Fatalf("ModifyModelRequest() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("emitted events = %+v, want compact started and compacted", events)
	}
	if events[0].Type != EventContextCompactStarted {
		t.Fatalf("first event type = %s, want %s", events[0].Type, EventContextCompactStarted)
	}
	started, ok := events[0].Payload.(ContextCompactStartedPayload)
	if !ok {
		t.Fatalf("started payload = %T, want ContextCompactStartedPayload", events[0].Payload)
	}
	if started.ContextUsage.CurrentTotal != 80 || started.ContextUsage.ContextWindow != 100 {
		t.Fatalf("started context usage = %+v", started.ContextUsage)
	}
	if events[1].Type != EventContextCompacted {
		t.Fatalf("second event type = %s, want %s", events[1].Type, EventContextCompacted)
	}
}

func TestCtxMngMiddleware_ModifyModelRequestCompactsAfterToolResult(t *testing.T) {
	ctx := context.Background()
	tc := &recordingContextManager{
		messages: []*schema.Message{schema.UserMessage("run"), schema.AssistantMessage("call", nil)},
	}
	mw := &ctxMngMiddleware{core: tc, turnID: "r1"}

	got, err := mw.ModifyModelRequest(ctx, nil, []*schema.Message{schema.ToolMessage("done", "call_1")}, nil)
	if err != nil {
		t.Fatalf("ModifyModelRequest() error = %v", err)
	}
	if len(tc.forceCompactCalls) != 1 || tc.forceCompactCalls[0] != 3 {
		t.Fatalf("compact should run after appending tool result, calls=%v", tc.forceCompactCalls)
	}
	if len(got) != 3 || got[2].Role != schema.Tool {
		t.Fatalf("unexpected request messages: %+v", got)
	}
}

func TestCtxMngMiddleware_ModifyModelRequestReturnsAddHistoryError(t *testing.T) {
	ctx := context.Background()
	addErr := errors.New("add failed")
	tc := &recordingContextManager{addErr: addErr}
	mw := &ctxMngMiddleware{core: tc, turnID: "r1"}

	_, err := mw.ModifyModelRequest(ctx, nil, []*schema.Message{schema.UserMessage("new")}, nil)
	if !errors.Is(err, addErr) {
		t.Fatalf("ModifyModelRequest() error = %v, want %v", err, addErr)
	}
}

func TestCtxMngMiddleware_ModifyModelStreamResponseForwardsChunksAndPersistsMergedMessage(t *testing.T) {
	ctx := context.Background()
	tc := &recordingContextManager{}
	mw := &ctxMngMiddleware{core: tc, turnID: "r1"}

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
		t.Fatalf("ModifyModelStreamResponse() error = %v", err)
	}

	gotChunks, err := collectMessageChunks(out)
	if err != nil {
		t.Fatalf("collectMessageChunks() error = %v", err)
	}
	if len(gotChunks) != len(inputChunks) {
		t.Fatalf("chunk count = %d, want %d", len(gotChunks), len(inputChunks))
	}
	if gotChunks[0].Content != "hel" || gotChunks[1].Content != "lo" {
		t.Fatalf("stream chunks should be forwarded unchanged: %+v", gotChunks)
	}

	waitUntil(t, func() bool { return tc.messageCount() == 1 })
	persisted := tc.History(ctx)
	full := persisted[0]
	if full.Role != schema.Assistant || full.Content != "hello" {
		t.Fatalf("unexpected merged message: %+v", full)
	}
	if len(full.ToolCalls) != 1 || full.ToolCalls[0].Function.Arguments != `{"value":"world"}` {
		t.Fatalf("unexpected merged tool call: %+v", full.ToolCalls)
	}
}
