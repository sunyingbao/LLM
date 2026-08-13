package clienttest

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	"eino-cli/deepagent/cloud/protocol/timeline"
	runtimeclient "eino-cli/deepagent/runtime"
)

type Fake struct {
	mu          sync.Mutex
	nextThread  int
	nextTurn    int
	nextEvent   int
	threads     map[string]*runtimeclient.Thread
	events      map[string][]timeline.Event
	subscribers map[string]map[*fakeSubscription]struct{}
}

func NewFake() (client *Fake) {
	client = &Fake{
		threads:     make(map[string]*runtimeclient.Thread),
		events:      make(map[string][]timeline.Event),
		subscribers: make(map[string]map[*fakeSubscription]struct{}),
	}
	return client
}

func (client *Fake) CreateThread(ctx context.Context, req runtimeclient.CreateThreadRequest) (result *runtimeclient.CreateThreadResult, err error) {
	if req.Runtime != runtimeclient.RuntimeLocal {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "create_thread", Runtime: req.Runtime}
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	client.nextThread++
	thread := &runtimeclient.Thread{
		Ref:            runtimeclient.GlobalThreadRef{Runtime: req.Runtime, Namespace: req.Namespace, ThreadID: fmt.Sprintf("thread-%d", client.nextThread)},
		DefinitionName: req.Definition.Name, DefinitionVersion: req.Definition.Version,
		Workspace: req.Workspace, Title: req.Title, State: runtimeclient.ThreadStateIdle,
	}
	client.threads[thread.Ref.ThreadID] = thread
	copy := *thread
	return &runtimeclient.CreateThreadResult{Thread: &copy}, nil
}

func (client *Fake) Submit(ctx context.Context, req runtimeclient.SubmitRequest) (result *runtimeclient.SubmitResult, err error) {
	if err = req.Input.Validate(); err != nil {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "submit", Cause: err}
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if err = client.validateRefLocked(req.Ref); err != nil {
		return nil, err
	}
	client.nextTurn++
	turnID := fmt.Sprintf("turn-%d", client.nextTurn)
	eventType := "TURN_FINISHED"
	if len(req.Input.Parts) > 0 && req.Input.Parts[0].Text == "block" {
		eventType = "INTERRUPT_REQUIRED"
		client.threads[req.Ref.ThreadID].State = runtimeclient.ThreadStateBlocked
	}
	if eventType == "TURN_FINISHED" {
		text := req.Input.Parts[0].Text
		toolStart, _ := json.Marshal(protoevent.ToolCallEventPayload{ToolCallID: "tool-" + turnID, ToolName: "fixture", Status: protoevent.ToolCallStatusStarted})
		client.appendEventLocked(req.Ref, timeline.Event{EventType: protoevent.EventTypeToolCallStarted.String(), ThreadID: req.Ref.ThreadID, TurnID: turnID, Payload: timeline.NormalizePayload(toolStart)})
		toolEnd, _ := json.Marshal(protoevent.ToolCallEventPayload{ToolCallID: "tool-" + turnID, ToolName: "fixture", Status: protoevent.ToolCallStatusFinished})
		client.appendEventLocked(req.Ref, timeline.Event{EventType: protoevent.EventTypeToolCallFinished.String(), ThreadID: req.Ref.ThreadID, TurnID: turnID, Payload: timeline.NormalizePayload(toolEnd)})
		delta, _ := json.Marshal(protoevent.AssistantDeltaEventPayload{Delta: text})
		client.appendEventLocked(req.Ref, timeline.Event{EventType: protoevent.EventTypeAssistantDelta.String(), ThreadID: req.Ref.ThreadID, TurnID: turnID, Payload: timeline.NormalizePayload(delta)})
	}
	client.appendEventLocked(req.Ref, timeline.Event{EventType: eventType, ThreadID: req.Ref.ThreadID, TurnID: turnID, Payload: timeline.NormalizePayload(nil)})
	return &runtimeclient.SubmitResult{Ref: req.Ref, TurnID: turnID}, nil
}

