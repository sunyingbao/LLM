package agentthread

import (
	"context"
	"time"

	"code.byted.org/gopkg/logs/v2"
)

// run executes one logical turn, drains all turn-local events, and only then
// releases the thread's active-run slot. Keeping this sequence together makes
// the completion boundary explicit: a turn is complete after execution and
// event forwarding have both finished.
func (t *DeepAgentThread) run(ctx context.Context, run *activeRun, runner *TurnRunner) {
	err := runner.RunTurn(ctx, run.input, run.turnRunOptions())
	runner.closeEventBus()
	<-run.eventsDrained
	t.finishRun(run, err)
}

// forwardRunEvents copies events from a single TurnRunner into the thread-wide
// event channel and attaches the inputs consumed by that logical turn.
func (t *DeepAgentThread) forwardRunEvents(run *activeRun) {
	defer close(run.eventsDrained)
	for ev := range run.events {
		ev.ConsumedInputs, ev.ConsumedInputsMeta = t.consumedInputsSnapshot(run)
		runQueueLen := len(run.events)
		runQueueCap := cap(run.events)
		threadQueueLen := len(t.evCh)
		threadQueueCap := cap(t.evCh)
		startedAt := time.Now()
		t.evCh <- ev
		if elapsed := time.Since(startedAt); elapsed > threadEventForwardWarnDuration {
			logs.CtxWarn(context.Background(), "[DeepAgentThread::forwardRunEvents] slow event forward: thread_id=%s turn_id=%s event_type=%s elapsed=%s run_queue_len=%d run_queue_cap=%d thread_queue_len_before=%d thread_queue_cap=%d",
				t.ThreadID, run.turnID, ev.Type, elapsed, runQueueLen, runQueueCap, threadQueueLen, threadQueueCap)
		}
	}
}

// finishRun closes the logical turn and clears the thread's current runner if
// it is still the same run that was started. The identity check prevents a
// stale goroutine from clearing a newer run.
func (t *DeepAgentThread) finishRun(run *activeRun, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	run.complete(err)
	if t.current == run {
		t.current = nil
		t.runner = nil
	}
}
