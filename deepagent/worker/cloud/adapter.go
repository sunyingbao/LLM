//go:build !windows

package cloud

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	"eino-cli/deepagent/worker"
)

func messageFromAC(message *ac.Message) *agentworker.Message {
	if message == nil {
		return nil
	}
	return &agentworker.Message{
		ID:       fmt.Sprint(message.GetMessageId()),
		Sender:   senderFromAC(message.GetSender()),
		Type:     agentworker.MessageType(message.GetMessageType()),
		Payload:  append([]byte(nil), message.GetPayload()...),
		Metadata: maps.Clone(message.GetMetadata()),
	}
}

func senderFromAC(sender *ac.Sender) *agentworker.Sender {
	if sender == nil {
		return nil
	}
	return &agentworker.Sender{
		Type: agentworker.SenderType(fmt.Sprint(sender.GetSenderType())),
		ID:   sender.GetSenderId(),
	}
}

func eventToAC(threadID int64, event *agentworker.Event) *ac.Event {
	if event == nil {
		return nil
	}
	acEvent := &ac.Event{
		ThreadId:          threadID,
		TurnId:            event.TurnID,
		EventType:         string(event.Type),
		Payload:           append([]byte(nil), event.Payload...),
		Metadata:          maps.Clone(event.Metadata),
		PersistToEventLog: event.PersistToEventLog,
		FanoutToSession:   event.FanoutToSession,
	}
	if event.ID != "" {
		if id, err := strconv.ParseInt(strings.TrimSpace(event.ID), 10, 64); err == nil {
			acEvent.EventId = &id
		}
	}
	if !event.TS.IsZero() {
		ts := event.TS.UnixMilli()
		acEvent.CreatedAtMs = &ts
	}
	return acEvent
}

func releaseStatusFromBlock(block *agentworker.PendingBlock) *ac.ThreadStatus {
	if block != nil {
		status := ac.ThreadStatus_BLOCKED
		return &status
	}
	return nil
}

func parseInt64(value string, name string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("invalid %s %q", name, value)
	}
	return id, nil
}

func timeFromMs(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
