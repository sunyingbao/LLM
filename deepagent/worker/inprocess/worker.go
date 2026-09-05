package inprocess

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"eino-cli/deepagent/worker"
)

const defaultActorBuffer = 16
const defaultSubscriberBuffer = 16

type Dependencies struct {
	ThreadStateStore ThreadStateStore
	EventStore       EventStore
	ThreadFactory    ThreadFactory
}

func (d Dependencies) Validate() error {
	switch {
	case d.ThreadStateStore == nil:
		return ErrMissingThreadStateStore
	case d.EventStore == nil:
		return ErrMissingEventStore
	case d.ThreadFactory == nil:
		return ErrMissingThreadFactory
	default:
		return nil
	}
}

// Worker is the local, single-process agent host.
//
// It owns private live thread actors in memory and reconstructs them lazily
// from durable ThreadState when input arrives.
type Worker struct {
	threadStateStore ThreadStateStore
	eventStore       EventStore
	threadFactory    ThreadFactory

	mu          sync.Mutex
	actors      map[string]*threadActor
	subs        map[string]map[uint64]chan *agentworker.Event
	sessionSubs map[string]map[uint64]chan *agentworker.Event
	nextSubID   uint64
}

type InterruptThreadRequest struct {
	ThreadID string
	agentworker.ThreadInterruptRequest
}

type InterruptThreadStatus string

const (
	InterruptThreadAccepted     InterruptThreadStatus = "accepted"
	InterruptThreadNoLiveActor  InterruptThreadStatus = "no_live_actor"
	InterruptThreadNoActiveTurn InterruptThreadStatus = "no_active_turn"
)

type InterruptThreadResult struct {
	ThreadID   string
	Status     InterruptThreadStatus
	ActiveTurn *agentworker.ActiveTurn
}

func NewWorker(deps Dependencies) (*Worker, error) {
	if err := deps.Validate(); err != nil {
		return nil, err
	}
	return &Worker{
		threadStateStore: deps.ThreadStateStore,
		eventStore:       deps.EventStore,
		threadFactory:    deps.ThreadFactory,
		actors:           make(map[string]*threadActor),
		subs:             make(map[string]map[uint64]chan *agentworker.Event),
		sessionSubs:      make(map[string]map[uint64]chan *agentworker.Event),
	}, nil
}

func (w *Worker) CreateThread(ctx context.Context, spec CreateThreadSpec) (*ThreadState, error) {
	if w == nil || w.threadStateStore == nil {
		return nil, ErrMissingThreadStateStore
	}
	if spec.SessionID == "" && spec.ParentThreadID == "" {
		return nil, ErrMissingSessionID
	}
	if spec.ParentThreadID != "" {
		parent, err := w.threadStateStore.GetThread(ctx, spec.ParentThreadID)
		if err != nil {
			return nil, err
		}
		if spec.UserID == 0 {
			spec.UserID = parent.UserID
		} else if parent.UserID != 0 && spec.UserID != parent.UserID {
			return nil, ErrInvalidThreadState
		}
		if spec.SessionID == "" {
			spec.SessionID = parent.SessionID
		} else if parent.SessionID != "" && spec.SessionID != parent.SessionID {
			return nil, ErrInvalidThreadState
		}
		if spec.Profile.Cwd == "" {
			spec.Profile.Cwd = parent.Profile.Cwd
		}
	}
	if spec.UserID == 0 {
		return nil, ErrMissingUserID
	}
	created, err := w.threadStateStore.CreateThread(ctx, spec)
	if err != nil {
		return nil, err
	}
	if created == nil {
		return nil, ErrInvalidThreadState
	}
	return created, nil
}

func (w *Worker) GetThread(ctx context.Context, threadID string) (*ThreadState, error) {
	if w == nil || w.threadStateStore == nil {
		return nil, ErrMissingThreadStateStore
	}
	if threadID == "" {
		return nil, ErrMissingThreadID
	}
	return w.threadStateStore.GetThread(ctx, threadID)
}

