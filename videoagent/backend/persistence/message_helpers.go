package persistence

import (
	"strings"

	"eino-cli/videoagent/backend/contract"
)

func messageText(message contract.Message) string {
	parts := make([]string, 0, len(message.Parts))
	for _, part := range message.Parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}
