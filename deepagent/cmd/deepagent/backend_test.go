package main

import (
	"context"
	"testing"
	"time"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
)

func TestLocalAgentServiceWatchSessionRegistersUnknownChildThread(t *testing.T) {
	ctx := context.Background()
	store := newTestLocalStore(t)
	worker, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: store.threadState,
		EventStore:       store.events,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newTestAgentThread()
			rt.postFn = func(msg *agentworker.Message) error {
				go rt.emitEvent(&agentworker.Event{
					ID:       "ev_child_live",
					ThreadID: state.ID,
					Type:     agentworker.EventType("assistant"),
					Payload:  []byte("child"),
					TS:       time.Now(),
				})
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	defer worker.Close(ctx)

	service := NewLocalAgentService(ctx, AppConfig{UserID: 1001}, worker, NewThreadRefRegistry(), NewToolExecPolicy())
	parent, err := service.CreateRootThread(ctx, "hello")
	if err != nil {
		t.Fatalf("CreateRootThread() error = %v", err)
	}
	if err := service.WatchSession(ctx, parent.SessionID); err != nil {
		t.Fatalf("WatchSession() error = %v", err)
	}
	child, err := worker.CreateThread(ctx, inprocess.CreateThreadSpec{ParentThreadID: parent.ID, Title: "child"})
	if err != nil {
		t.Fatalf("CreateThread(child) error = %v", err)
	}
	if err := worker.PostMessage(ctx, child.ID, &agentworker.Message{ID: "msg_child", Type: agentworker.MessageTypeText}); err != nil {
		t.Fatalf("PostMessage(child) error = %v", err)
	}

	update := receiveLocalUpdate(t, service.Updates())
	if update.Thread == nil || update.Thread.ID != child.ID {
		t.Fatalf("update = %+v, want child thread", update)
	}
	if update.Event == nil || update.Event.ID != "ev_child_live" || update.Event.ThreadID != child.ID {
		t.Fatalf("update = %+v, want child event", update)
	}
}

func TestLocalAgentServiceCancelRunningThreadCallsWorkerInterrupt(t *testing.T) {
	ctx := context.Background()
	store := newTestLocalStore(t)
	active := &agentworker.ActiveTurn{TurnID: "turn_1", ConsumedMessageIDs: []string{"msg_1"}}
	interrupts := make(chan agentworker.ThreadInterruptRequest, 1)
	worker, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: store.threadState,
		EventStore:       store.events,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newTestAgentThread()
			rt.activeTurnFn = func() *agentworker.ActiveTurn { return active }
			rt.interruptFn = func(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
				interrupts <- req
				go rt.emitEvent(&agentworker.Event{
					ID:       "ev_interrupted",
					ThreadID: state.ID,
					TurnID:   "turn_1",
					Type:     agentworker.EventType(agentthread.EventInterrupted),
					Payload:  []byte("interrupted"),
					TS:       time.Now(),
				})
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	defer worker.Close(ctx)

	service := NewLocalAgentService(ctx, AppConfig{UserID: 1001}, worker, NewThreadRefRegistry(), NewToolExecPolicy())
	thread, err := service.CreateRootThread(ctx, "hello")
	if err != nil {
		t.Fatalf("CreateRootThread() error = %v", err)
	}
	if _, err := service.SendUserMessage(ctx, thread.ID, "hello"); err != nil {
		t.Fatalf("SendUserMessage() error = %v", err)
	}
	if err := service.WatchSession(ctx, thread.SessionID); err != nil {
		t.Fatalf("WatchSession() error = %v", err)
	}
	got, err := service.CancelRunningThread(ctx, thread.ID, "user_stop")
	if err != nil {
		t.Fatalf("CancelRunningThread() error = %v", err)
	}
	if got == nil || got.Status != inprocess.InterruptThreadAccepted || got.ActiveTurn == nil || got.ActiveTurn.TurnID != "turn_1" {
		t.Fatalf("CancelRunningThread() = %+v, want accepted active turn", got)
	}
	select {
	case req := <-interrupts:
		if req.Kind != agentworker.ThreadInterruptKindCancelInput || req.Reason != "user_stop" {
			t.Fatalf("interrupt request = %+v", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for interrupt")
	}
	update := receiveLocalEventUpdate(t, service.Updates(), "ev_interrupted")
	if update.Event == nil || update.Event.ID != "ev_interrupted" || update.Event.ThreadID != thread.ID {
		t.Fatalf("cancel event update = %+v, want interrupted event", update)
	}
}

func receiveLocalUpdate(t *testing.T, updates <-chan localUpdate) localUpdate {
	t.Helper()
	select {
	case update := <-updates:
		return update
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for local update")
		return localUpdate{}
	}
}

func receiveLocalEventUpdate(t *testing.T, updates <-chan localUpdate, eventID string) localUpdate {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event != nil && update.Event.ID == eventID {
				return update
			}
		case <-deadline:
			t.Fatalf("timeout waiting for event update %q", eventID)
			return localUpdate{}
		}
	}
}

type testAgentThread struct {
	items        chan agentworker.ThreadOutputItem
	postFn       func(msg *agentworker.Message) error
	interruptFn  func(ctx context.Context, req agentworker.ThreadInterruptRequest) error
	activeTurnFn func() *agentworker.ActiveTurn
}

func newTestAgentThread() *testAgentThread {
	return &testAgentThread{items: make(chan agentworker.ThreadOutputItem, 8)}
}

func (t *testAgentThread) Init(ctx context.Context) (*agentworker.ThreadOutput, error) {
	return &agentworker.ThreadOutput{Items: t.items}, nil
}

func (t *testAgentThread) PostMessage(ctx context.Context, msg *agentworker.Message) (*agentworker.PostMessageResult, error) {
	if t.postFn != nil {
		return nil, t.postFn(msg)
	}
	return nil, nil
}

func (t *testAgentThread) Interrupt(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
	if t.interruptFn != nil {
		return t.interruptFn(ctx, req)
	}
	return nil
}

func (t *testAgentThread) ActiveTurn() *agentworker.ActiveTurn {
	if t.activeTurnFn != nil {
		return t.activeTurnFn()
	}
	return nil
}

func (t *testAgentThread) Close(ctx context.Context) error { return nil }

func (t *testAgentThread) emitEvent(ev *agentworker.Event) {
	t.items <- agentworker.ThreadOutputItem{Event: ev}
}
