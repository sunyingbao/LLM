package tasktool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"code.byted.org/lang/gg/gmap"
	"github.com/cloudwego/eino/components/tool"
)

func TestTaskToolSpawnTaskReturnsTargetAndInitialMessageID(t *testing.T) {
	host := newFakeHost()
	parent := host.mustCreateRootThread("sess_1", "main")

	toolset := &TaskTool{
		Host:              host,
		ThreadID:          parent.ID,
		SessionID:         parent.SessionID,
		Metadata:          map[string]string{"biz_key": "biz_value"},
		SpawnProfile:      ThreadProfile{Cwd: "/repo"},
		WorkerConcurrency: 2,
		OnThreadSpawned: func(ctx context.Context, spawned SpawnedThread) (string, error) {
			if spawned.Profile.Role != "reviewer" {
				t.Fatalf("spawned role = %q", spawned.Profile.Role)
			}
			if spawned.Profile.Cwd != "/repo" {
				t.Fatalf("spawned cwd = %q", spawned.Profile.Cwd)
			}
			if spawned.Metadata["biz_key"] != "biz_value" {
				t.Fatalf("spawned metadata = %+v", spawned.Metadata)
			}
			return "alice", nil
		},
	}

	out := invokeTaskTool(t, toolset.newSpawnTaskTool(), `{"title":"child","role":" reviewer ","content":"do child work","metadata":{"task_type":"research"}}`)
	if out.Errmsg != "" {
		t.Fatalf("errmsg = %q", out.Errmsg)
	}
	if !strings.Contains(string(out.Data), `"target":"alice"`) {
		t.Fatalf("spawn output = %s", out.Data)
	}
	if !strings.Contains(string(out.Data), `"initial_message_id":"msg_`) {
		t.Fatalf("spawn output = %s", out.Data)
	}
	if len(host.threads) != 2 {
		t.Fatalf("len(host.threads) = %d, want 2", len(host.threads))
	}
	if host.lastCreateThread.ParentThreadID != parent.ID {
		t.Fatalf("parent_thread_id = %q, want %q", host.lastCreateThread.ParentThreadID, parent.ID)
	}
	if host.lastCreateThread.Profile.Role != "reviewer" {
		t.Fatalf("role = %q, want reviewer", host.lastCreateThread.Profile.Role)
	}
	if host.lastCreateThread.Profile.Cwd != "/repo" {
		t.Fatalf("cwd = %q, want /repo", host.lastCreateThread.Profile.Cwd)
	}
	if host.lastCreateThread.InitialMessage == nil || host.lastCreateThread.InitialMessage.Payload == nil {
		t.Fatalf("initial message = %+v", host.lastCreateThread.InitialMessage)
	}
}

func TestTaskToolSpawnTaskAllowsEmptyRole(t *testing.T) {
	host := newFakeHost()
	parent := host.mustCreateRootThread("sess_1", "main")
	toolset := &TaskTool{
		Host:      host,
		ThreadID:  parent.ID,
		SessionID: parent.SessionID,
	}

	out := invokeTaskTool(t, toolset.newSpawnTaskTool(), `{"title":"child","content":"do child work"}`)
	if out.Errmsg != "" {
		t.Fatalf("errmsg = %q", out.Errmsg)
	}
	if host.lastCreateThread.Profile.Role != "" {
		t.Fatalf("role = %q, want empty", host.lastCreateThread.Profile.Role)
	}
}

