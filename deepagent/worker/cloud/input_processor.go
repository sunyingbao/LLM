//go:build !windows

package cloud

import (
	"context"
	"errors"
	"fmt"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/coordinator"
	agentworker "eino-cli/deepagent/worker"
)

type inputMessageProcessor struct {
	lifecycle *claimCoordinator
	pending   []*coordinator.Message
	signal    chan struct{}
	done      chan struct{}
	result    *inputStopReason
}

func (p *inputMessageProcessor) run() {
	defer close(p.done)
	logs.CtxInfo(p.lifecycle.ctx, "[agentworker] input processor start: thread_id=%d initial_pending_count=%d poll_interval=%s",
		p.lifecycle.claim.thread.ThreadID, len(p.pending), p.lifecycle.pollEvery)
	defer func() {
		logs.CtxInfo(p.lifecycle.ctx, "[agentworker] input processor stopped: thread_id=%d remaining_pending_count=%d", p.lifecycle.claim.thread.ThreadID, len(p.pending))
	}()
	pollTicker := time.NewTicker(p.lifecycle.pollEvery)
	defer pollTicker.Stop()
	consecutivePullErrors := 0

	for {
		if signal := p.handlePendingMessages(); signal != nil {
			p.finish(signal)
			return
		}

		select {
		case <-p.lifecycle.ctx.Done():
			return
		case <-p.lifecycle.stopCh:
			return
		case <-p.lifecycle.acceptDone:
			return
		case <-pollTicker.C:
			pullCtx := p.lifecycle.worker.newPullLogContext(p.lifecycle.ctx, p.lifecycle.claim.lease)
			messages, err := p.lifecycle.worker.pullPendingMessages(pullCtx, p.lifecycle.claim.lease)
			if err != nil {
				if p.lifecycle.ctx.Err() != nil {
					return
				}
				consecutivePullErrors++
				logs.CtxError(pullCtx,
					"[agentworker] pull pending messages failed, will retry without stopping active claim: consecutive_errors=%d err=%v",
					consecutivePullErrors, err,
				)
				if p.waitPullRetryBackoff(consecutivePullErrors) {
					return
				}
				continue
			}
			consecutivePullErrors = 0
			if len(messages) == 0 {
				continue
			}
			logs.CtxInfo(pullCtx, "[agentworker] pull pending messages success: count=%d message_ids=%v", len(messages), messageIDs(messages))
			p.pending = append(p.pending, messages...)
		}
	}
}

func (p *inputMessageProcessor) waitPullRetryBackoff(consecutiveErrors int) bool {
	backoff := pullErrorBackoff(p.lifecycle.pollEvery, consecutiveErrors)
	if backoff <= 0 {
		return false
	}
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-p.lifecycle.ctx.Done():
		return true
	case <-p.lifecycle.stopCh:
		return true
	case <-p.lifecycle.acceptDone:
		return true
	case <-timer.C:
		return false
	}
}

func pullErrorBackoff(base time.Duration, consecutiveErrors int) time.Duration {
	if consecutiveErrors <= 0 {
		return 0
	}
	if base <= 0 {
		base = defaultMessagePollInterval
	}
	backoff := base
	for i := 1; i < consecutiveErrors && backoff < maxPullErrorBackoff; i++ {
		backoff *= 2
	}
	if backoff > maxPullErrorBackoff {
		return maxPullErrorBackoff
	}
	return backoff
}

func (p *inputMessageProcessor) finish(signal *inputStopReason) {
	if signal == nil {
		return
	}
	if p.result == nil {
		copy := *signal
		p.result = &copy
	}
	p.sendSignal()
}

func (p *inputMessageProcessor) sendSignal() {
	select {
	case p.signal <- struct{}{}:
	case <-p.lifecycle.ctx.Done():
	case <-p.lifecycle.stopCh:
	}
}

// handlePendingMessages posts locally buffered AC messages to runtime and acks
// them after runtime accepts the post. Once the processor is stopped, remaining
// local pending messages stay unacked so the next worker can claim them.
func (p *inputMessageProcessor) handlePendingMessages() *inputStopReason {
	for len(p.pending) > 0 {
		select {
		case <-p.lifecycle.ctx.Done():
			return nil
		case <-p.lifecycle.stopCh:
			return nil
		case <-p.lifecycle.acceptDone:
			return nil
		default:
		}
		message := p.pending[0]
		p.pending = p.pending[1:]
		if message == nil {
			continue
		}
		signal := p.handlePendingMessage(message)
		if signal != nil {
			return signal
		}
	}
	return nil
}

