package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
	sdkruntime "eino-cli/deepagent/runtime"
)

const (
	httpTestSessionID = "1788607944320815478"
	httpTestThreadID  = "1788607944320815489"
	httpTestMessageID = "1788607944320815491"
)

func TestHTTPRuntimeUsesBackendSession(t *testing.T) {
	queueReady := make(chan struct{})
	submitted := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("X-Bytedance-User"); got != "opaque-user-token" {
			t.Errorf("user token header = %q", got)
		}
		if got := request.Header.Get("X-Deep-Agent-SDK-Test-UID"); got != "" {
			t.Errorf("test identity header must be absent, got %q", got)
		}
		switch request.URL.Path {
		case "/ad/deep_agent_sdk/create_session":
			writeHTTPJSON(t, writer, map[string]any{
				"session_view": map[string]any{"session": httpSessionFixture(httpTestSessionID, "0")},
				"BaseResp":     map[string]any{"StatusCode": 0, "StatusMessage": ""},
			})
		case "/ad/deep_agent_sdk/subscribe_timeline":
			writer.Header().Set("Content-Type", "text/event-stream")
			flusher := writer.(http.Flusher)
			fmt.Fprint(writer, "event: queue\r\ndata: {\"queue_id\":\"queue-1\",\"BaseResp\":{\"StatusCode\":0}}\r\n\r\n")
			flusher.Flush()
			once.Do(func() { close(queueReady) })
			select {
			case <-submitted:
			case <-request.Context().Done():
				return
			}
			writeSSEFixture(writer, "old", "TURN_STARTED", "other-thread", "old-turn", `{"consumed_message_ids":["old-message"]}`)
			writeSSEFixture(writer, "1", "TURN_STARTED", httpTestThreadID, "turn-1", `{"consumed_message_ids":["`+httpTestMessageID+`"]}`)
			writeSSEFixture(writer, "2", "ASSISTANT_DELTA", httpTestThreadID, "turn-1", `{"delta":"hello "}`)
			writeSSEFixture(writer, "3", "ASSISTANT_DELTA", httpTestThreadID, "turn-1", `{"delta":"remote"}`)
			writeSSEFixture(writer, "4", "TURN_FINISHED", httpTestThreadID, "turn-1", `{"consumed_message_ids":["`+httpTestMessageID+`"]}`)
			flusher.Flush()
		case "/ad/deep_agent_sdk/submit_input":
			select {
			case <-queueReady:
			default:
				t.Error("submit_input arrived before queue-ready frame")
			}
			var body map[string]json.RawMessage
			decodeHTTPRequest(t, request, &body)
			assertJSONString(t, body["session_id"], httpTestSessionID)
			var mode int
			if err := json.Unmarshal(body["mode"], &mode); err != nil || mode != 1 {
				t.Errorf("plan mode = %d, error = %v", mode, err)
			}
			if _, exists := body["thread_id"]; exists {
				t.Error("first submit must let the backend create the main thread")
			}
			close(submitted)
			writeHTTPJSON(t, writer, map[string]any{
				"message":      map[string]any{"thread_id": httpTestThreadID, "message_id": httpTestMessageID},
				"session_view": map[string]any{"session": httpSessionFixture(httpTestSessionID, httpTestThreadID)},
				"BaseResp":     map[string]any{"StatusCode": 0},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	runtime, err := NewHTTPRuntime(server.URL, "cli-project", "opaque-user-token")
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}
	if _, err = runtime.SetPlanMode(context.Background(), true); err != nil {
		t.Fatalf("SetPlanMode() error = %v", err)
	}
	stream, err := runtime.StartTurn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	defer stream.Close()
	if stream.Ref.ThreadID != httpTestThreadID || stream.Ref.Namespace != "cli-project" {
		t.Fatalf("stream ref = %+v", stream.Ref)
	}
	var output strings.Builder
	for event := range stream.Events {
		if !stream.AcceptEvent(event) {
			continue
		}
		switch protoevent.EventType(event.EventType) {
		case protoevent.EventTypeAssistantDelta:
			var payload protoevent.AssistantDeltaEventPayload
			if err = json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			output.WriteString(payload.Delta)
		case protoevent.EventTypeTurnFinished:
			if output.String() != "hello remote" {
				t.Fatalf("output = %q", output.String())
			}
			if stream.TurnID != "turn-1" {
				t.Fatalf("turn id = %q", stream.TurnID)
			}
			if got := runtime.Session(); got.ID != httpTestSessionID || got.MainThreadID != httpTestThreadID {
				t.Fatalf("session = %+v", got)
			}
			return
		}
	}
	t.Fatalf("timeline closed early: %v", stream.Err())
}

func TestHTTPRuntimeResumePreservesDecisionsAndStructuredAnswers(t *testing.T) {
	var bodies []map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ad/deep_agent_sdk/get_session":
			writeHTTPJSON(t, writer, map[string]any{"session_view": map[string]any{"session": httpSessionFixture(httpTestSessionID, httpTestThreadID)}, "BaseResp": map[string]any{"StatusCode": 0}})
		case "/ad/deep_agent_sdk/submit_input":
			var body map[string]json.RawMessage
			decodeHTTPRequest(t, request, &body)
			bodies = append(bodies, body)
			writeHTTPJSON(t, writer, map[string]any{"message": map[string]any{"thread_id": httpTestThreadID, "message_id": "next"}, "BaseResp": map[string]any{"StatusCode": 0}})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	runtime, err := NewHTTPRuntime(server.URL, "cli-project", "token")
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.OpenSession(context.Background(), httpTestSessionID); err != nil {
		t.Fatal(err)
	}
	ref := sdkruntime.GlobalThreadRef{Runtime: sdkruntime.RuntimeRemote, Namespace: "cli-project", ThreadID: httpTestThreadID}
	common := protoinput.ResumeTurnPayload{TurnID: "turn-1", CheckpointID: "checkpoint-1", InterruptID: "interrupt-1", ConsumedMessageIDs: []string{"m1", "m2"}}
	denied := common
	denied.ToolName = "execute"
	denied.ArgumentsInJSON = `{"command":"false"}`
	denied.Approval = &protoinput.ApprovalDecision{Approved: false, Reason: "unsafe", CancelTurn: true}
	if err = runtime.Resume(context.Background(), ref, denied); err != nil {
		t.Fatalf("Resume(denied) error = %v", err)
	}
	answers := common
	answers.RequestUserInput = &protoinput.RequestUserInputResponse{Answers: map[string]protoinput.RequestUserInputAnswer{
		"environment": {Answers: []string{"staging"}},
		"regions":     {Answers: []string{"eu-west", "ap-south"}},
	}}
	if err = runtime.Resume(context.Background(), ref, answers); err != nil {
		t.Fatalf("Resume(answers) error = %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("submit count = %d", len(bodies))
	}
	var approval struct {
		Approved   bool `json:"approved"`
		CancelTurn bool `json:"cancel_turn"`
	}
	if err = json.Unmarshal(bodies[0]["approval"], &approval); err != nil {
		t.Fatal(err)
	}
	if approval.Approved || !approval.CancelTurn {
		t.Fatalf("approval = %+v", approval)
	}
	var consumed []string
	if err = json.Unmarshal(bodies[0]["consumed_message_ids"], &consumed); err != nil || strings.Join(consumed, ",") != "m1,m2" {
		t.Fatalf("consumed ids = %v, error = %v", consumed, err)
	}
	var structured protoinput.RequestUserInputResponse
	if err = json.Unmarshal(bodies[1]["request_user_input"], &structured); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(structured.Answers["regions"].Answers, ","); got != "eu-west,ap-south" {
		t.Fatalf("structured regions = %q", got)
	}
	if _, flattened := bodies[1]["content"]; flattened {
		t.Fatal("structured answers must not be flattened into content")
	}
}

func TestHTTPRuntimeListsEveryPageAndOpensLatestActiveSession(t *testing.T) {
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ad/deep_agent_sdk/list_sessions" {
			http.NotFound(writer, request)
			return
		}
		var body struct {
			Cursor string `json:"cursor"`
			Status *int   `json:"status"`
		}
		decodeHTTPRequest(t, request, &body)
		cursors = append(cursors, body.Cursor)
		if body.Cursor == "" {
			writeHTTPJSON(t, writer, map[string]any{"sessions": []any{httpSessionFixture("9007199254740993", "11")}, "page_info": map[string]any{"has_more": true, "next_cursor": "page-2"}, "BaseResp": map[string]any{"StatusCode": 0}})
			return
		}
		writeHTTPJSON(t, writer, map[string]any{"sessions": []any{httpSessionFixture("9007199254740995", "12")}, "page_info": map[string]any{"has_more": false}, "BaseResp": map[string]any{"StatusCode": 0}})
	}))
	t.Cleanup(server.Close)
	runtime, err := NewHTTPRuntime(server.URL, "cli-project", "token")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := runtime.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 2 || sessions[1].ID != "9007199254740995" || strings.Join(cursors, ",") != ",page-2" {
		t.Fatalf("sessions = %+v, cursors = %v", sessions, cursors)
	}
}

