package inprocess_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
	localstore "eino-cli/deepagent/worker/inprocess/store"
)

func TestWorkerCreateChildThreadInheritsSession(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			return newFakeRuntime(state.ID), nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	parent, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID:    1001,
		SessionID: "sess_1",
		Title:     "main",
	})
	if err != nil {
		t.Fatalf("CreateThread(parent) error = %v", err)
	}
	child, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{
		ParentThreadID: parent.ID,
		Title:          "child",
	})
	if err != nil {
		t.Fatalf("CreateThread(child) error = %v", err)
	}
	if child.SessionID != parent.SessionID {
		t.Fatalf("child.SessionID = %q, want %q", child.SessionID, parent.SessionID)
	}
	if child.UserID != parent.UserID {
		t.Fatalf("child.UserID = %d, want %d", child.UserID, parent.UserID)
	}
	if child.RootThreadID != parent.ID {
		t.Fatalf("child.RootThreadID = %q, want %q", child.RootThreadID, parent.ID)
	}
}

func TestWorkerCreateThreadRequiresUserID(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			return newFakeRuntime(state.ID), nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	_, err = w.CreateThread(ctx, inprocess.CreateThreadSpec{SessionID: "sess_1"})
	if err != inprocess.ErrMissingUserID {
		t.Fatalf("CreateThread() error = %v, want %v", err, inprocess.ErrMissingUserID)
	}
}

func TestWorkerPassesSelfToThreadFactory(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	var gotWorker *inprocess.Worker
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			gotWorker = worker
			return newFakeRuntime(state.ID), nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{Type: agentworker.MessageTypeText, Payload: []byte("hello")}); err != nil {
		t.Fatalf("PostMessage() error = %v", err)
	}
	if gotWorker != w {
		t.Fatalf("ThreadFactory worker = %p, want %p", gotWorker, w)
	}
}

func TestWorkerPostMessageWithResultPreservesTurnID(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			runtime := newFakeRuntime(state.ID)
			runtime.postResult = &agentworker.PostMessageResult{TurnID: "turn-1"}
			return runtime, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	result, err := w.PostMessageWithResult(ctx, thread.ID, &agentworker.Message{Type: agentworker.MessageTypeText})
	if err != nil {
		t.Fatalf("PostMessageWithResult() error = %v", err)
	}
	if result == nil || result.TurnID != "turn-1" {
		t.Fatalf("PostMessageWithResult() = %+v", result)
	}
}

func TestWorkerDoesNotPassCallerContextToRuntime(t *testing.T) {
	type ctxKey string
	const callerKey ctxKey = "caller"

	ctx := context.WithValue(context.Background(), callerKey, "parent-tool-context")
	threadStates, eventStore := openSQLiteStores(t)
	var initCtxValue any
	var postCtxValue any
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newFakeRuntime(state.ID)
			rt.initFn = func(ctx context.Context) error {
				initCtxValue = ctx.Value(callerKey)
				return nil
			}
			rt.postCtxFn = func(ctx context.Context, msg *agentworker.Message) error {
				postCtxValue = ctx.Value(callerKey)
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{Type: agentworker.MessageTypeText, Payload: []byte("hello")}); err != nil {
		t.Fatalf("PostMessage() error = %v", err)
	}
	if initCtxValue != nil {
		t.Fatalf("runtime Init context value = %v, want nil", initCtxValue)
	}
	if postCtxValue != nil {
		t.Fatalf("runtime PostMessage context value = %v, want nil", postCtxValue)
	}
}

func TestWorkerCreateChildThreadRejectsUserMismatch(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			return newFakeRuntime(state.ID), nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	parent, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread(parent) error = %v", err)
	}
	_, err = w.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID:         2002,
		ParentThreadID: parent.ID,
	})
	if err != inprocess.ErrInvalidThreadState {
		t.Fatalf("CreateThread(child) error = %v, want %v", err, inprocess.ErrInvalidThreadState)
	}
}