func (w *Worker) ListThreadsBySession(ctx context.Context, sessionID string, opts ListThreadsOptions) ([]*ThreadState, error) {
	if w == nil || w.threadStateStore == nil {
		return nil, ErrMissingThreadStateStore
	}
	if sessionID == "" {
		return nil, ErrMissingSessionID
	}
	return w.threadStateStore.ListThreadsBySession(ctx, sessionID, opts)
}

func (w *Worker) ListThreads(ctx context.Context, opts ListThreadsOptions) ([]*ThreadState, error) {
	if w == nil || w.threadStateStore == nil {
		return nil, ErrMissingThreadStateStore
	}
	return w.threadStateStore.ListThreads(ctx, opts)
}

func (w *Worker) PostMessage(ctx context.Context, threadID string, msg *agentworker.Message) (err error) {
	_, err = w.PostMessageWithResult(ctx, threadID, msg)
	return err
}

// PostMessageWithResult preserves the stable turn assignment returned by the runtime.
func (w *Worker) PostMessageWithResult(ctx context.Context, threadID string, msg *agentworker.Message) (result *agentworker.PostMessageResult, err error) {
	if w == nil || w.threadStateStore == nil {
		return nil, ErrMissingThreadStateStore
	}
	if threadID == "" {
		return nil, ErrMissingThreadID
	}
	if msg == nil {
		return nil, ErrInvalidMessage
	}
	state, err := w.threadStateStore.GetThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if state.ClosedAt != nil {
		return nil, ErrThreadClosed
	}
	if state.PendingBlock != nil {
		return nil, ErrThreadBlocked
	}
	for {
		actor, err := w.ensureActor(context.Background(), state)
		if err != nil {
			return nil, err
		}
		if result, err = actor.enqueue(ctx, msg); err == nil {
			return result, nil
		} else if err != errActorStopped {
			return nil, err
		}
		state, err = w.threadStateStore.GetThread(ctx, threadID)
		if err != nil {
			return nil, err
		}
		if state.ClosedAt != nil {
			return nil, ErrThreadClosed
		}
		if state.PendingBlock != nil {
			return nil, ErrThreadBlocked
		}
	}
}

// ResumeFromBlock only clears the durable pending block marker.
// The actual follow-up input should be sent later via PostMessage.
func (w *Worker) ResumeFromBlock(ctx context.Context, req ResumeFromBlockRequest) error {
	if w == nil || w.threadStateStore == nil {
		return ErrMissingThreadStateStore
	}
	if req.ThreadID == "" {
		return ErrMissingThreadID
	}
	if req.InterruptID == "" {
		return ErrInvalidResumeRequest
	}
	state, err := w.threadStateStore.GetThread(ctx, req.ThreadID)
	if err != nil {
		return err
	}
	if state.PendingBlock == nil {
		return ErrInvalidResumeRequest
	}
	if state.PendingBlock.InterruptID != "" && state.PendingBlock.InterruptID != req.InterruptID {
		return ErrInvalidResumeRequest
	}
	_, err = w.threadStateStore.UpdateThread(ctx, req.ThreadID, UpdateThreadStatePatch{
		ClearPendingBlock: true,
	})
	return err
}

func (w *Worker) ListEvents(ctx context.Context, threadID string, opts ListEventsOptions) ([]*agentworker.Event, error) {
	if w == nil || w.eventStore == nil {
		return nil, ErrMissingEventStore
	}
	if threadID == "" {
		return nil, ErrMissingThreadID
	}
	return w.eventStore.ListEvents(ctx, threadID, opts)
}

func (w *Worker) InterruptThread(ctx context.Context, req InterruptThreadRequest) (*InterruptThreadResult, error) {
	if w == nil || w.threadStateStore == nil {
		return nil, ErrMissingThreadStateStore
	}
	if req.ThreadID == "" {
		return nil, ErrMissingThreadID
	}
	if req.Kind == "" {
		return nil, ErrInvalidInterruptRequest
	}
	state, err := w.threadStateStore.GetThread(ctx, req.ThreadID)
	if err != nil {
		return nil, err
	}
	if state.ClosedAt != nil {
		return nil, ErrThreadClosed
	}

	w.mu.Lock()
	actor := w.actors[req.ThreadID]
	w.mu.Unlock()
	result := &InterruptThreadResult{ThreadID: req.ThreadID}
	if actor == nil {
		result.Status = InterruptThreadNoLiveActor
		return result, nil
	}
	activeTurn := cloneActiveTurn(actor.runtime.ActiveTurn())
	if activeTurn == nil {
		result.Status = InterruptThreadNoActiveTurn
		return result, nil
	}
	if err := actor.runtime.Interrupt(ctx, req.ThreadInterruptRequest); err != nil {
		return nil, fmt.Errorf("AgentThread.Interrupt: %w", err)
	}
	result.Status = InterruptThreadAccepted
	result.ActiveTurn = activeTurn
	return result, nil
}

