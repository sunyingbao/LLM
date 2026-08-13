//go:build !windows

package thread

import (
	"strings"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/middleware/planmode"
	"eino-cli/deepagent/worker"
)

func yieldFromAgentEvent(ev agentthread.Event) *agentworker.ThreadYield {
	block, reason, ok := yieldBlockForAgentEvent(ev)
	if !ok {
		return nil
	}
	return &agentworker.ThreadYield{
		Reason: reason,
		Block:  block,
	}
}

func yieldBlockForAgentEvent(ev agentthread.Event) (*agentworker.PendingBlock, string, bool) {
	switch ev.Type {
	case agentthread.EventTurnEnd:
		return nil, "", true
	case agentthread.EventApproveRequested:
		payload, _ := ev.Payload.(agentthread.ApprovalRequiredPayload)
		return &agentworker.PendingBlock{
			TurnID:       ev.TurnID,
			InterruptID:  payload.InterruptID,
			CheckpointID: payload.CheckpointID,
			Kind:         "approval",
		}, "deepagent waiting for approval", true
	case agentthread.EventFollowUpRequested:
		payload, _ := ev.Payload.(agentthread.FollowUpRequestedPayload)
		return &agentworker.PendingBlock{
			TurnID:       ev.TurnID,
			InterruptID:  payload.InterruptID,
			CheckpointID: payload.CheckpointID,
			Kind:         "follow_up",
		}, "deepagent waiting for followup", true
	case agentthread.EventInterrupted:
		payload, _ := ev.Payload.(agentthread.InterruptedPayload)
		if isExternalInterrupt(payload) {
			if payload.Metadata["kind"] == string(agentworker.ThreadInterruptKindWorkerShutdownTimeout) {
				reason := strings.TrimSpace(payload.Metadata["reason"])
				if reason == "" {
					reason = "worker shutdown timeout"
				}
				return nil, reason, true
			}
			return nil, "", false
		}
		kind := "interrupted"
		reason := "deepagent interrupted"
		if _, ok := payload.Info.(*planmode.RequestUserInputInfo); ok {
			kind = "plan_input"
			reason = "deepagent waiting for plan input"
		}
		if !isRecoverableRuntimeInterrupt(payload) {
			return nil, "deepagent interrupted without resumable context", true
		}
		if kind == "interrupted" {
			kind = interruptKind(payload)
			reason = "deepagent waiting for interrupt"
		}
		return &agentworker.PendingBlock{
			TurnID:       ev.TurnID,
			InterruptID:  payload.InterruptID,
			CheckpointID: payload.CheckpointID,
			Kind:         kind,
		}, reason, true
	default:
		return nil, "", false
	}
}
