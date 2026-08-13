package deepagents

import (
	"context"

	"eino-cli/deepagent/core/middleware"
	"github.com/cloudwego/eino/compose"
)

func (a *DeepAgent) setGraphInterruptHandle(handle middleware.GraphInterruptHandle) {
	if a == nil {
		return
	}
	var pending []compose.GraphInterruptOption
	a.graphInterruptMu.Lock()
	a.graphInterruptHandle = handle
	a.graphInterruptUsed = false
	if len(a.pendingGraphInterrupt) > 0 {
		pending = append([]compose.GraphInterruptOption(nil), a.pendingGraphInterrupt...)
		a.pendingGraphInterrupt = nil
		a.graphInterruptUsed = true
	}
	a.graphInterruptMu.Unlock()

	if handle != nil && pending != nil {
		handle(pending...)
	}
}

// Interrupt 中断当前正在执行的 Agent 图；若图尚未启动，则暂存中断请求。
func (a *DeepAgent) Interrupt(opts ...compose.GraphInterruptOption) bool {
	if a == nil {
		return false
	}

	copiedOpts := append([]compose.GraphInterruptOption(nil), opts...)
	var handle middleware.GraphInterruptHandle
	a.graphInterruptMu.Lock()
	if a.graphInterruptHandle == nil {
		a.pendingGraphInterrupt = copiedOpts
		a.graphInterruptMu.Unlock()
		return true
	}
	if a.graphInterruptUsed {
		a.graphInterruptMu.Unlock()
		return true
	}
	handle = a.graphInterruptHandle
	a.graphInterruptUsed = true
	a.graphInterruptMu.Unlock()

	handle(copiedOpts...)
	return true
}

// Close 目前无需清理，预留接口供 subAgentRunnerAdapter 实现 SubAgentRunner。
func (a *DeepAgent) Close(ctx context.Context) error {
	return nil
}