func TestWorkerListThreadsCatalogFilters(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			return newFakeRuntime(state.ID), nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	rootA, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID:    1001,
		SessionID: "sess_a",
		Title:     "root-a",
		Profile:   inprocess.ThreadProfile{Cwd: "/repo"},
	})
	if err != nil {
		t.Fatalf("CreateThread(rootA) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	childA, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{
		ParentThreadID: rootA.ID,
		Title:          "child-a",
	})
	if err != nil {
		t.Fatalf("CreateThread(childA) error = %v", err)
	}
	if childA.Profile.Cwd != "/repo" {
		t.Fatalf("childA.Profile.Cwd = %q, want /repo", childA.Profile.Cwd)
	}
	time.Sleep(10 * time.Millisecond)
	rootOtherUser, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID:    2002,
		SessionID: "sess_other",
		Title:     "root-other",
		Profile:   inprocess.ThreadProfile{Cwd: "/repo"},
	})
	if err != nil {
		t.Fatalf("CreateThread(rootOtherUser) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	rootClosed, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID:    1001,
		SessionID: "sess_closed",
		Title:     "root-closed",
		Profile:   inprocess.ThreadProfile{Cwd: "/other"},
	})
	if err != nil {
		t.Fatalf("CreateThread(rootClosed) error = %v", err)
	}
	closedAt := time.Now()
	if _, err := threadStates.UpdateThread(ctx, rootClosed.ID, inprocess.UpdateThreadStatePatch{
		ClosedAt: &closedAt,
	}); err != nil {
		t.Fatalf("UpdateThread(rootClosed) error = %v", err)
	}

	threads, err := w.ListThreads(ctx, inprocess.ListThreadsOptions{UserID: 1001})
	if err != nil {
		t.Fatalf("ListThreads(user) error = %v", err)
	}
	assertThreadIDs(t, threads, childA.ID, rootA.ID)

	roots, err := w.ListThreads(ctx, inprocess.ListThreadsOptions{UserID: 1001, RootOnly: true})
	if err != nil {
		t.Fatalf("ListThreads(root only) error = %v", err)
	}
	assertThreadIDs(t, roots, rootA.ID)

	rootsWithClosed, err := w.ListThreads(ctx, inprocess.ListThreadsOptions{
		UserID:        1001,
		RootOnly:      true,
		IncludeClosed: true,
	})
	if err != nil {
		t.Fatalf("ListThreads(root only include closed) error = %v", err)
	}
	assertThreadIDs(t, rootsWithClosed, rootClosed.ID, rootA.ID)

	repoThreads, err := w.ListThreads(ctx, inprocess.ListThreadsOptions{UserID: 1001, Cwd: "/repo"})
	if err != nil {
		t.Fatalf("ListThreads(cwd) error = %v", err)
	}
	assertThreadIDs(t, repoThreads, childA.ID, rootA.ID)

	sessionThreads, err := w.ListThreads(ctx, inprocess.ListThreadsOptions{UserID: 1001, SessionID: "sess_a"})
	if err != nil {
		t.Fatalf("ListThreads(session) error = %v", err)
	}
	assertThreadIDs(t, sessionThreads, childA.ID, rootA.ID)

	otherUserThreads, err := w.ListThreads(ctx, inprocess.ListThreadsOptions{UserID: 2002})
	if err != nil {
		t.Fatalf("ListThreads(other user) error = %v", err)
	}
	assertThreadIDs(t, otherUserThreads, rootOtherUser.ID)

	createdASC, err := w.ListThreads(ctx, inprocess.ListThreadsOptions{
		UserID:        1001,
		RootOnly:      true,
		IncludeClosed: true,
		OrderBy:       inprocess.ListThreadsOrderByCreatedAt,
	})
	if err != nil {
		t.Fatalf("ListThreads(created asc) error = %v", err)
	}
	assertThreadIDs(t, createdASC, rootA.ID, rootClosed.ID)

	legacySessionThreads, err := w.ListThreadsBySession(ctx, "sess_closed", inprocess.ListThreadsOptions{})
	if err != nil {
		t.Fatalf("ListThreadsBySession(closed) error = %v", err)
	}
	assertThreadIDs(t, legacySessionThreads, rootClosed.ID)
}

