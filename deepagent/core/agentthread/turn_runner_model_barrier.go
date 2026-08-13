package agentthread

import (
	"context"
	"sync"
	"time"

	"code.byted.org/gopkg/logs/v2"
)

const modelBarrierWaitWarnThreshold = 50 * time.Millisecond

type modelEventBarrier struct {
	done chan struct{}
	once sync.Once
}

func newModelEventBarrier() *modelEventBarrier {
	return &modelEventBarrier{done: make(chan struct{})}
}

func (b *modelEventBarrier) release() {
	if b == nil {
		return
	}
	b.once.Do(func() {
		close(b.done)
	})
}

func (r *TurnRunner) beginModelEventBarrier(ctx context.Context) *modelEventBarrier {
	if r.cfg != nil && r.cfg.EnableStreamToolCall {
		return nil
	}
	barrier := newModelEventBarrier()
	r.modelBarrierMu.Lock()
	if r.modelBarrier != nil {
		r.modelBarrier.release()
		logs.CtxWarn(ctx, "[TurnRunner::modelBarrier] release stale model barrier before new model call: thread_id=%s turn_id=%s",
			r.threadID, r.turnID)
	}
	r.modelBarrier = barrier
	r.modelBarrierMu.Unlock()
	return barrier
}

func (r *TurnRunner) currentModelEventBarrier() *modelEventBarrier {
	if r.cfg != nil && r.cfg.EnableStreamToolCall {
		return nil
	}
	r.modelBarrierMu.Lock()
	defer r.modelBarrierMu.Unlock()
	return r.modelBarrier
}

func (r *TurnRunner) releaseModelEventBarrier(barrier *modelEventBarrier) {
	if barrier == nil {
		return
	}
	barrier.release()
	r.modelBarrierMu.Lock()
	if r.modelBarrier == barrier {
		r.modelBarrier = nil
	}
	r.modelBarrierMu.Unlock()
}

func (r *TurnRunner) waitModelEventBarrier(ctx context.Context) bool {
	barrier := r.currentModelEventBarrier()
	if barrier == nil {
		return true
	}
	startedAt := time.Now()
	select {
	case <-barrier.done:
	case <-ctx.Done():
		logs.CtxWarn(ctx, "[TurnRunner::modelBarrier] stop waiting for llm_end before tool start: thread_id=%s turn_id=%s err=%v",
			r.threadID, r.turnID, ctx.Err())
		return false
	}
	if elapsed := time.Since(startedAt); elapsed > modelBarrierWaitWarnThreshold {
		logs.CtxWarn(ctx, "[TurnRunner::modelBarrier] waited for llm_end before tool start: thread_id=%s turn_id=%s elapsed=%s",
			r.threadID, r.turnID, elapsed)
	}
	return true
}
