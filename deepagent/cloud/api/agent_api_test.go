package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
	"eino-cli/deepagent/coordinator"
)

func TestSubmitInputSendsUserMessageAndWakesThread(t *testing.T) {
	fake := &fakeCoordinator{sendRef: &MessageRef{ThreadID: "12", MessageID: "34"}}
	agent := &AgentAPI{Coordinator: fake}

	got, err := agent.Submit(context.Background(), SubmitRequest{
		UserID:   "1234",
		ThreadID: "12",
		Input: &SubmitInput{
			Mode: protoinput.UserMessageModeImplPlan,
			Parts: []protoinput.MessagePart{{
				Type: protoinput.MessagePartTypeText,
				Text: "hello",
				Extra: map[string]json.RawMessage{
					"adapter": json.RawMessage(`{"part_id":"part-1"}`),
				},
			}},
			Extra: map[string]json.RawMessage{
				"adapter": json.RawMessage(`{"message_id":"msg-1"}`),
			},
		},
		Metadata: map[string]string{"logid": "abc"},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got.Message.MessageID != "34" {
		t.Fatalf("message ref = %+v", got.Message)
	}
	if fake.lastSend.MessageType != protoinput.MessageTypeInput || !fake.lastSend.WakeThread {
		t.Fatalf("send request = %+v", fake.lastSend)
	}
	if fake.lastSend.Metadata[protoinput.MetadataTurnMode] != protoinput.TurnModePlan || fake.lastSend.Metadata["logid"] != "abc" {
		t.Fatalf("metadata = %+v", fake.lastSend.Metadata)
	}
	var payload protoinput.UserMessage
	if err := json.Unmarshal(fake.lastSend.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Parts[0].Text != "hello" || payload.Mode != protoinput.UserMessageModeImplPlan {
		t.Fatalf("payload = %+v", payload)
	}
	if got := string(payload.Extra["adapter"]); got != `{"message_id":"msg-1"}` {
		t.Fatalf("payload extra = %s", got)
	}
	if got := string(payload.Parts[0].Extra["adapter"]); got != `{"part_id":"part-1"}` {
		t.Fatalf("payload part extra = %s", got)
	}
}

func TestSubmitResumeUsesResumeFromBlock(t *testing.T) {
	fake := &fakeCoordinator{resumeRef: &MessageRef{ThreadID: "12", MessageID: "35"}}
	agent := &AgentAPI{Coordinator: fake}

	_, err := agent.Submit(context.Background(), SubmitRequest{
		UserID:   "1234",
		ThreadID: "12",
		Resume: &ResumeInput{
			TurnID:       "turn-1",
			CheckpointID: "ckpt",
			InterruptID:  "interrupt",
			Approval:     &protoinput.ApprovalDecision{Approved: true},
		},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if fake.lastResume.MessageType != protoinput.MessageTypeResume || fake.lastResume.Reason != "user_resume" {
		t.Fatalf("resume request = %+v", fake.lastResume)
	}
	var payload protoinput.ResumeTurnPayload
	if err := json.Unmarshal(fake.lastResume.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.TurnID != "turn-1" || payload.Approval == nil || !payload.Approval.Approved {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSubmitResumeInterruptUsesResumeFromBlock(t *testing.T) {
	fake := &fakeCoordinator{resumeRef: &MessageRef{ThreadID: "12", MessageID: "36"}}
	agent := &AgentAPI{Coordinator: fake}

	_, err := agent.Submit(context.Background(), SubmitRequest{
		UserID:   "1234",
		ThreadID: "12",
		Resume: &ResumeInput{
			TurnID:       "turn-1",
			CheckpointID: "ckpt",
			InterruptID:  "interrupt",
			Interrupt: &protoinput.InterruptResumePayload{
				Kind: "follow_up",
				Data: json.RawMessage(`{"user_answer":"ok"}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if fake.lastResume.MessageType != protoinput.MessageTypeResume || fake.lastResume.Reason != "user_resume" {
		t.Fatalf("resume request = %+v", fake.lastResume)
	}
	var payload protoinput.ResumeTurnPayload
	if err := json.Unmarshal(fake.lastResume.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.TurnID != "turn-1" || payload.Interrupt == nil || payload.Interrupt.Kind != "follow_up" {
		t.Fatalf("payload = %+v", payload)
	}
	if string(payload.Interrupt.Data) != `{"user_answer":"ok"}` {
		t.Fatalf("interrupt data = %s", payload.Interrupt.Data)
	}
}

func TestSubmitRequiresExactlyOnePayloadKind(t *testing.T) {
	agent := &AgentAPI{Coordinator: &fakeCoordinator{}}
	_, err := agent.Submit(context.Background(), SubmitRequest{
		ThreadID: "12",
		Input:    &SubmitInput{Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: "hello"}}},
		Compact:  &CompactInput{},
	})
	if err == nil {
		t.Fatalf("Submit() expected error")
	}
}

func TestCreateThreadInitialInputRecordsParentMetadata(t *testing.T) {
	fake := &fakeCoordinator{createResult: &CreateThreadResult{Thread: &Thread{ID: "13"}}}
	agent := &AgentAPI{Coordinator: fake}

	_, err := agent.CreateThread(context.Background(), CreateThreadRequest{
		UserID:         "1234",
		SessionID:      "42",
		ParentThreadID: "12",
		InitialInput: &SubmitInput{
			Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: "spawn"}},
		},
	})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if fake.lastCreate.Metadata[MetadataParentThreadID] != "12" {
		t.Fatalf("metadata = %+v", fake.lastCreate.Metadata)
	}
	if fake.lastCreate.InitialInput != nil || fake.lastCreate.InitialMessage == nil || fake.lastCreate.InitialMessage.MessageType != protoinput.MessageTypeInput {
		t.Fatalf("create request = %+v", fake.lastCreate)
	}
}

func TestThreadFromCoordinatorPopulatesParentThreadID(t *testing.T) {
	thread := threadFromCoordinator(&coordinator.Thread{
		ThreadID:  13,
		SessionID: "42",
		Metadata:  map[string]string{"parent_thread_id": "12"},
	})
	if thread.ParentThreadID != "12" {
		t.Fatalf("parent thread id = %q", thread.ParentThreadID)
	}
}

func TestListTimelineUsesSessionEvents(t *testing.T) {
	fake := &fakeCoordinator{
		sessionEvents: &ListEventsResult{
			Events:     []*timeline.Event{{EventID: "a", ThreadID: "2", CreatedAtMs: 10}, {EventID: "b", ThreadID: "1", CreatedAtMs: 20}},
			NextCursor: "session-cursor-1",
			HasMore:    true,
		},
	}
	agent := &AgentAPI{Coordinator: fake}

	got, err := agent.ListTimeline(context.Background(), ListTimelineRequest{SessionID: "42"})
	if err != nil {
		t.Fatalf("ListTimeline() error = %v", err)
	}
	if len(got.Events) != 2 || got.Events[0].EventID != "a" || got.Events[1].EventID != "b" {
		t.Fatalf("events = %+v", got.Events)
	}
	if got.NextCursor != "session-cursor-1" || !got.HasMore {
		t.Fatalf("page = has_more:%v next_cursor:%q", got.HasMore, got.NextCursor)
	}
	if got.Events[0].SessionID != "42" || got.Events[1].SessionID != "42" {
		t.Fatalf("session ids = %+v", got.Events)
	}
	if fake.listSessionEventsCalls != 1 {
		t.Fatalf("ListSessionEvents calls = %d", fake.listSessionEventsCalls)
	}
	if fake.listSessionThreadsCalls != 0 {
		t.Fatalf("ListSessionThreads calls = %d", fake.listSessionThreadsCalls)
	}
	if fake.listEventsCalls != 0 {
		t.Fatalf("ListEvents calls = %d", fake.listEventsCalls)
	}
}

func TestListTimelineSessionCursorIsOpaqueAndReturnedFromAC(t *testing.T) {
	fake := &fakeCoordinator{
		sessionEvents: &ListEventsResult{
			Events:     []*timeline.Event{{EventID: "10", ThreadID: "1", CreatedAtMs: 10}},
			NextCursor: "opaque:cursor:next",
			HasMore:    true,
		},
	}
	agent := &AgentAPI{Coordinator: fake}

	got, err := agent.ListTimeline(context.Background(), ListTimelineRequest{
		SessionID: "42",
		Cursor:    "opaque:cursor:prev",
		Limit:     2,
		Backward:  true,
	})
	if err != nil {
		t.Fatalf("ListTimeline() error = %v", err)
	}
	if !got.HasMore || got.NextCursor != "opaque:cursor:next" {
		t.Fatalf("page = has_more:%v next_cursor:%q", got.HasMore, got.NextCursor)
	}
	if fake.lastListSessionEvents.SessionID != "42" || fake.lastListSessionEvents.Cursor != "opaque:cursor:prev" || fake.lastListSessionEvents.Limit != 2 || !fake.lastListSessionEvents.Backward {
		t.Fatalf("session events request = %+v", fake.lastListSessionEvents)
	}
}

func TestSubscribeTimelineEmitsQueueAndFilteredEvents(t *testing.T) {
	stop := errors.New("stop")
	fake := &fakeCoordinator{stream: &fakeTimelineStream{frames: []*TimelineFrame{
		{QueueID: "q1"},
		{Event: &timeline.Event{EventID: "skip", ThreadID: "2"}},
		{Event: &timeline.Event{EventID: "keep", ThreadID: "1"}},
	}}}
	agent := &AgentAPI{Coordinator: fake}
	events := make([]TimelineFrame, 0)

	err := agent.SubscribeTimeline(context.Background(), SubscribeTimelineRequest{SessionID: "42", ThreadID: "1"}, func(ctx context.Context, frame TimelineFrame) error {
		events = append(events, frame)
		if frame.Event != nil {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("SubscribeTimeline() error = %v", err)
	}
	if len(events) != 2 || events[0].QueueID != "q1" || events[1].Event.EventID != "keep" || events[1].Event.SessionID != "42" {
		t.Fatalf("frames = %+v", events)
	}
}

func TestSubscribeTimelineClassifiesEOFAsExpectedClose(t *testing.T) {
	if !isExpectedSubscribeClose(context.Background(), io.EOF) {
		t.Fatal("io.EOF should be classified as an expected subscription close")
	}
}

func TestStopRunningCancelsRunningThreadsBeforeBlockedThreads(t *testing.T) {
	fake := &fakeCoordinator{sessionThreads: []*Thread{
		{ID: "1", Status: "BLOCKED"},
		{ID: "2", Status: "RUNNING"},
		{ID: "3", Status: "RUNNING"},
	}}
	agent := &AgentAPI{Coordinator: fake}

	got, err := agent.StopRunning(context.Background(), StopRunningRequest{SessionID: "42"})
	if err != nil {
		t.Fatalf("StopRunning() error = %v", err)
	}
	if !reflect.DeepEqual(fake.canceledThreads, []string{"2", "3"}) {
		t.Fatalf("canceled = %+v", fake.canceledThreads)
	}
	if len(got.ControlMessages) != 2 {
		t.Fatalf("control messages = %+v", got.ControlMessages)
	}
}

func TestStopRunningRejectsLatestApprovalWhenCancelReportsBlocked(t *testing.T) {
	argumentsJSON := `{"cmd":"sleep 30"}`
	approvalPayload, err := json.Marshal(protoevent.ApprovalRequiredEventPayload{
		InterruptID:        "interrupt-1",
		CheckpointID:       "checkpoint-1",
		ToolName:           "exec_command",
		ArgumentsJSON:      &argumentsJSON,
		ConsumedMessageIDs: []string{"message-1"},
	})
	if err != nil {
		t.Fatalf("marshal approval payload: %v", err)
	}
	fake := &fakeCoordinator{
		cancelErr:      errors.New(`agent_coordinator.CancelInput status_code=409 status_message="thread blocked"`),
		resumeRef:      &MessageRef{ThreadID: "1", MessageID: "resume-1"},
		sessionThreads: []*Thread{{ID: "1", Status: "BLOCKED"}},
		eventsByThread: map[string]*ListEventsResult{
			"1": {Events: []*timeline.Event{{
				EventID:     "approval-1",
				EventType:   protoevent.EventTypeApprovalRequired.String(),
				ThreadID:    "1",
				TurnID:      "turn-1",
				CreatedAtMs: 10,
				Payload:     approvalPayload,
			}}},
		},
	}
	agent := &AgentAPI{Coordinator: fake}

	got, err := agent.StopRunning(context.Background(), StopRunningRequest{
		UserID:    "1234",
		SessionID: "42",
		Reason:    "test_stop",
	})
	if err != nil {
		t.Fatalf("StopRunning() error = %v", err)
	}
	if len(got.ControlMessages) != 1 || got.ControlMessages[0].MessageID != "resume-1" {
		t.Fatalf("control messages = %+v", got.ControlMessages)
	}
	if fake.lastResume.UserID != "1234" || fake.lastResume.ThreadID != "1" || fake.lastResume.MessageType != protoinput.MessageTypeResume {
		t.Fatalf("resume request = %+v", fake.lastResume)
	}
	var payload protoinput.ResumeTurnPayload
	if err := json.Unmarshal(fake.lastResume.Payload, &payload); err != nil {
		t.Fatalf("unmarshal resume payload: %v", err)
	}
	if payload.TurnID != "turn-1" || payload.CheckpointID != "checkpoint-1" || payload.InterruptID != "interrupt-1" {
		t.Fatalf("resume identity = %+v", payload)
	}
	if payload.ToolName != "exec_command" || payload.ArgumentsInJSON != argumentsJSON || !reflect.DeepEqual(payload.ConsumedMessageIDs, []string{"message-1"}) {
		t.Fatalf("resume tool context = %+v", payload)
	}
	if payload.Approval == nil || payload.Approval.Approved || payload.Approval.Reason != "test_stop" {
		t.Fatalf("approval decision = %+v", payload.Approval)
	}
	if !payload.Approval.CancelTurn {
		t.Fatalf("approval cancel_turn = false, want true")
	}
}

type fakeCoordinator struct {
	lastCreate CreateThreadRequest
	lastSend   SendMessageRequest
	lastResume ResumeFromBlockRequest

	createResult    *CreateThreadResult
	sendRef         *MessageRef
	resumeRef       *MessageRef
	cancelErr       error
	sessionThreads  []*Thread
	sessionEvents   *ListEventsResult
	eventsByThread  map[string]*ListEventsResult
	stream          TimelineStream
	canceledThreads []string

	listSessionThreadsCalls int
	listSessionEventsCalls  int
	listEventsCalls         int
	lastListSessionEvents   ListSessionEventsRequest
}

func (f *fakeCoordinator) CreateThread(ctx context.Context, req CreateThreadRequest) (*CreateThreadResult, error) {
	f.lastCreate = req
	return f.createResult, nil
}

func (f *fakeCoordinator) SendMessage(ctx context.Context, req SendMessageRequest) (*MessageRef, error) {
	f.lastSend = req
	return f.sendRef, nil
}

func (f *fakeCoordinator) ResumeFromBlock(ctx context.Context, req ResumeFromBlockRequest) (*MessageRef, error) {
	f.lastResume = req
	return f.resumeRef, nil
}

func (f *fakeCoordinator) CancelInput(ctx context.Context, req CancelInputRequest) (*MessageRef, error) {
	f.canceledThreads = append(f.canceledThreads, req.ThreadID)
	if f.cancelErr != nil {
		return nil, f.cancelErr
	}
	return &MessageRef{ThreadID: req.ThreadID, MessageID: "cancel-" + req.ThreadID}, nil
}

func (f *fakeCoordinator) ListSessionThreads(ctx context.Context, req ListSessionThreadsRequest) (*ListSessionThreadsResult, error) {
	f.listSessionThreadsCalls++
	return &ListSessionThreadsResult{Threads: f.sessionThreads}, nil
}

func (f *fakeCoordinator) ListSessionEvents(ctx context.Context, req ListSessionEventsRequest) (*ListEventsResult, error) {
	f.listSessionEventsCalls++
	f.lastListSessionEvents = req
	if f.sessionEvents != nil {
		return f.sessionEvents, nil
	}
	return &ListEventsResult{}, nil
}

func (f *fakeCoordinator) ListEvents(ctx context.Context, req ListEventsRequest) (*ListEventsResult, error) {
	f.listEventsCalls++
	if f.eventsByThread != nil && f.eventsByThread[req.ThreadID] != nil {
		return f.eventsByThread[req.ThreadID], nil
	}
	return &ListEventsResult{}, nil
}

func (f *fakeCoordinator) ListTurnEvents(ctx context.Context, req ListTurnEventsRequest) (*ListEventsResult, error) {
	return &ListEventsResult{}, nil
}

func (f *fakeCoordinator) SubscribeSession(ctx context.Context, req SubscribeSessionRequest) (TimelineStream, error) {
	return f.stream, nil
}

type fakeTimelineStream struct {
	frames []*TimelineFrame
	pos    int
}

func (s *fakeTimelineStream) Recv() (*TimelineFrame, error) {
	if s.pos >= len(s.frames) {
		return nil, io.EOF
	}
	frame := s.frames[s.pos]
	s.pos++
	return frame, nil
}
