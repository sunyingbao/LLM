package tui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
	clientruntime "eino-cli/deepagent/host/runtime"
	sdkruntime "eino-cli/deepagent/runtime"
	tea "github.com/charmbracelet/bubbletea"
)

type resumeRecordingRuntime struct {
	stubRuntime
	requests chan protoinput.ResumeTurnPayload
}

func (runtime *resumeRecordingRuntime) Resume(ctx context.Context, ref sdkruntime.GlobalThreadRef, payload protoinput.ResumeTurnPayload) (err error) {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case runtime.requests <- payload:
		return nil
	}
}

func TestApprovalTimelineDecisionResumesUnifiedTurn(t *testing.T) {
	arguments := `{"command":"pwd"}`
	raw, err := json.Marshal(protoevent.ApprovalRequiredEventPayload{
		InterruptID: "interrupt-1", CheckpointID: "checkpoint-1", ToolName: "execute", ArgumentsJSON: &arguments,
	})
	if err != nil {
		t.Fatalf("marshal approval payload: %v", err)
	}
	runtime := &resumeRecordingRuntime{requests: make(chan protoinput.ResumeTurnPayload, 1)}
	stream := &clientruntime.TurnStream{Ref: sdkruntime.GlobalThreadRef{Runtime: sdkruntime.RuntimeLocal, ThreadID: "thread-1"}, TurnID: "turn-1"}
	messages := make(chan tea.Msg, 1)
	results := make(chan resumeResult, 1)
	requestApprovalResume(context.Background(), runtime, stream, timeline.Event{
		TurnID: "turn-1", EventType: protoevent.EventTypeApprovalRequired.String(), Payload: raw,
	}, messages, results)

	request := (<-messages).(approvalRequest)
	request.reply <- true
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf("resume result error=%v", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("approval did not resume turn")
	}
	payload := <-runtime.requests
	if payload.TurnID != "turn-1" || payload.CheckpointID != "checkpoint-1" || payload.InterruptID != "interrupt-1" || payload.Approval == nil || !payload.Approval.Approved {
		t.Fatalf("resume payload=%+v", payload)
	}
}

func TestTurnStreamLocksRemoteTurnFromFirstTurnStarted(t *testing.T) {
	stream := &clientruntime.TurnStream{}
	if stream.AcceptEvent(timeline.Event{TurnID: "old", EventType: protoevent.EventTypeAssistantMessage.String()}) {
		t.Fatal("accepted event before remote turn started")
	}
	if !stream.AcceptEvent(timeline.Event{TurnID: "turn-remote", EventType: protoevent.EventTypeTurnStarted.String()}) {
		t.Fatal("did not accept first remote turn start")
	}
	if !stream.AcceptEvent(timeline.Event{TurnID: "turn-remote", EventType: protoevent.EventTypeAssistantDelta.String()}) {
		t.Fatal("did not accept event from locked remote turn")
	}
	if stream.AcceptEvent(timeline.Event{TurnID: "other", EventType: protoevent.EventTypeTurnStarted.String()}) {
		t.Fatal("accepted event from another turn")
	}
}

func TestPlanInputTimelineReturnsAnswersUnderActualQuestionIDs(t *testing.T) {
	raw, err := json.Marshal(protoevent.PlanInputRequiredEventPayload{
		InterruptID: "interrupt-plan", CheckpointID: "checkpoint-plan",
		ConsumedMessageIDs: []string{"message-1"},
		Questions: []*protoevent.PlanInputQuestion{
			{ID: "scope", Header: "Scope", Question: "What should change?", Options: []*protoevent.PlanInputQuestionOption{{Label: "SDK only", Description: "Keep product stable"}}},
			{ID: "validation", Header: "Validation", Question: "Which checks?", Options: []*protoevent.PlanInputQuestionOption{{Label: "Focused", Description: "Run package tests"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &resumeRecordingRuntime{requests: make(chan protoinput.ResumeTurnPayload, 1)}
	stream := &clientruntime.TurnStream{Ref: sdkruntime.GlobalThreadRef{Runtime: sdkruntime.RuntimeRemote, ThreadID: "thread-1"}, TurnID: "turn-1"}
	messages := make(chan tea.Msg, 1)
	results := make(chan resumeResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	requestPlanInputResume(ctx, runtime, stream, timeline.Event{TurnID: "turn-1", EventType: protoevent.EventTypePlanInputRequired.String(), Payload: raw}, messages, results)

	request := (<-messages).(questionRequest)
	request.reply <- map[string][]string{"scope": {"SDK only"}, "validation": {"Focused"}}
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-ctx.Done():
		t.Fatal("plan answer did not resume")
	}
	payload := <-runtime.requests
	if payload.RequestUserInput == nil || payload.RequestUserInput.Answers["scope"].Answers[0] != "SDK only" || payload.RequestUserInput.Answers["validation"].Answers[0] != "Focused" {
		t.Fatalf("request_user_input = %+v", payload.RequestUserInput)
	}
	if len(payload.ConsumedMessageIDs) != 1 || payload.ConsumedMessageIDs[0] != "message-1" {
		t.Fatalf("consumed IDs = %v", payload.ConsumedMessageIDs)
	}
}

func TestFollowUpTimelineUsesExactInterruptSchema(t *testing.T) {
	raw, err := json.Marshal(protoevent.InterruptRequiredEventPayload{
		InterruptID: "interrupt-follow", CheckpointID: "checkpoint-follow", Kind: "follow_up",
		Info: json.RawMessage(`{"questions":["Which account?"]}`), ConsumedMessageIDs: []string{"message-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &resumeRecordingRuntime{requests: make(chan protoinput.ResumeTurnPayload, 1)}
	stream := &clientruntime.TurnStream{Ref: sdkruntime.GlobalThreadRef{Runtime: sdkruntime.RuntimeRemote, ThreadID: "thread-1"}, TurnID: "turn-1"}
	messages := make(chan tea.Msg, 1)
	results := make(chan resumeResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	requestInterruptResume(ctx, runtime, stream, timeline.Event{TurnID: "turn-1", EventType: protoevent.EventTypeInterruptRequired.String(), Payload: raw}, messages, results)

	request := (<-messages).(questionRequest)
	request.reply <- map[string][]string{"answer": {"production"}}
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-ctx.Done():
		t.Fatal("follow-up answer did not resume")
	}
	payload := <-runtime.requests
	if payload.Interrupt == nil || payload.Interrupt.Kind != "follow_up" || string(payload.Interrupt.Data) != `{"user_answer":"production"}` {
		t.Fatalf("interrupt = %+v", payload.Interrupt)
	}
	if len(payload.ConsumedMessageIDs) != 1 || payload.ConsumedMessageIDs[0] != "message-2" {
		t.Fatalf("consumed IDs = %v", payload.ConsumedMessageIDs)
	}
}