func (w *Worker) CloseThread(ctx context.Context, threadID string) error {
	if w == nil || w.threadStateStore == nil {
		return ErrMissingThreadStateStore
	}
	if threadID == "" {
		return ErrMissingThreadID
	}
	state, err := w.threadStateStore.GetThread(ctx, threadID)
	if err != nil {
		return err
	}
	if state.ClosedAt == nil {
		now := time.Now()
		if _, err := w.threadStateStore.UpdateThread(ctx, threadID, UpdateThreadStatePatch{
			ClearPendingBlock: true,
			ClosedAt:          &now,
		}); err != nil {
			return err
		}
	}
	w.mu.Lock()
	actor := w.actors[threadID]
	delete(w.actors, threadID)
	subs := w.subs[threadID]
	delete(w.subs, threadID)
	w.mu.Unlock()
	if actor != nil {
		actor.shutdown(ctx)
	}
	for _, ch := range subs {
		close(ch)
	}
	return nil
}

// SubscribeSessionEvents subscribes to future live events of one session.
//
// Historical replay is not included; callers should subscribe first, then merge
// durable history from their thread/event stores with these live events.
func (w *Worker) SubscribeSessionEvents(ctx context.Context, sessionID string, buffer int) (*SessionEventSubscription, error) {
	if w == nil || w.threadStateStore == nil {
		return nil, ErrMissingThreadStateStore
	}
	if sessionID == "" {
		return nil, ErrMissingSessionID
	}
	if buffer <= 0 {
		buffer = defaultSubscriberBuffer
	}
	ch := make(chan *agentworker.Event, buffer)
	w.mu.Lock()
	w.nextSubID++
	subID := w.nextSubID
	if w.sessionSubs[sessionID] == nil {
		w.sessionSubs[sessionID] = make(map[uint64]chan *agentworker.Event)
	}
	w.sessionSubs[sessionID][subID] = ch
	w.mu.Unlock()
	sub := &SessionEventSubscription{
		Events: ch,
		cancel: func() {
			w.removeSessionSubscription(sessionID, subID)
		},
	}
	return sub, nil
}

// SubscribeThreadEvents subscribes to future live events of one thread.
//
// Historical replay is not included; callers should use ListEvents first when
// they need history before live updates.
func (w *Worker) SubscribeThreadEvents(ctx context.Context, threadID string, buffer int) (*ThreadEventSubscription, error) {
	if w == nil || w.threadStateStore == nil {
		return nil, ErrMissingThreadStateStore
	}
	if threadID == "" {
		return nil, ErrMissingThreadID
	}
	if _, err := w.threadStateStore.GetThread(ctx, threadID); err != nil {
		return nil, err
	}
	if buffer <= 0 {
		buffer = defaultSubscriberBuffer
	}
	ch := make(chan *agentworker.Event, buffer)
	w.mu.Lock()
	w.nextSubID++
	subID := w.nextSubID
	if w.subs[threadID] == nil {
		w.subs[threadID] = make(map[uint64]chan *agentworker.Event)
	}
	w.subs[threadID][subID] = ch
	w.mu.Unlock()
	sub := &ThreadEventSubscription{
		Events: ch,
		cancel: func() {
			w.removeSubscription(threadID, subID)
		},
	}
	return sub, nil
}