func TestWorkerPostMessagePersistsEvents(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)

	var factoryCalls atomic.Int32
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			factoryCalls.Add(1)
			rt := newFakeRuntime(state.ID)
			rt.postFn = func(msg *agentworker.Message) error {
				go func() {
					rt.emitEvent(&agentworker.Event{
						ID:       "ev_1",
						ThreadID: state.ID,
						Type:     agentworker.EventType("assistant"),
						Payload:  []byte("ok"),
						TS:       time.Now(),
					})
					rt.yield(agentworker.ThreadYield{})
					close(rt.items)
				}()
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}

	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{
		ID:      "msg_1",
		Type:    agentworker.MessageTypeText,
		Payload: []byte("hello"),
	}); err != nil {
		t.Fatalf("PostMessage() error = %v", err)
	}

	waitFor(t, func() bool {
		evs, err := w.ListEvents(ctx, thread.ID, inprocess.ListEventsOptions{})
		return err == nil && len(evs) == 1
	})

	gotEvents, err := w.ListEvents(ctx, thread.ID, inprocess.ListEventsOptions{})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(gotEvents) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(gotEvents))
	}
	if factoryCalls.Load() != 1 {
		t.Fatalf("factoryCalls = %d, want 1", factoryCalls.Load())
	}
}

func TestWorkerSubscribeThreadEventsReceivesLiveEvents(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)

	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newFakeRuntime(state.ID)
			rt.postFn = func(msg *agentworker.Message) error {
				go func() {
					rt.emitEvent(&agentworker.Event{
						ID:       "ev_live",
						ThreadID: state.ID,
						Type:     agentworker.EventType("assistant"),
						Payload:  []byte("live"),
						TS:       time.Now(),
					})
					rt.yield(agentworker.ThreadYield{})
					close(rt.items)
				}()
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	sub, err := w.SubscribeThreadEvents(ctx, thread.ID, 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents() error = %v", err)
	}
	defer sub.Close()
	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{
		ID:      "msg_live",
		Type:    agentworker.MessageTypeText,
		Payload: []byte("hello"),
	}); err != nil {
		t.Fatalf("PostMessage() error = %v", err)
	}
	select {
	case ev := <-sub.Events:
		if ev == nil || ev.ID != "ev_live" {
			t.Fatalf("unexpected live event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for live event")
	}
}

func TestWorkerInterruptThreadAccepted(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	activeTurn := &agentworker.ActiveTurn{TurnID: "turn_1", ConsumedMessageIDs: []string{"msg_1"}}
	var gotReq agentworker.ThreadInterruptRequest
	var interruptCalls atomic.Int32

	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newFakeRuntime(state.ID)
			rt.activeTurnFn = func() *agentworker.ActiveTurn { return activeTurn }
			rt.interruptFn = func(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
				interruptCalls.Add(1)
				gotReq = req
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	defer w.Close(ctx)
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{ID: "msg_1", Type: agentworker.MessageTypeText}); err != nil {
		t.Fatalf("PostMessage() error = %v", err)
	}

	timeout := 50 * time.Millisecond
	got, err := w.InterruptThread(ctx, inprocess.InterruptThreadRequest{
		ThreadID: thread.ID,
		ThreadInterruptRequest: agentworker.ThreadInterruptRequest{
			Kind:             agentworker.ThreadInterruptKindCancelInput,
			ControlMessageID: "ctrl_1",
			CutoffMessageID:  "msg_1",
			Reason:           "user_stop",
			Timeout:          &timeout,
		},
	})
	if err != nil {
		t.Fatalf("InterruptThread() error = %v", err)
	}
	if got.Status != inprocess.InterruptThreadAccepted {
		t.Fatalf("InterruptThread().Status = %q, want %q", got.Status, inprocess.InterruptThreadAccepted)
	}
	if got.ThreadID != thread.ID {
		t.Fatalf("InterruptThread().ThreadID = %q, want %q", got.ThreadID, thread.ID)
	}
	if got.ActiveTurn == nil || got.ActiveTurn.TurnID != activeTurn.TurnID {
		t.Fatalf("InterruptThread().ActiveTurn = %+v, want turn snapshot", got.ActiveTurn)
	}
	if interruptCalls.Load() != 1 {
		t.Fatalf("interruptCalls = %d, want 1", interruptCalls.Load())
	}
	if gotReq.Kind != agentworker.ThreadInterruptKindCancelInput ||
		gotReq.ControlMessageID != "ctrl_1" ||
		gotReq.CutoffMessageID != "msg_1" ||
		gotReq.Reason != "user_stop" ||
		gotReq.Timeout != &timeout {
		t.Fatalf("runtime interrupt request = %+v", gotReq)
	}
}

