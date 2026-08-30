package agentthread

import (
	"maps"
	"time"

	"github.com/cloudwego/eino/compose"
)

// Interrupt requests an interruption of the currently running DeepAgent graph.
// A nil timeout uses Eino's default interrupt behavior.
func (r *TurnRunner) Interrupt(timeout *time.Duration) bool {
	return r.InterruptWithOptions(InterruptOptions{Timeout: timeout})
}

// InterruptWithOptions records the external interrupt metadata before asking
// DeepAgent to stop. The recorded metadata is consumed when RunTurn translates
// the graph interrupt into an agentthread event.
func (r *TurnRunner) InterruptWithOptions(opts InterruptOptions) bool {
	r.mu.Lock()
	agent := r.agent
	if agent == nil {
		r.mu.Unlock()
		return false
	}
	r.interruptOpts = InterruptOptions{
		Timeout:  opts.Timeout,
		Metadata: maps.Clone(opts.Metadata),
	}
	r.interruptActive = true
	r.mu.Unlock()
	if opts.Timeout == nil {
		return agent.Interrupt()
	}
	return agent.Interrupt(compose.WithGraphInterruptTimeout(*opts.Timeout))
}
