//go:build !windows

package thread

import (
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/constant"
)

func isHiddenInternalToolEvent(ev agentthread.Event) bool {
	switch ev.Type {
	case agentthread.EventToolStart:
		payload, ok := ev.Payload.(agentthread.ToolStartPayload)
		return ok && payload.Name == constant.ToolUpdatePlan
	case agentthread.EventToolCallOutputChunk:
		payload, ok := ev.Payload.(agentthread.ToolCallOutputChunkPayload)
		return ok && payload.Name == constant.ToolUpdatePlan
	case agentthread.EventToolEnd:
		payload, ok := ev.Payload.(agentthread.ToolEndPayload)
		return ok && payload.Name == constant.ToolUpdatePlan
	default:
		return false
	}
}