func TestWorkerInterruptThreadNoLiveActor(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	var factoryCalls atomic.Int32
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			factoryCalls.Add(1)
			return newFakeRuntime(state.ID), nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}

	got, err := w.InterruptThread(ctx, inprocess.InterruptThreadRequest{
		ThreadID: thread.ID,
		ThreadInterruptRequest: agentworker.ThreadInterruptRequest{
			Kind: agentworker.ThreadInterruptKindCancelInput,
		},
	})
	if err != nil {
		t.Fatalf("InterruptThread() error = %v", err)
	}
	if got.Status != inprocess.InterruptThreadNoLiveActor {
		t.Fatalf("InterruptThread().Status = %q, want %q", got.Status, inprocess.InterruptThreadNoLiveActor)
	}
	if factoryCalls.Load() != 0 {
		t.Fatalf("factoryCalls = %d, want 0", factoryCalls.Load())
	}
}

func TestWorkerInterruptThreadNoActiveTurn(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	var interruptCalls atomic.Int32
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newFakeRuntime(state.ID)
			rt.interruptFn = func(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
				interruptCalls.Add(1)
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	defer w.Close(ctx)
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{ID: "msg_1", Type: agentworker.MessageTypeText}); err != nil {
		t.Fatalf("PostMessage() error = %v", err)
	}

	got, err := w.InterruptThread(ctx, inprocess.InterruptThreadRequest{
		ThreadID: thread.ID,
		ThreadInterruptRequest: agentworker.ThreadInterruptRequest{
			Kind: agentworker.ThreadInterruptKindCancelInput,
		},
	})
	if err != nil {
		t.Fatalf("InterruptThread() error = %v", err)
	}
	if got.Status != inprocess.InterruptThreadNoActiveTurn {
		t.Fatalf("InterruptThread().Status = %q, want %q", got.Status, inprocess.InterruptThreadNoActiveTurn)
	}
	if got.ActiveTurn != nil {
		t.Fatalf("InterruptThread().ActiveTurn = %+v, want nil", got.ActiveTurn)
	}
	if interruptCalls.Load() != 0 {
		t.Fatalf("interruptCalls = %d, want 0", interruptCalls.Load())
	}
}

func TestWorkerInterruptThreadClosed(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			return newFakeRuntime(state.ID), nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if err := w.CloseThread(ctx, thread.ID); err != nil {
		t.Fatalf("CloseThread() error = %v", err)
	}

	_, err = w.InterruptThread(ctx, inprocess.InterruptThreadRequest{
		ThreadID: thread.ID,
		ThreadInterruptRequest: agentworker.ThreadInterruptRequest{
			Kind: agentworker.ThreadInterruptKindCancelInput,
		},
	})
	if !errors.Is(err, inprocess.ErrThreadClosed) {
		t.Fatalf("InterruptThread() error = %v, want %v", err, inprocess.ErrThreadClosed)
	}
}