func TestTaskToolSpawnTaskFormatsInitialMessage(t *testing.T) {
	host := newFakeHost()
	parent := host.mustCreateRootThread("sess_1", "main")
	formatterCalled := false
	toolset := &TaskTool{
		Host:      host,
		ThreadID:  parent.ID,
		SessionID: parent.SessionID,
		Metadata: map[string]string{
			"tool_default": "thread-only",
			"shared":       "tool-thread",
		},
		SpawnInitialMessageMetadata: map[string]string{
			"shared":        "spawn-initial",
			"initial_only":  "initial",
			"formatter_key": "spawn-override",
		},
		FormatOutbound: func(ctx context.Context, msg OutboundMessage) (*FormattedOutboundMessage, error) {
			formatterCalled = true
			if msg.FromThreadID != parent.ID {
				t.Fatalf("FromThreadID = %q, want %q", msg.FromThreadID, parent.ID)
			}
			if msg.Content != "do child work" {
				t.Fatalf("Content = %q", msg.Content)
			}
			if msg.Target != "" {
				t.Fatalf("Target = %q, want empty", msg.Target)
			}
			return &FormattedOutboundMessage{
				Payload: []byte("formatted:" + msg.Content),
				Metadata: map[string]string{
					"formatter_key": "formatter",
					"shared":        "formatter",
				},
			}, nil
		},
		WorkerConcurrency: 2,
	}

	out := invokeTaskTool(t, toolset.newSpawnTaskTool(), `{"title":"child","content":"do child work","metadata":{"task_type":"research","shared":"thread-input"}}`)
	if out.Errmsg != "" {
		t.Fatalf("errmsg = %q", out.Errmsg)
	}
	if !formatterCalled {
		t.Fatalf("FormatOutbound was not called")
	}
	initial := host.lastCreateThread.InitialMessage
	if initial == nil {
		t.Fatalf("initial message is nil")
	}
	if string(initial.Payload) != "formatted:do child work" {
		t.Fatalf("initial payload = %q", string(initial.Payload))
	}
	if initial.Metadata["formatter_key"] != "formatter" ||
		initial.Metadata["shared"] != "formatter" ||
		initial.Metadata["initial_only"] != "initial" {
		t.Fatalf("initial metadata = %+v", initial.Metadata)
	}
	if _, ok := initial.Metadata["task_type"]; ok {
		t.Fatalf("spawn input metadata leaked into initial message: %+v", initial.Metadata)
	}
	if _, ok := initial.Metadata["tool_default"]; ok {
		t.Fatalf("TaskTool metadata leaked into initial message: %+v", initial.Metadata)
	}
	if host.lastCreateThread.Metadata["task_type"] != "research" ||
		host.lastCreateThread.Metadata["tool_default"] != "thread-only" ||
		host.lastCreateThread.Metadata["shared"] != "thread-input" {
		t.Fatalf("thread metadata = %+v", host.lastCreateThread.Metadata)
	}
}

func TestTaskToolDescriptionsStayLowLevel(t *testing.T) {
	toolset := &TaskTool{Host: fakeNoopHost{}}
	tools := map[string]tool.BaseTool{}
	for _, tl := range toolset.Tools() {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("tool Info() error = %v", err)
		}
		tools[info.Name] = tl
	}

	sendInfo, err := tools[ToolSendMessage].Info(context.Background())
	if err != nil {
		t.Fatalf("send_message Info() error = %v", err)
	}
	for _, want := range []string{"existing task thread", "complete message body", "message_id"} {
		if !strings.Contains(sendInfo.Desc, want) {
			t.Fatalf("send_message desc missing %q: %s", want, sendInfo.Desc)
		}
	}

	spawnInfo, err := tools[ToolSpawnTask].Info(context.Background())
	if err != nil {
		t.Fatalf("spawn_task Info() error = %v", err)
	}
	if !strings.Contains(spawnInfo.Desc, "optional title") {
		t.Fatalf("spawn_task desc should keep title optional at the low-level tool boundary: %s", spawnInfo.Desc)
	}
	if strings.Contains(spawnInfo.Desc, "clear boundary") || strings.Contains(spawnInfo.Desc, "independently actionable") {
		t.Fatalf("spawn_task desc should not carry collaboration discipline: %s", spawnInfo.Desc)
	}

	waitInfo, err := tools[ToolWaitMessage].Info(context.Background())
	if err != nil {
		t.Fatalf("wait_message Info() error = %v", err)
	}
	for _, want := range []string{"observed state", "done", "timed_out", "state"} {
		if !strings.Contains(waitInfo.Desc, want) {
			t.Fatalf("wait_message desc missing %q: %s", want, waitInfo.Desc)
		}
	}
	if strings.Contains(waitInfo.Desc, "observed as complete") {
		t.Fatalf("wait_message desc should not imply every observed terminal state is completion: %s", waitInfo.Desc)
	}

	closeInfo, err := tools[ToolCloseTask].Info(context.Background())
	if err != nil {
		t.Fatalf("close_task Info() error = %v", err)
	}
	if !strings.Contains(closeInfo.Desc, "target task thread") {
		t.Fatalf("close_task desc should stay low-level: %s", closeInfo.Desc)
	}
	if strings.Contains(closeInfo.Desc, "completed, failed, cancelled") {
		t.Fatalf("close_task desc should not carry collaboration discipline: %s", closeInfo.Desc)
	}
}