func TestHTTPRuntimeFollowSessionReplaysOnlyActiveTurn(t *testing.T) {
	streamClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ad/deep_agent_sdk/subscribe_timeline":
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "event: queue\ndata: {\"queue_id\":\"follow-queue\",\"BaseResp\":{\"StatusCode\":0}}\n\n")
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
			close(streamClosed)
		case "/ad/deep_agent_sdk/get_session":
			writeHTTPJSON(t, writer, map[string]any{
				"session_view": map[string]any{
					"session": httpSessionFixture(httpTestSessionID, httpTestThreadID),
					"threads": []any{map[string]any{"thread_id": httpTestThreadID, "status": 2, "role": 1}},
				},
				"BaseResp": map[string]any{"StatusCode": 0},
			})
		case "/ad/deep_agent_sdk/list_timeline":
			writeHTTPJSON(t, writer, map[string]any{
				"events": []any{
					httpTimelineFixture("1", "TURN_STARTED", "turn-old", map[string]any{"consumed_message_ids": []string{"old-message"}}),
					httpTimelineFixture("2", "APPROVAL_REQUIRED", "turn-old", map[string]any{"interrupt_id": "old", "checkpoint_id": "old", "tool_name": "execute"}),
					httpTimelineFixture("3", "TURN_FINISHED", "turn-old", map[string]any{}),
					httpTimelineFixture("4", "TURN_STARTED", "turn-active", map[string]any{"consumed_message_ids": []string{"active-message"}}),
					httpTimelineFixture("5", "APPROVAL_REQUIRED", "turn-active", map[string]any{"interrupt_id": "answered", "checkpoint_id": "answered", "tool_name": "execute"}),
					httpTimelineFixture("6", "TOOL_CALL_STARTED", "turn-active", map[string]any{"tool_call_id": "tool-1", "tool_name": "execute", "status": 1}),
					httpTimelineFixture("7", "TOOL_CALL_FINISHED", "turn-active", map[string]any{"tool_call_id": "tool-1", "tool_name": "execute", "status": 2}),
					httpTimelineFixture("8", "APPROVAL_REQUIRED", "turn-active", map[string]any{"interrupt_id": "active", "checkpoint_id": "active", "tool_name": "write_file"}),
				},
				"page_info": map[string]any{"has_more": false}, "BaseResp": map[string]any{"StatusCode": 0},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	runtime, err := NewHTTPRuntime(server.URL, "cli-project", "token")
	if err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.session = SessionInfo{ID: httpTestSessionID, ProjectName: "cli-project", MainThreadID: httpTestThreadID}
	runtime.sessionValidated = true
	runtime.mu.Unlock()
	history, stream, err := runtime.FollowSession(context.Background())
	if err != nil {
		t.Fatalf("FollowSession() error = %v", err)
	}
	if stream == nil {
		t.Fatal("READY main thread did not return a stream")
	}
	defer stream.Close()
	if len(history) != 7 || history[1].TurnID != "turn-old" || history[4].EventID != "5" {
		t.Fatalf("completed history = %+v", history)
	}
	first := receiveHTTPEvent(t, stream.Events)
	if first.TurnID != "turn-active" || first.EventID != "8" || first.EventType != "APPROVAL_REQUIRED" {
		t.Fatalf("active replay = %+v", first)
	}
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-streamClosed:
	case <-time.After(time.Second):
		t.Fatal("follow subscription was not closed")
	}
}

