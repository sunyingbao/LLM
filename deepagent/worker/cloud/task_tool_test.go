//go:build !windows

package cloud

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"eino-cli/deepagent/coordinator"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/tasktool"
	"github.com/cloudwego/eino/components/tool"
)

func TestTaskToolSpawnTaskMergesBusinessMetadata(t *testing.T) {
	var gotReq *coordinator.CreateThreadRequest
	var gotSpawned tasktool.SpawnedThread
	coordinatorrpc.SetMock.CreateThread(func(ctx context.Context, req coordinator.CreateThreadRequest) (coordinator.CreateThreadResult, error) {
		gotReq = &req
		return coordinator.CreateThreadResult{
			Thread: &coordinator.Thread{
				ThreadID:  2001,
				SessionID: "sess-parent",
				Title:     "child",
				Status:    coordinator.ThreadStatusReady,
				Metadata:  req.Metadata,
				Profile:   req.Profile,
			},
			InitialMessage: &coordinator.Message{MessageID: 2002},
		}, nil
	})
	defer coordinatorrpc.SetMock.CreateThread(nil)

	toolset := &testTaskToolConfig{
		Namespace:         "ns",
		Env:               "ppe_a",
		ThreadID:          1001,
		UserID:            12345,
		SessionID:         "sess-parent",
		SpawnProfile:      tasktool.ThreadProfile{Cwd: "/repo"},
		WorkerConcurrency: 2,
		Metadata: map[string]string{
			"biz_key":          "biz_value",
			"parent_thread_id": "from_static",
		},
		SpawnInitialMessageMetadata: map[string]string{
			"from": "parent",
		},
		OnThreadSpawned: func(ctx context.Context, spawned tasktool.SpawnedThread) (string, error) {
			gotSpawned = spawned
			return "alice", nil
		},
	}

	out := invokeTaskToolSpawnTask(t, toolset, `{"title":"child","role":" reviewer ","content":"do child work","metadata":{"task_type":"research","parent_thread_id":"from_business"}}`)
	if out.Errmsg != "" {
		t.Fatalf("spawn errmsg = %q", out.Errmsg)
	}
	var result struct {
		InitialMessageID string `json:"initial_message_id"`
		Target           string `json:"target"`
		Warning          string `json:"warning"`
	}
	decodeTaskToolData(t, out.Data, &result)
	if result.InitialMessageID != "2002" || result.Target != "alice" || result.Warning != "" {
		t.Fatalf("spawn result = %+v", result)
	}
	if gotReq == nil {
		t.Fatalf("CreateThread was not called")
	}
	if gotReq.Env != "ppe_a" {
		t.Fatalf("env = %q", gotReq.Env)
	}
	if gotReq.SessionID != "sess-parent" {
		t.Fatalf("session_id = %q", gotReq.SessionID)
	}
	if gotReq.UserID != 12345 {
		t.Fatalf("user_id = %d", gotReq.UserID)
	}
	if gotReq.Metadata["task_type"] != "research" || gotReq.Metadata["biz_key"] != "biz_value" ||
		gotReq.Metadata["parent_thread_id"] != "from_business" {
		t.Fatalf("metadata = %+v", gotReq.Metadata)
	}
	if gotReq.Profile.Role != "reviewer" || gotReq.Profile.Cwd != "/repo" {
		t.Fatalf("profile = %+v", gotReq.Profile)
	}
	if gotReq.InitialMessage.Metadata["from"] != "parent" {
		t.Fatalf("initial message metadata = %+v", gotReq.InitialMessage.Metadata)
	}
	if gotReq.InitialMessage.MessageType != string(agentworker.MessageTypeText) {
		t.Fatalf("message_type = %q", gotReq.InitialMessage.MessageType)
	}
	if string(gotReq.InitialMessage.Payload) != "do child work" {
		t.Fatalf("payload content = %q", string(gotReq.InitialMessage.Payload))
	}
	if gotReq.InitialMessage.SenderID != "1001" {
		t.Fatalf("sender_id = %q", gotReq.InitialMessage.SenderID)
	}
	if gotSpawned.ThreadID != "2001" || gotSpawned.InitialMessageID != "2002" ||
		gotSpawned.SessionID != "sess-parent" || gotSpawned.Title != "child" ||
		gotSpawned.Profile.Role != "reviewer" || gotSpawned.Profile.Cwd != "/repo" {
		t.Fatalf("spawned = %+v", gotSpawned)
	}
	if gotSpawned.Metadata["biz_key"] != "biz_value" || gotSpawned.Metadata["task_type"] != "research" {
		t.Fatalf("spawned metadata = %+v", gotSpawned.Metadata)
	}
}