func TestTaskToolDescriptionsCanBeOverridden(t *testing.T) {
	toolset := &TaskTool{
		Host: fakeNoopHost{},
		Descriptions: TaskToolDescriptions{
			SendMessage: "custom send description",
			SpawnTask:   "custom spawn description",
			WaitMessage: "custom wait description",
			CloseTask:   "custom close description",
		},
		SpawnMetadataDescription: "custom metadata description",
	}
	tools := map[string]tool.BaseTool{}
	for _, tl := range toolset.Tools() {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("tool Info() error = %v", err)
		}
		tools[info.Name] = tl
	}
	wantDescs := map[string]string{
		ToolSendMessage: "custom send description",
		ToolSpawnTask:   "custom spawn description Metadata: custom metadata description",
		ToolWaitMessage: "custom wait description",
		ToolCloseTask:   "custom close description",
	}
	for name, want := range wantDescs {
		info, err := tools[name].Info(context.Background())
		if err != nil {
			t.Fatalf("%s Info() error = %v", name, err)
		}
		if info.Desc != want {
			t.Fatalf("%s desc = %q, want %q", name, info.Desc, want)
		}
	}
}

func TestTaskToolSendMessageRejectsSelf(t *testing.T) {
	toolset := &TaskTool{
		Host:     fakeNoopHost{},
		ThreadID: "thread_1",
	}
	out := invokeTaskTool(t, toolset.newSendMessageTool(), `{"target":"thread_1","content":"hello"}`)
	if !strings.Contains(out.Errmsg, "current thread") {
		t.Fatalf("errmsg = %q", out.Errmsg)
	}
}

func TestTaskToolTaskResultModifierMutatesResults(t *testing.T) {
	host := newFakeHost()
	parent := host.mustCreateRootThread("sess_1", "main")
	child := host.mustCreateChildThread(parent.ID, "child")
	modifier := &fakeTaskResultModifier{}
	toolset := &TaskTool{
		Host:               host,
		ThreadID:           parent.ID,
		SessionID:          parent.SessionID,
		TaskResultModifier: modifier,
		OnThreadSpawned: func(ctx context.Context, spawned SpawnedThread) (string, error) {
			return "alice", nil
		},
	}

	spawnOut := invokeTaskTool(t, toolset.newSpawnTaskTool(), `{"title":"child","content":"do child work"}`)
	if spawnOut.Errmsg != "" {
		t.Fatalf("spawn errmsg = %q", spawnOut.Errmsg)
	}
	var spawnResult TaskToolSpawnTaskResult
	if err := json.Unmarshal(spawnOut.Data, &spawnResult); err != nil {
		t.Fatalf("unmarshal spawn result: %v", err)
	}
	if spawnResult.Target != "modified-alice" {
		t.Fatalf("spawn target = %q", spawnResult.Target)
	}
	extra, ok := spawnResult.Extra.(map[string]any)
	if !ok || extra["kind"] != "spawn" {
		t.Fatalf("spawn extra = %#v", spawnResult.Extra)
	}

	sendOut := invokeTaskTool(t, toolset.newSendMessageTool(), `{"target":"`+child.ID+`","content":"hello"}`)
	if sendOut.Errmsg != "" {
		t.Fatalf("send errmsg = %q", sendOut.Errmsg)
	}
	var sendResult TaskToolSendMessageResult
	if err := json.Unmarshal(sendOut.Data, &sendResult); err != nil {
		t.Fatalf("unmarshal send result: %v", err)
	}
	extra, ok = sendResult.Extra.(map[string]any)
	if !ok || extra["kind"] != "send" {
		t.Fatalf("send extra = %#v", sendResult.Extra)
	}
	if !modifier.spawnCalled || !modifier.sendCalled {
		t.Fatalf("modifier calls: spawn=%v send=%v", modifier.spawnCalled, modifier.sendCalled)
	}
	if modifier.spawnThreadID == "" || modifier.spawnInitialMessageID == "" {
		t.Fatalf("spawn hook did not receive thread/message: thread=%q message=%q", modifier.spawnThreadID, modifier.spawnInitialMessageID)
	}
	if modifier.sendMessageID == "" {
		t.Fatalf("send hook did not receive message")
	}
}

