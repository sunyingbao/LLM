//go:build !windows

package thread

import (
	"context"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/worker"
)

const threadOutputObserverQueueSize = 256

func (t *Runtime) startThreadOutputObserver(ctx context.Context) {
	if t.threadOutputObserver == nil {
		return
	}
	t.observerOnce.Do(func() {
		t.observerQueue = make(chan ThreadOutputObservation, threadOutputObserverQueueSize)
		observerCtx, cancel := context.WithCancel(ctx)
		t.observerCancel = cancel
		go t.runThreadOutputObserver(observerCtx)
	})
}

func (t *Runtime) runThreadOutputObserver(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case obs := <-t.observerQueue:
			t.callThreadOutputObserver(ctx, obs)
		}
	}
}

func (t *Runtime) enqueueThreadOutputObservation(ctx context.Context, observation ThreadOutputObservation) {
	select {
	case t.observerQueue <- observation:
	default:
		logs.CtxWarn(ctx, "[cloudagent] drop thread output observation: 对话流ID=%s thread_id=%s queue_size=%d", t.sessionID, t.threadID, threadOutputObserverQueueSize)
	}
}

func (t *Runtime) threadOutputObservation(item agentworker.ThreadOutputItem) (ThreadOutputObservation, bool) {
	if t.threadOutputObserver == nil || t.observerQueue == nil {
		return ThreadOutputObservation{}, false
	}
	return ThreadOutputObservation{
		SessionID: t.sessionID,
		ThreadID:  t.threadID,
		Item:      cloneThreadOutputItem(item),
	}, true
}

func (t *Runtime) callThreadOutputObserver(ctx context.Context, obs ThreadOutputObservation) {
	startedAt := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			logs.CtxError(ctx, "[cloudagent] thread output observer panic: 对话流ID=%s thread_id=%s recovered=%v", t.sessionID, t.threadID, recovered)
		}
		if elapsed := time.Since(startedAt); elapsed > time.Second {
			logs.CtxWarn(ctx, "[cloudagent] thread output observer slow: 对话流ID=%s thread_id=%s elapsed=%s", t.sessionID, t.threadID, elapsed)
		}
	}()
	t.threadOutputObserver(ctx, obs)
}

func cloneThreadOutputItem(item agentworker.ThreadOutputItem) agentworker.ThreadOutputItem {
	return agentworker.ThreadOutputItem{
		Event: cloneWorkerEvent(item.Event),
		Yield: cloneThreadYield(item.Yield),
	}
}

func cloneWorkerEvent(event *agentworker.Event) *agentworker.Event {
	if event == nil {
		return nil
	}
	clone := *event
	clone.Payload = append([]byte(nil), event.Payload...)
	clone.Metadata = cloneStringMap(event.Metadata)
	clone.PersistToEventLog = cloneBoolPtr(event.PersistToEventLog)
	clone.FanoutToSession = cloneBoolPtr(event.FanoutToSession)
	return &clone
}

func cloneThreadYield(yield *agentworker.ThreadYield) *agentworker.ThreadYield {
	if yield == nil {
		return nil
	}
	clone := *yield
	if yield.Block != nil {
		block := *yield.Block
		clone.Block = &block
	}
	return &clone
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneBoolPtr(in *bool) *bool {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