func TestTaskToolSpawnTaskFormatsInitialCloudRequest(t *testing.T) {
	var gotReq *coordinator.CreateThreadRequest
	coordinatorrpc.SetMock.CreateThread(func(ctx context.Context, req coordinator.CreateThreadRequest) (coordinator.CreateThreadResult, error) {
		gotReq = &req
		return coordinator.CreateThreadResult{
			Thread: &coordinator.Thread{
				ThreadID:  2001,
				SessionID: "sess-parent",
				Status:    coordinator.ThreadStatusReady,
			},
			InitialMessage: &coordinator.Message{MessageID: 2002},
		}, nil
	})
	defer coordinatorrpc.SetMock.CreateThread(nil)

	toolset := &testTaskToolConfig{
		Namespace:         "ns",
		ThreadID:          1001,
		SessionID:         "sess-parent",
		WorkerConcurrency: 2,
		SpawnInitialMessageMetadata: map[string]string{
			"spawn_initial":   "true",
			"from_thread_ref": "static-ref",
		},
		FormatOutbound: func(ctx context.Context, msg tasktool.OutboundMessage) (*tasktool.FormattedOutboundMessage, error) {
			if msg.FromThreadID != "1001" || msg.Target != "" || msg.Content != "do child work" {
				t.Fatalf("outbound = %+v", msg)
			}
			return &tasktool.FormattedOutboundMessage{
				Payload: []byte("formatted:" + msg.Content),
				Metadata: map[string]string{
					"from_thread_ref": "main",
					"formatter_only":  "true",
				},
			}, nil
		},
	}

	out := invokeTaskToolSpawnTask(t, toolset, `{"title":"child","content":"do child work"}`)
	if out.Errmsg != "" {
		t.Fatalf("spawn errmsg = %q", out.Errmsg)
	}
	if gotReq == nil || gotReq.InitialMessage == nil {
		t.Fatalf("CreateThread initial message missing: %+v", gotReq)
	}
	initial := gotReq.InitialMessage
	if payload := string(initial.Payload); payload != "formatted:do child work" {
		t.Fatalf("initial payload = %q", payload)
	}
	if initial.SenderID != "1001" {
		t.Fatalf("sender_id = %q", initial.SenderID)
	}
	if initial.Metadata["from_thread_ref"] != "main" ||
		initial.Metadata["formatter_only"] != "true" ||
		initial.Metadata["spawn_initial"] != "true" {
		t.Fatalf("initial metadata = %+v", initial.Metadata)
	}
}

func TestCoordinatorTaskHostCreateThreadProfileFromRequest(t *testing.T) {
	ctx := context.Background()
	host := CoordinatorTaskHost{Namespace: "ns", UserID: 12345}

	var gotReq *coordinator.CreateThreadRequest
	coordinatorrpc.SetMock.CreateThread(func(ctx context.Context, req coordinator.CreateThreadRequest) (coordinator.CreateThreadResult, error) {
		gotReq = &req
		return coordinator.CreateThreadResult{
			Thread: &coordinator.Thread{
				ThreadID:  2001,
				SessionID: "sess_1",
				Status:    coordinator.ThreadStatusReady,
				Profile:   req.Profile,
			},
		}, nil
	})
	thread, err := host.CreateThread(ctx, tasktool.CreateThreadRequest{
		SessionID: "sess_1",
		Profile:   tasktool.ThreadProfile{Role: " request_role ", Cwd: "/repo"},
		Metadata:  map[string]string{"biz_key": "biz_value"},
	})
	coordinatorrpc.SetMock.CreateThread(nil)
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if gotReq.Metadata["biz_key"] != "biz_value" {
		t.Fatalf("metadata = %+v", gotReq.Metadata)
	}
	if gotReq.Profile.Role != "request_role" || gotReq.Profile.Cwd != "/repo" {
		t.Fatalf("profile = %+v", gotReq.Profile)
	}
	if thread.Profile.Role != "request_role" {
		t.Fatalf("thread.Profile.Role = %q, want request_role", thread.Profile.Role)
	}
	if thread.Profile.Cwd != "/repo" {
		t.Fatalf("thread.Profile.Cwd = %q, want /repo", thread.Profile.Cwd)
	}

}