func TestTaskToolInputValidatorReturnsErrorsBeforeHostSideEffects(t *testing.T) {
	host := newFakeHost()
	parent := host.mustCreateRootThread("sess_1", "main")
	child := host.mustCreateChildThread(parent.ID, "child")
	baseCreates := host.createCount
	baseSends := host.sendCount
	baseCloses := host.closeCount
	baseLists := host.listCount

	var sendCalled, spawnCalled, waitCalled, closeCalled bool
	toolset := &TaskTool{
		Host:      host,
		ThreadID:  parent.ID,
		SessionID: parent.SessionID,
		InputValidator: TaskToolInputValidator{
			SendMessage: func(ctx context.Context, input *TaskToolSendMessageInput) error {
				sendCalled = true
				if input.Target != child.ID || input.Content != "blocked send" || input.Metadata["k"] != "v" {
					t.Fatalf("send input = %+v", input)
				}
				return errors.New("send blocked")
			},
			SpawnTask: func(ctx context.Context, input *TaskToolSpawnTaskInput) error {
				spawnCalled = true
				if input.Title != "child" || input.Role != "worker" || input.Content != "blocked spawn" || input.Metadata["role"] != "restricted" {
					t.Fatalf("spawn input = %+v", input)
				}
				return errors.New("spawn blocked")
			},
			WaitMessage: func(ctx context.Context, input *TaskToolWaitMessageInput) error {
				waitCalled = true
				if len(input.Targets) != 1 || input.Targets[0].Target != child.ID || input.Targets[0].MessageID != "msg_1" || input.TimeoutMS != 100 {
					t.Fatalf("wait input = %+v", input)
				}
				return errors.New("wait blocked")
			},
			CloseTask: func(ctx context.Context, input *TaskToolCloseTaskInput) error {
				closeCalled = true
				if input.Target != child.ID || input.Reason != "blocked close" {
					t.Fatalf("close input = %+v", input)
				}
				return errors.New("close blocked")
			},
		},
		MessageWaitObserver: func(events []*Event, messageID string) MessageWaitResult {
			t.Fatalf("MessageWaitObserver should not be called")
			return MessageWaitResult{}
		},
	}

	assertBlocked := func(name string, out taskToolOutput, want string) {
		t.Helper()
		if out.Errmsg != want {
			t.Fatalf("%s errmsg = %q, want %q", name, out.Errmsg, want)
		}
	}

	assertBlocked("send", invokeTaskTool(t, toolset.newSendMessageTool(), `{"target":"`+child.ID+`","content":"blocked send","metadata":{"k":"v"}}`), "send blocked")
	assertBlocked("spawn", invokeTaskTool(t, toolset.newSpawnTaskTool(), `{"title":"child","role":"worker","content":"blocked spawn","metadata":{"role":"restricted"}}`), "spawn blocked")
	assertBlocked("wait", invokeTaskTool(t, toolset.newWaitMessageTool(), `{"targets":[{"target":"`+child.ID+`","message_id":"msg_1"}],"timeout_ms":100}`), "wait blocked")
	assertBlocked("close", invokeTaskTool(t, toolset.newCloseTaskTool(), `{"target":"`+child.ID+`","reason":"blocked close"}`), "close blocked")

	if !sendCalled || !spawnCalled || !waitCalled || !closeCalled {
		t.Fatalf("validator calls: send=%v spawn=%v wait=%v close=%v", sendCalled, spawnCalled, waitCalled, closeCalled)
	}
	if host.createCount != baseCreates || host.sendCount != baseSends || host.closeCount != baseCloses || host.listCount != baseLists {
		t.Fatalf("host side effects changed: create %d->%d send %d->%d close %d->%d list %d->%d",
			baseCreates, host.createCount, baseSends, host.sendCount, baseCloses, host.closeCount, baseLists, host.listCount)
	}
}