func TestWorkerInterruptThreadRuntimeRejection(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	rejectErr := errors.New("runtime rejected interrupt")
	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newFakeRuntime(state.ID)
			rt.activeTurnFn = func() *agentworker.ActiveTurn { return &agentworker.ActiveTurn{TurnID: "turn_1"} }
			rt.interruptFn = func(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
				return rejectErr
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	defer w.Close(ctx)
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{ID: "msg_1", Type: agentworker.MessageTypeText}); err != nil {
		t.Fatalf("PostMessage() error = %v", err)
	}

	_, err = w.InterruptThread(ctx, inprocess.InterruptThreadRequest{
		ThreadID: thread.ID,
		ThreadInterruptRequest: agentworker.ThreadInterruptRequest{
			Kind: agentworker.ThreadInterruptKindCancelInput,
		},
	})
	if !errors.Is(err, rejectErr) {
		t.Fatalf("InterruptThread() error = %v, want wrapping %v", err, rejectErr)
	}
}

func TestWorkerSubscribeSessionEventsReceivesFutureChildThreadEvent(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)

	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newFakeRuntime(state.ID)
			rt.postFn = func(msg *agentworker.Message) error {
				go func() {
					rt.emitEvent(&agentworker.Event{
						ID:      "ev_child",
						Type:    agentworker.EventType("assistant"),
						Payload: []byte("child"),
						TS:      time.Now(),
					})
				}()
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	defer w.Close(ctx)
	parent, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread(parent) error = %v", err)
	}
	sub, err := w.SubscribeSessionEvents(ctx, parent.SessionID, 4)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents() error = %v", err)
	}
	defer sub.Close()
	child, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{ParentThreadID: parent.ID, Title: "child"})
	if err != nil {
		t.Fatalf("CreateThread(child) error = %v", err)
	}
	if err := w.PostMessage(ctx, child.ID, &agentworker.Message{ID: "msg_child", Type: agentworker.MessageTypeText}); err != nil {
		t.Fatalf("PostMessage(child) error = %v", err)
	}

	select {
	case ev := <-sub.Events:
		if ev == nil || ev.ID != "ev_child" || ev.ThreadID != child.ID {
			t.Fatalf("unexpected session event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for child session event")
	}
}

