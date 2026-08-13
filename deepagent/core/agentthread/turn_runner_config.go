package agentthread

import (
	"maps"

	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/subagent"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
)

// Clone returns a turn-local copy of the runner config.
//
// Dependency objects such as models, backends, stores, loaders, and middleware
// instances are intentionally shared by reference. Container fields are copied
// so turn-local edits cannot mutate the thread-level base config by appending to
// slices or writing maps.
func (c *TurnRunnerConfig) Clone() *TurnRunnerConfig {
	if c == nil {
		return &TurnRunnerConfig{}
	}
	out := *c
	out.Tools = append([]tool.BaseTool(nil), c.Tools...)
	if c.FilesystemConfig != nil {
		fs := *c.FilesystemConfig
		out.FilesystemConfig = &fs
	}
	if c.HITLConfig != nil {
		hitl := *c.HITLConfig
		hitl.NeedApproveTools = maps.Clone(c.HITLConfig.NeedApproveTools)
		hitl.ToolPolicyGates = maps.Clone(c.HITLConfig.ToolPolicyGates)
		hitl.NeedReviewAndEditTools = maps.Clone(c.HITLConfig.NeedReviewAndEditTools)
		out.HITLConfig = &hitl
	}
	out.SubAgents = append([]*subagent.SubAgent(nil), c.SubAgents...)
	out.InterruptAfterNodes = append([]string(nil), c.InterruptAfterNodes...)
	out.Middlewares = append([]middleware.Middleware(nil), c.Middlewares...)
	out.Callbacks = append([]callbacks.Handler(nil), c.Callbacks...)
	return &out
}
