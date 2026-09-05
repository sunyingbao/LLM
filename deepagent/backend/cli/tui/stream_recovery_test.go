package tui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"eino-cli/deepagent/cloud/protocol/timeline"
	runtimeRun "eino-cli/deepagent/host/run"
	clientruntime "eino-cli/deepagent/host/runtime"
	tea "github.com/charmbracelet/bubbletea"
)

func TestTimelineRestoresFullMessagesAfterPartialDeltas(t *testing.T) {
	events := make(chan timeline.Event, 4)
	events <- timeline.Event{TurnID: "turn", EventType: "ASSISTANT_DELTA", Payload: json.RawMessage(`{"delta":"AC","llm_response_id":"response-1"}`)}
	events <- timeline.Event{TurnID: "turn", EventType: "ASSISTANT_MESSAGE", Payload: json.RawMessage(`{"parts":[{"type":"text","text":"ABC"}],"llm_response_id":"response-1"}`)}
	events <- timeline.Event{TurnID: "turn", EventType: "ASSISTANT_MESSAGE", Payload: json.RawMessage(`{"parts":[{"type":"text","text":"DEF"}],"llm_response_id":"response-2"}`)}
	events <- timeline.Event{TurnID: "turn", EventType: "TURN_FINISHED"}
	close(events)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	messages := make(chan tea.Msg, 16)
	active := streamRun{ctx: ctx, cancel: cancel, complete: func(runtimeRun.Status, string, error) {}, snapshot: func() {}}
	consumeTimeline(&remoteStubRuntime{}, active, &clientruntime.TurnStream{TurnID: "turn", Events: events}, messages, nil)
	var done doneMsg
	for message := range messages {
		if result, ok := message.(doneMsg); ok {
			done = result
		}
	}
	if done.err != nil || done.output != "ABCDEF" {
		t.Fatalf("completed output = %+v", done)
	}
	if ctx.Err() == nil {
		t.Fatal("completed stream left prompt context active")
	}
}

func TestDetachedTimelineDoesNotBlockOnAbandonedUI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	messages := make(chan tea.Msg)
	events := make(chan timeline.Event)
	finished := make(chan struct{})
	active := streamRun{ctx: ctx, cancel: cancel, complete: func(runtimeRun.Status, string, error) {}, snapshot: func() {}}
	t.Cleanup(func() {
		cancel()
		for range messages {
		}
	})
	go func() {
		consumeTimeline(&remoteStubRuntime{}, active, &clientruntime.TurnStream{TurnID: "turn", Events: events}, messages, nil)
		close(finished)
	}()
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("detached stream blocked sending to an abandoned UI")
	}
}

func TestModelProgressDismissesPromptsWithoutAnswering(t *testing.T) {
	for _, message := range []tea.Msg{chunkMsg("continued"), streamOutputMsg("recovered")} {
		model, err := New(&remoteStubRuntime{}, "")
		if err != nil {
			t.Fatal(err)
		}
		approvalReply := make(chan bool, 1)
		questionReply := make(chan map[string][]string, 1)
		model.hitlQueue = []approvalRequest{{reply: approvalReply}}
		model.questionQueue = []questionRequest{{reply: questionReply}}
		_, _ = model.Update(message)
		if len(model.hitlQueue) != 0 || len(model.questionQueue) != 0 {
			t.Fatal("server model progress left an already answered prompt visible")
		}
		if len(approvalReply) != 0 || len(questionReply) != 0 {
			t.Fatal("dismissing a stale prompt submitted an answer")
		}
	}
}
