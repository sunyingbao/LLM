//go:build !windows

package thread

import (
	"encoding/json"
	"fmt"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/worker"
)

func parseUserMessage(message *agentworker.Message) (protoinput.UserMessage, error) {
	if message == nil {
		return protoinput.UserMessage{}, fmt.Errorf("message is required")
	}
	var input protoinput.UserMessage
	if err := json.Unmarshal(message.Payload, &input); err != nil {
		return protoinput.UserMessage{}, fmt.Errorf("unmarshal user message: %w", err)
	}
	if err := input.Validate(); err != nil {
		return protoinput.UserMessage{}, err
	}
	return input, nil
}