func TestHTTPRuntimeFollowSessionReturnsFinishedHistoryWhenStatusIsStale(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ad/deep_agent_sdk/subscribe_timeline":
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "event: queue\ndata: {\"queue_id\":\"finished-queue\",\"BaseResp\":{\"StatusCode\":0}}\n\n")
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
		case "/ad/deep_agent_sdk/get_session":
			writeHTTPJSON(t, writer, map[string]any{
				"session_view": map[string]any{
					"session": httpSessionFixture(httpTestSessionID, httpTestThreadID),
					"threads": []any{map[string]any{"thread_id": httpTestThreadID, "status": 3, "role": 1}},
				},
				"BaseResp": map[string]any{"StatusCode": 0},
			})
		case "/ad/deep_agent_sdk/list_timeline":
			writeHTTPJSON(t, writer, map[string]any{
				"events": []any{
					httpTimelineFixture("1", "TURN_STARTED", "turn-done", map[string]any{"consumed_message_ids": []string{"message"}}),
					httpTimelineFixture("2", "APPROVAL_REQUIRED", "turn-done", map[string]any{"interrupt_id": "old", "checkpoint_id": "old", "tool_name": "execute"}),
					httpTimelineFixture("3", "TURN_FINISHED", "turn-done", map[string]any{}),
				},
				"page_info": map[string]any{"has_more": false}, "BaseResp": map[string]any{"StatusCode": 0},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	runtime, err := NewHTTPRuntime(server.URL, "cli-project", "token")
	if err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.session = SessionInfo{ID: httpTestSessionID, ProjectName: "cli-project", MainThreadID: httpTestThreadID}
	runtime.sessionValidated = true
	runtime.mu.Unlock()
	history, stream, err := runtime.FollowSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stream != nil {
		defer stream.Close()
	}
	if stream != nil || len(history) != 3 {
		t.Fatalf("history count = %d, stream = %v", len(history), stream)
	}
}

func TestHTTPRuntimeDoesNotRetryUncertainSubmitAndSeparatesStopFromClose(t *testing.T) {
	var submitCount atomic.Int32
	var stopCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ad/deep_agent_sdk/subscribe_timeline":
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "event: queue\ndata: {\"queue_id\":\"queue\",\"BaseResp\":{\"StatusCode\":0}}\n\n")
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
		case "/ad/deep_agent_sdk/submit_input":
			if submitCount.Add(1) == 1 {
				writer.WriteHeader(http.StatusServiceUnavailable)
				writeHTTPJSON(t, writer, map[string]any{"BaseResp": map[string]any{"StatusCode": 503, "StatusMessage": "uncertain"}})
				return
			}
			t.Fatal("submit_input was retried")
		case "/ad/deep_agent_sdk/stop_running":
			stopCount.Add(1)
			writeHTTPJSON(t, writer, map[string]any{"BaseResp": map[string]any{"StatusCode": 0}})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	runtime, err := NewHTTPRuntime(server.URL, "cli-project", "token")
	if err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.session = SessionInfo{ID: httpTestSessionID, ProjectName: "cli-project", MainThreadID: httpTestThreadID}
	runtime.sessionValidated = true
	runtime.mu.Unlock()
	if _, err = runtime.StartTurn(context.Background(), "uncertain"); err == nil {
		t.Fatal("uncertain submit succeeded")
	}
	if submitCount.Load() != 1 || stopCount.Load() != 0 {
		t.Fatalf("submit count = %d, stop count = %d", submitCount.Load(), stopCount.Load())
	}

	stream := &TurnStream{
		Ref: runtime.threadRef(httpTestThreadID),
		stop: func(ctx context.Context) (stopErr error) {
			return runtime.stopSession(ctx, httpTestSessionID)
		},
	}
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
	if stopCount.Load() != 0 {
		t.Fatal("Close must not send stop_running")
	}
	if err = stream.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stopCount.Load() != 1 {
		t.Fatalf("Stop count = %d", stopCount.Load())
	}
}

