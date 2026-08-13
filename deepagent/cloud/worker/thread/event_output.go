//go:build !windows

package thread

import (
	"encoding/json"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/worker"
)

const metadataAgentEventID = "agent_event_id"

func threadOutputItem(sessionID string, threadID string, ev agentthread.Event, usage *agentthread.ContextUsageSnapshot, outputConfig OutputConfig) (*agentworker.ThreadOutputItem, error) {
	event, err := workerEvent(sessionID, threadID, ev, usage, outputConfig)
	if err != nil {
		return nil, err
	}
	yield := yieldFromAgentEvent(ev)
	if event == nil && yield == nil {
		return nil, nil
	}
	return &agentworker.ThreadOutputItem{Event: event, Yield: yield}, nil
}

func workerEvent(_ string, threadID string, ev agentthread.Event, usage *agentthread.ContextUsageSnapshot, outputConfig OutputConfig) (*agentworker.Event, error) {
	if isHiddenInternalToolEvent(ev) {
		return nil, nil
	}

	var fanoutOnly bool
	switch ev.Type {
	case agentthread.EventLLMRequesting:
		return nil, nil
	case agentthread.EventLLMToken,
		agentthread.EventToolStart,
		agentthread.EventToolCallOutputChunk,
		agentthread.EventPlanUpdated:
		fanoutOnly = true
	}

	eventType, eventPayload, err := agentEventPayloadForOutput(ev, usage)
	if err != nil {
		return nil, err
	}
	if eventPayload == nil {
		return nil, nil
	}
	payload, err := json.Marshal(eventPayload)
	if err != nil {
		return nil, err
	}
	event := &agentworker.Event{
		ID:       ev.ID,
		ThreadID: threadID,
		TurnID:   ev.TurnID,
		Type:     agentworker.EventType(eventType.String()),
		Payload:  payload,
		Metadata: map[string]string{metadataAgentEventID: ev.ID},
		TS:       ev.TS,
	}
	if fanoutOnly {
		setEventLogPersist(event, false)
		fanout := true
		event.FanoutToSession = &fanout
	}
	if opt, ok := outputConfig.eventLogOption(eventType.String()); ok {
		setEventLogPersist(event, opt.Persist)
	}
	return event, nil
}

func setEventLogPersist(event *agentworker.Event, persist bool) {
	if event == nil {
		return
	}
	value := persist
	event.PersistToEventLog = &value
}