func TestWorkerSessionFanoutFalseDoesNotAffectThreadSubscription(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)

	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newFakeRuntime(state.ID)
			rt.postFn = func(msg *agentworker.Message) error {
				go func() {
					rt.emitEvent(&agentworker.Event{
						ID:              "ev_thread_only",
						ThreadID:        state.ID,
						Type:            agentworker.EventType("assistant"),
						Payload:         []byte("thread only"),
						FanoutToSession: testBoolPtr(false),
						TS:              time.Now(),
					})
				}()
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	defer w.Close(ctx)
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	threadSub, err := w.SubscribeThreadEvents(ctx, thread.ID, 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents() error = %v", err)
	}
	defer threadSub.Close()
	sessionSub, err := w.SubscribeSessionEvents(ctx, thread.SessionID, 1)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents() error = %v", err)
	}
	defer sessionSub.Close()

	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{ID: "msg_1", Type: agentworker.MessageTypeText}); err != nil {
		t.Fatalf("PostMessage() error = %v", err)
	}
	select {
	case ev := <-threadSub.Events:
		if ev == nil || ev.ID != "ev_thread_only" {
			t.Fatalf("unexpected thread event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for thread event")
	}
	select {
	case ev := <-sessionSub.Events:
		t.Fatalf("session subscription received event despite FanoutToSession=false: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWorkerCloseThreadDoesNotCloseSessionSubscribers(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)

	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newFakeRuntime(state.ID)
			rt.postFn = func(msg *agentworker.Message) error {
				go func() {
					rt.emitEvent(&agentworker.Event{
						ID:       "ev_after_close",
						ThreadID: state.ID,
						Type:     agentworker.EventType("assistant"),
						Payload:  []byte("after close"),
						TS:       time.Now(),
					})
				}()
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	defer w.Close(ctx)
	parent, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread(parent) error = %v", err)
	}
	threadSub, err := w.SubscribeThreadEvents(ctx, parent.ID, 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents() error = %v", err)
	}
	sessionSub, err := w.SubscribeSessionEvents(ctx, parent.SessionID, 2)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents() error = %v", err)
	}
	defer sessionSub.Close()
	if err := w.CloseThread(ctx, parent.ID); err != nil {
		t.Fatalf("CloseThread() error = %v", err)
	}
	select {
	case _, ok := <-threadSub.Events:
		if ok {
			t.Fatal("thread subscription still open after CloseThread")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for thread subscription close")
	}
	nextThread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: parent.SessionID, Title: "next"})
	if err != nil {
		t.Fatalf("CreateThread(next) error = %v", err)
	}
	if err := w.PostMessage(ctx, nextThread.ID, &agentworker.Message{ID: "msg_next", Type: agentworker.MessageTypeText}); err != nil {
		t.Fatalf("PostMessage(next) error = %v", err)
	}
	select {
	case ev := <-sessionSub.Events:
		if ev == nil || ev.ID != "ev_after_close" || ev.ThreadID != nextThread.ID {
			t.Fatalf("unexpected session event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for session event after CloseThread")
	}
}

func TestWorkerPublishEventHonorsPersistHint(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)

	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newFakeRuntime(state.ID)
			rt.postFn = func(msg *agentworker.Message) error {
				go func() {
					rt.emitEvent(&agentworker.Event{
						ID:                "ev_live_only",
						ThreadID:          state.ID,
						Type:              agentworker.EventType("llm_token"),
						Payload:           []byte("token"),
						PersistToEventLog: testBoolPtr(false),
						FanoutToSession:   testBoolPtr(true),
						TS:                time.Now(),
					})
					rt.yield(agentworker.ThreadYield{})
					close(rt.items)
				}()
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	sub, err := w.SubscribeThreadEvents(ctx, thread.ID, 1)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents() error = %v", err)
	}
	defer sub.Close()
	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{
		ID:      "msg_live_only",
		Type:    agentworker.MessageTypeText,
		Payload: []byte("hello"),
	}); err != nil {
		t.Fatalf("PostMessage() error = %v", err)
	}
	select {
	case ev := <-sub.Events:
		if ev == nil || ev.ID != "ev_live_only" {
			t.Fatalf("unexpected live event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for live event")
	}

	waitFor(t, func() bool {
		state, err := w.GetThread(ctx, thread.ID)
		return err == nil && state != nil && state.PendingBlock == nil
	})
	gotEvents, err := w.ListEvents(ctx, thread.ID, inprocess.ListEventsOptions{})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(gotEvents) != 0 {
		t.Fatalf("len(events) = %d, want 0; events=%+v", len(gotEvents), gotEvents)
	}
}

func TestWorkerRuntimeOutputItemsPersistEventBeforeFinish(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)

	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newFakeRuntime(state.ID)
			rt.postFn = func(msg *agentworker.Message) error {
				go func() {
					rt.emitEvent(&agentworker.Event{
						ID:       "ev_block",
						ThreadID: state.ID,
						Type:     agentworker.EventType("approve_requested"),
						Payload:  []byte("approval"),
						TS:       time.Now(),
					})
					rt.yield(agentworker.ThreadYield{
						Block: &agentworker.PendingBlock{
							TurnID:       "turn_1",
							InterruptID:  "interrupt_1",
							CheckpointID: "ckpt_1",
							Kind:         "approval",
						},
					})
					close(rt.items)
				}()
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{
		ID:      "msg_block",
		Type:    agentworker.MessageTypeText,
		Payload: []byte("hello"),
	}); err != nil {
		t.Fatalf("PostMessage() error = %v", err)
	}
	waitFor(t, func() bool {
		current, err := w.GetThread(ctx, thread.ID)
		return err == nil && current.PendingBlock != nil
	})
	events, err := w.ListEvents(ctx, thread.ID, inprocess.ListEventsOptions{})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].ID != "ev_block" {
		t.Fatalf("events = %+v, want ev_block persisted before finish", events)
	}
}