func (w *Worker) Close(ctx context.Context) error {
	w.mu.Lock()
	actors := make([]*threadActor, 0, len(w.actors))
	for _, actor := range w.actors {
		actors = append(actors, actor)
	}
	w.actors = make(map[string]*threadActor)
	subs := w.subs
	w.subs = make(map[string]map[uint64]chan *agentworker.Event)
	sessionSubs := w.sessionSubs
	w.sessionSubs = make(map[string]map[uint64]chan *agentworker.Event)
	w.mu.Unlock()
	for _, actor := range actors {
		actor.shutdown(ctx)
	}
	for _, threadSubs := range subs {
		for _, ch := range threadSubs {
			close(ch)
		}
	}
	for _, subs := range sessionSubs {
		for _, ch := range subs {
			close(ch)
		}
	}
	return nil
}

type postEnvelope struct {
	msg *agentworker.Message
	ack chan postAck
}

type postAck struct {
	result *agentworker.PostMessageResult
	err    error
}

type threadActor struct {
	threadID  string
	sessionID string
	runtime   agentworker.AgentThread
	input     chan postEnvelope
	stop      chan struct{}
	once      sync.Once
}

func (a *threadActor) enqueue(ctx context.Context, msg *agentworker.Message) (result *agentworker.PostMessageResult, err error) {
	env := postEnvelope{
		msg: msg,
		ack: make(chan postAck, 1),
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.stop:
		return nil, errActorStopped
	case a.input <- env:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case ack := <-env.ack:
		return ack.result, ack.err
	case <-a.stop:
		return nil, errActorStopped
	}
}

func (a *threadActor) shutdown(ctx context.Context) {
	a.once.Do(func() {
		close(a.stop)
		_ = a.runtime.Close(ctx)
	})
}

var errActorStopped = errors.New("actor stopped")

func (w *Worker) ensureActor(ctx context.Context, state *ThreadState) (*threadActor, error) {
	if state == nil || state.ID == "" {
		return nil, ErrInvalidThreadState
	}
	w.mu.Lock()
	if actor := w.actors[state.ID]; actor != nil {
		w.mu.Unlock()
		return actor, nil
	}
	w.mu.Unlock()

	runtime, output, err := w.buildRuntime(ctx, state)
	if err != nil {
		return nil, err
	}
	actor := &threadActor{
		threadID:  state.ID,
		sessionID: state.SessionID,
		runtime:   runtime,
		input:     make(chan postEnvelope, defaultActorBuffer),
		stop:      make(chan struct{}),
	}

	w.mu.Lock()
	if existing := w.actors[state.ID]; existing != nil {
		w.mu.Unlock()
		_ = runtime.Close(ctx)
		return existing, nil
	}
	w.actors[state.ID] = actor
	w.mu.Unlock()

	go w.runActor(actor, output)
	return actor, nil
}

func (w *Worker) buildRuntime(ctx context.Context, state *ThreadState) (agentworker.AgentThread, *agentworker.ThreadOutput, error) {
	runtime, err := w.threadFactory(ctx, state, w)
	if err != nil {
		return nil, nil, err
	}
	if runtime == nil {
		return nil, nil, ErrInvalidThreadState
	}
	output, err := runtime.Init(ctx)
	if err != nil {
		_ = runtime.Close(ctx)
		return nil, nil, err
	}
	if output == nil {
		output = &agentworker.ThreadOutput{}
	}
	return runtime, output, nil
}

func (w *Worker) runActor(actor *threadActor, output *agentworker.ThreadOutput) {
	items := (<-chan agentworker.ThreadOutputItem)(nil)
	if output != nil {
		items = output.Items
	}
	for {
		select {
		case item, ok := <-items:
			if !ok {
				w.yieldActor(actor, agentworker.ThreadYield{})
				return
			}
			if w.handleRuntimeOutputItem(actor, item) {
				return
			}
			continue
		default:
		}
		select {
		case <-actor.stop:
			w.removeActor(actor)
			return
		case env := <-actor.input:
			result, err := actor.runtime.PostMessage(context.Background(), env.msg)
			env.ack <- postAck{result: result, err: err}
		case item, ok := <-items:
			if !ok {
				w.yieldActor(actor, agentworker.ThreadYield{})
				return
			}
			if w.handleRuntimeOutputItem(actor, item) {
				return
			}
		}
	}
}

