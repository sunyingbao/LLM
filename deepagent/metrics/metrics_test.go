package metrics

import (
	"errors"
	"sync"
	"testing"

	"code.byted.org/gopkg/apm_vendor_interface"
	metricsv4 "code.byted.org/gopkg/metrics/v4"
)

func TestMetricsNoopBeforeInit(t *testing.T) {
	resetForTest()
	h := DefineDeltaCounter("test_noop_before_init", "result")

	EmitDelta(h, 1, Tag("result", "ok"))
	Observe(DefineTimer("test_timer_before_init"), 12)
	Store(DefineStore("test_store_before_init"), 3)
}

func TestInitBindsExistingMetricAndEmit(t *testing.T) {
	resetForTest()
	h := DefineDeltaCounter("test_existing", "result")
	client := &fakeClient{}

	Init(client)
	EmitDelta(h, 2, Tag("result", "ok"))

	if got, want := client.createdNames(), []string{"test_existing"}; !equalStrings(got, want) {
		t.Fatalf("created metrics = %v, want %v", got, want)
	}
	if got := client.metric("test_existing").emits; got != 1 {
		t.Fatalf("emits = %d, want 1", got)
	}
	if got := client.metric("test_existing").lastTags; len(got) != 1 || got[0].Name != "result" || got[0].Value != "ok" {
		t.Fatalf("tags = %#v, want result=ok", got)
	}
}

func TestLateRegisterAfterInitBindsImmediately(t *testing.T) {
	resetForTest()
	client := &fakeClient{}
	Init(client)

	h := DefineTimer("test_late", "kind")
	Observe(h, 7, Tag("kind", "latency"))

	if got, want := client.createdNames(), []string{"test_late"}; !equalStrings(got, want) {
		t.Fatalf("created metrics = %v, want %v", got, want)
	}
	if got := client.metric("test_late").emits; got != 1 {
		t.Fatalf("emits = %d, want 1", got)
	}
}

func TestInitSameClientIsIdempotent(t *testing.T) {
	resetForTest()
	h := DefineDeltaCounter("test_reinit", "result")
	client := &fakeClient{}

	Init(client)
	EmitDelta(h, 1, Tag("result", "first"))
	Init(client)
	EmitDelta(h, 1, Tag("result", "second"))

	if got, want := client.createdNames(), []string{"test_reinit"}; !equalStrings(got, want) {
		t.Fatalf("created metrics = %v, want %v", got, want)
	}
	if got := client.metric("test_reinit").emits; got != 2 {
		t.Fatalf("emits = %d, want 2", got)
	}
	if got := client.metric("test_reinit").lastTags; len(got) != 1 || got[0].Value != "second" {
		t.Fatalf("tags = %#v, want second emit tags", got)
	}
}

func TestInitNonComparableClientDoesNotPanic(t *testing.T) {
	resetForTest()
	h := DefineDeltaCounter("test_non_comparable")
	client := nonComparableClient{marks: []string{"x"}}

	Init(client)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Init panicked: %v", r)
		}
	}()
	Init(client)
	EmitDelta(h, 1)
}

func TestDisableResetsMetricsToNoop(t *testing.T) {
	resetForTest()
	h := DefineStore("test_disable", "state")
	client := &fakeClient{}

	Init(client)
	Store(h, 1, Tag("state", "active"))
	Disable()
	Store(h, 2, Tag("state", "inactive"))

	if got := client.metric("test_disable").emits; got != 1 {
		t.Fatalf("emits = %d, want 1", got)
	}
}

func TestMetricCreateErrorLeavesHandleNoop(t *testing.T) {
	resetForTest()
	h := DefineDeltaCounter("test_create_error")
	client := &fakeClient{createErr: errors.New("create failed")}

	Init(client)
	EmitDelta(h, 1)

	if got := client.createdNames(); len(got) != 1 || got[0] != "test_create_error" {
		t.Fatalf("created metrics = %v, want [test_create_error]", got)
	}
	if len(client.metrics) != 0 {
		t.Fatalf("metrics = %d, want 0", len(client.metrics))
	}
}