func TestTaskToolSpawnTaskWorksWithoutBusinessMetadata(t *testing.T) {
	coordinatorrpc.SetMock.CreateThread(func(ctx context.Context, req coordinator.CreateThreadRequest) (coordinator.CreateThreadResult, error) {
		if req.SessionID != "" {
			t.Fatalf("session_id = %q, want empty", req.SessionID)
		}
		return coordinator.CreateThreadResult{
			Thread:         &coordinator.Thread{ThreadID: 2001, Status: coordinator.ThreadStatusReady},
			InitialMessage: &coordinator.Message{MessageID: 2002},
		}, nil
	})
	defer coordinatorrpc.SetMock.CreateThread(nil)

	out := invokeTaskToolSpawnTask(t, &testTaskToolConfig{Namespace: "ns"}, `{"content":"child"}`)
	if out.Errmsg != "" {
		t.Fatalf("spawn errmsg = %q", out.Errmsg)
	}
	var result struct {
		InitialMessageID string `json:"initial_message_id"`
		Target           string `json:"target"`
	}
	decodeTaskToolData(t, out.Data, &result)
	if result.InitialMessageID != "2002" || result.Target != "2001" {
		t.Fatalf("result = %+v", result)
	}
}

func TestTaskToolSpawnTaskOutputHidesThreadID(t *testing.T) {
	coordinatorrpc.SetMock.CreateThread(func(ctx context.Context, req coordinator.CreateThreadRequest) (coordinator.CreateThreadResult, error) {
		return coordinator.CreateThreadResult{
			Thread:         &coordinator.Thread{ThreadID: 2001, Status: coordinator.ThreadStatusReady},
			InitialMessage: &coordinator.Message{MessageID: 2002},
		}, nil
	})
	defer coordinatorrpc.SetMock.CreateThread(nil)

	out := invokeTaskToolSpawnTask(t, &testTaskToolConfig{Namespace: "ns"}, `{"content":"child"}`)
	payload := string(out.Data)
	if strings.Contains(payload, "thread_id") {
		t.Fatalf("spawn output should not expose thread_id: %s", payload)
	}
	if !strings.Contains(payload, `"target":"2001"`) || !strings.Contains(payload, `"initial_message_id":"2002"`) {
		t.Fatalf("spawn output = %s", payload)
	}
}

func TestTaskToolSendMessageRejectsSelfByDefault(t *testing.T) {
	out := invokeTaskToolSendMessage(t, &testTaskToolConfig{Namespace: "ns", ThreadID: 1001}, `{"target":"1001","content":"hello"}`)
	if !strings.Contains(out.Errmsg, "current thread") {
		t.Fatalf("errmsg = %q", out.Errmsg)
	}
}

func TestTaskToolSendMessageMergesBusinessMetadata(t *testing.T) {
	var gotReq *coordinator.SubmitInputRequest
	coordinatorrpc.SetMock.SubmitInput(func(ctx context.Context, req coordinator.SubmitInputRequest) (coordinator.SubmitInputResult, error) {
		gotReq = &req
		return coordinator.SubmitInputResult{
			Message: &coordinator.Message{MessageID: 3002, Sender: &coordinator.Sender{Type: req.SenderType, ID: req.SenderID}, MessageType: req.MessageType, Payload: req.Payload, Metadata: req.Metadata},
		}, nil
	})
	defer coordinatorrpc.SetMock.SubmitInput(nil)

	toolset := &testTaskToolConfig{
		Namespace: "ns",
		ThreadID:  1001,
		Metadata: map[string]string{
			"biz_key":          "biz_value",
			"parent_thread_id": "from_static",
		},
	}
	out := invokeTaskToolSendMessage(t, toolset, `{"target":"3001","content":"hello","metadata":{"task_type":"notify","parent_thread_id":"from_business"}}`)
	if out.Errmsg != "" {
		t.Fatalf("send errmsg = %q", out.Errmsg)
	}
	var result struct {
		MessageID string `json:"message_id"`
	}
	decodeTaskToolData(t, out.Data, &result)
	if result.MessageID != "3002" {
		t.Fatalf("result = %+v", result)
	}
	if gotReq == nil {
		t.Fatalf("SendMessage was not called")
	}
	if gotReq.Metadata["task_type"] != "notify" || gotReq.Metadata["biz_key"] != "biz_value" ||
		gotReq.Metadata["parent_thread_id"] != "from_business" {
		t.Fatalf("metadata = %+v", gotReq.Metadata)
	}
	if gotReq.ThreadID != 3001 {
		t.Fatalf("thread_id = %d", gotReq.ThreadID)
	}
	if gotReq.SenderID != "1001" {
		t.Fatalf("sender_id = %q", gotReq.SenderID)
	}
	if gotReq.MessageType != string(agentworker.MessageTypeText) {
		t.Fatalf("message_type = %q", gotReq.MessageType)
	}
	if payload := string(gotReq.Payload); payload != "hello" {
		t.Fatalf("payload = %q", payload)
	}
}

