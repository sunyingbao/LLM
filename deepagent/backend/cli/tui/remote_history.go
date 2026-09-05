package tui

import (
	"encoding/json"
	"strings"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	"eino-cli/deepagent/cloud/protocol/timeline"
)

func applyRemoteHistory(model *Model, history []timeline.Event) {
	resetConversationUI(model)
	for _, event := range history {
		if protoevent.EventType(event.EventType) == protoevent.EventTypeAssistantMessage {
			var payload protoevent.MessageEventPayload
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			text := strings.TrimSpace(assistantText(event.Payload))
			if text == "" {
				continue
			}
			role := "assistant"
			if payload.Sender != nil && payload.Sender.SenderType == protoevent.SenderTypeUser {
				role = "user"
			}
			pushMessage(model, role, text)
			continue
		}
		_, _ = applyTimelineEvent(model, event)
	}
	rebuildHistory(model)
}