func TestTaskToolWaitMessageReturnsObservedResult(t *testing.T) {
	host := newFakeHost()
	parent := host.mustCreateRootThread("sess_1", "main")
	child := host.mustCreateChildThread(parent.ID, "child")

	toolset := &TaskTool{
		Host:      host,
		ThreadID:  parent.ID,
		SessionID: parent.SessionID,
		MessageWaitObserver: func(events []*Event, messageID string) MessageWaitResult {
			for _, ev := range events {
				if ev.Metadata["message_id"] == messageID {
					return MessageWaitResult{
						Done:   true,
						Result: string(ev.Payload),
					}
				}
			}
			return MessageWaitResult{}
		},
	}

	sendOut := invokeTaskTool(t, toolset.newSendMessageTool(), `{"target":"`+child.ID+`","content":"hello"}`)
	if sendOut.Errmsg != "" {
		t.Fatalf("send errmsg = %q", sendOut.Errmsg)
	}
	if host.lastSendMessage.ThreadID != child.ID || host.lastSendMessage.FromThreadID != parent.ID {
		t.Fatalf("send request = %+v", host.lastSendMessage)
	}
	msgID := extractTaskToolMessageID(t, sendOut.Data)
	host.appendEvent(child.ID, &Event{
		ID:       "ev_done",
		ThreadID: child.ID,
		Type:     "turn_end",
		Payload:  []byte("done"),
		Metadata: map[string]string{"message_id": msgID},
		TS:       time.Now(),
	})
	out := invokeTaskTool(t, toolset.newWaitMessageTool(), `{"target":"`+child.ID+`","message_id":"`+msgID+`"}`)
	if out.Errmsg != "" {
		t.Fatalf("wait errmsg = %q", out.Errmsg)
	}
	if !strings.Contains(string(out.Data), `"result":"done"`) {
		t.Fatalf("wait output = %s", out.Data)
	}
	if !strings.Contains(string(out.Data), `"done":true`) || !strings.Contains(string(out.Data), `"timed_out":false`) || !strings.Contains(string(out.Data), `"state":"completed"`) {
		t.Fatalf("wait output missing state fields = %s", out.Data)
	}
}

func TestTaskToolCloseTaskReturnsObserverString(t *testing.T) {
	host := newFakeHost()
	parent := host.mustCreateRootThread("sess_1", "main")
	child := host.mustCreateChildThread(parent.ID, "child")
	host.appendEvent(child.ID, &Event{
		ID:       "ev_done",
		ThreadID: child.ID,
		Type:     "turn_end",
		Payload:  []byte("last result"),
		TS:       time.Now(),
	})

	toolset := &TaskTool{
		Host:      host,
		ThreadID:  parent.ID,
		SessionID: parent.SessionID,
		CloseTaskObserver: func(events []*Event) string {
			if len(events) != 1 {
				t.Fatalf("len(events) = %d, want 1", len(events))
			}
			return "closed with " + string(events[0].Payload)
		},
	}

	out := invokeTaskTool(t, toolset.newCloseTaskTool(), `{"target":"`+child.ID+`","reason":"done"}`)
	if out.Errmsg != "" {
		t.Fatalf("close errmsg = %q", out.Errmsg)
	}
	var result string
	if err := json.Unmarshal(out.Data, &result); err != nil {
		t.Fatalf("unmarshal close result: %v", err)
	}
	if result != "closed with last result" {
		t.Fatalf("close result = %q", result)
	}
	if host.lastCloseThread.ThreadID != child.ID || host.lastCloseThread.Reason != "done" {
		t.Fatalf("close request = %+v", host.lastCloseThread)
	}
	if !host.closed[child.ID] {
		t.Fatalf("child thread was not closed")
	}
}

func TestTaskToolCloseTaskDefaultsToThreadClosed(t *testing.T) {
	host := newFakeHost()
	parent := host.mustCreateRootThread("sess_1", "main")
	child := host.mustCreateChildThread(parent.ID, "child")
	toolset := &TaskTool{Host: host, ThreadID: parent.ID, SessionID: parent.SessionID}

	out := invokeTaskTool(t, toolset.newCloseTaskTool(), `{"target":"`+child.ID+`"}`)
	if out.Errmsg != "" {
		t.Fatalf("close errmsg = %q", out.Errmsg)
	}
	var result string
	if err := json.Unmarshal(out.Data, &result); err != nil {
		t.Fatalf("unmarshal close result: %v", err)
	}
	if result != "thread closed" {
		t.Fatalf("close result = %q", result)
	}
}

type fakeHost struct {
	threads          map[string]*Thread
	events           map[string][]*Event
	closed           map[string]bool
	createCount      int
	sendCount        int
	closeCount       int
	listCount        int
	lastCreateThread CreateThreadRequest
	lastSendMessage  SendMessageRequest
	lastCloseThread  CloseThreadRequest
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		threads: make(map[string]*Thread),
		events:  make(map[string][]*Event),
		closed:  make(map[string]bool),
	}
}

func (h *fakeHost) mustCreateRootThread(sessionID, title string) *Thread {
	thread, err := h.CreateThread(context.Background(), CreateThreadRequest{SessionID: sessionID, Title: title})
	if err != nil {
		panic(err)
	}
	return thread
}

