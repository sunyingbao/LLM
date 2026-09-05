//go:build !windows

package thread

import (
	"context"
	"eino-cli/deepagent/core/agentthread"
	agentworker "eino-cli/deepagent/worker"
	"time"

	"code.byted.org/gopkg/logs/v2"
)

const (
	threadOutputBridgeBufferSize   = 4096
	threadOutputDeliverWarnElapsed = 50 * time.Millisecond
)

type threadOutputBridge struct {
	agentEvents <-chan agentthread.Event
	inbox       chan agentworker.ThreadOutputItem
	items       chan agentworker.ThreadOutputItem
	output      *agentworker.ThreadOutput
	stop        context.CancelFunc
	done        chan struct{}
}

func (b *threadOutputBridge) start(ctx context.Context, runtime *Runtime) *agentworker.ThreadOutput {
	if b.output != nil {
		return b.output
	}
	b.inbox = make(chan agentworker.ThreadOutputItem, threadOutputBridgeBufferSize)
	b.items = make(chan agentworker.ThreadOutputItem, threadOutputBridgeBufferSize)
	b.done = make(chan struct{})
	bridgeCtx, cancel := context.WithCancel(ctx)
	b.stop = cancel
	go func(done chan struct{}) {
		defer close(done)
		runtime.runOutputBridge(bridgeCtx, b)
	}(b.done)
	b.output = &agentworker.ThreadOutput{Items: b.items}
	return b.output
}

func (b *threadOutputBridge) stopAndWait() {
	if b == nil {
		return
	}
	cancel := b.stop
	done := b.done
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (b *threadOutputBridge) send(ctx context.Context, item agentworker.ThreadOutputItem) bool {
	if b == nil || b.inbox == nil {
		return false
	}
	done := b.done
	if done != nil {
		select {
		case <-done:
			return false
		default:
		}
	}
	select {
	case b.inbox <- item:
		return true
	case <-done:
		return false
	case <-ctx.Done():
		return false
	}
}

func (b *threadOutputBridge) deliver(ctx context.Context, runtime *Runtime, item agentworker.ThreadOutputItem) bool {
	if b == nil || b.items == nil {
		return false
	}
	observation, observe := runtime.threadOutputObservation(item)
	queueLen := len(b.items)
	queueCap := cap(b.items)
	eventType := ""
	if item.Event != nil {
		eventType = string(item.Event.Type)
	}
	startedAt := time.Now()
	select {
	case b.items <- item:
		if elapsed := time.Since(startedAt); elapsed > threadOutputDeliverWarnElapsed {
			logs.CtxWarn(ctx, "[cloudagent] slow thread output deliver: 对话流ID=%s thread_id=%s event_type=%s elapsed=%s queue_len_before=%d queue_cap=%d",
				runtime.sessionID, runtime.threadID, eventType, elapsed, queueLen, queueCap)
		}
		if observe {
			runtime.enqueueThreadOutputObservation(ctx, observation)
		}
		return true
	case <-ctx.Done():
		return false
	}
}
