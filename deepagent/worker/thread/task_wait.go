//go:build !windows

package thread

import (
	"encoding/json"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	"eino-cli/deepagent/worker/tasktool"
)

// TaskMessageWaitObserver interprets CloudAgent stream events for tasktool
// wait_message. CloudAgent owns only protocol decoding; tasktool owns the shared
// completion-state semantics.
func TaskMessageWaitObserver(events []*tasktool.Event, messageID string) tasktool.MessageWaitResult {
	observations := make([]tasktool.MessageObservation, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		streamEvent := taskStreamEvent(event)
		observation := tasktool.MessageObservation{
			MessageIDs: taskStreamEventMessageIDs(streamEvent),
			TurnID:     event.TurnID,
			ObservedAt: event.TS,
		}
		recognized := true
		switch event.Type {
		case protoevent.EventTypeTurnStarted.String():
			// A correlation-only observation associates the input message with its turn.
		case protoevent.EventTypeAssistantMessage.String():
			observation.Kind = tasktool.MessageObservationResponse
			if streamEvent.Message != nil {
				observation.Result = messagePayloadText(streamEvent.Message)
			}
		case protoevent.EventTypeApprovalRequired.String():
			observation.Kind = tasktool.MessageObservationApprovalRequired
			observation.Result = "子任务已暂停，正在等待人工审批。"
		case protoevent.EventTypePlanInputRequired.String():
			observation.Kind = tasktool.MessageObservationFollowupRequired
			observation.Result = "子任务已暂停，正在等待补充输入。"
		case protoevent.EventTypeTurnInterrupted.String():
			observation.Kind = tasktool.MessageObservationInterrupted
			observation.Result = "子任务已被中断。"
		case protoevent.EventTypeError.String():
			observation.Kind = tasktool.MessageObservationFailed
			if streamEvent.Error != nil {
				observation.Result = streamEvent.Error.Message
			}
		case protoevent.EventTypeTurnFinished.String():
			observation.Kind = tasktool.MessageObservationCompleted
		default:
			recognized = false
		}
		if recognized {
			observations = append(observations, observation)
		}
	}
	return tasktool.ObserveMessageCompletion(observations, messageID)
}

type taskStreamPayload struct {
	Message            *protoevent.MessageEventPayload
	Approval           *protoevent.ApprovalRequiredEventPayload
	Error              *protoevent.ErrorEventPayload
	PlanInputRequired  *protoevent.PlanInputRequiredEventPayload
	TurnFinished       *protoevent.TurnFinishedEventPayload
	ConsumedMessageIDs []string
}

func taskStreamEvent(event *tasktool.Event) *taskStreamPayload {
	out := &taskStreamPayload{}
	if event == nil {
		return out
	}
	switch event.Type {
	case protoevent.EventTypeTurnStarted.String(), protoevent.EventTypeAssistantMessage.String():
		var payload protoevent.MessageEventPayload
		_ = json.Unmarshal(event.Payload, &payload)
		out.Message = &payload
		out.ConsumedMessageIDs = payload.ConsumedMessageIDs
	case protoevent.EventTypeApprovalRequired.String():
		var payload protoevent.ApprovalRequiredEventPayload
		_ = json.Unmarshal(event.Payload, &payload)
		out.Approval = &payload
		out.ConsumedMessageIDs = payload.ConsumedMessageIDs
	case protoevent.EventTypePlanInputRequired.String():
		var payload protoevent.PlanInputRequiredEventPayload
		_ = json.Unmarshal(event.Payload, &payload)
		out.PlanInputRequired = &payload
		out.ConsumedMessageIDs = payload.ConsumedMessageIDs
	case protoevent.EventTypeError.String(), protoevent.EventTypeTurnInterrupted.String():
		var payload protoevent.ErrorEventPayload
		_ = json.Unmarshal(event.Payload, &payload)
		out.Error = &payload
		out.ConsumedMessageIDs = payload.ConsumedMessageIDs
	case protoevent.EventTypeTurnFinished.String():
		var payload protoevent.TurnFinishedEventPayload
		_ = json.Unmarshal(event.Payload, &payload)
		out.TurnFinished = &payload
		out.ConsumedMessageIDs = payload.ConsumedMessageIDs
	}
	return out
}

func taskStreamEventMessageIDs(event *taskStreamPayload) []string {
	if event == nil {
		return nil
	}
	if len(event.ConsumedMessageIDs) > 0 {
		return event.ConsumedMessageIDs
	}
	if event.Message != nil && event.Message.MessageID != nil && *event.Message.MessageID != "" {
		return []string{*event.Message.MessageID}
	}
	return nil
}

func messagePayloadText(message *protoevent.MessageEventPayload) string {
	if message == nil {
		return ""
	}
	for _, part := range message.Parts {
		if part.Type == protoevent.MessagePartTypeText && part.Text != "" {
			return part.Text
		}
	}
	return ""
}
