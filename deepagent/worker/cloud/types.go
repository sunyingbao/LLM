//go:build !windows

package cloud

import (
	"context"
	"errors"

	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	"eino-cli/deepagent/worker"
)

var (
	ErrMissingNamespace          = errors.New("agentworker/cloud: namespace is required")
	ErrMissingClient             = errors.New("agentworker/cloud: client is required")
	ErrMissingAgentThreadFactory = errors.New("agentworker/cloud: agent thread factory is required")
	ErrMissingThread             = errors.New("agentworker/cloud: thread is required")
	ErrMissingLease              = errors.New("agentworker/cloud: lease is required")
)

// AgentThreadFactory creates a thread-scoped runtime for one claimed Agent
// Coordinator thread.
type AgentThreadFactory func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error)
