package agentthread

import (
	"context"
	"time"

	"code.byted.org/gopkg/logs/v2"
)

// emitEvent is the single producer boundary for events emitted by one turn.
// The event lock keeps closeEventBus from racing with late callbacks.
func (r *TurnRunner) emitEvent(ctx context.Context, typ EventType, payload any) {
	loc := eventLocationFromContext(ctx)
	if loc == (EventLocation{}) && r.agent != nil {
		loc = EventLocation{
			AgentName:  r.agent.Name(),
			AgentDepth: r.agent.Depth(),
		}
	}
	ev := Event{
		Loc:      loc,
		ID:       r.eventIDProvider(ctx, r.threadID, r.turnID),
		TS:       time.Now(),
		ThreadID: r.threadID,
		TurnID:   r.turnID,
		Type:     typ,
		Payload:  payload,
	}

	r.eventMu.RLock()
	defer r.eventMu.RUnlock()
	if r.eventClosed {
		logs.CtxWarn(ctx, "[TurnRunner::emitEvent] drop late event after turn event channel closed: thread_id=%s turn_id=%s event_type=%s",
			r.threadID, r.turnID, typ)
		return
	}
	queueLen := len(r.eventBus)
	queueCap := cap(r.eventBus)
	startedAt := time.Now()
	r.eventBus <- ev
	if elapsed := time.Since(startedAt); elapsed > eventEnqueueWarnThreshold {
		logs.CtxWarn(ctx, "[TurnRunner::emitEvent] slow event enqueue: thread_id=%s turn_id=%s event_type=%s elapsed=%s queue_len_before=%d queue_cap=%d",
			r.threadID, r.turnID, typ, elapsed, queueLen, queueCap)
	}
}

// closeEventBus closes the turn-local event stream exactly once. The thread
// waits for the forwarding goroutine before declaring the turn complete.
func (r *TurnRunner) closeEventBus() {
	r.eventMu.Lock()
	defer r.eventMu.Unlock()
	if r.eventClosed {
		return
	}
	r.eventClosed = true
	close(r.eventBus)
}
