//go:build !windows

package thread

import (
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/worker"
)

func userInputMode(message *agentworker.Message, mode protoinput.UserMessageMode) protoinput.UserMessageMode {
	if mode != "" {
		return mode
	}
	return messageMetadataMode(message)
}

func messageMetadataMode(message *agentworker.Message) protoinput.UserMessageMode {
	if message == nil || message.Metadata == nil {
		return ""
	}
	switch message.Metadata[MetadataTurnMode] {
	case TurnModePlan:
		return protoinput.UserMessageModeImplPlan
	default:
		return ""
	}
}

// threadInterruptMetadata is the metadata forwarded into Run's
// own interrupted event. Compact has its own worker event and does not use this.
func threadInterruptMetadata(req agentworker.ThreadInterruptRequest) map[string]string {
	metadata := map[string]string{}
	if req.Kind != "" {
		metadata["kind"] = string(req.Kind)
	}
	if req.Reason != "" {
		metadata["reason"] = req.Reason
	}
	if req.ControlMessageID != "" {
		metadata["control_message_id"] = req.ControlMessageID
	}
	if req.CutoffMessageID != "" {
		metadata["cutoff_message_id"] = req.CutoffMessageID
	}
	return metadata
}