func TestWorkerPostMessageRestartsActorAfterRuntimeDone(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)
	var factoryCalls atomic.Int32
	firstClosed := make(chan struct{}, 1)

	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			callNo := factoryCalls.Add(1)
			rt := newFakeRuntime(state.ID)
			if callNo == 1 {
				rt.closeFn = func() { firstClosed <- struct{}{} }
			}
			rt.postFn = func(msg *agentworker.Message) error {
				go func() {
					rt.yield(agentworker.ThreadYield{})
					close(rt.items)
				}()
				return nil
			}
			return rt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if _, err := threadStates.UpdateThread(ctx, thread.ID, inprocess.UpdateThreadStatePatch{
		PendingBlock: &agentworker.PendingBlock{
			TurnID:       "turn_1",
			InterruptID:  "interrupt_1",
			CheckpointID: "ckpt_1",
			Kind:         "approve",
		},
	}); err != nil {
		t.Fatalf("UpdateThread() error = %v", err)
	}
	if err := w.ResumeFromBlock(ctx, inprocess.ResumeFromBlockRequest{
		ThreadID:    thread.ID,
		InterruptID: "interrupt_1",
	}); err != nil {
		t.Fatalf("ResumeFromBlock() error = %v", err)
	}

	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{
		ID:      "msg_1",
		Type:    agentworker.MessageTypeText,
		Payload: []byte("hello"),
	}); err != nil {
		t.Fatalf("PostMessage(first) error = %v", err)
	}

	select {
	case <-firstClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first runtime close")
	}

	if err := w.PostMessage(ctx, thread.ID, &agentworker.Message{
		ID:      "msg_2",
		Type:    agentworker.MessageTypeText,
		Payload: []byte("hello again"),
	}); err != nil {
		t.Fatalf("PostMessage(second) error = %v", err)
	}

	waitFor(t, func() bool { return factoryCalls.Load() == 2 })
}

func TestWorkerResumeFromBlockClearsPendingBlock(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)

	w, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			return newFakeRuntime(state.ID), nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	thread, err := w.CreateThread(ctx, inprocess.CreateThreadSpec{UserID: 1001, SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if _, err := threadStates.UpdateThread(ctx, thread.ID, inprocess.UpdateThreadStatePatch{
		PendingBlock: &agentworker.PendingBlock{
			TurnID:       "turn_1",
			InterruptID:  "interrupt_1",
			CheckpointID: "ckpt_1",
			Kind:         "approve",
		},
	}); err != nil {
		t.Fatalf("UpdateThread() error = %v", err)
	}
	if err := w.ResumeFromBlock(ctx, inprocess.ResumeFromBlockRequest{
		ThreadID:    thread.ID,
		InterruptID: "interrupt_1",
	}); err != nil {
		t.Fatalf("ResumeFromBlock() error = %v", err)
	}
	current, err := w.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread() error = %v", err)
	}
	if current.PendingBlock != nil {
		t.Fatalf("PendingBlock = %+v, want nil", current.PendingBlock)
	}
}

type fakeRuntime struct {
	threadID     string
	items        chan agentworker.ThreadOutputItem
	initFn       func(ctx context.Context) error
	postFn       func(msg *agentworker.Message) error
	postCtxFn    func(ctx context.Context, msg *agentworker.Message) error
	postResult   *agentworker.PostMessageResult
	interruptFn  func(ctx context.Context, req agentworker.ThreadInterruptRequest) error
	activeTurnFn func() *agentworker.ActiveTurn
	closeFn      func()
}

func newFakeRuntime(threadID string) *fakeRuntime {
	return &fakeRuntime{
		threadID: threadID,
		items:    make(chan agentworker.ThreadOutputItem, 8),
	}
}

func (r *fakeRuntime) ThreadID() string { return r.threadID }

func (r *fakeRuntime) Init(ctx context.Context) (*agentworker.ThreadOutput, error) {
	if r.initFn != nil {
		if err := r.initFn(ctx); err != nil {
			return nil, err
		}
	}
	return &agentworker.ThreadOutput{
		Items: r.items,
	}, nil
}

