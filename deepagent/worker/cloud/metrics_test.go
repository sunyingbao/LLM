//go:build !windows

package cloud

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"code.byted.org/gopkg/apm_vendor_interface"
	metricsv4 "code.byted.org/gopkg/metrics/v4"
	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	sdkmetrics "eino-cli/deepagent/metrics"
	"eino-cli/deepagent/worker"
)

func TestAgentworkerMetricsRecordClaimLifecycle(t *testing.T) {
	client := initCloudMetricsTest(t)

	recordActiveClaim("ns-a", 1)
	recordActiveClaim("ns-a", 1)
	recordActiveClaim("ns-a", -1)
	recordClaimFinished("ns-a", "release", 12*time.Millisecond)

	if metric := client.metric("agentworker_active_claims"); metric.emits != 3 {
		t.Fatalf("active claim emits = %d, want 3", metric.emits)
	}
	if got := client.metric("agentworker_active_claims").lastValue; got == nil {
		t.Fatalf("active claim value is nil")
	}
	assertMetricTags(t, client.metric("agentworker_active_claims"), map[string]string{"namespace": "ns-a"})
	if got := client.metric("agentworker_claim_finished_total").emits; got != 1 {
		t.Fatalf("claim finished emits = %d, want 1", got)
	}
	assertMetricTags(t, client.metric("agentworker_claim_finished_total"), map[string]string{
		"namespace": "ns-a",
		"result":    "release",
	})
	if got := client.metric("agentworker_claim_duration_ms").emits; got != 1 {
		t.Fatalf("claim duration emits = %d, want 1", got)
	}
}

func TestAgentworkerMetricsRecordMessageBoundaries(t *testing.T) {
	client := initCloudMetricsTest(t)

	recordMessagesReceived("ns-a", []*ac.Message{
		nil,
		{MessageType: "user_message"},
		{MessageType: "control.cancel_input"},
	})
	recordMessageAccepted("ns-a", &ac.Message{MessageType: "user_message"})
	recordMessageAck("ns-a", 2)

	if got := client.metric("agentworker_message_received_total").emits; got != 2 {
		t.Fatalf("message received emits = %d, want 2", got)
	}
	assertMetricTags(t, client.metric("agentworker_message_received_total"), map[string]string{
		"namespace":    "ns-a",
		"message_type": "control.cancel_input",
	})
	if got := client.metric("agentworker_message_accepted_total").emits; got != 1 {
		t.Fatalf("message accepted emits = %d, want 1", got)
	}
	if got := client.metric("agentworker_message_ack_batch_size").emits; got != 1 {
		t.Fatalf("message ack batch emits = %d, want 1", got)
	}
}

func TestAgentworkerMetricsRecordReleaseBlockAndInterrupt(t *testing.T) {
	client := initCloudMetricsTest(t)

	recordRelease("ns-a", nil)
	recordRelease("ns-a", &agentworker.PendingBlock{Kind: "hitl"})
	recordClosedRelease("ns-a")
	recordInterrupt("ns-a", agentworker.ThreadInterruptKindCancelInput)
	recordShutdownDrain("ns-a", "timeout")

	if got := client.metric("agentworker_release_total").emits; got != 3 {
		t.Fatalf("release emits = %d, want 3", got)
	}
	assertMetricTags(t, client.metric("agentworker_release_total"), map[string]string{
		"namespace":     "ns-a",
		"target_status": "closed",
	})
	if got := client.metric("agentworker_block_total").emits; got != 1 {
		t.Fatalf("block emits = %d, want 1", got)
	}
	assertMetricTags(t, client.metric("agentworker_block_total"), map[string]string{
		"namespace":  "ns-a",
		"block_kind": "hitl",
	})
	assertMetricTags(t, client.metric("agentworker_interrupt_total"), map[string]string{
		"namespace": "ns-a",
		"kind":      string(agentworker.ThreadInterruptKindCancelInput),
	})
	assertMetricTags(t, client.metric("agentworker_shutdown_drain_total"), map[string]string{
		"namespace": "ns-a",
		"result":    "timeout",
	})
}

