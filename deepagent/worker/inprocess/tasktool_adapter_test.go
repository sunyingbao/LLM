package inprocess_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/inprocess"
	"eino-cli/deepagent/worker/tasktool"
	"github.com/cloudwego/eino/components/tool"
)

func TestTaskToolHostAdapterSpawnAndWait(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)

	worker, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			rt := newFakeRuntime(state.ID)
			rt.postFn = func(msg *agentworker.Message) error {
				go func() {
					rt.emitEvent(&agentworker.Event{
						ID:       "ev_" + msg.ID,
						ThreadID: state.ID,
						Type:     agentworker.EventType("turn_end"),
						Payload:  []byte("done"),
						Metadata: map[string]string{"message_id": msg.ID},
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
	parent, err := worker.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID:    1001,
		SessionID: "sess_1",
		Title:     "main",
		Profile:   inprocess.ThreadProfile{Cwd: "/repo"},
	})
	if err != nil {
		t.Fatalf("CreateThread(parent) error = %v", err)
	}

	toolset := &tasktool.TaskTool{
		Host:              inprocess.TaskToolHostAdapter{Worker: worker},
		ThreadID:          parent.ID,
		SessionID:         parent.SessionID,
		WorkerConcurrency: 2,
		MessageWaitObserver: func(events []*tasktool.Event, messageID string) tasktool.MessageWaitResult {
			for _, ev := range events {
				if ev.Metadata["message_id"] == messageID {
					return tasktool.MessageWaitResult{Done: true, Result: string(ev.Payload)}
				}
			}
			return tasktool.MessageWaitResult{}
		},
	}

	spawnOut := invokeGenericTaskTool(t, toolset.Tools()[1], `{"title":"child","role":"reviewer","content":"child work"}`)
	if spawnOut.Errmsg != "" {
		t.Fatalf("spawn errmsg = %q", spawnOut.Errmsg)
	}
	if !strings.Contains(string(spawnOut.Data), `"target":"`) {
		t.Fatalf("spawn output = %s", spawnOut.Data)
	}
	childTarget := extractTaskToolTarget(t, spawnOut.Data)
	childThread, err := worker.GetThread(ctx, childTarget)
	if err != nil {
		t.Fatalf("GetThread(child) error = %v", err)
	}
	if childThread.UserID != parent.UserID {
		t.Fatalf("child.UserID = %d, want %d", childThread.UserID, parent.UserID)
	}
	if childThread.Profile.Role != "reviewer" {
		t.Fatalf("child.Profile.Role = %q, want reviewer", childThread.Profile.Role)
	}
	if childThread.Profile.Cwd != "/repo" {
		t.Fatalf("child.Profile.Cwd = %q, want /repo", childThread.Profile.Cwd)
	}

	sendOut := invokeGenericTaskTool(t, toolset.Tools()[0], `{"target":"`+childTarget+`","content":"hello"}`)
	if sendOut.Errmsg != "" {
		t.Fatalf("send errmsg = %q", sendOut.Errmsg)
	}
	msgID := extractTaskToolMessageIDFromGeneric(t, sendOut.Data)

	waitOut := invokeGenericTaskTool(t, toolset.Tools()[2], `{"target":"`+childTarget+`","message_id":"`+msgID+`"}`)
	if waitOut.Errmsg != "" {
		t.Fatalf("wait errmsg = %q", waitOut.Errmsg)
	}
	if !strings.Contains(string(waitOut.Data), `"result":"done"`) {
		t.Fatalf("wait output = %s", waitOut.Data)
	}

	closeOut := invokeGenericTaskTool(t, toolset.Tools()[3], `{"target":"`+childTarget+`","reason":"done"}`)
	if closeOut.Errmsg != "" {
		t.Fatalf("close errmsg = %q", closeOut.Errmsg)
	}
	var closeResult string
	if err := json.Unmarshal(closeOut.Data, &closeResult); err != nil {
		t.Fatalf("unmarshal close result: %v", err)
	}
	if closeResult != "thread closed" {
		t.Fatalf("close result = %q", closeResult)
	}
	closedChild, err := worker.GetThread(ctx, childTarget)
	if err != nil {
		t.Fatalf("GetThread(closed child) error = %v", err)
	}
	if closedChild.ClosedAt == nil {
		t.Fatalf("child thread was not closed")
	}
	sendAfterClose := invokeGenericTaskTool(t, toolset.Tools()[0], `{"target":"`+childTarget+`","content":"after close"}`)
	if !strings.Contains(sendAfterClose.Errmsg, "thread is closed") {
		t.Fatalf("send after close errmsg = %q", sendAfterClose.Errmsg)
	}
}

func TestTaskToolHostAdapterUsesDefaultUserIDForRootThread(t *testing.T) {
	ctx := context.Background()
	threadStates, eventStore := openSQLiteStores(t)

	worker, err := inprocess.NewWorker(inprocess.Dependencies{
		ThreadStateStore: threadStates,
		EventStore:       eventStore,
		ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
			return newFakeRuntime(state.ID), nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}

	created, err := (inprocess.TaskToolHostAdapter{Worker: worker, UserID: 2002}).CreateThread(ctx, tasktool.CreateThreadRequest{
		SessionID: "sess_2",
		Title:     "root",
		Profile:   tasktool.ThreadProfile{Role: "explorer", Cwd: "/repo"},
	})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if created.UserID != 2002 {
		t.Fatalf("created.UserID = %d, want 2002", created.UserID)
	}
	if created.Profile.Role != "explorer" {
		t.Fatalf("created.Profile.Role = %q, want explorer", created.Profile.Role)
	}
	if created.Profile.Cwd != "/repo" {
		t.Fatalf("created.Profile.Cwd = %q, want /repo", created.Profile.Cwd)
	}
}

type genericTaskToolOutput struct {
	Data   json.RawMessage `json:"data"`
	Errmsg string          `json:"errmsg"`
}

func invokeGenericTaskTool(t *testing.T, baseTool tool.BaseTool, args string) genericTaskToolOutput {
	t.Helper()
	invokable := baseTool.(tool.InvokableTool)
	out, err := invokable.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	var parsed genericTaskToolOutput
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	return parsed
}

func extractTaskToolTarget(t *testing.T, data json.RawMessage) string {
	t.Helper()
	var payload struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal target: %v", err)
	}
	return payload.Target
}

func extractTaskToolMessageIDFromGeneric(t *testing.T, data json.RawMessage) string {
	t.Helper()
	var payload struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal message id: %v", err)
	}
	return payload.MessageID
}
