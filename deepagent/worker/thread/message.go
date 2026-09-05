//go:build !windows

package thread

import (
	"encoding/json"
	"strconv"
	"strings"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/worker"
	"github.com/cloudwego/eino/schema"
)

const (
	MessageTypeInput      agentworker.MessageType = agentworker.MessageType(protoinput.MessageTypeInput)
	MessageTypeResumeTurn agentworker.MessageType = agentworker.MessageType(protoinput.MessageTypeResume)
	MessageTypeCompact    agentworker.MessageType = agentworker.MessageType(protoinput.MessageTypeCompact)

	MetadataTurnMode = protoinput.MetadataTurnMode
	TurnModePlan     = protoinput.TurnModePlan

	einoMessageAttributeExtraKey = "__cloudagent_message_attribute__"
	legacyMessageIDExtraKey      = "message_id"
)

type MessageAttribute struct {
	MessageID  string `json:"message_id,omitempty"`
	SenderID   string `json:"sender_id,omitempty"`
	SenderType string `json:"sender_type,omitempty"`
}

func init() {
	// MessageAttribute is stored in schema.Message.Extra. Eino checkpoint
	// serialization needs a stable name to restore custom Extra values.
	schema.RegisterName[MessageAttribute]("_cloudagent_message_attribute")
	schema.RegisterName[cloudInputParts]("_cloudagent_input_parts")
	schema.RegisterName[protoinput.MessagePart]("_cloudagent_input_message_part")
}

func attributeFromWorkerMessage(message *agentworker.Message) MessageAttribute {
	if message == nil {
		return MessageAttribute{}
	}
	attr := MessageAttribute{MessageID: strings.TrimSpace(message.ID)}
	if message.Sender != nil {
		attr.SenderID = strings.TrimSpace(message.Sender.ID)
		attr.SenderType = strings.TrimSpace(string(message.Sender.Type))
	}
	return attr
}

func attachAttribute(msg *schema.Message, attr MessageAttribute) {
	if msg == nil || attr.empty() {
		return
	}
	if msg.Extra == nil {
		msg.Extra = make(map[string]any, 1)
	}
	msg.Extra[einoMessageAttributeExtraKey] = attr
}

func attributeFromMessage(msg *schema.Message) MessageAttribute {
	if msg == nil || msg.Extra == nil {
		return MessageAttribute{}
	}
	if attr, ok := attributeFromExtraValue(msg.Extra[einoMessageAttributeExtraKey]); ok {
		return attr
	}
	if messageID, ok := stringIDFromAny(msg.Extra[legacyMessageIDExtraKey]); ok {
		return MessageAttribute{MessageID: messageID}
	}
	return MessageAttribute{}
}

func ConsumedMessageIDs(inputs []*schema.Message) []string {
	if len(inputs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(inputs))
	for _, input := range inputs {
		attr := attributeFromMessage(input)
		if attr.MessageID != "" {
			ids = append(ids, attr.MessageID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func attributeFromExtraValue(raw any) (MessageAttribute, bool) {
	switch v := raw.(type) {
	case MessageAttribute:
		attr := v.normalized()
		return attr, !attr.empty()
	case *MessageAttribute:
		if v == nil {
			return MessageAttribute{}, false
		}
		attr := v.normalized()
		return attr, !attr.empty()
	case map[string]any:
		attr := MessageAttribute{}
		if messageID, ok := stringIDFromAny(v["message_id"]); ok {
			attr.MessageID = messageID
		}
		if senderID, ok := stringIDFromAny(v["sender_id"]); ok {
			attr.SenderID = senderID
		}
		if senderType, ok := stringIDFromAny(v["sender_type"]); ok {
			attr.SenderType = senderType
		}
		attr = attr.normalized()
		return attr, !attr.empty()
	default:
		return MessageAttribute{}, false
	}
}

func (a MessageAttribute) normalized() MessageAttribute {
	return MessageAttribute{
		MessageID:  strings.TrimSpace(a.MessageID),
		SenderID:   strings.TrimSpace(a.SenderID),
		SenderType: strings.TrimSpace(a.SenderType),
	}
}

func (a MessageAttribute) empty() bool {
	a = a.normalized()
	return a.MessageID == "" && a.SenderID == "" && a.SenderType == ""
}

func stringIDFromAny(raw any) (string, bool) {
	switch v := raw.(type) {
	case string:
		id := strings.TrimSpace(v)
		return id, id != ""
	case int64:
		if v == 0 {
			return "", false
		}
		return strconv.FormatInt(v, 10), true
	case int:
		if v == 0 {
			return "", false
		}
		return strconv.FormatInt(int64(v), 10), true
	case int32:
		if v == 0 {
			return "", false
		}
		return strconv.FormatInt(int64(v), 10), true
	case float64:
		if v == 0 {
			return "", false
		}
		return strconv.FormatInt(int64(v), 10), true
	case json.Number:
		if n, err := v.Int64(); err == nil && n != 0 {
			return strconv.FormatInt(n, 10), true
		}
		return "", false
	default:
		return "", false
	}
}
