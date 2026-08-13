//go:build !windows

package thread

import (
	"encoding/json"
	"testing"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	"eino-cli/deepagent/worker/tasktool"
)

func mustTaskStreamPayload(t *testing.T, payload any) []byte {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return data
}

func TestTaskMessageWaitObserverReportsApprovalAsNonTerminal(t *testing.T) {
	result := TaskMessageWaitObserver([]*tasktool.Event{
		{
			Type: protoevent.EventTypeAssistantMessage.String(),
			Payload: mustTaskStreamPayload(t, &protoevent.MessageEventPayload{
				Parts: []protoevent.MessagePart{{Type: protoevent.MessagePartTypeText, Text: "before approval"}},
			}),
		},
		{
			Type: protoevent.EventTypeApprovalRequired.String(),
			Payload: mustTaskStreamPayload(t, &protoevent.ApprovalRequiredEventPayload{
				ConsumedMessageIDs: []string{"123"},
				ToolName:           "execute",
			}),
		},
	}, "123")

	if result.Done {
		t.Fatalf("approval request should not complete wait_message: %+v", result)
	}
	if result.State != tasktool.WaitMessageStateApprovalRequired {
		t.Fatalf("state = %q", result.State)
	}
	if result.Result != "子任务已暂停，正在等待人工审批。" {
		t.Fatalf("result = %q", result.Result)
	}
}

func TestTaskMessageWaitObserverTerminalEventOverridesEarlierApproval(t *testing.T) {
	result := TaskMessageWaitObserver([]*tasktool.Event{
		{
			TurnID: "turn_target",
			Type:   protoevent.EventTypeApprovalRequired.String(),
			Payload: mustTaskStreamPayload(t, &protoevent.ApprovalRequiredEventPayload{
				ConsumedMessageIDs: []string{"123"},
				ToolName:           "execute",
			}),
		},
		{
			TurnID: "turn_target",
			Type:   protoevent.EventTypeError.String(),
			Payload: mustTaskStreamPayload(t, &protoevent.ErrorEventPayload{
				ConsumedMessageIDs: []string{"123"},
				Message:            "resume failed",
			}),
		},
	}, "123")

	if !result.Done {
		t.Fatalf("terminal error should complete wait_message: %+v", result)
	}
	if result.State != tasktool.WaitMessageStateFailed {
		t.Fatalf("state = %q", result.State)
	}
	if result.Result != "resume failed" {
		t.Fatalf("result = %q", result.Result)
	}
}

func TestTaskMessageWaitObserverUsesTargetTurnAfterMessageMatch(t *testing.T) {
	result := TaskMessageWaitObserver([]*tasktool.Event{
		{
			TurnID: "turn_target",
			Type:   protoevent.EventTypeTurnStarted.String(),
			Payload: mustTaskStreamPayload(t, &protoevent.MessageEventPayload{
				ConsumedMessageIDs: []string{"123"},
			}),
		},
		{
			TurnID: "turn_target",
			Type:   protoevent.EventTypeAssistantMessage.String(),
			Payload: mustTaskStreamPayload(t, &protoevent.MessageEventPayload{
				Parts: []protoevent.MessagePart{{Type: protoevent.MessagePartTypeText, Text: "child finished"}},
			}),
		},
		{
			TurnID:  "turn_target",
			Type:    protoevent.EventTypeTurnFinished.String(),
			Payload: mustTaskStreamPayload(t, &protoevent.TurnFinishedEventPayload{}),
		},
	}, "123")

	if !result.Done {
		t.Fatalf("target turn should complete wait_message: %+v", result)
	}
	if result.State != tasktool.WaitMessageStateCompleted {
		t.Fatalf("state = %q", result.State)
	}
	if result.Result != "child finished" {
		t.Fatalf("result = %q", result.Result)
	}
}