func (client *Fake) Resume(ctx context.Context, req runtimeclient.ResumeRequest) (result *runtimeclient.SubmitResult, err error) {
	if err = req.Payload.Validate(); err != nil {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "resume", Cause: err}
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if err = client.validateRefLocked(req.Ref); err != nil {
		return nil, err
	}
	client.threads[req.Ref.ThreadID].State = runtimeclient.ThreadStateIdle
	client.appendEventLocked(req.Ref, timeline.Event{EventType: "TURN_FINISHED", ThreadID: req.Ref.ThreadID, TurnID: req.Payload.TurnID, Payload: timeline.NormalizePayload(nil)})
	return &runtimeclient.SubmitResult{Ref: req.Ref, TurnID: req.Payload.TurnID}, nil
}

func (client *Fake) Stop(ctx context.Context, req runtimeclient.StopRequest) (result *runtimeclient.StopResult, err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if err = client.validateRefLocked(req.Ref); err != nil {
		return nil, err
	}
	return &runtimeclient.StopResult{Ref: req.Ref, Stopped: true}, nil
}

func (client *Fake) GetThread(ctx context.Context, ref runtimeclient.GlobalThreadRef) (thread *runtimeclient.Thread, err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if err = client.validateRefLocked(ref); err != nil {
		return nil, err
	}
	copy := *client.threads[ref.ThreadID]
	return &copy, nil
}

func (client *Fake) ListThreads(ctx context.Context, query runtimeclient.ListThreadsQuery) (result *runtimeclient.ListThreadsResult, err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	result = &runtimeclient.ListThreadsResult{}
	for _, thread := range client.threads {
		if query.Runtime != "" && thread.Ref.Runtime != query.Runtime || query.Namespace != "" && thread.Ref.Namespace != query.Namespace {
			continue
		}
		copy := *thread
		result.Threads = append(result.Threads, &copy)
	}
	sort.Slice(result.Threads, func(i, j int) bool { return result.Threads[i].Ref.ThreadID < result.Threads[j].Ref.ThreadID })
	return result, nil
}

func (client *Fake) ListTimeline(ctx context.Context, query runtimeclient.TimelineQuery) (result *runtimeclient.TimelineResult, err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if err = client.validateRefLocked(query.Ref); err != nil {
		return nil, err
	}
	result = &runtimeclient.TimelineResult{Events: append([]timeline.Event(nil), client.events[query.Ref.ThreadID]...)}
	return result, nil
}

func (client *Fake) SubscribeTimeline(ctx context.Context, query runtimeclient.TimelineQuery) (subscription runtimeclient.TimelineSubscription, err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if err = client.validateRefLocked(query.Ref); err != nil {
		return nil, err
	}
	sub := &fakeSubscription{events: make(chan timeline.Event, 16)}
	if client.subscribers[query.Ref.ThreadID] == nil {
		client.subscribers[query.Ref.ThreadID] = make(map[*fakeSubscription]struct{})
	}
	client.subscribers[query.Ref.ThreadID][sub] = struct{}{}
	sub.onClose = func() {
		client.mu.Lock()
		delete(client.subscribers[query.Ref.ThreadID], sub)
		client.mu.Unlock()
	}
	return sub, nil
}

func (client *Fake) validateRefLocked(ref runtimeclient.GlobalThreadRef) (err error) {
	if ref.Runtime != runtimeclient.RuntimeLocal {
		return &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "validate_ref", Runtime: ref.Runtime}
	}
	if _, ok := client.threads[ref.ThreadID]; !ok {
		return &runtimeclient.Error{Code: runtimeclient.ErrorCodeNotFound, Op: "validate_ref", Runtime: ref.Runtime}
	}
	return nil
}

func (client *Fake) appendEventLocked(ref runtimeclient.GlobalThreadRef, event timeline.Event) {
	client.nextEvent++
	event.EventID = fmt.Sprintf("event-%d", client.nextEvent)
	client.events[ref.ThreadID] = append(client.events[ref.ThreadID], event)
	for subscriber := range client.subscribers[ref.ThreadID] {
		subscriber.events <- event
	}
}

type fakeSubscription struct {
	events  chan timeline.Event
	onClose func()
	once    sync.Once
}

func (subscription *fakeSubscription) Events() (events <-chan timeline.Event) {
	return subscription.events
}
func (subscription *fakeSubscription) Err() (err error) { return nil }
func (subscription *fakeSubscription) Close() (err error) {
	subscription.once.Do(func() {
		if subscription.onClose != nil {
			subscription.onClose()
		}
		close(subscription.events)
	})
	return nil
}