func TestHTTPRuntimeThreadReferenceKeepsEndpointProjectSessionAndThreadSeparate(t *testing.T) {
	runtime, err := NewHTTPRuntime("https://agent.example.test/root", "cli-project", "token")
	if err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.session = SessionInfo{ID: httpTestSessionID, ProjectName: "cli-project", ProjectPath: "/workspace", MainThreadID: httpTestThreadID}
	runtime.sessionValidated = true
	runtime.mu.Unlock()
	payload, err := runtime.ExportThreadRef()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"namespace":"`+httpTestSessionID+`"`) || strings.Contains(string(payload), `"thread_id":"`+httpTestSessionID+`"`) {
		t.Fatalf("session ID leaked into thread identity fields: %s", payload)
	}
	same, err := NewHTTPRuntime("https://agent.example.test/root/", "cli-project", "other-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = same.ImportThreadRef(payload); err != nil {
		t.Fatalf("ImportThreadRef() error = %v", err)
	}
	if got := same.Session(); got.ID != httpTestSessionID || got.MainThreadID != httpTestThreadID {
		t.Fatalf("imported session = %+v", got)
	}
	otherEndpoint, _ := NewHTTPRuntime("https://other.example.test", "cli-project", "token")
	if err = otherEndpoint.ImportThreadRef(payload); err == nil {
		t.Fatal("endpoint switch was accepted")
	}
	otherProject, _ := NewHTTPRuntime("https://agent.example.test/root", "other-project", "token")
	if err = otherProject.ImportThreadRef(payload); err == nil {
		t.Fatal("project switch was accepted")
	}
}

func TestTurnStreamWaitsForSubmittedMessageBeforeAcceptingRemoteTurn(t *testing.T) {
	stream := &TurnStream{
		Ref:               sdkruntime.GlobalThreadRef{Runtime: sdkruntime.RuntimeRemote, Namespace: "project", ThreadID: "thread-1"},
		expectedMessageID: "new-message",
	}
	oldPayload := timeline.NormalizePayload([]byte(`{"consumed_message_ids":["old-message"]}`))
	newPayload := timeline.NormalizePayload([]byte(`{"consumed_message_ids":["new-message"]}`))
	if stream.AcceptEvent(timeline.Event{ThreadID: "thread-1", TurnID: "old-turn", EventType: protoevent.EventTypeTurnStarted.String(), Payload: oldPayload}) {
		t.Fatal("pending input from another turn was accepted")
	}
	if !stream.AcceptEvent(timeline.Event{ThreadID: "thread-1", TurnID: "new-turn", EventType: protoevent.EventTypeTurnStarted.String(), Payload: newPayload}) {
		t.Fatal("submitted message turn was not accepted")
	}
	if stream.AcceptEvent(timeline.Event{ThreadID: "other-thread", TurnID: "new-turn", EventType: protoevent.EventTypeAssistantDelta.String(), Payload: json.RawMessage(`{"delta":"wrong"}`)}) {
		t.Fatal("event from another thread was accepted")
	}
}

func TestTurnStreamLatchesJoinedInputWithoutAnotherTurnStarted(t *testing.T) {
	stream := &TurnStream{
		Ref:               sdkruntime.GlobalThreadRef{Runtime: sdkruntime.RuntimeRemote, Namespace: "project", ThreadID: "thread-active"},
		expectedMessageID: "joined-message",
	}
	oldPayload := timeline.NormalizePayload([]byte(`{"delta":"old","consumed_message_ids":["old-message"]}`))
	joinedPayload := timeline.NormalizePayload([]byte(`{"delta":"joined","consumed_message_ids":["joined-message"]}`))
	if stream.AcceptEvent(timeline.Event{ThreadID: "thread-active", TurnID: "turn-active", EventType: protoevent.EventTypeAssistantDelta.String(), Payload: oldPayload}) {
		t.Fatal("unrelated event from the active turn was accepted")
	}
	if stream.AcceptEvent(timeline.Event{ThreadID: "thread-other", TurnID: "turn-other", EventType: protoevent.EventTypeAssistantMessage.String(), Payload: joinedPayload}) {
		t.Fatal("joined message from another thread was accepted")
	}
	if !stream.AcceptEvent(timeline.Event{ThreadID: "thread-active", TurnID: "turn-active", EventType: protoevent.EventTypeAssistantDelta.String(), Payload: joinedPayload}) {
		t.Fatal("joined input event did not latch the active turn")
	}
	if stream.TurnID != "turn-active" {
		t.Fatalf("latched turn ID = %q", stream.TurnID)
	}
	if !stream.AcceptEvent(timeline.Event{ThreadID: "thread-active", TurnID: "turn-active", EventType: protoevent.EventTypeTurnFinished.String(), Payload: json.RawMessage(`{}`)}) {
		t.Fatal("finish from the latched joined-input turn was rejected")
	}
}

func TestHTTPRuntimeRejectsSubmitRedirectWithoutForwardingUserToken(t *testing.T) {
	var destinationRequests atomic.Int32
	var destinationToken atomic.Value
	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		destinationRequests.Add(1)
		destinationToken.Store(request.Header.Get(httpUserTokenHeader))
		writeHTTPJSON(t, writer, map[string]any{
			"message":  map[string]any{"thread_id": httpTestThreadID, "message_id": httpTestMessageID},
			"BaseResp": map[string]any{"StatusCode": 0},
		})
	}))
	t.Cleanup(destination.Close)
	var submitAttempts atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ad/deep_agent_sdk/subscribe_timeline":
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "event: queue\ndata: {\"queue_id\":\"redirect-queue\",\"BaseResp\":{\"StatusCode\":0}}\n\n")
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
		case "/ad/deep_agent_sdk/submit_input":
			submitAttempts.Add(1)
			http.Redirect(writer, request, destination.URL+"/capture", http.StatusTemporaryRedirect)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(source.Close)
	runtime, err := NewHTTPRuntime(source.URL, "cli-project", "redirect-secret")
	if err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.session = SessionInfo{ID: httpTestSessionID, ProjectName: "cli-project", MainThreadID: httpTestThreadID}
	runtime.sessionValidated = true
	runtime.mu.Unlock()
	stream, err := runtime.StartTurn(context.Background(), "must stay on source")
	if stream != nil {
		defer stream.Close()
	}
	if err == nil {
		t.Fatal("redirected submit_input was accepted")
	}
	if submitAttempts.Load() != 1 {
		t.Fatalf("submit attempts = %d", submitAttempts.Load())
	}
	if destinationRequests.Load() != 0 {
		t.Fatalf("redirect destination requests = %d, token = %q", destinationRequests.Load(), destinationToken.Load())
	}
}

func TestNewHTTPRuntimeRejectsURLUserinfoWithoutExposingSecret(t *testing.T) {
	const secret = "url-password-secret"
	runtime, err := NewHTTPRuntime("https://remote-user:"+secret+"@agent.example.test", "cli-project", "token")
	if err == nil {
		runtime.mu.Lock()
		runtime.session = SessionInfo{ID: httpTestSessionID, ProjectName: "cli-project", MainThreadID: httpTestThreadID}
		runtime.mu.Unlock()
		payload, exportErr := runtime.ExportThreadRef()
		if exportErr == nil && strings.Contains(string(payload), secret) {
			t.Errorf("export exposed URL credential: %s", payload)
		}
		t.Fatal("URL userinfo was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("constructor error exposed URL credential: %v", err)
	}
}

func httpSessionFixture(sessionID string, mainThreadID string) map[string]any {
	return map[string]any{
		"session_id": sessionID, "uid": "1234", "title": "HTTP task", "main_thread_id": mainThreadID,
		"project_name": "cli-project", "project_path": "/workspace/cli-project", "status": 1,
	}
}

func httpTimelineFixture(eventID string, eventType string, turnID string, payload any) map[string]any {
	return map[string]any{
		"event_id": eventID, "event_type": eventType, "session_id": httpTestSessionID,
		"thread_id": httpTestThreadID, "turn_id": turnID, "payload": payload,
	}
}

func decodeHTTPRequest(t *testing.T, request *http.Request, output any) {
	t.Helper()
	if err := json.NewDecoder(request.Body).Decode(output); err != nil {
		t.Fatalf("decode request: %v", err)
	}
}

func assertJSONString(t *testing.T, raw json.RawMessage, expected string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(raw, &got); err != nil || got != expected {
		t.Fatalf("JSON string = %q, error = %v, expected %q", got, err, expected)
	}
}

func writeHTTPJSON(t *testing.T, writer http.ResponseWriter, payload any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func writeSSEFixture(writer http.ResponseWriter, eventID string, eventType string, threadID string, turnID string, payload string) {
	fmt.Fprintf(writer, "event: event\ndata: {\"event\":{\"event_id\":%q,\"event_type\":%q,\"session_id\":%q,\"thread_id\":%q,\"turn_id\":%q,\"payload\":%s},\"BaseResp\":{\"StatusCode\":0}}\n\n", eventID, eventType, httpTestSessionID, threadID, turnID, payload)
}

func receiveHTTPEvent(t *testing.T, events <-chan timeline.Event) timeline.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return timeline.Event{}
	}
}
