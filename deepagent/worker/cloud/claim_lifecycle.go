//go:build !windows

package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/coordinator"
	agentworker "eino-cli/deepagent/worker"
)

type claimResult struct {
	thread          *coordinator.Thread
	lease           *coordinator.Lease
	pendingMessages []*coordinator.Message
}

type claimActionKind int

const (
	claimActionRelease claimActionKind = iota + 1
	claimActionCompleteClose
)

type claimAction struct {
	kind             claimActionKind
	reason           string
	err              error
	block            *agentworker.PendingBlock
	controlMessageID int64
}

func newReleaseAction(reason string, err error) *claimAction {
	return &claimAction{
		kind:   claimActionRelease,
		reason: reason,
		err:    err,
	}
}

func newCompleteCloseAction(controlMessageID int64, reason string) *claimAction {
	if reason == "" {
		reason = defaultCloseThreadReason
	}
	return &claimAction{
		kind:             claimActionCompleteClose,
		reason:           reason,
		controlMessageID: controlMessageID,
	}
}

func (e *claimAction) releaseReason(extraErr error) string {
	if e.reason != "" {
		return e.reason
	}
	if e.err != nil || extraErr != nil {
		return defaultErrorReleaseReason
	}
	return defaultReleaseReason
}

type inputStopKind int

const (
	inputStopFailed inputStopKind = iota + 1
	inputStopCloseHandled
	inputStopThreadClosed
)

type inputStopReason struct {
	kind           inputStopKind
	reason         string
	err            error
	closeMessageID int64
}

type runtimeYield struct {
	reason string
	err    error
	block  *agentworker.PendingBlock
}

type claimProgress struct {
	lastInputUnixNano atomic.Int64
}

func (p *claimProgress) markInputActivity() {
	p.lastInputUnixNano.Store(time.Now().UnixNano())
}

// claimCoordinator owns the claim lifecycle decision. The input and output
// processors only report facts: input stopped because of delivery/control-plane
// state, or the runtime yielded through output. Before returning a claim action,
// the coordinator stops both processors, lets output drain ready items, and
// resolves one final control-plane action.
type claimCoordinator struct {
	worker      *Worker
	ctx         context.Context
	acceptDone  <-chan struct{}
	stopCh      chan struct{}
	stopOnce    sync.Once
	claim       *claimResult
	agentThread agentworker.AgentThread
	pollEvery   time.Duration
	input       *inputMessageProcessor
	output      *outputItemProcessor
	progress    *claimProgress
	idleSince   time.Time
	wasActive   bool
	seenInput   int64
}