func (r *fakeRuntime) emitEvent(ev *agentworker.Event) {
	r.items <- agentworker.ThreadOutputItem{Event: ev}
}

func (r *fakeRuntime) yield(yield agentworker.ThreadYield) {
	r.items <- agentworker.ThreadOutputItem{Yield: &yield}
}

func (r *fakeRuntime) PostMessage(ctx context.Context, msg *agentworker.Message) (result *agentworker.PostMessageResult, err error) {
	if r.postCtxFn != nil {
		return nil, r.postCtxFn(ctx, msg)
	}
	if r.postFn != nil {
		return nil, r.postFn(msg)
	}
	return r.postResult, nil
}

func (r *fakeRuntime) Interrupt(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
	if r.interruptFn != nil {
		return r.interruptFn(ctx, req)
	}
	return nil
}

func (r *fakeRuntime) ActiveTurn() *agentworker.ActiveTurn {
	if r.activeTurnFn != nil {
		return r.activeTurnFn()
	}
	return nil
}

func (r *fakeRuntime) Close(ctx context.Context) error {
	if r.closeFn != nil {
		r.closeFn()
	}
	return nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func assertThreadIDs(t *testing.T, threads []*inprocess.ThreadState, want ...string) {
	t.Helper()
	if len(threads) != len(want) {
		t.Fatalf("len(threads) = %d, want %d; got=%v want=%v", len(threads), len(want), threadIDs(threads), want)
	}
	for i := range want {
		if threads[i].ID != want[i] {
			t.Fatalf("threads[%d].ID = %q, want %q; got=%v want=%v", i, threads[i].ID, want[i], threadIDs(threads), want)
		}
	}
}

func threadIDs(threads []*inprocess.ThreadState) []string {
	out := make([]string, 0, len(threads))
	for _, thread := range threads {
		if thread == nil {
			out = append(out, "<nil>")
			continue
		}
		out = append(out, thread.ID)
	}
	return out
}

func testBoolPtr(v bool) *bool {
	return &v
}

func TestSQLiteThreadStateStorePersistsProfile(t *testing.T) {
	ctx := context.Background()
	threadStates, _ := openSQLiteStores(t)
	created, err := threadStates.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID:    1001,
		SessionID: "sess_1",
		Title:     "main",
		Profile:   inprocess.ThreadProfile{Role: "reviewer", Cwd: "/repo"},
	})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	got, err := threadStates.GetThread(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetThread() error = %v", err)
	}
	if got.Profile.Role != "reviewer" {
		t.Fatalf("got.Profile.Role = %q, want reviewer", got.Profile.Role)
	}
	if got.Profile.Cwd != "/repo" {
		t.Fatalf("got.Profile.Cwd = %q, want /repo", got.Profile.Cwd)
	}
	listed, err := threadStates.ListThreads(ctx, inprocess.ListThreadsOptions{UserID: 1001, Cwd: "/repo"})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Profile.Role != "reviewer" || listed[0].Profile.Cwd != "/repo" {
		t.Fatalf("listed = %+v", listed)
	}
	legacy, err := threadStates.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID:    1001,
		SessionID: "sess_1",
		Title:     "legacy",
	})
	if err != nil {
		t.Fatalf("CreateThread(legacy) error = %v", err)
	}
	if !legacy.Profile.Empty() {
		t.Fatalf("legacy.Profile = %+v, want empty", legacy.Profile)
	}
}

func openSQLiteStores(t *testing.T) (*localstore.SQLiteThreadStateStore, *localstore.SQLiteEventStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "agentworker-inprocess.sqlite")
	threadStates, err := localstore.OpenSQLiteThreadStateStore(dbPath, "")
	if err != nil {
		t.Fatalf("OpenSQLiteThreadStateStore() error = %v", err)
	}
	events, err := localstore.OpenSQLiteEventStore(dbPath, "")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	return threadStates, events
}
