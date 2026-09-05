//go:build !windows

package thread

import (
	"strings"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	"eino-cli/deepagent/core/agentthread"
	"github.com/cloudwego/eino/schema"
)

func messageEventPayloadFromTurnStart(payload agentthread.TurnStartPayload, consumedInputs []*schema.Message) *protoevent.MessageEventPayload {
	input := payload.Input
	if input == nil && len(consumedInputs) > 0 {
		input = consumedInputs[0]
	}
	parts := schemaUserMessageToCloudParts(input)
	attr := attributeFromConsumedInputs(consumedInputs)
	if len(parts) == 0 && attr.empty() {
		return nil
	}
	event := &protoevent.MessageEventPayload{
		Parts:     parts,
		MessageID: stringPtrIfNotEmpty(attr.MessageID),
	}
	if attr.SenderID != "" || attr.SenderType != "" {
		event.Sender = &protoevent.Sender{
			SenderType: senderTypeFromString(attr.SenderType),
			SenderID:   attr.SenderID,
		}
	}
	return event
}

func textParts(content string) []protoevent.MessagePart {
	if content == "" {
		return nil
	}
	return []protoevent.MessagePart{{Type: "text", Text: content}}
}

func attributeFromConsumedInputs(inputs []*schema.Message) MessageAttribute {
	for _, input := range inputs {
		attr := attributeFromMessage(input)
		if !attr.empty() {
			return attr
		}
	}
	return MessageAttribute{}
}

func senderTypeFromString(senderType string) protoevent.SenderType {
	switch strings.ToUpper(strings.TrimSpace(senderType)) {
	case "SYSTEM":
		return protoevent.SenderTypeSystem
	case "AGENT":
		return protoevent.SenderTypeAgent
	default:
		return protoevent.SenderTypeUser
	}
}
