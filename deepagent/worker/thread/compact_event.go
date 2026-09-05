//go:build !windows

package thread

import (
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/worker"
)

const agentEventContextCompactInterrupted agentthread.EventType = "context_compact_interrupted"

type contextCompactInterruptedPayload struct {
	Kind             string
	Reason           string
	ControlMessageID string
	CutoffMessageID  string
}

func newContextCompactInterruptedPayload(req agentworker.ThreadInterruptRequest) contextCompactInterruptedPayload {
	return contextCompactInterruptedPayload{
		Kind:             string(req.Kind),
		Reason:           req.Reason,
		ControlMessageID: req.ControlMessageID,
		CutoffMessageID:  req.CutoffMessageID,
	}
}