func (p *inputMessageProcessor) handlePendingMessage(message *coordinator.Message) *inputStopReason {
	messageCtx := p.lifecycle.worker.newMessageLogContext(p.lifecycle.ctx, p.lifecycle.claim.thread, message)
	logs.CtxInfo(messageCtx, "[agentworker] handle pending message: message_id=%d message_type=%s payload_bytes=%d metadata_keys=%v",
		message.MessageID, message.MessageType, len(message.Payload), metadataKeys(message.Metadata))
	switch message.MessageType {
	case MessageTypeControlCancelInput:
		return p.handleCancelInputControl(messageCtx, message)
	case MessageTypeControlCloseThread:
		return p.handleCloseThreadControl(messageCtx, message)
	}
	logs.CtxInfo(messageCtx, "[agentworker] enqueue message start: message=%s", messagePreview(message))
	messageRuntimeCtx := contextWithMessageRequestMeta(messageCtx, message)
	result, err := p.lifecycle.worker.enqueueMessage(messageRuntimeCtx, p.lifecycle.agentThread, p.lifecycle.claim.thread, message)
	if err != nil {
		if p.lifecycle.ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, agentworker.ErrThreadClosed) {
			logs.CtxInfo(messageCtx, "[agentworker] runtime rejected message because thread is closed, leave message pending and release: %v", err)
			return &inputStopReason{kind: inputStopThreadClosed, reason: defaultThreadClosedReason}
		}
		logs.CtxError(messageCtx, "[agentworker] enqueue message failed: %v", err)
		return &inputStopReason{kind: inputStopFailed, reason: postMessageFailedReason, err: err}
	}
	ackCtx := p.lifecycle.worker.newMessageLogContext(p.lifecycle.ctx, p.lifecycle.claim.thread, message)
	triggerTurnID := ""
	if result != nil {
		triggerTurnID = result.TurnID
	}
	logs.CtxInfo(ackCtx, "[agentworker] ack message start: message_id=%d trigger_turn_id=%q", message.MessageID, triggerTurnID)
	if err := p.lifecycle.worker.ackMessage(ackCtx, p.lifecycle.claim.lease, message.MessageID, triggerTurnID); err != nil {
		logs.CtxError(ackCtx, "[agentworker] ack message failed: message_id=%d trigger_turn_id=%q err=%v", message.MessageID, triggerTurnID, err)
		return &inputStopReason{kind: inputStopFailed, reason: ackMessageFailedReason, err: err}
	}
	p.lifecycle.progress.markInputActivity()
	logs.CtxInfo(messageCtx, "[agentworker] enqueue message success")
	return nil
}

func (p *inputMessageProcessor) handleCancelInputControl(ctx context.Context, message *coordinator.Message) *inputStopReason {
	logs.CtxInfo(ctx, "[agentworker] handle cancel input control start")
	payload, parseErr := parseCancelInputPayload(message.Payload)
	ackCancelControl := func(phase string) *inputStopReason {
		ackCtx := p.lifecycle.worker.newMessageLogContext(p.lifecycle.ctx, p.lifecycle.claim.thread, message)
		logs.CtxInfo(ackCtx, "[agentworker] ack cancel input control start: phase=%s", phase)
		if err := p.lifecycle.worker.ackMessage(ackCtx, p.lifecycle.claim.lease, message.MessageID, ""); err != nil {
			logs.CtxError(ackCtx, "[agentworker] ack cancel input control failed: phase=%s err=%v", phase, err)
			return &inputStopReason{kind: inputStopFailed, reason: ackMessageFailedReason, err: err}
		}
		logs.CtxInfo(ackCtx, "[agentworker] ack cancel input control success: phase=%s", phase)
		return nil
	}
	if parseErr != nil || payload.CutoffMessageID <= 0 {
		err := parseErr
		if err == nil {
			err = fmt.Errorf("cancel input control missing cutoff_message_id")
		}
		p.pending = nil
		logs.CtxError(ctx, "[agentworker] cancel input control invalid, drop local pending and release: err=%v", err)
		if ackSignal := ackCancelControl("invalid_payload"); ackSignal != nil {
			return ackSignal
		}
		return &inputStopReason{kind: inputStopFailed, reason: controlInputFailedReason, err: err}
	}

	p.dropCanceledLocalPending(payload.CutoffMessageID)
	reason := payload.Reason
	if reason == "" {
		reason = "user_cancel"
	}
	if p.lifecycle.agentThread.ActiveTurn() == nil {
		logs.CtxInfo(ctx, "[agentworker] cancel input control complete without active turn")
		if ackSignal := ackCancelControl("no_active_turn"); ackSignal != nil {
			return ackSignal
		}
		return nil
	}
	runtimeCtx := contextWithMessageRequestMeta(ctx, message)
	logs.CtxInfo(ctx, "[agentworker] cancel input interrupt start: cutoff_message_id=%d reason=%q", payload.CutoffMessageID, reason)
	runtimeInterruptTimeout := p.lifecycle.worker.runtimeInterruptTimeout()
	if err := p.lifecycle.agentThread.Interrupt(runtimeCtx, agentworker.ThreadInterruptRequest{
		Kind:             agentworker.ThreadInterruptKindCancelInput,
		ControlMessageID: fmt.Sprint(message.MessageID),
		CutoffMessageID:  fmt.Sprint(payload.CutoffMessageID),
		Reason:           reason,
		Timeout:          &runtimeInterruptTimeout,
	}); err != nil {
		if p.lifecycle.ctx.Err() != nil {
			return nil
		}
		err = fmt.Errorf("AgentThread.Interrupt cancel_input: %w", err)
		logs.CtxError(ctx, "[agentworker] cancel input interrupt failed, leave control pending for retry: err=%v", err)
		return &inputStopReason{kind: inputStopFailed, reason: interruptFailedReason, err: err}
	}
	logs.CtxInfo(ctx, "[agentworker] cancel input interrupt accepted, wait for runtime drain: timeout_ms=%d", p.lifecycle.worker.interruptDrainTimeout().Milliseconds())
	timedOut, err := p.waitForInterruptDrain()
	if err != nil {
		if p.lifecycle.ctx.Err() != nil {
			return nil
		}
		logs.CtxError(ctx, "[agentworker] cancel input interrupt drain failed, leave control pending for retry: err=%v", err)
		return &inputStopReason{kind: inputStopFailed, reason: interruptFailedReason, err: err}
	}
	if timedOut {
		logs.CtxError(ctx, "[agentworker] cancel input interrupt drain timeout, leave control pending for retry")
		return &inputStopReason{kind: inputStopFailed, reason: defaultInterruptTimeoutReason}
	}
	if ackSignal := ackCancelControl("interrupt_drained"); ackSignal != nil {
		return ackSignal
	}
	logs.CtxInfo(ctx, "[agentworker] handle cancel input control success")
	return nil
}

