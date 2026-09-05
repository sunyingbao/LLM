//go:build !windows

package cloud

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"eino-cli/deepagent/coordinator"
	"eino-cli/deepagent/worker"
)

func messageFromCoordinator(message *coordinator.Message) (result *agentworker.Message) {
	if message == nil {
		return nil
	}
	return &agentworker.Message{
		ID:       fmt.Sprint(message.MessageID),
		Sender:   senderFromCoordinator(message.Sender),
		Type:     agentworker.MessageType(message.MessageType),
		Payload:  append([]byte(nil), message.Payload...),
		Metadata: maps.Clone(message.Metadata),
	}
}

func senderFromCoordinator(sender *coordinator.Sender) (result *agentworker.Sender) {
	if sender == nil {
		return nil
	}
	return &agentworker.Sender{
		Type: agentworker.SenderType(strings.ToUpper(string(sender.Type))),
		ID:   sender.ID,
	}
}

func eventToCoordinator(threadID int64, event *agentworker.Event) (result *coordinator.Event) {
	if event == nil {
		return nil
	}
	coordinatorEvent := &coordinator.Event{
		ThreadID:          threadID,
		TurnID:            event.TurnID,
		EventType:         string(event.Type),
		Payload:           append([]byte(nil), event.Payload...),
		Metadata:          maps.Clone(event.Metadata),
		PersistToEventLog: event.PersistToEventLog,
		FanoutToSession:   event.FanoutToSession,
	}
	if event.ID != "" {
		if id, err := strconv.ParseInt(strings.TrimSpace(event.ID), 10, 64); err == nil {
			coordinatorEvent.EventID = id
		}
	}
	if !event.TS.IsZero() {
		coordinatorEvent.CreatedAt = event.TS
	}
	return coordinatorEvent
}

func releaseStatusFromBlock(block *agentworker.PendingBlock) (status coordinator.ThreadStatus) {
	if block != nil {
		return coordinator.ThreadStatusBlocked
	}
	return ""
}

func parseInt64(value string, name string) (id int64, err error) {
	id, err = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("invalid %s %q", name, value)
	}
	return id, nil
}

func parseCancelInputPayload(payload []byte) (result CancelInputControlPayload, err error) {
	err = json.Unmarshal(payload, &result)
	return result, err
}

func parseCloseThreadPayload(payload []byte) (result CloseThreadControlPayload, err error) {
	err = json.Unmarshal(payload, &result)
	return result, err
}

func coordinatorError(op string, err error) (result error) {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", op, err)
}