func TestTaskToolSendMessageResolvesAliasAndFormatsPayload(t *testing.T) {
	var gotReq *coordinator.SubmitInputRequest
	coordinatorrpc.SetMock.SubmitInput(func(ctx context.Context, req coordinator.SubmitInputRequest) (coordinator.SubmitInputResult, error) {
		gotReq = &req
		return coordinator.SubmitInputResult{
			Message: &coordinator.Message{MessageID: 3002, Sender: &coordinator.Sender{Type: req.SenderType, ID: req.SenderID}, MessageType: req.MessageType, Payload: req.Payload, Metadata: req.Metadata},
		}, nil
	})
	defer coordinatorrpc.SetMock.SubmitInput(nil)

	toolset := &testTaskToolConfig{
		Namespace: "ns",
		ThreadID:  1001,
		ResolveTarget: func(ctx context.Context, target string) (int64, error) {
			if target != "alice" {
				t.Fatalf("target = %q", target)
			}
			return 3001, nil
		},
		FormatOutbound: func(ctx context.Context, msg tasktool.OutboundMessage) (*tasktool.FormattedOutboundMessage, error) {
			if msg.FromThreadID != "1001" || msg.Target != "alice" || msg.Content != "hello" {
				t.Fatalf("outbound = %+v", msg)
			}
			return &tasktool.FormattedOutboundMessage{Payload: []byte("wrapped:" + msg.Content)}, nil
		},
	}
	out := invokeTaskToolSendMessage(t, toolset, `{"target":"alice","content":"hello"}`)
	if out.Errmsg != "" {
		t.Fatalf("send errmsg = %q", out.Errmsg)
	}
	if gotReq.ThreadID != 3001 {
		t.Fatalf("thread_id = %d", gotReq.ThreadID)
	}
	if string(gotReq.Payload) != "wrapped:hello" {
		t.Fatalf("payload = %q", string(gotReq.Payload))
	}
}

func TestCoordinatorTaskHostCloseThread(t *testing.T) {
	ctx := context.Background()
	host := CoordinatorTaskHost{Namespace: "ns"}

	var gotReq *coordinator.RequestThreadCloseRequest
	coordinatorrpc.SetMock.RequestThreadClose(func(ctx context.Context, req coordinator.RequestThreadCloseRequest) (*coordinator.RequestThreadCloseResult, error) {
		gotReq = &req
		return &coordinator.RequestThreadCloseResult{}, nil
	})
	defer coordinatorrpc.SetMock.RequestThreadClose(nil)

	rsp, err := host.CloseThread(ctx, tasktool.CloseThreadRequest{ThreadID: "2001", Reason: "done"})
	if err != nil {
		t.Fatalf("CloseThread() error = %v", err)
	}
	if rsp == nil {
		t.Fatalf("CloseThread() returned nil rsp")
	}
	if gotReq == nil {
		t.Fatalf("CloseThread RPC was not called")
	}
	if gotReq.Namespace != "ns" || gotReq.ThreadID != 2001 || gotReq.Reason != "done" {
		t.Fatalf("close request = %+v", gotReq)
	}
}