func (p *inputMessageProcessor) handleCloseThreadControl(ctx context.Context, message *coordinator.Message) (stop *inputStopReason) {
	logs.CtxInfo(ctx, "[agentworker] handle close thread control start")
	reason := defaultCloseThreadReason
	payload, parseErr := parseCloseThreadPayload(message.Payload)
	if parseErr != nil {
		logs.CtxError(ctx, "[agentworker] close thread control payload invalid, continue with default reason: err=%v", parseErr)
	} else if payload.Reason != "" {
		reason = payload.Reason
	}
	p.pending = nil

	if p.lifecycle.agentThread.ActiveTurn() != nil {
		runtimeCtx := contextWithMessageRequestMeta(ctx, message)
		runtimeInterruptTimeout := p.lifecycle.worker.runtimeInterruptTimeout()
		if err := p.lifecycle.agentThread.Interrupt(runtimeCtx, agentworker.ThreadInterruptRequest{
			Kind:             agentworker.ThreadInterruptKindCloseThread,
			ControlMessageID: fmt.Sprint(message.MessageID),
			Reason:           reason,
			Timeout:          &runtimeInterruptTimeout,
		}); err != nil {
			logs.CtxError(ctx, "[agentworker] close thread interrupt failed, continue close: err=%v", err)
		}
		if timedOut, err := p.waitForInterruptDrain(); err != nil {
			logs.CtxError(ctx, "[agentworker] close thread interrupt drain failed, continue close: err=%v", err)
		} else if timedOut {
			logs.CtxError(ctx, "[agentworker] close thread interrupt drain timeout, continue close")
		}
	}

	logs.CtxInfo(ctx, "[agentworker] handle close thread control success")
	return &inputStopReason{kind: inputStopCloseHandled, reason: reason, closeMessageID: message.MessageID}
}

func (p *inputMessageProcessor) dropCanceledLocalPending(cutoffMessageID int64) {
	if len(p.pending) == 0 {
		return
	}
	filtered := p.pending[:0]
	for _, message := range p.pending {
		if message == nil {
			continue
		}
		if isOrdinaryMessage(message) && message.MessageID <= cutoffMessageID {
			continue
		}
		filtered = append(filtered, message)
	}
	p.pending = filtered
}

func isOrdinaryMessage(message *coordinator.Message) bool {
	return message != nil && !IsControlMessageType(message.MessageType)
}

func (p *inputMessageProcessor) waitForInterruptDrain() (bool, error) {
	timeout := p.lifecycle.worker.interruptDrainTimeout()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	pollInterval := p.lifecycle.worker.MessagePollInterval
	if pollInterval <= 0 {
		pollInterval = defaultMessagePollInterval
	}
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()

	for {
		if p.lifecycle.agentThread.ActiveTurn() == nil {
			return false, nil
		}
		select {
		case <-p.lifecycle.ctx.Done():
			return false, p.lifecycle.ctx.Err()
		case <-p.lifecycle.stopCh:
			return false, nil
		case <-poll.C:
			continue
		case <-timer.C:
			return true, nil
		}
	}
}