func newClaimCoordinator(
	worker *Worker,
	ctx context.Context,
	acceptDone <-chan struct{},
	claim *claimResult,
	agentThread agentworker.AgentThread,
	items <-chan agentworker.ThreadOutputItem,
	pollEvery time.Duration,
) (coordinator *claimCoordinator) {
	progress := &claimProgress{}
	stopCh := make(chan struct{})
	output := &outputItemProcessor{
		items:  items,
		signal: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	input := &inputMessageProcessor{
		pending: claim.pendingMessages,
		signal:  make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	coordinator = &claimCoordinator{
		worker:      worker,
		ctx:         ctx,
		acceptDone:  acceptDone,
		stopCh:      stopCh,
		claim:       claim,
		agentThread: agentThread,
		pollEvery:   pollEvery,
		input:       input,
		output:      output,
		progress:    progress,
		idleSince:   time.Now(),
		wasActive:   agentThread.ActiveTurn() != nil,
	}
	input.lifecycle = coordinator
	output.lifecycle = coordinator
	return coordinator
}

func (c *claimCoordinator) run() (action *claimAction) {
	logs.CtxInfo(c.ctx,
		"[agentworker] claim coordinator start: thread_id=%d pending_message_count=%d poll_interval=%s initially_active=%t",
		c.claim.thread.ThreadID, len(c.input.pending), c.pollEvery, c.wasActive,
	)
	go c.output.run()
	go c.input.run()

	idleTicker := time.NewTicker(c.pollEvery)
	defer idleTicker.Stop()

	for {
		if c.acceptStopped() {
			return c.runDraining("accept context canceled")
		}
		if end := c.idleRelease(); end != nil {
			return c.finish(end)
		}

		select {
		case <-c.input.signal:
			return c.finish(nil)
		case <-c.output.signal:
			return c.finish(nil)
		case <-c.ctx.Done():
			return c.finish(newReleaseAction(defaultErrorReleaseReason, c.ctx.Err()))
		case <-c.acceptDone:
			return c.runDraining("accept context canceled")
		case <-idleTicker.C:
			continue
		}
	}
}

func (c *claimCoordinator) stopProcessors() (inputSignal *inputStopReason, outputSignal *runtimeYield) {
	logs.CtxInfo(c.ctx, "[agentworker] claim coordinator stop processors start: thread_id=%d", c.claim.thread.ThreadID)
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	<-c.input.done
	inputSignal = c.input.result
	<-c.output.done
	outputSignal = c.output.result
	logs.CtxInfo(c.ctx,
		"[agentworker] claim coordinator stop processors complete: thread_id=%d input_signal=%s output_signal=%s",
		c.claim.thread.ThreadID, inputSignalSummary(inputSignal), runtimeYieldSummary(outputSignal),
	)
	return inputSignal, outputSignal
}

func (c *claimCoordinator) actionFromInputStop(signal inputStopReason) *claimAction {
	switch signal.kind {
	case inputStopFailed:
		return newReleaseAction(signal.reason, signal.err)
	case inputStopCloseHandled:
		return newCompleteCloseAction(signal.closeMessageID, signal.reason)
	case inputStopThreadClosed:
		return newReleaseAction(signal.reason, nil)
	default:
		return newReleaseAction(defaultErrorReleaseReason, fmt.Errorf("unknown input process signal kind: %d", signal.kind))
	}
}

func (c *claimCoordinator) finish(requested *claimAction) (action *claimAction) {
	input, output := c.stopProcessors()
	action = c.selectAction(requested, input, output)
	logs.CtxInfo(c.ctx, "[agentworker] claim completion: action=%s", claimActionSummary(action))
	return action
}

func (c *claimCoordinator) selectAction(requested *claimAction, input *inputStopReason, output *runtimeYield) (action *claimAction) {
	if input != nil && (input.kind == inputStopFailed || input.kind == inputStopCloseHandled) {
		return c.actionFromInputStop(*input)
	}
	if output != nil {
		return &claimAction{kind: claimActionRelease, reason: output.reason, err: output.err, block: output.block}
	}
	if input != nil {
		return c.actionFromInputStop(*input)
	}
	if requested != nil {
		return requested
	}
	return newReleaseAction(defaultReleaseReason, nil)
}

func claimActionSummary(action *claimAction) string {
	if action == nil {
		return "{}"
	}
	data, err := json.Marshal(map[string]interface{}{
		"kind":               action.kind,
		"reason":             action.reason,
		"err":                errorString(action.err),
		"block_present":      action.block != nil,
		"control_message_id": action.controlMessageID,
	})
	if err != nil {
		return fmt.Sprintf("kind=%d reason=%q err=%v block_present=%t control_message_id=%d summary_error=%v",
			action.kind, action.reason, action.err, action.block != nil, action.controlMessageID, err)
	}
	return string(data)
}

func inputSignalSummary(signal *inputStopReason) string {
	if signal == nil {
		return "{}"
	}
	data, err := json.Marshal(map[string]interface{}{
		"kind":             signal.kind,
		"reason":           signal.reason,
		"err":              errorString(signal.err),
		"close_message_id": signal.closeMessageID,
	})
	if err != nil {
		return fmt.Sprintf("kind=%d reason=%q err=%v close_message_id=%d summary_error=%v",
			signal.kind, signal.reason, signal.err, signal.closeMessageID, err)
	}
	return string(data)
}

func runtimeYieldSummary(signal *runtimeYield) string {
	if signal == nil {
		return "{}"
	}
	data, err := json.Marshal(map[string]interface{}{
		"reason":        signal.reason,
		"err":           errorString(signal.err),
		"block_present": signal.block != nil,
	})
	if err != nil {
		return fmt.Sprintf("reason=%q err=%v block_present=%t summary_error=%v", signal.reason, signal.err, signal.block != nil, err)
	}
	return string(data)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (c *claimCoordinator) runDraining(reason string) (action *claimAction) {
	timeout := c.worker.shutdownDrainTimeout()
	start := time.Now()
	initialTurnID, initialConsumed := activeTurnForLog(c.agentThread.ActiveTurn())
	logs.CtxInfo(c.ctx,
		"[agentworker] worker shutdown drain start: reason=%s timeout=%s active_turn=%s consumed_message_ids=%v",
		reason, timeout, initialTurnID, initialConsumed,
	)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	poll := time.NewTicker(c.pollEvery)
	defer poll.Stop()

	for {
		active := c.agentThread.ActiveTurn()
		if active == nil {
			logs.CtxInfo(c.ctx, "[agentworker] worker shutdown drain complete: elapsed=%s", time.Since(start))
			return c.finish(newReleaseAction(defaultGracefulReleaseReason, nil))
		}

		select {
		case <-c.input.signal:
			return c.finish(nil)
		case <-c.output.signal:
			return c.finish(nil)
		case <-c.ctx.Done():
			activeTurnID, consumed := activeTurnForLog(c.agentThread.ActiveTurn())
			logs.CtxInfo(c.ctx,
				"[agentworker] worker shutdown drain context done: elapsed=%s err=%v active_turn=%s consumed_message_ids=%v",
				time.Since(start), c.ctx.Err(), activeTurnID, consumed,
			)
			return c.finish(newReleaseAction(defaultErrorReleaseReason, c.ctx.Err()))
		case <-poll.C:
			continue
		case <-timer.C:
			active := c.agentThread.ActiveTurn()
			activeTurnID, consumed := activeTurnForLog(active)
			if active != nil {
				c.interruptShutdownTimeout(active)
			}
			logs.CtxInfo(c.ctx,
				"[agentworker] worker shutdown drain timeout: elapsed=%s active_turn=%s consumed_message_ids=%v",
				time.Since(start), activeTurnID, consumed,
			)
			return c.runShutdownInterruptDraining(start)
		}
	}
}

func (c *claimCoordinator) interruptShutdownTimeout(active *agentworker.ActiveTurn) {
	if active == nil {
		return
	}
	runtimeInterruptTimeout := c.worker.runtimeInterruptTimeoutForDrain(c.worker.shutdownInterruptDrainTimeout())
	err := c.agentThread.Interrupt(c.ctx, agentworker.ThreadInterruptRequest{
		Kind:    agentworker.ThreadInterruptKindWorkerShutdownTimeout,
		Reason:  defaultShutdownTimeoutReason,
		Timeout: &runtimeInterruptTimeout,
	})
	if err != nil {
		logs.CtxError(c.ctx,
			"[agentworker] worker shutdown timeout interrupt failed: turn_id=%s consumed_message_ids=%v err=%v",
			active.TurnID, active.ConsumedMessageIDs, err,
		)
		return
	}
	logs.CtxInfo(c.ctx,
		"[agentworker] worker shutdown timeout interrupt requested: turn_id=%s consumed_message_ids=%v",
		active.TurnID, active.ConsumedMessageIDs,
	)
}

func (c *claimCoordinator) runShutdownInterruptDraining(shutdownStartedAt time.Time) (action *claimAction) {
	timeout := c.worker.shutdownInterruptDrainTimeout()
	logs.CtxInfo(c.ctx, "[agentworker] worker shutdown interrupt drain start: timeout=%s", timeout)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	poll := time.NewTicker(c.pollEvery)
	defer poll.Stop()

	for {
		select {
		case <-c.input.signal:
			return c.finish(nil)
		case <-c.output.signal:
			return c.finish(nil)
		case <-c.ctx.Done():
			activeTurnID, consumed := activeTurnForLog(c.agentThread.ActiveTurn())
			logs.CtxInfo(c.ctx,
				"[agentworker] worker shutdown interrupt drain context done: elapsed=%s err=%v active_turn=%s consumed_message_ids=%v",
				time.Since(shutdownStartedAt), c.ctx.Err(), activeTurnID, consumed,
			)
			return c.finish(newReleaseAction(defaultErrorReleaseReason, c.ctx.Err()))
		case <-poll.C:
			continue
		case <-timer.C:
			activeTurnID, consumed := activeTurnForLog(c.agentThread.ActiveTurn())
			logs.CtxInfo(c.ctx,
				"[agentworker] worker shutdown interrupt drain timeout: elapsed=%s active_turn=%s consumed_message_ids=%v",
				time.Since(shutdownStartedAt), activeTurnID, consumed,
			)
			return c.finish(newReleaseAction(defaultShutdownTimeoutReason, nil))
		}
	}
}

func activeTurnForLog(active *agentworker.ActiveTurn) (string, []string) {
	if active == nil {
		return "", nil
	}
	return active.TurnID, append([]string(nil), active.ConsumedMessageIDs...)
}

func (c *claimCoordinator) acceptStopped() bool {
	select {
	case <-c.acceptDone:
		return true
	default:
		return false
	}
}

func (c *claimCoordinator) idleRelease() *claimAction {
	c.observeInputProgress()
	active := c.agentThread.ActiveTurn() != nil
	if active {
		c.wasActive = true
		return nil
	}
	if c.wasActive {
		c.wasActive = false
		c.idleSince = time.Now()
		logs.CtxInfo(c.ctx, "[agentworker] thread became inactive, start idle timeout")
		return nil
	}
	idleTimeout := c.worker.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	if time.Since(c.idleSince) < idleTimeout {
		return nil
	}
	logs.CtxInfo(c.ctx, "[agentworker] thread idle timeout reached")
	return newReleaseAction(defaultReleaseReason, nil)
}

func (c *claimCoordinator) observeInputProgress() {
	lastInput := c.progress.lastInputUnixNano.Load()
	if lastInput == 0 || lastInput == c.seenInput {
		return
	}
	c.seenInput = lastInput
	c.idleSince = time.Unix(0, lastInput)
	c.wasActive = c.agentThread.ActiveTurn() != nil
}