func resetForTest() {
	registry.mu.Lock()
	handles := registry.handles
	registry.initialized = false
	registry.client = nil
	registry.handles = nil
	registry.mu.Unlock()
	for _, h := range handles {
		h.bind(nil)
	}
}

type fakeClient struct {
	mu        sync.Mutex
	createErr error
	created   []string
	metrics   map[string]*fakeMetric
}

func (c *fakeClient) NewMetric(name string, tagNames ...string) (metricsv4.Metric, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.created = append(c.created, name)
	if c.createErr != nil {
		return nil, c.createErr
	}
	if c.metrics == nil {
		c.metrics = make(map[string]*fakeMetric)
	}
	if _, ok := c.metrics[name]; ok {
		return nil, errors.New("duplicate metric name")
	}
	m := &fakeMetric{name: name, tagNames: append([]string(nil), tagNames...)}
	c.metrics[name] = m
	return m, nil
}

func (c *fakeClient) NewMetricWithOps(name string, tagNames []string, options ...metricsv4.MetricOption) (metricsv4.Metric, error) {
	return c.NewMetric(name, tagNames...)
}

func (c *fakeClient) Close() {}
func (c *fakeClient) Flush() {}
func (c *fakeClient) GetConfigService() apm_vendor_interface.MetricsConfigService {
	return nil
}
func (c *fakeClient) GetTenant() string { return "test" }
func (c *fakeClient) IsTenantActive() bool {
	return true
}

type nonComparableClient struct {
	marks []string
}

func (c nonComparableClient) NewMetric(name string, tagNames ...string) (metricsv4.Metric, error) {
	return &fakeMetric{name: name, tagNames: append([]string(nil), tagNames...)}, nil
}

func (c nonComparableClient) NewMetricWithOps(name string, tagNames []string, options ...metricsv4.MetricOption) (metricsv4.Metric, error) {
	return c.NewMetric(name, tagNames...)
}

func (c nonComparableClient) Close() {}
func (c nonComparableClient) Flush() {}
func (c nonComparableClient) GetConfigService() apm_vendor_interface.MetricsConfigService {
	return nil
}
func (c nonComparableClient) GetTenant() string { return "test" }
func (c nonComparableClient) IsTenantActive() bool {
	return len(c.marks) >= 0
}

func (c *fakeClient) createdNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.created...)
}

func (c *fakeClient) metric(name string) *fakeMetric {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.metrics[name]
}

type fakeMetric struct {
	name     string
	tagNames []string
	lastTags []metricsv4.T
	emits    int
}

func (m *fakeMetric) WithTags(tags ...metricsv4.T) metricsv4.Emitter {
	m.lastTags = append([]metricsv4.T(nil), tags...)
	return fakeEmitter{metric: m}
}

func (m *fakeMetric) WithTagValues(tagValues ...string) metricsv4.Emitter {
	return fakeEmitter{metric: m}
}

func (m *fakeMetric) NewTagIndex() metricsv4.TagIndex {
	values := []string{}
	return metricsv4.TagIndex(&values)
}

func (m *fakeMetric) AppendTag(index metricsv4.TagIndex, name, value string) {}
func (m *fakeMetric) WithTagIndex(index metricsv4.TagIndex) metricsv4.Emitter {
	return fakeEmitter{metric: m}
}
func (m *fakeMetric) Close() {}
func (m *fakeMetric) Flush() {}

type fakeEmitter struct {
	metric *fakeMetric
}

func (e fakeEmitter) Emit(values ...*metricsv4.Value) error {
	e.metric.emits++
	return nil
}

func (e fakeEmitter) Emit1(value *metricsv4.Value, values ...*metricsv4.Value) error {
	e.metric.emits++
	return nil
}

func (e fakeEmitter) Emit2(v1 *metricsv4.Value, v2 *metricsv4.Value, values ...*metricsv4.Value) error {
	e.metric.emits++
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