func (w *Worker) handleRuntimeOutputItem(actor *threadActor, item agentworker.ThreadOutputItem) bool {
	if item.Event != nil {
		if item.Event.ThreadID == "" {
			item.Event.ThreadID = actor.threadID
		}
		w.publishActorEvent(context.Background(), actor, item.Event)
	}
	if item.Yield != nil {
		w.yieldActor(actor, *item.Yield)
		return true
	}
	return false
}

func (w *Worker) yieldActor(actor *threadActor, yield agentworker.ThreadYield) {
	ctx := context.Background()
	_, _ = w.applyRuntimeYield(ctx, actor.threadID, yield)
	actor.shutdown(ctx)
	w.removeActor(actor)
	// Wake any blocked senders after the actor is removed.
	for {
		select {
		case env := <-actor.input:
			env.ack <- postAck{err: errActorStopped}
		default:
			return
		}
	}
}

func (w *Worker) applyRuntimeYield(ctx context.Context, threadID string, yield agentworker.ThreadYield) (*ThreadState, error) {
	patch := UpdateThreadStatePatch{}
	if yield.Block != nil {
		patch.PendingBlock = clonePendingBlock(yield.Block)
	} else {
		patch.ClearPendingBlock = true
	}
	return w.threadStateStore.UpdateThread(ctx, threadID, patch)
}

func (w *Worker) removeActor(actor *threadActor) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if current := w.actors[actor.threadID]; current == actor {
		delete(w.actors, actor.threadID)
	}
}

func (w *Worker) publishEvent(ctx context.Context, ev *agentworker.Event) {
	w.publishActorEvent(ctx, nil, ev)
}

func (w *Worker) publishActorEvent(ctx context.Context, actor *threadActor, ev *agentworker.Event) {
	if ev == nil {
		return
	}
	if ev.PersistToEventLog == nil || *ev.PersistToEventLog {
		_ = w.eventStore.AppendEvent(ctx, ev)
	}
	w.mu.Lock()
	if threadSubs := w.subs[ev.ThreadID]; len(threadSubs) > 0 {
		for _, ch := range threadSubs {
			select {
			case ch <- cloneEvent(ev):
			default:
			}
		}
	}
	if actor != nil && actor.sessionID != "" && (ev.FanoutToSession == nil || *ev.FanoutToSession) {
		if subs := w.sessionSubs[actor.sessionID]; len(subs) > 0 {
			for _, ch := range subs {
				select {
				case ch <- cloneEvent(ev):
				default:
				}
			}
		}
	}
	w.mu.Unlock()
}

func (w *Worker) removeSubscription(threadID string, subID uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	threadSubs := w.subs[threadID]
	if threadSubs == nil {
		return
	}
	ch, ok := threadSubs[subID]
	if !ok {
		return
	}
	delete(threadSubs, subID)
	if len(threadSubs) == 0 {
		delete(w.subs, threadID)
	}
	close(ch)
}

func (w *Worker) removeSessionSubscription(sessionID string, subID uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	subs := w.sessionSubs[sessionID]
	if subs == nil {
		return
	}
	ch, ok := subs[subID]
	if !ok {
		return
	}
	delete(subs, subID)
	if len(subs) == 0 {
		delete(w.sessionSubs, sessionID)
	}
	close(ch)
}

func clonePendingBlock(in *agentworker.PendingBlock) *agentworker.PendingBlock {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneActiveTurn(in *agentworker.ActiveTurn) *agentworker.ActiveTurn {
	if in == nil {
		return nil
	}
	out := *in
	if in.ConsumedMessageIDs != nil {
		out.ConsumedMessageIDs = append([]string(nil), in.ConsumedMessageIDs...)
	}
	return &out
}

func cloneEvent(in *agentworker.Event) *agentworker.Event {
	if in == nil {
		return nil
	}
	out := *in
	if in.Payload != nil {
		out.Payload = append([]byte(nil), in.Payload...)
	}
	if in.Metadata != nil {
		out.Metadata = make(map[string]string, len(in.Metadata))
		for k, v := range in.Metadata {
			out.Metadata[k] = v
		}
	}
	return &out
}