func TestTaskToolWaitMessageReturnsObservedResult(t *testing.T) {
	coordinatorrpc.SetMock.ListEvents(func(ctx context.Context, req coordinator.ListEventsRequest) (coordinator.ListEventsResult, error) {
		if req.ThreadID != 2001 {
			t.Fatalf("thread_id = %d", req.ThreadID)
		}
		if req.Direction != coordinator.ListDirectionBackward {
			t.Fatalf("direction = %v", req.Direction)
		}
		if req.Limit != 5 {
			t.Fatalf("limit = %d", req.Limit)
		}
		if req.Order != coordinator.ListOrderCreatedAt {
			t.Fatalf("order_by = %v", req.Order)
		}
		return coordinator.ListEventsResult{
			Events: []coordinator.Event{
				{EventID: 1, ThreadID: 2001, TurnID: "turn_2002", EventType: "business.progress", Payload: []byte("almost")},
				{EventID: 2, ThreadID: 2001, TurnID: "turn_2002", EventType: "business.done", Payload: []byte("final answer")},
			},
			NextCursor: 2,
			HasMore:    false,
		}, nil
	})
	defer coordinatorrpc.SetMock.ListEvents(nil)

	toolset := &testTaskToolConfig{
		Namespace:         "ns",
		WorkerConcurrency: 2,
		WaitEventLimit:    5,
		ResolveTarget: func(ctx context.Context, target string) (int64, error) {
			if target != "alice" {
				t.Fatalf("target = %q", target)
			}
			return 2001, nil
		},
		MessageWaitObserver: func(events []coordinator.Event, messageID int64) tasktool.MessageWaitResult {
			if messageID != 2002 {
				t.Fatalf("message_id = %d", messageID)
			}
			for _, event := range events {
				if event.EventType == "business.done" {
					return tasktool.MessageWaitResult{Done: true, Result: string(event.Payload)}
				}
			}
			return tasktool.MessageWaitResult{}
		},
	}
	out := invokeTaskToolWaitMessage(t, toolset, `{"target":"alice","message_id":"2002","timeout_ms":1000}`)
	if out.Errmsg != "" {
		t.Fatalf("wait errmsg = %q", out.Errmsg)
	}
	var result struct {
		Res map[string]struct {
			Result   string                    `json:"result"`
			Done     bool                      `json:"done"`
			TimedOut bool                      `json:"timed_out"`
			State    tasktool.WaitMessageState `json:"state"`
			SysError string                    `json:"sys_error"`
		} `json:"res"`
	}
	decodeTaskToolData(t, out.Data, &result)
	got := result.Res["alice/2002"]
	if got.Result != "final answer" || !got.Done || got.TimedOut || got.State != tasktool.WaitMessageStateCompleted || got.SysError != "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestTaskToolWaitMessageReturnsMultipleResults(t *testing.T) {
	coordinatorrpc.SetMock.ListEvents(func(ctx context.Context, req coordinator.ListEventsRequest) (coordinator.ListEventsResult, error) {
		var events []coordinator.Event
		switch req.ThreadID {
		case 2001:
			events = []coordinator.Event{{EventID: 1, ThreadID: 2001, TurnID: "turn_2002", EventType: "business.done", Payload: []byte("first final")}}
		case 3001:
			events = []coordinator.Event{{EventID: 2, ThreadID: 3001, TurnID: "turn_3002", EventType: "business.done", Payload: []byte("second final")}}
		default:
			t.Fatalf("unexpected thread_id = %d", req.ThreadID)
		}
		return coordinator.ListEventsResult{
			Events:     events,
			NextCursor: events[len(events)-1].EventID,
			HasMore:    false,
		}, nil
	})
	defer coordinatorrpc.SetMock.ListEvents(nil)

	toolset := &testTaskToolConfig{
		Namespace:         "ns",
		WorkerConcurrency: 2,
		ResolveTarget: func(ctx context.Context, target string) (int64, error) {
			switch target {
			case "alice":
				return 2001, nil
			case "bob":
				return 3001, nil
			default:
				t.Fatalf("unexpected target = %q", target)
				return 0, nil
			}
		},
		MessageWaitObserver: func(events []coordinator.Event, messageID int64) tasktool.MessageWaitResult {
			for _, event := range events {
				if event.EventType == "business.done" {
					return tasktool.MessageWaitResult{Done: true, Result: string(event.Payload)}
				}
			}
			return tasktool.MessageWaitResult{}
		},
	}
	out := invokeTaskToolWaitMessage(t, toolset, `{"targets":[{"target":"alice","message_id":"2002"},{"target":"bob","message_id":"3002"}],"timeout_ms":1000}`)
	if out.Errmsg != "" {
		t.Fatalf("wait errmsg = %q", out.Errmsg)
	}
	var result struct {
		Res map[string]struct {
			Result string `json:"result"`
		} `json:"res"`
	}
	decodeTaskToolData(t, out.Data, &result)
	if result.Res["alice/2002"].Result != "first final" {
		t.Fatalf("first result = %+v", result.Res["alice/2002"])
	}
	if result.Res["bob/3002"].Result != "second final" {
		t.Fatalf("second result = %+v", result.Res["bob/3002"])
	}
}

