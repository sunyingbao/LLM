package videoagent

import (
	"context"
	"sync"
	"time"
)

const (
	MonitorNodeStarted        = "node_started"
	MonitorNodeCompleted      = "node_completed"
	MonitorNodeFailed         = "node_failed"
	MonitorCallback           = "callback"
	MonitorLeaseRenewalFailed = "lease_renewal_failed"
)

// RunEvent is the small observation contract shared by local and production monitors.
type RunEvent struct {
	Action     string    `json:"action"`
	RunID      string    `json:"run_id"`
	NodeID     string    `json:"node_id,omitempty"`
	Kind       NodeKind  `json:"kind,omitempty"`
	State      NodeState `json:"state,omitempty"`
	Message    string    `json:"message,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	At         time.Time `json:"at"`
}

// Monitor receives execution events without participating in workflow decisions.
type Monitor interface {
	Record(context.Context, RunEvent)
}

// MonitorFunc adapts a function to Monitor.
type MonitorFunc func(context.Context, RunEvent)

func (monitor MonitorFunc) Record(ctx context.Context, event RunEvent) {
	if monitor != nil {
		monitor(ctx, event)
	}
}

// Metrics stores process-local counters for health and local verification endpoints.
type Metrics struct {
	mu     sync.RWMutex
	counts map[string]int64
}

// NewMetrics creates an empty execution counter.
func NewMetrics() *Metrics {
	return &Metrics{counts: make(map[string]int64)}
}

// Record counts an execution event and can be used as a production Monitor sink.
func (metrics *Metrics) Record(_ context.Context, event RunEvent) {
	if metrics == nil {
		return
	}
	metrics.mu.Lock()
	metrics.counts[event.Action]++
	metrics.mu.Unlock()
}

// Snapshot returns a copy that can be safely serialized by an HTTP handler.
func (metrics *Metrics) Snapshot() map[string]int64 {
	if metrics == nil {
		return map[string]int64{}
	}
	metrics.mu.RLock()
	defer metrics.mu.RUnlock()
	snapshot := make(map[string]int64, len(metrics.counts))
	for key, value := range metrics.counts {
		snapshot[key] = value
	}
	return snapshot
}
