//go:build !windows

package cloud

import "strings"

const (
	// MessageTypeControlCancelInput is the Coordinator mailbox message type
	// used by control-plane callers to cancel input up to a cutoff message.
	MessageTypeControlCancelInput = "__agent_control_cancel_input"
	// MessageTypeControlCloseThread is the Coordinator mailbox message type
	// used by control-plane callers to close a thread.
	MessageTypeControlCloseThread = "__agent_control_close_thread"
)

// CancelInputControlPayload is the JSON payload for
// MessageTypeControlCancelInput.
type CancelInputControlPayload struct {
	CutoffMessageID int64  `json:"cutoff_message_id"`
	Reason          string `json:"reason,omitempty"`
}

// CloseThreadControlPayload is the JSON payload for
// MessageTypeControlCloseThread.
type CloseThreadControlPayload struct {
	Reason string `json:"reason,omitempty"`
}

// IsControlMessageType reports whether messageType uses the reserved cloud
// worker control prefix rather than the business-input namespace.
func IsControlMessageType(messageType string) bool {
	return strings.HasPrefix(messageType, "__agent_control_")
}
