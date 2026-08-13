// Package deepagents 提供基于中间件架构的深度代理实现
package deepagents

import (
	"sync"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/types"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// DeepAgent 实现了基于中间件架构的深度代理。
type DeepAgent struct {
	runnable              compose.Runnable[[]*schema.Message, *schema.Message]
	middlewareChain       *middleware.MiddlewareChain
	backend               backends.Backend
	callbacks             []callbacks.Handler
	graphState            *types.GraphState
	graphInterruptMu      sync.Mutex
	graphInterruptHandle  middleware.GraphInterruptHandle
	graphInterruptUsed    bool
	pendingGraphInterrupt []compose.GraphInterruptOption
	depth                 int
}

// GetGraphRunnable 返回编译完成的 Eino Runnable。
func (a *DeepAgent) GetGraphRunnable() compose.Runnable[[]*schema.Message, *schema.Message] {
	return a.runnable
}

// GraphState 返回 Agent 的持久化图状态。
func (a *DeepAgent) GraphState() *types.GraphState {
	return a.graphState
}

// Depth 返回当前 Agent 的嵌套深度。
func (a *DeepAgent) Depth() int {
	return a.depth
}

// Name 返回 Agent 对应的图名称。
func (a *DeepAgent) Name() string {
	return constant.GraphName
}
