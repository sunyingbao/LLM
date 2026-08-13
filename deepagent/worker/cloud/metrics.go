//go:build !windows

package cloud

import (
	"context"
	"errors"
	"sync"
	"time"

	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	sdkmetrics "eino-cli/deepagent/metrics"
	"eino-cli/deepagent/worker"
)

var (
	metricActiveClaims       = sdkmetrics.DefineStore("agentworker_active_claims", "namespace")
	metricScanResultCount    = sdkmetrics.DefineTimer("agentworker_scan_result_count", "namespace")
	metricClaimFinished      = sdkmetrics.DefineDeltaCounter("agentworker_claim_finished_total", "namespace", "result")
	metricClaimDuration      = sdkmetrics.DefineTimer("agentworker_claim_duration_ms", "namespace", "result")
	metricMessageReceived    = sdkmetrics.DefineDeltaCounter("agentworker_message_received_total", "namespace", "message_type")
	metricMessageAccepted    = sdkmetrics.DefineDeltaCounter("agentworker_message_accepted_total", "namespace", "message_type")
	metricMessageAckBatch    = sdkmetrics.DefineTimer("agentworker_message_ack_batch_size", "namespace")
	metricOutputItemReceived = sdkmetrics.DefineDeltaCounter("agentworker_output_item_received_total", "namespace", "item_type")
	metricEventAppendBatch   = sdkmetrics.DefineTimer("agentworker_event_append_batch_size", "namespace")
	metricRelease            = sdkmetrics.DefineDeltaCounter("agentworker_release_total", "namespace", "target_status")
	metricBlock              = sdkmetrics.DefineDeltaCounter("agentworker_block_total", "namespace", "block_kind")
	metricInterrupt          = sdkmetrics.DefineDeltaCounter("agentworker_interrupt_total", "namespace", "kind")
	metricShutdownDrain      = sdkmetrics.DefineDeltaCounter("agentworker_shutdown_drain_total", "namespace", "result")
	metricError              = sdkmetrics.DefineDeltaCounter("agentworker_error_total", "namespace", "where", "what")
)

var activeClaims = struct {
	sync.Mutex
	byNamespace map[string]int
}{byNamespace: make(map[string]int)}

func recordActiveClaim(namespace string, delta int) {
	activeClaims.Lock()
	next := activeClaims.byNamespace[namespace] + delta
	if next < 0 {
		next = 0
	}
	activeClaims.byNamespace[namespace] = next
	sdkmetrics.Store(metricActiveClaims, next, sdkmetrics.Tag("namespace", namespace))
	activeClaims.Unlock()
}

func recordScanResult(namespace string, count int) {
	sdkmetrics.Observe(metricScanResultCount, count, sdkmetrics.Tag("namespace", namespace))
}

func recordClaimFinished(namespace, result string, elapsed time.Duration) {
	if result == "" {
		result = "error"
	}
	sdkmetrics.EmitDelta(metricClaimFinished, 1,
		sdkmetrics.Tag("namespace", namespace),
		sdkmetrics.Tag("result", result),
	)
	sdkmetrics.Observe(metricClaimDuration, int(elapsed.Milliseconds()),
		sdkmetrics.Tag("namespace", namespace),
		sdkmetrics.Tag("result", result),
	)
}

func recordMessagesReceived(namespace string, messages []*ac.Message) {
	for _, message := range messages {
		if message == nil {
			continue
		}
		sdkmetrics.EmitDelta(metricMessageReceived, 1,
			sdkmetrics.Tag("namespace", namespace),
			sdkmetrics.Tag("message_type", message.GetMessageType()),
		)
	}
}

func recordMessageAccepted(namespace string, message *ac.Message) {
	if message == nil {
		return
	}
	sdkmetrics.EmitDelta(metricMessageAccepted, 1,
		sdkmetrics.Tag("namespace", namespace),
		sdkmetrics.Tag("message_type", message.GetMessageType()),
	)
}

func recordMessageAck(namespace string, count int) {
	sdkmetrics.Observe(metricMessageAckBatch, count, sdkmetrics.Tag("namespace", namespace))
}

func recordOutputItem(namespace string, item agentworker.ThreadOutputItem) {
	if item.Event != nil {
		sdkmetrics.EmitDelta(metricOutputItemReceived, 1,
			sdkmetrics.Tag("namespace", namespace),
			sdkmetrics.Tag("item_type", "event"),
		)
	}
	if item.Yield != nil {
		sdkmetrics.EmitDelta(metricOutputItemReceived, 1,
			sdkmetrics.Tag("namespace", namespace),
			sdkmetrics.Tag("item_type", "yield"),
		)
	}
	if item.Event == nil && item.Yield == nil {
		sdkmetrics.EmitDelta(metricOutputItemReceived, 1,
			sdkmetrics.Tag("namespace", namespace),
			sdkmetrics.Tag("item_type", "unknown"),
		)
	}
}

func recordEventAppend(namespace string, count int) {
	sdkmetrics.Observe(metricEventAppendBatch, count, sdkmetrics.Tag("namespace", namespace))
}

func recordRelease(namespace string, block *agentworker.PendingBlock) {
	target := "idle"
	if block != nil {
		target = "blocked"
	}
	sdkmetrics.EmitDelta(metricRelease, 1,
		sdkmetrics.Tag("namespace", namespace),
		sdkmetrics.Tag("target_status", target),
	)
	if block != nil {
		kind := block.Kind
		if kind == "" {
			kind = "unknown"
		}
		sdkmetrics.EmitDelta(metricBlock, 1,
			sdkmetrics.Tag("namespace", namespace),
			sdkmetrics.Tag("block_kind", kind),
		)
	}
}

func recordClosedRelease(namespace string) {
	sdkmetrics.EmitDelta(metricRelease, 1,
		sdkmetrics.Tag("namespace", namespace),
		sdkmetrics.Tag("target_status", "closed"),
	)
}

func recordInterrupt(namespace string, kind agentworker.ThreadInterruptKind) {
	sdkmetrics.EmitDelta(metricInterrupt, 1,
		sdkmetrics.Tag("namespace", namespace),
		sdkmetrics.Tag("kind", string(kind)),
	)
}

func recordShutdownDrain(namespace, result string) {
	sdkmetrics.EmitDelta(metricShutdownDrain, 1,
		sdkmetrics.Tag("namespace", namespace),
		sdkmetrics.Tag("result", result),
	)
}

func recordWorkerError(namespace, where string, err error) {
	if err == nil {
		return
	}
	sdkmetrics.EmitDelta(metricError, 1,
		sdkmetrics.Tag("namespace", namespace),
		sdkmetrics.Tag("where", where),
		sdkmetrics.Tag("what", classifyWorkerError(err)),
	)
}

func classifyWorkerError(err error) string {
	switch {
	case err == nil:
		return "unknown"
	case errors.Is(err, agentworker.ErrThreadClosed):
		return "thread_closed"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "context_cancelled"
	default:
		return "unknown"
	}
}
