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
