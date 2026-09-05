//go:build !windows

package thread

import (
	"context"
	"sync"
	"testing"
	"time"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/checkpointer"
	agentworker "eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/thread/runtimectx"

	modelcomp "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// runtimeCtxCapturingModel records the runtime ctx info observed on each model
// call so tests can assert what flowed through the DeepAgent run context.
type runtimeCtxCapturingModel struct {
	mu        sync.Mutex
	threadOK  bool
	turnOK    bool
	thread    runtimectx.ThreadIdentity
	turn      runtimectx.TurnIdentity
	resolved  []runtimectx.TurnIdentity
	handlers  []func(context.Context, []*schema.Message) (*schema.Message, error)
	callIndex int
}

func (m *runtimeCtxCapturingModel) Generate(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.Message, error) {
	_ = opts
	m.mu.Lock()
	m.thread, m.threadOK = runtimectx.ThreadIdentityFromContext(ctx)
	m.turn, m.turnOK = runtimectx.TurnIdentityFromContext(ctx)
	var handler func(context.Context, []*schema.Message) (*schema.Message, error)
	if m.callIndex < len(m.handlers) {
		handler = m.handlers[m.callIndex]
	}
	m.callIndex++
	m.mu.Unlock()
	if handler == nil {
		return schema.AssistantMessage("done", nil), nil
	}
	return handler(ctx, input)
}

func (m *runtimeCtxCapturingModel) Stream(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *runtimeCtxCapturingModel) BindTools(tools []*schema.ToolInfo) error {
	_ = tools
	return nil
}

func (m *runtimeCtxCapturingModel) WithTools(tools []*schema.ToolInfo) (modelcomp.ToolCallingChatModel, error) {
	_ = tools
	return m, nil
}

var _ modelcomp.ToolCallingChatModel = (*runtimeCtxCapturingModel)(nil)

func (m *runtimeCtxCapturingModel) snapshot() (runtimectx.ThreadIdentity, bool, runtimectx.TurnIdentity, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.thread, m.threadOK, m.turn, m.turnOK
}

func TestRuntimeUserInputExposesThreadAndTurnInfo(t *testing.T) {
	ctx := context.Background()
	model := &runtimeCtxCapturingModel{}
	eventBus := make(chan agentthread.Event, 16)
	threadInfo := runtimectx.ThreadIdentity{
		ThreadID:  "thread-info",
		SessionID: "session-info",
		UserID:    42,
		Namespace: "ns",
		Env:       "prod",
	}

	var resolverTurn runtimectx.TurnIdentity
	var resolverTurnOK bool
	deepThread := newDeepAgentThreadForTest("thread-info", &agentthread.TurnConfig{
		Agent: deepagents.Config{
			Model:           model,
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}, newRuntimeCompactContextManager(), eventBus, agentthread.WithTurnIDProvider(func(context.Context, string, *agentthread.Message) string {
		return "turn-info"
	}))
	runtime, err := NewRuntime(AdapterConfig{
		SessionID:  "session-info",
		ThreadID:   "thread-info",
		Thread:     deepThread,
		EventBus:   eventBus,
		ThreadInfo: threadInfo,
		TurnConfig: func(ctx context.Context, req TurnStartRequest) (*agentthread.TurnConfig, error) {
			resolverTurn, resolverTurnOK = runtimectx.TurnIdentityFromContext(ctx)
			return &agentthread.TurnConfig{
				Agent: deepagents.Config{
					Model:           model,
					CheckpointStore: checkpointer.NewInMemoryStore(),
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewRuntime() error=%v", err)
	}
	if _, err := runtime.Init(ctx); err != nil {
		t.Fatalf("Init() error=%v", err)
	}

	result, err := runtime.PostMessage(ctx, &agentworker.Message{
		ID:      "m-1",
		Type:    MessageTypeInput,
		Payload: mustJSON(t, protoinput.UserMessage{Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: "hi"}}}),
	})
	if err != nil {
		t.Fatalf("PostMessage() error=%v", err)
	}
	if result == nil || result.TurnID != "turn-info" {
		t.Fatalf("PostMessage() result=%+v", result)
	}
	waitUntilRuntimeInactive(t, runtime, time.Second)

	gotThread, threadOK, gotTurn, turnOK := model.snapshot()
	if !threadOK {
		t.Fatal("model did not observe ThreadInfo in run ctx")
	}
	if gotThread != threadInfo {
		t.Fatalf("model ThreadInfo=%+v, want %+v", gotThread, threadInfo)
	}
	if !turnOK {
		t.Fatal("model did not observe TurnInfo in run ctx")
	}
	wantTurn := runtimectx.TurnIdentity{
		ThreadID:  "thread-info",
		TurnID:    "turn-info",
		MessageID: "m-1",
	}
	if gotTurn != wantTurn {
		t.Fatalf("model TurnInfo=%+v, want %+v", gotTurn, wantTurn)
	}
	if !resolverTurnOK || resolverTurn != wantTurn {
		t.Fatalf("resolver TurnInfo=%+v ok=%v, want %+v (hook must run before resolver)", resolverTurn, resolverTurnOK, wantTurn)
	}
}

func TestRuntimeResumeExposesResumeTurnInfo(t *testing.T) {
	ctx := context.Background()
	model := &runtimeCtxCapturingModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(context.Context, []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("first", nil), nil
			},
			func(context.Context, []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("resumed", nil), nil
			},
		},
	}
	checkpoints := checkpointer.NewInMemoryStore()
	eventBus := make(chan agentthread.Event, 16)
	deepThread := newDeepAgentThreadForTest("thread-resume-info", &agentthread.TurnConfig{
		Agent: deepagents.Config{
			Model:           model,
			CheckpointStore: checkpoints,
		},
	}, newRuntimeCompactContextManager(), eventBus, agentthread.WithTurnIDProvider(func(context.Context, string, *agentthread.Message) string {
		return "turn-resume-info"
	}))
	first, err := deepThread.SubmitInput(ctx, schema.UserMessage("first"))
	if err != nil {
		t.Fatalf("SubmitInput() error=%v", err)
	}
	if err := first.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("first Wait() error=%v", err)
	}

	runtime, err := NewRuntime(AdapterConfig{
		SessionID:  "session-resume-info",
		ThreadID:   "thread-resume-info",
		Thread:     deepThread,
		EventBus:   eventBus,
		ThreadInfo: runtimectx.ThreadIdentity{ThreadID: "thread-resume-info", SessionID: "session-resume-info"},
	})
	if err != nil {
		t.Fatalf("NewRuntime() error=%v", err)
	}

	if _, err := runtime.postResumeTurn(ctx, resumeTurnCommand{
		message: &agentworker.Message{ID: "resume-1", Type: MessageTypeResumeTurn},
		payload: protoinput.ResumeTurnPayload{
			TurnID:       "turn-resume-info",
			CheckpointID: "thread-resume-info:turn-resume-info",
			InterruptID:  "interrupt-1",
			Approval:     &protoinput.ApprovalDecision{Approved: true},
		},
	}); err != nil {
		t.Fatalf("postResumeTurn() error=%v", err)
	}
	waitUntilRuntimeInactive(t, runtime, time.Second)

	_, _, gotTurn, turnOK := model.snapshot()
	if !turnOK {
		t.Fatal("resume model did not observe TurnInfo in run ctx")
	}
	wantTurn := runtimectx.TurnIdentity{
		ThreadID:  "thread-resume-info",
		TurnID:    "turn-resume-info",
		MessageID: "resume-1",
	}
	if gotTurn != wantTurn {
		t.Fatalf("resume model TurnInfo=%+v, want %+v", gotTurn, wantTurn)
	}
}

func TestRuntimeInitExposesThreadInfo(t *testing.T) {
	ctx := context.Background()
	eventBus := make(chan agentthread.Event, 4)
	threadInfo := runtimectx.ThreadIdentity{ThreadID: "thread-init", SessionID: "session-init", UserID: 7}

	var capturedCtx context.Context
	cm := &threadInfoCapturingContextManager{onReload: func(c context.Context) { capturedCtx = c }}
	deepThread := newDeepAgentThreadForTest("thread-init", &agentthread.TurnConfig{
		Agent: deepagents.Config{
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}, cm, eventBus)
	runtime, err := NewRuntime(AdapterConfig{
		SessionID:  "session-init",
		ThreadID:   "thread-init",
		Thread:     deepThread,
		EventBus:   eventBus,
		ThreadInfo: threadInfo,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error=%v", err)
	}
	if _, err := runtime.Init(ctx); err != nil {
		t.Fatalf("Init() error=%v", err)
	}
	if capturedCtx == nil {
		t.Fatal("Init did not run history reload")
	}
	got, ok := runtimectx.ThreadIdentityFromContext(capturedCtx)
	if !ok || got != threadInfo {
		t.Fatalf("Init ctx ThreadInfo=%+v ok=%v, want %+v", got, ok, threadInfo)
	}
}

func TestNewRuntimeDefaultsThreadInfoIDs(t *testing.T) {
	eventBus := make(chan agentthread.Event, 1)
	deepThread := newDeepAgentThreadForTest("thread-default", &agentthread.TurnConfig{
		Agent: deepagents.Config{
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}, newRuntimeCompactContextManager(), eventBus)
	runtime, err := NewRuntime(AdapterConfig{
		SessionID: "session-default",
		ThreadID:  "thread-default",
		Thread:    deepThread,
		EventBus:  eventBus,
		// ThreadInfo intentionally left empty.
	})
	if err != nil {
		t.Fatalf("NewRuntime() error=%v", err)
	}
	if runtime.threadInfo.ThreadID != "thread-default" {
		t.Fatalf("default ThreadInfo.ThreadID=%q, want thread-default", runtime.threadInfo.ThreadID)
	}
	if runtime.threadInfo.SessionID != "session-default" {
		t.Fatalf("default ThreadInfo.SessionID=%q, want session-default", runtime.threadInfo.SessionID)
	}
}

type threadInfoCapturingContextManager struct {
	*runtimeCompactContextManager
	onReload func(context.Context)
}

func (m *threadInfoCapturingContextManager) ReloadHistory(ctx context.Context) error {
	if m.onReload != nil {
		m.onReload(ctx)
	}
	return nil
}
