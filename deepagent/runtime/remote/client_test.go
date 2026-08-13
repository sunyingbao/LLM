package remote

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	cloudapi "eino-cli/deepagent/cloud/api"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
	"eino-cli/deepagent/definition"
	runtimeclient "eino-cli/deepagent/runtime"
	"eino-cli/deepagent/runtime/clienttest"
)

type coordinatorStub struct {
	created       *cloudapi.Thread
	listed        []*cloudapi.Thread
	sent          cloudapi.SendMessageRequest
	events        []*timeline.Event
	stream        cloudapi.TimelineStream
	mu            sync.Mutex
	threads       map[string]*cloudapi.Thread
	eventByThread map[string][]*timeline.Event
	live          chan *cloudapi.TimelineFrame
	nextThread    int
	nextTurn      int
	nextEvent     int
}

func (stub *coordinatorStub) CreateThread(ctx context.Context, req cloudapi.CreateThreadRequest) (result *cloudapi.CreateThreadResult, err error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.created != nil {
		return &cloudapi.CreateThreadResult{Thread: stub.created}, nil
	}
	stub.ensureStateLocked()
	stub.nextThread++
	thread := &cloudapi.Thread{ID: fmt.Sprintf("thread-%d", stub.nextThread), SessionID: req.SessionID, ParentThreadID: req.ParentThreadID, Title: req.Title, Profile: req.Profile, Metadata: req.Metadata, Status: "IDLE"}
	stub.threads[thread.ID] = thread
	copy := *thread
	return &cloudapi.CreateThreadResult{Thread: &copy}, nil
}
func (stub *coordinatorStub) SendMessage(ctx context.Context, req cloudapi.SendMessageRequest) (result *cloudapi.MessageRef, err error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.sent = req
	stub.ensureStateLocked()
	stub.nextTurn++
	turnID := fmt.Sprintf("turn-%d", stub.nextTurn)
	eventType := "TURN_FINISHED"
	if bytes.Contains(req.Payload, []byte("block")) {
		eventType = "INTERRUPT_REQUIRED"
		stub.threads[req.ThreadID].Status = "BLOCKED"
	}
	stub.appendEventLocked(req.ThreadID, turnID, eventType)
	return &cloudapi.MessageRef{ThreadID: req.ThreadID, MessageID: "message-1"}, nil
}
func (stub *coordinatorStub) ResumeFromBlock(ctx context.Context, req cloudapi.ResumeFromBlockRequest) (result *cloudapi.MessageRef, err error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.ensureStateLocked()
	stub.threads[req.ThreadID].Status = "IDLE"
	stub.appendEventLocked(req.ThreadID, "resumed", "TURN_FINISHED")
	return &cloudapi.MessageRef{ThreadID: req.ThreadID, MessageID: "message-2"}, nil
}
func (stub *coordinatorStub) CancelInput(ctx context.Context, req cloudapi.CancelInputRequest) (result *cloudapi.MessageRef, err error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.threads[req.ThreadID] != nil {
		stub.threads[req.ThreadID].Status = "INTERRUPTED"
	}
	return &cloudapi.MessageRef{ThreadID: req.ThreadID, MessageID: "message-3"}, nil
}
func (stub *coordinatorStub) ListSessionThreads(ctx context.Context, req cloudapi.ListSessionThreadsRequest) (result *cloudapi.ListSessionThreadsResult, err error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.listed != nil {
		return &cloudapi.ListSessionThreadsResult{Threads: stub.listed}, nil
	}
	stub.ensureStateLocked()
	result = &cloudapi.ListSessionThreadsResult{}
	for _, thread := range stub.threads {
		if thread.SessionID == req.SessionID {
			copy := *thread
			result.Threads = append(result.Threads, &copy)
		}
	}
	return result, nil
}
func (stub *coordinatorStub) ListSessionEvents(ctx context.Context, req cloudapi.ListSessionEventsRequest) (result *cloudapi.ListEventsResult, err error) {
	return &cloudapi.ListEventsResult{}, nil
}
func (stub *coordinatorStub) ListEvents(ctx context.Context, req cloudapi.ListEventsRequest) (result *cloudapi.ListEventsResult, err error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.eventByThread != nil {
		events := make([]*timeline.Event, 0, len(stub.eventByThread[req.ThreadID]))
		for _, event := range stub.eventByThread[req.ThreadID] {
			copy := *event
			events = append(events, &copy)
		}
		return &cloudapi.ListEventsResult{Events: events}, nil
	}
	return &cloudapi.ListEventsResult{Events: append([]*timeline.Event(nil), stub.events...)}, nil
}
func (stub *coordinatorStub) ListTurnEvents(ctx context.Context, req cloudapi.ListTurnEventsRequest) (result *cloudapi.ListEventsResult, err error) {
	return &cloudapi.ListEventsResult{}, nil
}
func (stub *coordinatorStub) SubscribeSession(ctx context.Context, req cloudapi.SubscribeSessionRequest) (stream cloudapi.TimelineStream, err error) {
	if stub.stream != nil {
		return stub.stream, nil
	}
	stub.mu.Lock()
	stub.ensureStateLocked()
	live := stub.live
	stub.mu.Unlock()
	return &channelTimelineStream{ctx: ctx, frames: live}, nil
}