func TestTaskToolWaitMessageTimesOutUndoneTargets(t *testing.T) {
	coordinatorrpc.SetMock.ListEvents(func(ctx context.Context, req coordinator.ListEventsRequest) (coordinator.ListEventsResult, error) {
		return coordinator.ListEventsResult{
			Events:  []coordinator.Event{},
			HasMore: false,
		}, nil
	})
	defer coordinatorrpc.SetMock.ListEvents(nil)

	toolset := &testTaskToolConfig{
		Namespace:         "ns",
		WorkerConcurrency: 2,
		ResolveTarget: func(ctx context.Context, target string) (int64, error) {
			if target != "alice" {
				t.Fatalf("target = %q", target)
			}
			return 2001, nil
		},
		MessageWaitObserver: func(events []coordinator.Event, messageID int64) tasktool.MessageWaitResult {
			return tasktool.MessageWaitResult{Result: "still running"}
		},
	}
	out := invokeTaskToolWaitMessage(t, toolset, `{"target":"alice","message_id":"2002","timeout_ms":10}`)
	if out.Errmsg != "" {
		t.Fatalf("wait errmsg = %q", out.Errmsg)
	}
	var result struct {
		Res map[string]struct {
			Result   string                    `json:"result"`
			Done     bool                      `json:"done"`
			TimedOut bool                      `json:"timed_out"`
			State    tasktool.WaitMessageState `json:"state"`
			SysError string                    `json:"sys_error"`
		} `json:"res"`
	}
	decodeTaskToolData(t, out.Data, &result)
	got := result.Res["alice/2002"]
	if got.Result != "still running" || got.Done || !got.TimedOut || got.State != tasktool.WaitMessageStateWaiting || got.SysError != "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestTaskToolWaitMessageRequiresObserver(t *testing.T) {
	out := invokeTaskToolWaitMessage(t, &testTaskToolConfig{Namespace: "ns"}, `{"target":"alice","message_id":"1002"}`)
	if !strings.Contains(out.Errmsg, "observer") {
		t.Fatalf("errmsg = %q", out.Errmsg)
	}
}

func TestTaskToolWaitMessageValidatesTargets(t *testing.T) {
	toolset := &testTaskToolConfig{
		Namespace: "ns",
		MessageWaitObserver: func(events []coordinator.Event, messageID int64) tasktool.MessageWaitResult {
			return tasktool.MessageWaitResult{}
		},
	}
	out := invokeTaskToolWaitMessage(t, toolset, `{"targets":[{"target":"alice"}]}`)
	if !strings.Contains(out.Errmsg, "targets[0].message_id") {
		t.Fatalf("errmsg = %q", out.Errmsg)
	}
}

type taskToolOutput struct {
	Data   json.RawMessage `json:"data"`
	Errmsg string          `json:"errmsg"`
}

type testTaskToolConfig struct {
	Namespace                   string
	Env                         string
	ThreadID                    int64
	UserID                      int64
	SessionID                   string
	WorkerConcurrency           int
	WaitEventLimit              int
	Metadata                    map[string]string
	SpawnProfile                tasktool.ThreadProfile
	SpawnInitialMessageMetadata map[string]string
	ResolveTarget               func(ctx context.Context, target string) (int64, error)
	OnThreadSpawned             func(ctx context.Context, spawned tasktool.SpawnedThread) (string, error)
	FormatOutbound              func(ctx context.Context, msg tasktool.OutboundMessage) (*tasktool.FormattedOutboundMessage, error)
	MessageWaitObserver         func(events []coordinator.Event, messageID int64) tasktool.MessageWaitResult
	CloseTaskObserver           func(events []coordinator.Event) string
}

