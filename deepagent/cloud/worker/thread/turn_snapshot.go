//go:build !windows

package thread

import (
	"context"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/worker"
)

func (t *Runtime) runnerConfig(ctx context.Context, turnID string, message *agentworker.Message, mode protoinput.UserMessageMode, resume bool) (*agentthread.TurnRunnerConfig, error) {
	if t == nil || t.turnRunnerConfig == nil {
		return nil, nil
	}
	return t.turnRunnerConfig(ctx, TurnRunnerConfigRequest{TurnID: turnID, Mode: mode, Message: message, Resume: resume})
}
