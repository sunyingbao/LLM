//go:build !windows

package thread

import (
	"context"
	"errors"
	"fmt"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/worker"
)

type compactOperation struct {
	turnID             string
	consumedMessageIDs []string
	consumedInputsMeta []any
	cancel             context.CancelFunc
	interrupted        bool
	interrupt          agentworker.ThreadInterruptRequest
}

func (t *Runtime) postCompact(ctx context.Context, cmd compactCommand) error {
	turnID := cmd.turnID
	if t.thread.ActiveTurn() != nil || t.activeCompact() != nil {
		logs.CtxInfo(ctx, "[cloudagent] compact rejected because thread is running: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, cmd.messageID(), turnID)
		t.emitAgentEvent(ctx, agentthread.Event{
			ID:                 t.eventID(turnID),
			TS:                 time.Now(),
			ThreadID:           t.threadID,
			TurnID:             turnID,
			Type:               agentthread.EventError,
			Payload:            agentthread.ErrorPayload{Message: "compact rejected: thread is running"},
			ConsumedInputs:     cmd.consumedInputs,
			ConsumedInputsMeta: cmd.consumedInputsMeta,
		})
		return nil
	}

	compactCtx, cancel := context.WithCancel(ctx)
	op := &compactOperation{
		turnID:             turnID,
		consumedMessageIDs: cmd.consumedMessageIDs,
		consumedInputsMeta: cmd.consumedInputsMeta,
		cancel:             cancel,
	}
	if !t.beginCompact(op) {
		cancel()
		logs.CtxInfo(ctx, "[cloudagent] compact rejected by active state: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, cmd.messageID(), turnID)
		t.emitAgentEvent(ctx, agentthread.Event{
			ID:                 t.eventID(turnID),
			TS:                 time.Now(),
			ThreadID:           t.threadID,
			TurnID:             turnID,
			Type:               agentthread.EventError,
			Payload:            agentthread.ErrorPayload{Message: "compact rejected: thread is running"},
			ConsumedInputs:     cmd.consumedInputs,
			ConsumedInputsMeta: cmd.consumedInputsMeta,
		})
		return nil
	}
	defer t.finishCompact(op)
	defer cancel()

	logs.CtxInfo(ctx, "[cloudagent] compact started: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, cmd.messageID(), turnID)
	t.emitAgentEvent(ctx, agentthread.Event{
		ID:       t.eventID(turnID),
		TS:       time.Now(),
		ThreadID: t.threadID,
		TurnID:   turnID,
		Type:     agentthread.EventContextCompactStarted,
		Payload: agentthread.ContextCompactStartedPayload{
			ContextUsage: t.thread.ContextManager().ContextUsage(),
		},
		ConsumedInputs:     cmd.consumedInputs,
		ConsumedInputsMeta: cmd.consumedInputsMeta,
	})
	payload, err := t.thread.CompactWithTurnID(compactCtx, turnID)
	if err != nil {
		if errors.Is(err, agentthread.ErrThreadRunning) {
			t.emitAgentEvent(ctx, agentthread.Event{
				ID:                 t.eventID(turnID),
				TS:                 time.Now(),
				ThreadID:           t.threadID,
				TurnID:             turnID,
				Type:               agentthread.EventError,
				Payload:            agentthread.ErrorPayload{Message: "compact rejected: thread is running"},
				ConsumedInputs:     cmd.consumedInputs,
				ConsumedInputsMeta: cmd.consumedInputsMeta,
			})
			return nil
		}
		if req, ok := t.compactInterruptRequest(op); ok {
			logs.CtxInfo(ctx, "[cloudagent] compact interrupted: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s kind=%s", t.sessionID, t.threadID, cmd.messageID(), turnID, req.Kind)
			t.emitCompactInterruptedEvent(context.WithoutCancel(ctx), op, req)
			return nil
		}
		logs.CtxError(ctx, "[cloudagent] compact failed: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s err=%v", t.sessionID, t.threadID, cmd.messageID(), turnID, err)
		t.emitAgentEvent(ctx, agentthread.Event{
			ID:                 t.eventID(turnID),
			TS:                 time.Now(),
			ThreadID:           t.threadID,
			TurnID:             turnID,
			Type:               agentthread.EventError,
			Payload:            agentthread.ErrorPayload{Message: fmt.Sprintf("compact failed: %v", err)},
			ConsumedInputs:     cmd.consumedInputs,
			ConsumedInputsMeta: cmd.consumedInputsMeta,
		})
		return nil
	}
	if payload == nil {
		logs.CtxInfo(ctx, "[cloudagent] compact skipped: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, cmd.messageID(), turnID)
		t.emitAgentEvent(ctx, agentthread.Event{
			ID:                 t.eventID(turnID),
			TS:                 time.Now(),
			ThreadID:           t.threadID,
			TurnID:             turnID,
			Type:               agentthread.EventError,
			Payload:            agentthread.ErrorPayload{Message: "compact skipped: no compaction produced a new context"},
			ConsumedInputs:     cmd.consumedInputs,
			ConsumedInputsMeta: cmd.consumedInputsMeta,
		})
		return nil
	}
	logs.CtxInfo(ctx, "[cloudagent] compact finished: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, cmd.messageID(), turnID)
	t.emitAgentEvent(ctx, agentthread.Event{
		ID:                 t.eventID(turnID),
		TS:                 time.Now(),
		ThreadID:           t.threadID,
		TurnID:             turnID,
		Type:               agentthread.EventContextCompacted,
		Payload:            *payload,
		ConsumedInputs:     cmd.consumedInputs,
		ConsumedInputsMeta: cmd.consumedInputsMeta,
	})
	return nil
}

func (t *Runtime) beginCompact(op *compactOperation) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.compact != nil || t.thread.ActiveTurn() != nil {
		return false
	}
	t.compact = op
	return true
}

func (t *Runtime) finishCompact(op *compactOperation) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.compact == op {
		t.compact = nil
	}
}

func (t *Runtime) activeCompact() *compactOperation {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.compact == nil {
		return nil
	}
	copy := *t.compact
	copy.consumedMessageIDs = append([]string(nil), t.compact.consumedMessageIDs...)
	copy.consumedInputsMeta = append([]any(nil), t.compact.consumedInputsMeta...)
	return &copy
}

func (t *Runtime) interruptCompact(_ context.Context, req agentworker.ThreadInterruptRequest) bool {
	t.mu.Lock()
	op := t.compact
	if op == nil {
		t.mu.Unlock()
		return false
	}
	if op.interrupted {
		t.mu.Unlock()
		return true
	}
	op.interrupted = true
	op.interrupt = req
	cancel := op.cancel
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return true
}

func (t *Runtime) compactInterruptRequest(op *compactOperation) (agentworker.ThreadInterruptRequest, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.compact != op || !op.interrupted {
		return agentworker.ThreadInterruptRequest{}, false
	}
	return op.interrupt, true
}

func (t *Runtime) emitCompactInterruptedEvent(ctx context.Context, op *compactOperation, req agentworker.ThreadInterruptRequest) {
	if op == nil {
		return
	}
	t.emitAgentEvent(ctx, agentthread.Event{
		ID:                 t.eventID(op.turnID),
		TS:                 time.Now(),
		ThreadID:           t.threadID,
		TurnID:             op.turnID,
		Type:               agentEventContextCompactInterrupted,
		Payload:            newContextCompactInterruptedPayload(req),
		ConsumedInputs:     compactConsumedInputsFromIDs(op.consumedMessageIDs),
		ConsumedInputsMeta: op.consumedInputsMeta,
	})
}