func newTestTaskTool(cfg *testTaskToolConfig) *tasktool.TaskTool {
	if cfg == nil || cfg.Namespace == "" {
		return nil
	}
	toolset := &tasktool.TaskTool{
		Host: CoordinatorTaskHost{
			Namespace: cfg.Namespace,
			Env:       cfg.Env,
			UserID:    cfg.UserID,
		},
		ThreadID:                    strconv.FormatInt(cfg.ThreadID, 10),
		SessionID:                   cfg.SessionID,
		WorkerConcurrency:           cfg.WorkerConcurrency,
		WaitEventLimit:              cfg.WaitEventLimit,
		Metadata:                    cfg.Metadata,
		SpawnProfile:                cfg.SpawnProfile,
		SpawnInitialMessageMetadata: cfg.SpawnInitialMessageMetadata,
		OnThreadSpawned:             cfg.OnThreadSpawned,
		FormatOutbound:              cfg.FormatOutbound,
	}
	if cfg.ResolveTarget != nil {
		toolset.ResolveTarget = func(ctx context.Context, target string) (string, error) {
			threadID, err := cfg.ResolveTarget(ctx, target)
			if err != nil {
				return "", err
			}
			return strconv.FormatInt(threadID, 10), nil
		}
	}
	if cfg.MessageWaitObserver != nil {
		toolset.MessageWaitObserver = func(events []*tasktool.Event, messageID string) tasktool.MessageWaitResult {
			parsedMessageID, err := strconv.ParseInt(messageID, 10, 64)
			if err != nil {
				return tasktool.MessageWaitResult{Done: true, SysError: err.Error()}
			}
			return cfg.MessageWaitObserver(taskToolTestEventsToAC(events), parsedMessageID)
		}
	}
	if cfg.CloseTaskObserver != nil {
		toolset.CloseTaskObserver = func(events []*tasktool.Event) string {
			return cfg.CloseTaskObserver(taskToolTestEventsToAC(events))
		}
	}
	return toolset
}

func invokeTaskToolSpawnTask(t *testing.T, cfg *testTaskToolConfig, args string) taskToolOutput {
	t.Helper()
	return invokeTaskToolByIndex(t, cfg, 1, args)
}

func invokeTaskToolSendMessage(t *testing.T, cfg *testTaskToolConfig, args string) taskToolOutput {
	t.Helper()
	return invokeTaskToolByIndex(t, cfg, 0, args)
}

func invokeTaskToolWaitMessage(t *testing.T, cfg *testTaskToolConfig, args string) taskToolOutput {
	t.Helper()
	return invokeTaskToolByIndex(t, cfg, 2, args)
}

func invokeTaskToolCloseTask(t *testing.T, cfg *testTaskToolConfig, args string) taskToolOutput {
	t.Helper()
	return invokeTaskToolByIndex(t, cfg, 3, args)
}

func invokeTaskToolByIndex(t *testing.T, cfg *testTaskToolConfig, idx int, args string) taskToolOutput {
	t.Helper()
	toolset := newTestTaskTool(cfg)
	if toolset == nil {
		t.Fatalf("newTestTaskTool returned nil")
	}
	tools := toolset.Tools()
	if len(tools) <= idx {
		t.Fatalf("tool index %d not available; got %d tools", idx, len(tools))
	}
	invokable := tools[idx].(tool.InvokableTool)
	out, err := invokable.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	return decodeTaskToolOutput(t, out)
}

func taskToolTestEventsToAC(events []*tasktool.Event) []coordinator.Event {
	out := make([]coordinator.Event, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		threadID, _ := strconv.ParseInt(ev.ThreadID, 10, 64)
		eventID, _ := strconv.ParseInt(ev.ID, 10, 64)
		acEvent := coordinator.Event{
			ThreadID:  threadID,
			TurnID:    ev.TurnID,
			EventType: ev.Type,
			Payload:   append([]byte(nil), ev.Payload...),
			Metadata:  ev.Metadata,
		}
		if eventID != 0 {
			acEvent.EventID = eventID
		}
		out = append(out, acEvent)
	}
	return out
}

func decodeTaskToolOutput(t *testing.T, raw string) taskToolOutput {
	t.Helper()
	var out taskToolOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode output %q: %v", raw, err)
	}
	return out
}

func decodeTaskToolData(t *testing.T, raw json.RawMessage, out any) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode data %s: %v", raw, err)
	}
}