func (stub *coordinatorStub) ensureStateLocked() {
	if stub.threads == nil {
		stub.threads = make(map[string]*cloudapi.Thread)
	}
	if stub.eventByThread == nil {
		stub.eventByThread = make(map[string][]*timeline.Event)
	}
	if stub.live == nil {
		stub.live = make(chan *cloudapi.TimelineFrame, 64)
	}
}

func (stub *coordinatorStub) appendEventLocked(threadID, turnID, eventType string) {
	stub.nextEvent++
	event := &timeline.Event{EventID: fmt.Sprintf("event-%d", stub.nextEvent), EventType: eventType, ThreadID: threadID, TurnID: turnID}
	stub.eventByThread[threadID] = append(stub.eventByThread[threadID], event)
	liveEvent := *event
	select {
	case stub.live <- &cloudapi.TimelineFrame{Event: &liveEvent}:
	default:
	}
}

type eofTimelineStream struct{}

func (eofTimelineStream) Recv() (frame *cloudapi.TimelineFrame, err error) { return nil, io.EOF }

type channelTimelineStream struct {
	ctx    context.Context
	frames <-chan *cloudapi.TimelineFrame
}

func (stream *channelTimelineStream) Recv() (frame *cloudapi.TimelineFrame, err error) {
	select {
	case <-stream.ctx.Done():
		return nil, stream.ctx.Err()
	case frame = <-stream.frames:
		return frame, nil
	}
}

type sliceTimelineStream struct {
	frames []*cloudapi.TimelineFrame
}

func (stream *sliceTimelineStream) Recv() (frame *cloudapi.TimelineFrame, err error) {
	if len(stream.frames) == 0 {
		return nil, io.EOF
	}
	frame = stream.frames[0]
	stream.frames = stream.frames[1:]
	return frame, nil
}

func TestClientCreatesAndSubmitsRemoteThread(t *testing.T) {
	stub := &coordinatorStub{created: &cloudapi.Thread{ID: "42", SessionID: "session-1", Title: "remote", Status: "RUNNING", Profile: cloudapi.ThreadProfile{Cwd: "/repo"}, Metadata: map[string]string{
		metadataDefinitionName: "assistant", metadataDefinitionVersion: "v1",
	}}}
	client, err := New(&cloudapi.AgentAPI{Coordinator: stub}, "7")
	if err != nil {
		t.Fatalf("New() error=%v", err)
	}
	created, err := client.CreateThread(context.Background(), runtimeclient.CreateThreadRequest{
		Runtime: runtimeclient.RuntimeRemote, Namespace: "session-1", Title: "remote",
		Definition: agentdefinition.Definition{Name: "assistant", Version: "v1"}, Workspace: runtimeclient.WorkspaceSpec{Cwd: "/repo"},
	})
	if err != nil {
		t.Fatalf("CreateThread() error=%v", err)
	}
	if created.Thread.Ref.Runtime != runtimeclient.RuntimeRemote || created.Thread.Ref.ThreadID != "42" || created.Thread.State != runtimeclient.ThreadStateRunning {
		t.Fatalf("CreateThread() thread=%+v", created.Thread)
	}
	submitted, err := client.Submit(context.Background(), runtimeclient.SubmitRequest{Ref: created.Thread.Ref, Input: protoinput.UserMessage{Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: "hello"}}}})
	if err != nil {
		t.Fatalf("Submit() error=%v", err)
	}
	if submitted.TurnID != "" || stub.sent.ThreadID != "42" || stub.sent.MessageType != protoinput.MessageTypeInput {
		t.Fatalf("Submit() result=%+v request=%+v", submitted, stub.sent)
	}
}

