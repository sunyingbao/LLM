//go:build !windows

package cloud

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"code.byted.org/gopkg/thrift"
	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	"code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/base"
	coordinatorrpc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/rpc/ad_creative_aic_agent_coordinator"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/tasktool"
	"github.com/cloudwego/eino/components/tool"
)

func TestTaskToolSpawnTaskMergesBusinessMetadata(t *testing.T) {
	var gotReq *ac.CreateThreadRequest
	var gotSpawned tasktool.SpawnedThread
	coordinatorrpc.SetMock.CreateThread(func(ctx context.Context, req *ac.CreateThreadRequest) (*ac.CreateThreadResponse, error) {
		gotReq = req
		return &ac.CreateThreadResponse{
			Thread: &ac.Thread{
				ThreadId:  2001,
				SessionId: thrift.StringPtr("sess-parent"),
				Title:     thrift.StringPtr("child"),
				Status:    ac.ThreadStatus_READY,
				Metadata:  req.GetMetadata(),
				Profile:   req.GetProfile(),
			},
			InitialMessage: &ac.Message{MessageId: 2002},
			BaseResp:       okTaskToolBaseResp(),
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
	if gotReq.GetEnv() != "ppe_a" {
		t.Fatalf("env = %q", gotReq.GetEnv())
	}
	if gotReq.GetSessionId() != "sess-parent" {
		t.Fatalf("session_id = %q", gotReq.GetSessionId())
	}
	if gotReq.GetUserId() != 12345 {
		t.Fatalf("user_id = %d", gotReq.GetUserId())
	}
	if gotReq.GetMetadata()["task_type"] != "research" || gotReq.GetMetadata()["biz_key"] != "biz_value" ||
		gotReq.GetMetadata()["parent_thread_id"] != "from_business" {
		t.Fatalf("metadata = %+v", gotReq.GetMetadata())
	}
	if gotReq.GetProfile().GetRole() != "reviewer" || gotReq.GetProfile().GetCwd() != "/repo" {
		t.Fatalf("profile = %+v", gotReq.GetProfile())
	}
	if gotReq.GetInitialMessage().GetMetadata()["from"] != "parent" {
		t.Fatalf("initial message metadata = %+v", gotReq.GetInitialMessage().GetMetadata())
	}
	if gotReq.GetInitialMessage().GetMessageType() != string(agentworker.MessageTypeText) {
		t.Fatalf("message_type = %q", gotReq.GetInitialMessage().GetMessageType())
	}
	if string(gotReq.GetInitialMessage().GetPayload()) != "do child work" {
		t.Fatalf("payload content = %q", string(gotReq.GetInitialMessage().GetPayload()))
	}
	if gotReq.GetInitialMessage().GetSender().GetSenderId() != "1001" {
		t.Fatalf("sender_id = %q", gotReq.GetInitialMessage().GetSender().GetSenderId())
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
	var gotReq *ac.CreateThreadRequest
	coordinatorrpc.SetMock.CreateThread(func(ctx context.Context, req *ac.CreateThreadRequest) (*ac.CreateThreadResponse, error) {
		gotReq = req
		return &ac.CreateThreadResponse{
			Thread: &ac.Thread{
				ThreadId:  2001,
				SessionId: thrift.StringPtr("sess-parent"),
				Status:    ac.ThreadStatus_READY,
			},
			InitialMessage: &ac.Message{MessageId: 2002},
			BaseResp:       okTaskToolBaseResp(),
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
	if gotReq == nil || gotReq.GetInitialMessage() == nil {
		t.Fatalf("CreateThread initial message missing: %+v", gotReq)
	}
	initial := gotReq.GetInitialMessage()
	if payload := string(initial.GetPayload()); payload != "formatted:do child work" {
		t.Fatalf("initial payload = %q", payload)
	}
	if initial.GetSender().GetSenderId() != "1001" {
		t.Fatalf("sender_id = %q", initial.GetSender().GetSenderId())
	}
	if initial.GetMetadata()["from_thread_ref"] != "main" ||
		initial.GetMetadata()["formatter_only"] != "true" ||
		initial.GetMetadata()["spawn_initial"] != "true" {
		t.Fatalf("initial metadata = %+v", initial.GetMetadata())
	}
}

func TestCoordinatorTaskHostCreateThreadProfileFromRequest(t *testing.T) {
	ctx := context.Background()
	host := CoordinatorTaskHost{Namespace: "ns", UserID: 12345}

	var gotReq *ac.CreateThreadRequest
	coordinatorrpc.SetMock.CreateThread(func(ctx context.Context, req *ac.CreateThreadRequest) (*ac.CreateThreadResponse, error) {
		gotReq = req
		return &ac.CreateThreadResponse{
			Thread: &ac.Thread{
				ThreadId:  2001,
				SessionId: thrift.StringPtr("sess_1"),
				Status:    ac.ThreadStatus_READY,
				Profile:   req.GetProfile(),
			},
			BaseResp: okTaskToolBaseResp(),
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
	if gotReq.GetMetadata()["biz_key"] != "biz_value" {
		t.Fatalf("metadata = %+v", gotReq.GetMetadata())
	}
	if gotReq.GetProfile().GetRole() != "request_role" || gotReq.GetProfile().GetCwd() != "/repo" {
		t.Fatalf("profile = %+v", gotReq.GetProfile())
	}
	if thread.Profile.Role != "request_role" {
		t.Fatalf("thread.Profile.Role = %q, want request_role", thread.Profile.Role)
	}
	if thread.Profile.Cwd != "/repo" {
		t.Fatalf("thread.Profile.Cwd = %q, want /repo", thread.Profile.Cwd)
	}

}

func TestTaskToolSpawnTaskWorksWithoutBusinessMetadata(t *testing.T) {
	coordinatorrpc.SetMock.CreateThread(func(ctx context.Context, req *ac.CreateThreadRequest) (*ac.CreateThreadResponse, error) {
		if req.GetSessionId() != "" {
			t.Fatalf("session_id = %q, want empty", req.GetSessionId())
		}
		return &ac.CreateThreadResponse{
			Thread:         &ac.Thread{ThreadId: 2001, Status: ac.ThreadStatus_READY},
			InitialMessage: &ac.Message{MessageId: 2002},
			BaseResp:       okTaskToolBaseResp(),
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
	coordinatorrpc.SetMock.CreateThread(func(ctx context.Context, req *ac.CreateThreadRequest) (*ac.CreateThreadResponse, error) {
		return &ac.CreateThreadResponse{
			Thread:         &ac.Thread{ThreadId: 2001, Status: ac.ThreadStatus_READY},
			InitialMessage: &ac.Message{MessageId: 2002},
			BaseResp:       okTaskToolBaseResp(),
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
	var gotReq *ac.SendMessageRequest
	coordinatorrpc.SetMock.SendMessage(func(ctx context.Context, req *ac.SendMessageRequest) (*ac.SendMessageResponse, error) {
		gotReq = req
		return &ac.SendMessageResponse{
			Message:  &ac.Message{MessageId: 3002, Sender: req.GetSender(), MessageType: req.GetMessageType(), Payload: req.GetPayload(), Metadata: req.GetMetadata()},
			BaseResp: okTaskToolBaseResp(),
		}, nil
	})
	defer coordinatorrpc.SetMock.SendMessage(nil)

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
	if gotReq.GetMetadata()["task_type"] != "notify" || gotReq.GetMetadata()["biz_key"] != "biz_value" ||
		gotReq.GetMetadata()["parent_thread_id"] != "from_business" {
		t.Fatalf("metadata = %+v", gotReq.GetMetadata())
	}
	if gotReq.GetThreadId() != 3001 {
		t.Fatalf("thread_id = %d", gotReq.GetThreadId())
	}
	if gotReq.GetSender().GetSenderId() != "1001" {
		t.Fatalf("sender_id = %q", gotReq.GetSender().GetSenderId())
	}
	if gotReq.GetMessageType() != string(agentworker.MessageTypeText) {
		t.Fatalf("message_type = %q", gotReq.GetMessageType())
	}
	if payload := string(gotReq.GetPayload()); payload != "hello" {
		t.Fatalf("payload = %q", payload)
	}
}

func TestTaskToolSendMessageResolvesAliasAndFormatsPayload(t *testing.T) {
	var gotReq *ac.SendMessageRequest
	coordinatorrpc.SetMock.SendMessage(func(ctx context.Context, req *ac.SendMessageRequest) (*ac.SendMessageResponse, error) {
		gotReq = req
		return &ac.SendMessageResponse{
			Message:  &ac.Message{MessageId: 3002, Sender: req.GetSender(), MessageType: req.GetMessageType(), Payload: req.GetPayload(), Metadata: req.GetMetadata()},
			BaseResp: okTaskToolBaseResp(),
		}, nil
	})
	defer coordinatorrpc.SetMock.SendMessage(nil)

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
	if gotReq.GetThreadId() != 3001 {
		t.Fatalf("thread_id = %d", gotReq.GetThreadId())
	}
	if string(gotReq.GetPayload()) != "wrapped:hello" {
		t.Fatalf("payload = %q", string(gotReq.GetPayload()))
	}
}

func TestCoordinatorTaskHostCloseThread(t *testing.T) {
	ctx := context.Background()
	host := CoordinatorTaskHost{Namespace: "ns"}

	var gotReq *ac.CloseThreadRequest
	coordinatorrpc.SetMock.CloseThread(func(ctx context.Context, req *ac.CloseThreadRequest) (*ac.CloseThreadResponse, error) {
		gotReq = req
		return &ac.CloseThreadResponse{BaseResp: okTaskToolBaseResp()}, nil
	})
	defer coordinatorrpc.SetMock.CloseThread(nil)

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
	if gotReq.GetNamespace() != "ns" || gotReq.GetThreadId() != 2001 || gotReq.GetReason() != "done" {
		t.Fatalf("close request = %+v", gotReq)
	}
}

func TestTaskToolWaitMessageReturnsObservedResult(t *testing.T) {
	coordinatorrpc.SetMock.ListEvents(func(ctx context.Context, req *ac.ListEventsRequest) (*ac.ListEventsResponse, error) {
		if req.GetThreadId() != 2001 {
			t.Fatalf("thread_id = %d", req.GetThreadId())
		}
		if req.GetDirection() != ac.EventListDirection_BACKWARD {
			t.Fatalf("direction = %v", req.GetDirection())
		}
		if req.GetLimit() != 5 {
			t.Fatalf("limit = %d", req.GetLimit())
		}
		if req.GetOrderBy() != ac.EventListOrder_CREATED_AT_IN_THREAD_SEQ {
			t.Fatalf("order_by = %v", req.GetOrderBy())
		}
		return &ac.ListEventsResponse{
			Events: []*ac.Event{
				{EventId: thrift.Int64Ptr(1), ThreadId: 2001, TurnId: "turn_2002", EventType: "business.progress", Payload: []byte("almost")},
				{EventId: thrift.Int64Ptr(2), ThreadId: 2001, TurnId: "turn_2002", EventType: "business.done", Payload: []byte("final answer")},
			},
			NextCursor: thrift.Int64Ptr(2),
			HasMore:    thrift.BoolPtr(false),
			BaseResp:   okTaskToolBaseResp(),
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
		MessageWaitObserver: func(events []*ac.Event, messageID int64) tasktool.MessageWaitResult {
			if messageID != 2002 {
				t.Fatalf("message_id = %d", messageID)
			}
			for _, event := range events {
				if event.GetEventType() == "business.done" {
					return tasktool.MessageWaitResult{Done: true, Result: string(event.GetPayload())}
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
	coordinatorrpc.SetMock.ListEvents(func(ctx context.Context, req *ac.ListEventsRequest) (*ac.ListEventsResponse, error) {
		var events []*ac.Event
		switch req.GetThreadId() {
		case 2001:
			events = []*ac.Event{{EventId: thrift.Int64Ptr(1), ThreadId: 2001, TurnId: "turn_2002", EventType: "business.done", Payload: []byte("first final")}}
		case 3001:
			events = []*ac.Event{{EventId: thrift.Int64Ptr(2), ThreadId: 3001, TurnId: "turn_3002", EventType: "business.done", Payload: []byte("second final")}}
		default:
			t.Fatalf("unexpected thread_id = %d", req.GetThreadId())
		}
		return &ac.ListEventsResponse{
			Events:     events,
			NextCursor: thrift.Int64Ptr(events[len(events)-1].GetEventId()),
			HasMore:    thrift.BoolPtr(false),
			BaseResp:   okTaskToolBaseResp(),
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
		MessageWaitObserver: func(events []*ac.Event, messageID int64) tasktool.MessageWaitResult {
			for _, event := range events {
				if event.GetEventType() == "business.done" {
					return tasktool.MessageWaitResult{Done: true, Result: string(event.GetPayload())}
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
	coordinatorrpc.SetMock.ListEvents(func(ctx context.Context, req *ac.ListEventsRequest) (*ac.ListEventsResponse, error) {
		return &ac.ListEventsResponse{
			Events:   []*ac.Event{},
			HasMore:  thrift.BoolPtr(false),
			BaseResp: okTaskToolBaseResp(),
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
		MessageWaitObserver: func(events []*ac.Event, messageID int64) tasktool.MessageWaitResult {
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
		MessageWaitObserver: func(events []*ac.Event, messageID int64) tasktool.MessageWaitResult {
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
	MessageWaitObserver         func(events []*ac.Event, messageID int64) tasktool.MessageWaitResult
	CloseTaskObserver           func(events []*ac.Event) string
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

func taskToolTestEventsToAC(events []*tasktool.Event) []*ac.Event {
	out := make([]*ac.Event, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		threadID, _ := strconv.ParseInt(ev.ThreadID, 10, 64)
		eventID, _ := strconv.ParseInt(ev.ID, 10, 64)
		acEvent := &ac.Event{
			ThreadId:  threadID,
			TurnId:    ev.TurnID,
			EventType: ev.Type,
			Payload:   append([]byte(nil), ev.Payload...),
			Metadata:  ev.Metadata,
		}
		if eventID != 0 {
			acEvent.EventId = &eventID
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

func okTaskToolBaseResp() *base.BaseResp {
	return &base.BaseResp{StatusCode: 0, StatusMessage: "OK"}
}
