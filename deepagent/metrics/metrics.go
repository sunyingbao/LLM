package metrics

import (
	"reflect"
	"sync"

	metricsv4 "code.byted.org/gopkg/metrics/v4"
)

// T is one metrics tag.
type T = metricsv4.T

// Handle is an SDK metric descriptor plus its current runtime binding.
//
// Handles are safe to keep as package-level variables. Before Init, or after
// Disable, emits through a Handle are no-ops.
type Handle struct {
	name     string
	tagKeys  []string
	emitKind emitKind

	mu     sync.RWMutex
	metric metricsv4.Metric
	client metricsv4.Client
}

type emitKind int

const (
	emitDelta emitKind = iota + 1
	emitTimer
	emitStore
)

type registryState struct {
	mu          sync.Mutex
	initialized bool
	client      metricsv4.Client
	handles     []*Handle
}

var registry registryState

// Tag builds a metrics tag.
func Tag(name, value string) T {
	return metricsv4.T{Name: name, Value: value}
}

// DefineDeltaCounter defines a delta counter metric descriptor.
func DefineDeltaCounter(name string, tagKeys ...string) *Handle {
	return define(name, emitDelta, tagKeys...)
}

// DefineTimer defines a timer metric descriptor.
func DefineTimer(name string, tagKeys ...string) *Handle {
	return define(name, emitTimer, tagKeys...)
}

// DefineStore defines a store/gauge metric descriptor.
func DefineStore(name string, tagKeys ...string) *Handle {
	return define(name, emitStore, tagKeys...)
}

// Init binds all registered metric descriptors to client.
//
// Passing nil is valid and switches all registered metrics to explicit no-op.
// Metrics registered after Init are bound immediately to the current client.
func Init(client metricsv4.Client) {
	registry.mu.Lock()
	registry.initialized = true
	registry.client = client
	handles := append([]*Handle(nil), registry.handles...)
	registry.mu.Unlock()

	for _, h := range handles {
		h.bind(client)
	}
}

// Disable resets all registered metrics to no-op.
func Disable() {
	Init(nil)
}

// EmitDelta emits a delta counter value. It is a no-op until Init binds a
// working metrics client.
func EmitDelta(h *Handle, n int, tags ...T) {
	emit(h, metricsv4.Add(n), tags...)
}

// Observe emits a timer sample. It is a no-op until Init binds a working
// metrics client.
func Observe(h *Handle, n int, tags ...T) {
	emit(h, metricsv4.Observe(n), tags...)
}

// Store emits a store/gauge value. It is a no-op until Init binds a working
// metrics client.
func Store(h *Handle, n int, tags ...T) {
	emit(h, metricsv4.Store(n), tags...)
}

func define(name string, kind emitKind, tagKeys ...string) *Handle {
	h := &Handle{
		name:     name,
		emitKind: kind,
		tagKeys:  append([]string(nil), tagKeys...),
	}

	registry.mu.Lock()
	registry.handles = append(registry.handles, h)
	initialized := registry.initialized
	client := registry.client
	registry.mu.Unlock()

	if initialized {
		h.bind(client)
	}
	return h
}

func (h *Handle) bind(client metricsv4.Client) {
	if h == nil {
		return
	}
	h.mu.RLock()
	sameClient := sameMetricClient(h.client, client)
	hasMetric := h.metric != nil
	h.mu.RUnlock()
	if client != nil && sameClient && hasMetric {
		return
	}
	var metric metricsv4.Metric
	if client != nil {
		if m, err := client.NewMetric(h.name, h.tagKeys...); err == nil {
			metric = m
		} else if sameClient && hasMetric {
			return
		}
	}
	h.mu.Lock()
	h.metric = metric
	h.client = client
	h.mu.Unlock()
}

func emit(h *Handle, value *metricsv4.Value, tags ...T) {
	if h == nil || value == nil {
		return
	}
	h.mu.RLock()
	metric := h.metric
	h.mu.RUnlock()
	if metric == nil {
		return
	}
	_ = metric.WithTags(tags...).Emit1(value)
}

func sameMetricClient(a, b metricsv4.Client) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	av := reflect.ValueOf(a)
	bv := reflect.ValueOf(b)
	if av.Type() != bv.Type() || !av.Type().Comparable() {
		return false
	}
	return av.Interface() == bv.Interface()
}