func TestClientListsAndGetsRemoteThreads(t *testing.T) {
	stub := &coordinatorStub{listed: []*cloudapi.Thread{{ID: "42", Status: "BLOCKED", Metadata: map[string]string{metadataDefinitionName: "assistant", metadataDefinitionVersion: "v1"}}}}
	client, err := New(&cloudapi.AgentAPI{Coordinator: stub}, "7")
	if err != nil {
		t.Fatalf("New() error=%v", err)
	}
	listed, err := client.ListThreads(context.Background(), runtimeclient.ListThreadsQuery{Runtime: runtimeclient.RuntimeRemote, Namespace: "session-1"})
	if err != nil || len(listed.Threads) != 1 || listed.Threads[0].State != runtimeclient.ThreadStateBlocked {
		t.Fatalf("ListThreads() result=%+v error=%v", listed, err)
	}
	thread, err := client.GetThread(context.Background(), runtimeclient.GlobalThreadRef{Runtime: runtimeclient.RuntimeRemote, Namespace: "session-1", ThreadID: "42"})
	if err != nil || thread.Ref.ThreadID != "42" {
		t.Fatalf("GetThread() thread=%+v error=%v", thread, err)
	}
}

func TestSubscribeTimelineReplaysAndDeduplicatesLiveEvents(t *testing.T) {
	replayed := &timeline.Event{EventID: "event-1", EventType: "TURN_STARTED", ThreadID: "42", TurnID: "turn-1"}
	live := &timeline.Event{EventID: "event-2", EventType: "ASSISTANT_DELTA", ThreadID: "42", TurnID: "turn-1"}
	stub := &coordinatorStub{
		events: []*timeline.Event{replayed},
		stream: &sliceTimelineStream{frames: []*cloudapi.TimelineFrame{
			{Event: replayed},
			{Event: live},
		}},
	}
	client, err := New(&cloudapi.AgentAPI{Coordinator: stub}, "7")
	if err != nil {
		t.Fatalf("New() error=%v", err)
	}
	subscription, err := client.SubscribeTimeline(context.Background(), runtimeclient.TimelineQuery{Ref: runtimeclient.GlobalThreadRef{
		Runtime: runtimeclient.RuntimeRemote, Namespace: "session-1", ThreadID: "42",
	}})
	if err != nil {
		t.Fatalf("SubscribeTimeline() error=%v", err)
	}
	t.Cleanup(func() { _ = subscription.Close() })
	var eventIDs []string
	deadline := time.After(time.Second)
	for len(eventIDs) < 2 {
		select {
		case event, ok := <-subscription.Events():
			if !ok {
				t.Fatalf("subscription closed early: ids=%v error=%v", eventIDs, subscription.Err())
			}
			eventIDs = append(eventIDs, event.EventID)
		case <-deadline:
			t.Fatalf("subscription timed out: ids=%v", eventIDs)
		}
	}
	if eventIDs[0] != "event-1" || eventIDs[1] != "event-2" {
		t.Fatalf("event ids=%v", eventIDs)
	}
	if _, ok := <-subscription.Events(); ok {
		t.Fatal("duplicate event was emitted")
	}
}

func TestRemoteClientSatisfiesRuntimeClientContract(t *testing.T) {
	stub := &coordinatorStub{}
	clienttest.RunWithOptions(t, func(t *testing.T) (client runtimeclient.Client, cleanup func()) {
		client, err := New(&cloudapi.AgentAPI{Coordinator: stub}, "7")
		if err != nil {
			t.Fatalf("New() error=%v", err)
		}
		return client, func() {}
	}, clienttest.Options{Runtime: runtimeclient.RuntimeRemote, Namespace: "contract", SubmitReturnsTurnID: false})
}
