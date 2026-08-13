//go:build !windows

package thread

import (
	"context"
	"strings"
	"time"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/worker"
)

func (t *Runtime) emitCancelTurnEvents(ctx context.Context, payload protoinput.ResumeTurnPayload) {
	reason := strings.TrimSpace(payload.Approval.Reason)
	if reason == "" {
		reason = string(agentworker.ThreadInterruptKindCancelInput)
	}
	consumed := compactConsumedInputsFromIDs(payload.ConsumedMessageIDs)
	t.emitAgentEvent(ctx, agentthread.Event{
		ID:       t.eventID(payload.TurnID),
		TS:       time.Now(),
		ThreadID: t.threadID,
		TurnID:   payload.TurnID,
		Type:     agentthread.EventInterrupted,
		Payload: agentthread.InterruptedPayload{
			Source:       "external",
			InterruptID:  payload.InterruptID,
			CheckpointID: payload.CheckpointID,
			Metadata: map[string]string{
				"kind":   string(agentworker.ThreadInterruptKindCancelInput),
				"reason": reason,
			},
		},
		ConsumedInputs: consumed,
	})
	t.emitAgentEvent(ctx, agentthread.Event{
		ID:             t.eventID(payload.TurnID),
		TS:             time.Now(),
		ThreadID:       t.threadID,
		TurnID:         payload.TurnID,
		Type:           agentthread.EventTurnEnd,
		Payload:        agentthread.TurnEndPayload{},
		ConsumedInputs: consumed,
	})
}
