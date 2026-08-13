//go:build !windows

package thread

import (
	"fmt"
	"maps"
	"strings"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/worker"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

type userInputCommand struct {
	message *agentworker.Message
	input   protoinput.UserMessage
	schema  *schema.Message
	mode    protoinput.UserMessageMode
}

type resumeTurnCommand struct {
	message *agentworker.Message
	payload protoinput.ResumeTurnPayload
	mode    protoinput.UserMessageMode
}

type compactCommand struct {
	message            *agentworker.Message
	turnID             string
	consumedMessageIDs []string
	consumedInputs     []*schema.Message
	consumedInputsMeta []any
}

func decodeUserInputCommand(message *agentworker.Message) (userInputCommand, error) {
	input, err := parseUserMessage(message)
	if err != nil {
		return userInputCommand{}, err
	}
	userInput, err := userMessageToEinoMessage(input)
	if err != nil {
		return userInputCommand{}, err
	}
	attachAttribute(userInput, attributeFromWorkerMessage(message))
	return userInputCommand{
		message: message,
		input:   input,
		schema:  userInput,
		mode:    userInputMode(message, input.Mode),
	}, nil
}

func decodeResumeTurnCommand(message *agentworker.Message) (resumeTurnCommand, error) {
	payload, err := parseResumePayload(message)
	if err != nil {
		return resumeTurnCommand{}, err
	}
	return resumeTurnCommand{
		message: message,
		payload: payload,
		mode:    resumeMessageMode(message),
	}, nil
}

func decodeCompactCommand(message *agentworker.Message) compactCommand {
	consumed := compactConsumedMessageIDs(message)
	return compactCommand{
		message:            message,
		turnID:             compactTurnID(message),
		consumedMessageIDs: consumed,
		consumedInputs:     compactConsumedInputsFromIDs(consumed),
		consumedInputsMeta: compactConsumedInputsMeta(message, consumed),
	}
}

func (c userInputCommand) messageID() string {
	if c.message == nil {
		return ""
	}
	return c.message.ID
}

func (c resumeTurnCommand) messageID() string {
	if c.message == nil {
		return ""
	}
	return c.message.ID
}

func (c compactCommand) messageID() string {
	if c.message == nil {
		return ""
	}
	return c.message.ID
}

func compactTurnID(message *agentworker.Message) string {
	if message != nil {
		id := strings.TrimSpace(message.ID)
		if id != "" {
			return "compact_" + id
		}
	}
	return "compact_" + uuid.NewString()
}

func compactConsumedMessageIDs(message *agentworker.Message) []string {
	if message == nil || strings.TrimSpace(message.ID) == "" {
		return nil
	}
	return []string{strings.TrimSpace(message.ID)}
}

func compactConsumedInputsFromIDs(ids []string) []*schema.Message {
	if len(ids) == 0 {
		return nil
	}
	out := make([]*schema.Message, 0, len(ids))
	for _, id := range ids {
		msg := schema.SystemMessage("compact")
		attachAttribute(msg, MessageAttribute{MessageID: id})
		out = append(out, msg)
	}
	return out
}

func compactConsumedInputsMeta(message *agentworker.Message, consumedMessageIDs []string) []any {
	if message == nil || len(consumedMessageIDs) == 0 || len(message.Metadata) == 0 {
		return nil
	}
	return []any{maps.Clone(message.Metadata)}
}

func unsupportedRuntimeCommand(message *agentworker.Message) error {
	if message == nil {
		return fmt.Errorf("worker message is required")
	}
	return fmt.Errorf("unsupported message type: %s", message.Type)
}
