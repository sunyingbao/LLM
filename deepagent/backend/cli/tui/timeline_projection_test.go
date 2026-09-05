package tui

import (
	"encoding/json"
	"testing"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	"eino-cli/deepagent/cloud/protocol/timeline"
	sdkruntime "eino-cli/deepagent/runtime"
)

func TestRuntimeDisplayNameIncludesRuntimeKind(t *testing.T) {
	if got := runtimeDisplayName("deepseek", sdkruntime.RuntimeLocal); got != "deepseek [local]" {
		t.Fatalf("runtimeDisplayName()=%q", got)
	}
}

func TestApplyTimelineEventProjectsToolLifecycle(t *testing.T) {
	arguments := `{"command":"pwd"}`
	delta := "/tmp\n"
	result := `{"exit_code":0}`
	model := timelineProjectionModel()

	applyTestTimelineEvent(t, model, protoevent.EventTypeToolCallStarted, protoevent.ToolCallEventPayload{
		ToolCallID: "call-1", ToolName: "Bash", ArgumentsJSON: &arguments,
	})
	if len(model.toolBlocks) != 1 || model.toolBlocks[0].name != "Bash" {
		t.Fatalf("tool start was not projected: %#v", model.toolBlocks)
	}

	applyTestTimelineEvent(t, model, protoevent.EventTypeToolCallOutputDelta, protoevent.ToolCallEventPayload{
		ToolCallID: "call-1", OutputDelta: &delta,
	})
	if len(model.toolBlocks[0].lines) != 1 || model.toolBlocks[0].lines[0] != "/tmp" {
		t.Fatalf("tool output delta was not projected: %#v", model.toolBlocks[0].lines)
	}

	applyTestTimelineEvent(t, model, protoevent.EventTypeToolCallFinished, protoevent.ToolCallEventPayload{
		ToolCallID: "call-1", ResultJSON: &result,
	})
	if model.toolBlocks[0].lines[0] != result {
		t.Fatalf("tool result was not projected: %#v", model.toolBlocks[0].lines)
	}
	if _, exists := model.toolBlocksByCall["call-1"]; exists {
		t.Fatal("finished tool call remained in active lookup")
	}
}

func TestApplyTimelineEventProjectsPlanAndTerminalState(t *testing.T) {
	model := timelineProjectionModel()
	applyTestTimelineEvent(t, model, protoevent.EventTypePlanUpdated, protoevent.PlanUpdatedEventPayload{Items: []*protoevent.PlanItem{
		{ID: "1", Content: "inspect", Status: "completed"},
		{ID: "2", Content: "implement", Status: "in_progress"},
	}})
	if len(model.todos) != 2 || model.todos[1].Content != "implement" || model.todos[1].Status != "in_progress" {
		t.Fatalf("plan was not projected: %#v", model.todos)
	}

	applyTestTimelineEvent(t, model, protoevent.EventTypeTurnInterrupted, struct{}{})
	if !model.interrupted {
		t.Fatal("turn interruption was not projected")
	}

	applyTestTimelineEvent(t, model, protoevent.EventTypeError, protoevent.ErrorEventPayload{Message: "runtime failed"})
	if model.lastErr == nil || model.lastErr.Error() != "runtime failed" {
		t.Fatalf("runtime error was not projected: %v", model.lastErr)
	}
}

func timelineProjectionModel() (model *Model) {
	model = &Model{
		toolBlocksByCall: make(map[string]*toolBlock),
		toolPreviewLines: 5,
		toolArgsMaxChars: 80,
		width:            120,
		height:           40,
	}
	return model
}

func applyTestTimelineEvent(t *testing.T, model *Model, eventType protoevent.EventType, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s payload: %v", eventType, err)
	}
	_, _ = applyTimelineEvent(model, timeline.Event{EventType: eventType.String(), Payload: raw})
}