func TestAgentworkerMetricsRecordOutputAndErrors(t *testing.T) {
	client := initCloudMetricsTest(t)

	recordOutputItem("ns-a", agentworker.ThreadOutputItem{Event: &agentworker.Event{}})
	recordOutputItem("ns-a", agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{}})
	recordOutputItem("ns-a", agentworker.ThreadOutputItem{})
	recordEventAppend("ns-a", 1)
	recordWorkerError("ns-a", "post_message", agentworker.ErrThreadClosed)
	recordWorkerError("ns-a", "pull_messages", context.Canceled)
	recordWorkerError("ns-a", "scan_threads", errors.New("boom"))

	if got := client.metric("agentworker_output_item_received_total").emits; got != 3 {
		t.Fatalf("output item emits = %d, want 3", got)
	}
	assertMetricTags(t, client.metric("agentworker_output_item_received_total"), map[string]string{
		"namespace": "ns-a",
		"item_type": "unknown",
	})
	if got := client.metric("agentworker_event_append_batch_size").emits; got != 1 {
		t.Fatalf("event append emits = %d, want 1", got)
	}
	if got := client.metric("agentworker_error_total").emits; got != 3 {
		t.Fatalf("worker error emits = %d, want 3", got)
	}
	assertMetricTags(t, client.metric("agentworker_error_total"), map[string]string{
		"namespace": "ns-a",
		"where":     "scan_threads",
		"what":      "unknown",
	})
}

func initCloudMetricsTest(t *testing.T) *fakeCloudMetricClient {
	t.Helper()
	resetActiveClaimsForTest()
	client := &fakeCloudMetricClient{}
	sdkmetrics.Init(client)
	t.Cleanup(func() {
		sdkmetrics.Disable()
		resetActiveClaimsForTest()
	})
	return client
}

func resetActiveClaimsForTest() {
	activeClaims.Lock()
	activeClaims.byNamespace = make(map[string]int)
	activeClaims.Unlock()
}

func assertMetricTags(t *testing.T, metric *fakeCloudMetric, want map[string]string) {
	t.Helper()
	if metric == nil {
		t.Fatalf("metric is nil")
	}
	got := make(map[string]string, len(metric.lastTags))
	for _, tag := range metric.lastTags {
		got[tag.Name] = tag.Value
	}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("tag %q = %q, want %q; all tags=%v", name, got[name], value, got)
		}
	}
}

type fakeCloudMetricClient struct {
	mu      sync.Mutex
	metrics map[string]*fakeCloudMetric
}

func (c *fakeCloudMetricClient) NewMetric(name string, tagNames ...string) (metricsv4.Metric, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.metrics == nil {
		c.metrics = make(map[string]*fakeCloudMetric)
	}
	m := &fakeCloudMetric{name: name, tagNames: append([]string(nil), tagNames...)}
	c.metrics[name] = m
	return m, nil
}

func (c *fakeCloudMetricClient) NewMetricWithOps(name string, tagNames []string, options ...metricsv4.MetricOption) (metricsv4.Metric, error) {
	return c.NewMetric(name, tagNames...)
}

func (c *fakeCloudMetricClient) Close() {}
func (c *fakeCloudMetricClient) Flush() {}
func (c *fakeCloudMetricClient) GetConfigService() apm_vendor_interface.MetricsConfigService {
	return nil
}
func (c *fakeCloudMetricClient) GetTenant() string { return "test" }
func (c *fakeCloudMetricClient) IsTenantActive() bool {
	return true
}

func (c *fakeCloudMetricClient) metric(name string) *fakeCloudMetric {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.metrics[name]
}

type fakeCloudMetric struct {
	name      string
	tagNames  []string
	lastTags  []metricsv4.T
	lastValue *metricsv4.Value
	emits     int
}

func (m *fakeCloudMetric) WithTags(tags ...metricsv4.T) metricsv4.Emitter {
	m.lastTags = append([]metricsv4.T(nil), tags...)
	return fakeCloudEmitter{metric: m}
}

func (m *fakeCloudMetric) WithTagValues(tagValues ...string) metricsv4.Emitter {
	return fakeCloudEmitter{metric: m}
}

func (m *fakeCloudMetric) NewTagIndex() metricsv4.TagIndex {
	values := []string{}
	return metricsv4.TagIndex(&values)
}

func (m *fakeCloudMetric) AppendTag(index metricsv4.TagIndex, name, value string) {}
func (m *fakeCloudMetric) WithTagIndex(index metricsv4.TagIndex) metricsv4.Emitter {
	return fakeCloudEmitter{metric: m}
}
func (m *fakeCloudMetric) Close() {}
func (m *fakeCloudMetric) Flush() {}

type fakeCloudEmitter struct {
	metric *fakeCloudMetric
}

func (e fakeCloudEmitter) Emit(values ...*metricsv4.Value) error {
	e.metric.emits++
	if len(values) > 0 {
		e.metric.lastValue = values[len(values)-1]
	}
	return nil
}

func (e fakeCloudEmitter) Emit1(value *metricsv4.Value, values ...*metricsv4.Value) error {
	e.metric.emits++
	e.metric.lastValue = value
	return nil
}

func (e fakeCloudEmitter) Emit2(v1 *metricsv4.Value, v2 *metricsv4.Value, values ...*metricsv4.Value) error {
	e.metric.emits++
	e.metric.lastValue = v2
	return nil
}