func (h *fakeHost) mustCreateChildThread(parentID, title string) *Thread {
	parent := h.threads[parentID]
	thread, err := h.CreateThread(context.Background(), CreateThreadRequest{
		SessionID:      parent.SessionID,
		ParentThreadID: parentID,
		Title:          title,
	})
	if err != nil {
		panic(err)
	}
	return thread
}

func (h *fakeHost) CreateThread(ctx context.Context, req CreateThreadRequest) (*Thread, error) {
	if req.SessionID == "" {
		return nil, context.Canceled
	}
	h.createCount++
	h.lastCreateThread = req
	id := fmt.Sprintf("thread_%d", h.createCount)
	thread := &Thread{
		ID:        id,
		SessionID: req.SessionID,
		Title:     req.Title,
		Profile:   req.Profile,
		Metadata:  gmap.Clone(req.Metadata),
	}
	h.threads[id] = thread
	if req.InitialMessage != nil {
		thread.InitialMessageID = req.InitialMessage.ID
	}
	return thread, nil
}

func (h *fakeHost) SendMessage(ctx context.Context, req SendMessageRequest) (*Message, error) {
	if _, ok := h.threads[req.ThreadID]; !ok {
		return nil, context.Canceled
	}
	h.sendCount++
	h.lastSendMessage = req
	return req.Message, nil
}

func (h *fakeHost) CloseThread(ctx context.Context, req CloseThreadRequest) (*ClosedThreadRsp, error) {
	if _, ok := h.threads[req.ThreadID]; !ok {
		return nil, context.Canceled
	}
	h.closeCount++
	h.lastCloseThread = req
	h.closed[req.ThreadID] = true
	return &ClosedThreadRsp{}, nil
}

func (h *fakeHost) ListEvents(ctx context.Context, req ListEventsRequest) ([]*Event, error) {
	h.listCount++
	items := h.events[req.ThreadID]
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]*Event, len(items))
	copy(out, items)
	if req.Reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if req.Limit > 0 && len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}

func (h *fakeHost) appendEvent(threadID string, ev *Event) {
	h.events[threadID] = append(h.events[threadID], ev)
}

type fakeNoopHost struct{}

func (fakeNoopHost) CreateThread(ctx context.Context, req CreateThreadRequest) (*Thread, error) {
	return nil, nil
}
func (fakeNoopHost) SendMessage(ctx context.Context, req SendMessageRequest) (*Message, error) {
	return req.Message, nil
}
func (fakeNoopHost) CloseThread(ctx context.Context, req CloseThreadRequest) (*ClosedThreadRsp, error) {
	return &ClosedThreadRsp{}, nil
}
func (fakeNoopHost) ListEvents(ctx context.Context, req ListEventsRequest) ([]*Event, error) {
	return nil, nil
}

type fakeTaskResultModifier struct {
	sendCalled            bool
	sendMessageID         string
	spawnCalled           bool
	spawnThreadID         string
	spawnInitialMessageID string
}

func (m *fakeTaskResultModifier) ModifySendMessageResult(ctx context.Context, result *TaskToolSendMessageResult, message *Message) error {
	m.sendCalled = true
	if message != nil {
		m.sendMessageID = message.ID
	}
	result.Extra = map[string]string{"kind": "send"}
	return nil
}

func (m *fakeTaskResultModifier) ModifySpawnTaskResult(ctx context.Context, result *TaskToolSpawnTaskResult, thread *Thread, initialMessage *Message) error {
	m.spawnCalled = true
	if thread != nil {
		m.spawnThreadID = thread.ID
	}
	if initialMessage != nil {
		m.spawnInitialMessageID = initialMessage.ID
	}
	result.Target = "modified-" + result.Target
	result.Extra = map[string]string{"kind": "spawn"}
	return nil
}

type taskToolOutput struct {
	Data   json.RawMessage `json:"data"`
	Errmsg string          `json:"errmsg"`
}

func invokeTaskTool(t *testing.T, baseTool tool.BaseTool, args string) taskToolOutput {
	t.Helper()
	invokable := baseTool.(tool.InvokableTool)
	out, err := invokable.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	var parsed taskToolOutput
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	return parsed
}

func extractTaskToolMessageID(t *testing.T, data json.RawMessage) string {
	t.Helper()
	var payload struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal message id: %v", err)
	}
	return payload.MessageID
}
