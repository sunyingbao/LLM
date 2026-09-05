//go:build !windows

package cloud

import (
	"code.byted.org/gopkg/logs/v2"
	agentworker "eino-cli/deepagent/worker"
)

type outputItemProcessor struct {
	lifecycle *claimCoordinator
	items     <-chan agentworker.ThreadOutputItem
	signal    chan struct{}
	done      chan struct{}
	result    *runtimeYield
}

func (p *outputItemProcessor) run() {
	defer close(p.done)
	logs.CtxInfo(p.lifecycle.ctx, "[agentworker] output processor start: thread_id=%d", p.lifecycle.claim.thread.ThreadID)
	defer func() {
		logs.CtxInfo(p.lifecycle.ctx, "[agentworker] output processor stopped: thread_id=%d signal=%s", p.lifecycle.claim.thread.ThreadID, runtimeYieldSummary(p.result))
	}()
	for {
		select {
		case <-p.lifecycle.ctx.Done():
			return
		case <-p.lifecycle.stopCh:
			p.drainReady()
			return
		case item, ok := <-p.items:
			if !ok {
				return
			}
			p.handleThreadOutput(item)
		}
	}
}

func (p *outputItemProcessor) drainReady() {
	for {
		select {
		case item, ok := <-p.items:
			if !ok {
				return
			}
			p.handleThreadOutput(item)
		default:
			return
		}
	}
}

func (p *outputItemProcessor) handleThreadOutput(item agentworker.ThreadOutputItem) {
	if item.Event != nil {
		logs.CtxInfo(p.lifecycle.ctx, "[agentworker] output event received: thread_id=%d turn_id=%s event_type=%s payload_bytes=%d",
			p.lifecycle.claim.thread.ThreadID, item.Event.TurnID, item.Event.Type, len(item.Event.Payload))
		p.lifecycle.worker.appendEventBestEffort(p.lifecycle.ctx, p.lifecycle.claim.thread.ThreadID, item.Event, p.lifecycle.claim.lease.LeaseToken)
	}

	yield := item.Yield
	if yield == nil {
		return
	}
	logs.CtxInfo(p.lifecycle.ctx, "[agentworker] output yield received: thread_id=%d reason=%q err=%v block_present=%t",
		p.lifecycle.claim.thread.ThreadID, yield.Reason, yield.Err, yield.Block != nil)
	p.sendSignal(runtimeYield{
		reason: yield.Reason,
		err:    yield.Err,
		block:  yield.Block,
	})
}

func (p *outputItemProcessor) sendSignal(signal runtimeYield) {
	if p.result == nil {
		copy := signal
		p.result = &copy
	}
	select {
	case p.signal <- struct{}{}:
	default:
	}
}
