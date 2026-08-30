package agentthread

import (
	"context"
	"time"

	"code.byted.org/gopkg/logs/v2"
)

// executeTurn executes one logical turn, drains all turn-local events, and only then
// releases the thread's active-run slot. Keeping this sequence together makes
// the completion boundary explicit: a turn is complete after execution and
// event forwarding have both finished.
func (t *DeepAgentThread) executeTurn(ctx context.Context, turn *turn) {
	err := turn.runner.RunTurn(ctx, turn.input, turn.turnRunOptions())
	turn.runner.closeEventBus()
	<-turn.eventsDrained
	t.finishTurn(turn, err)
}

// forwardRunEvents copies events from a single TurnRunner into the thread-wide
// event channel and attaches the inputs consumed by that logical turn.
func (t *DeepAgentThread) forwardRunEvents(turn *turn) {
	defer close(turn.eventsDrained)
	for ev := range turn.events {
		ev.ConsumedInputs, ev.ConsumedInputsMeta = t.consumedInputsSnapshot(turn)
		runQueueLen := len(turn.events)
		runQueueCap := cap(turn.events)
		threadQueueLen := len(t.evCh)
		threadQueueCap := cap(t.evCh)
		startedAt := time.Now()
		t.evCh <- ev
		if elapsed := time.Since(startedAt); elapsed > threadEventForwardWarnDuration {
			logs.CtxWarn(context.Background(), "[DeepAgentThread::forwardRunEvents] slow event forward: thread_id=%s turn_id=%s event_type=%s elapsed=%s run_queue_len=%d run_queue_cap=%d thread_queue_len_before=%d thread_queue_cap=%d",
				t.ThreadID, turn.turnID, ev.Type, elapsed, runQueueLen, runQueueCap, threadQueueLen, threadQueueCap)
		}
	}
}

// finishTurn closes the logical turn and clears the thread's current turn only
// when it is still the same turn that was started.
func (t *DeepAgentThread) finishTurn(turn *turn, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	turn.complete(err)
	if t.current == turn {
		t.current = nil
	}
}
