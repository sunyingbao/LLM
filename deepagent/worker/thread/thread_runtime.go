//go:build !windows

package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/core/agentthread"
	agentworker "eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/thread/runtimectx"

	"code.byted.org/gopkg/logs/v2"
)

// ApprovalRememberer records a session-scoped approval reuse decision.
type ApprovalRememberer interface {
	RememberApproval(ctx context.Context, payload protoinput.ResumeTurnPayload)
}

type ApprovalRemembererFunc func(ctx context.Context, payload protoinput.ResumeTurnPayload)

func (f ApprovalRemembererFunc) RememberApproval(ctx context.Context, payload protoinput.ResumeTurnPayload) {
	f(ctx, payload)
}

// TurnFinishedObserver is called after a DeepAgent turn-end event is converted
// to worker output. Implementations should return quickly.
type TurnFinishedObserver func(ctx context.Context, ev agentthread.Event)

// ThreadOutputObservation is a read-only snapshot of one worker output item
// emitted by the CloudAgent thread runtime.
type ThreadOutputObservation struct {
	SessionID string
	ThreadID  string
	Item      agentworker.ThreadOutputItem
}

// ThreadOutputObserver is called after the CloudAgent thread runtime has
// successfully offered one output item to the worker host. Implementations
// should return quickly and must not rely on mutating the observed item.
type ThreadOutputObserver func(ctx context.Context, obs ThreadOutputObservation)

// InterruptResumeDecoder converts a generic CloudAgent interrupt resume payload
// into the typed data expected by a custom Eino interrupt handler.
type InterruptResumeDecoder func(ctx context.Context, payload protoinput.ResumeTurnPayload) (any, error)

// Runtime adapts DeepAgentThread to agentworker.AgentThread.
// It owns the common worker-facing protocol: structured user input, resume
// input, manual compaction, interrupt, active turn snapshot, and ordered output
// forwarding.
type Runtime struct {
	sessionID  string
	threadID   string
	threadInfo runtimectx.ThreadIdentity
	thread     *agentthread.DeepAgentThread

	turnConfig           func(context.Context, TurnStartRequest) (*agentthread.TurnConfig, error)
	approvalRemember     ApprovalRememberer
	turnFinishedObserver TurnFinishedObserver
	threadOutputObserver ThreadOutputObserver
	interruptResume      InterruptResumeDecoder
	outputConfig         OutputConfig
	observerQueue        chan ThreadOutputObservation
	observerOnce         sync.Once
	observerCancel       context.CancelFunc

	outputBridge *threadOutputBridge

	mu       sync.Mutex
	claimCtx context.Context
	closed   bool
	compact  *compactOperation
}

type TurnStartRequest struct {
	TurnID  string
	Mode    protoinput.UserMessageMode
	Message *agentworker.Message
	Resume  bool
}

type AdapterConfig struct {
	SessionID string
	ThreadID  string
	Thread    *agentthread.DeepAgentThread
	EventBus  <-chan agentthread.Event

	// ThreadInfo is the stable CloudAgent thread identity injected into the
	// DeepAgent run context. ThreadID and SessionID are defaulted from the
	// adapter config when left empty.
	ThreadInfo runtimectx.ThreadIdentity

	TurnConfig           func(context.Context, TurnStartRequest) (*agentthread.TurnConfig, error)
	ApprovalRemember     ApprovalRememberer
	TurnFinishedObserver TurnFinishedObserver
	ThreadOutputObserver ThreadOutputObserver
	InterruptResume      InterruptResumeDecoder
	Output               OutputConfig
}

func NewRuntime(cfg AdapterConfig) (*Runtime, error) {
	if cfg.Thread == nil {
		return nil, fmt.Errorf("cloudagent: deep agent thread is required")
	}
	if cfg.EventBus == nil {
		return nil, fmt.Errorf("cloudagent: event bus is required")
	}
	threadID := cfg.ThreadID
	if threadID == "" {
		threadID = cfg.Thread.ThreadID
	}
	if threadID == "" {
		return nil, fmt.Errorf("cloudagent: thread id is required")
	}
	threadInfo := cfg.ThreadInfo
	if threadInfo.ThreadID == "" {
		threadInfo.ThreadID = threadID
	}
	if threadInfo.SessionID == "" {
		threadInfo.SessionID = cfg.SessionID
	}
	return &Runtime{
		sessionID:            cfg.SessionID,
		threadID:             threadID,
		threadInfo:           threadInfo,
		thread:               cfg.Thread,
		turnConfig:           cfg.TurnConfig,
		approvalRemember:     cfg.ApprovalRemember,
		turnFinishedObserver: cfg.TurnFinishedObserver,
		threadOutputObserver: cfg.ThreadOutputObserver,
		interruptResume:      cfg.InterruptResume,
		outputConfig:         cloneOutputConfig(cfg.Output),
		outputBridge:         &threadOutputBridge{agentEvents: cfg.EventBus},
	}, nil
}

func (t *Runtime) Init(ctx context.Context) (*agentworker.ThreadOutput, error) {
	ctx = t.withThreadInfo(ctx)
	if err := t.ensureOpen(); err != nil {
		return nil, err
	}
	if err := t.thread.Init(ctx); err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.outputBridge.output != nil {
		return t.outputBridge.output, nil
	}
	t.claimCtx = ctx
	logs.CtxInfo(ctx, "[cloudagent] init thread runtime: 对话流ID=%s thread_id=%s", t.sessionID, t.threadID)
	t.startThreadOutputObserver(ctx)
	return t.outputBridge.start(ctx, t), nil
}

func (t *Runtime) PostMessage(ctx context.Context, message *agentworker.Message) (*agentworker.PostMessageResult, error) {
	ctx = t.withThreadInfo(ctx)
	if message == nil {
		return nil, fmt.Errorf("worker message is required")
	}
	if err := t.ensureOpen(); err != nil {
		logs.CtxError(ctx, "[cloudagent] reject message after thread closed: 对话流ID=%s thread_id=%s message_id=%s message_type=%s", t.sessionID, t.threadID, message.ID, message.Type)
		return nil, err
	}
	logs.CtxInfo(ctx, "[cloudagent] post message: 对话流ID=%s thread_id=%s message_id=%s message_type=%s", t.sessionID, t.threadID, message.ID, message.Type)
	switch message.Type {
	case MessageTypeInput:
		cmd, err := decodeUserInputCommand(message)
		if err != nil {
			return nil, err
		}
		return t.postUserInput(ctx, cmd)
	case MessageTypeResumeTurn:
		cmd, err := decodeResumeTurnCommand(message)
		if err != nil {
			return nil, err
		}
		return t.postResumeTurn(ctx, cmd)
	case MessageTypeCompact:
		cmd := decodeCompactCommand(message)
		if err := t.postCompact(ctx, cmd); err != nil {
			return nil, err
		}
		return &agentworker.PostMessageResult{TurnID: cmd.turnID}, nil
	default:
		return nil, unsupportedRuntimeCommand(message)
	}
}

func (t *Runtime) Interrupt(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
	ctx = t.withThreadInfo(ctx)
	logs.CtxInfo(ctx, "[cloudagent] interrupt thread: 对话流ID=%s thread_id=%s kind=%s control_message_id=%s cutoff_message_id=%s", t.sessionID, t.threadID, req.Kind, req.ControlMessageID, req.CutoffMessageID)
	if t.interruptCompact(ctx, req) {
		return nil
	}
	if t.thread.Interrupt(agentthread.InterruptOptions{Timeout: req.Timeout, Metadata: threadInterruptMetadata(req)}) {
		return nil
	}
	if t.ActiveTurn() == nil {
		return nil
	}
	return fmt.Errorf("interrupt active turn failed: kind=%s control_message_id=%s", req.Kind, req.ControlMessageID)
}

func (t *Runtime) ActiveTurn() *agentworker.ActiveTurn {
	if t == nil || t.thread == nil {
		return nil
	}
	if compact := t.activeCompact(); compact != nil {
		return &agentworker.ActiveTurn{
			TurnID:             compact.turnID,
			ConsumedMessageIDs: append([]string(nil), compact.consumedMessageIDs...),
		}
	}
	curTurn := t.thread.ActiveTurn()
	if curTurn == nil {
		return nil
	}
	return &agentworker.ActiveTurn{
		TurnID:             curTurn.TurnID(),
		ConsumedMessageIDs: ConsumedMessageIDs(curTurn.ConsumedInputs()),
	}
}

func (t *Runtime) Close(ctx context.Context) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	bridge := t.outputBridge
	cancelObserver := t.observerCancel
	t.mu.Unlock()
	logs.CtxInfo(ctx, "[cloudagent] close thread runtime: 对话流ID=%s thread_id=%s", t.sessionID, t.threadID)
	if cancelObserver != nil {
		cancelObserver()
	}
	if bridge != nil {
		bridge.stopAndWait()
	}
	return nil
}

func (t *Runtime) postUserInput(ctx context.Context, cmd userInputCommand) (posted *agentworker.PostMessageResult, err error) {
	opts := []agentthread.SubmitInputOption{}
	if cmd.message != nil && len(cmd.message.Metadata) > 0 {
		opts = append(opts, agentthread.WithInputMeta(maps.Clone(cmd.message.Metadata)))
	}
	opts = append(opts, agentthread.WithTurnStartHook(func(runCtx context.Context, req agentthread.TurnStartRequest) context.Context {
		return runtimectx.ContextWithTurnIdentity(runCtx, runtimectx.TurnIdentity{
			ThreadID:  t.threadInfo.ThreadID,
			TurnID:    req.TurnID,
			MessageID: workerMessageID(cmd.message),
		})
	}))
	if t.turnConfig != nil {
		opts = append(opts, agentthread.WithTurnConfigProvider(func(ctx context.Context, req agentthread.TurnStartRequest) (*agentthread.TurnConfig, error) {
			return t.runConfig(ctx, req.TurnID, cmd.message, cmd.mode, false)
		}))
	}
	result, err := t.thread.SubmitInput(ctx, cmd.schema, opts...)
	if err != nil {
		return nil, fmt.Errorf("submit input: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("submit input returned nil result")
	}
	if result.Started {
		logs.CtxInfo(ctx, "[cloudagent] turn submitted: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, workerMessageID(cmd.message), result.TurnID)
		t.waitSubmittedTurn(ctx, result.TurnHandle)
	}
	posted = &agentworker.PostMessageResult{TurnID: result.TurnID}
	return posted, nil
}

func (t *Runtime) postResumeTurn(ctx context.Context, cmd resumeTurnCommand) (posted *agentworker.PostMessageResult, err error) {
	payload := cmd.payload
	if payload.Approval != nil && payload.Approval.CancelTurn {
		t.emitCancelTurnEvents(ctx, payload)
		return &agentworker.PostMessageResult{TurnID: payload.TurnID}, nil
	}
	if payload.Interrupt != nil {
		logs.CtxInfo(ctx,
			"[cloudagent] interrupt resume received: 对话流ID=%s thread_id=%s turn_id=%s interrupt_id=%s checkpoint_id=%s kind=%s info_type=%s data_bytes=%d",
			t.sessionID, t.threadID, payload.TurnID, payload.InterruptID, payload.CheckpointID, payload.Interrupt.Kind, payload.Interrupt.InfoType, len(payload.Interrupt.Data),
		)
	}
	resumeData, err := t.resumeData(ctx, payload)
	if err != nil {
		if payload.Interrupt != nil {
			logs.CtxError(ctx,
				"[cloudagent] interrupt resume decode failed: 对话流ID=%s thread_id=%s turn_id=%s interrupt_id=%s checkpoint_id=%s kind=%s info_type=%s err=%v",
				t.sessionID, t.threadID, payload.TurnID, payload.InterruptID, payload.CheckpointID, payload.Interrupt.Kind, payload.Interrupt.InfoType, err,
			)
		}
		return nil, err
	}
	if payload.Interrupt != nil {
		logs.CtxInfo(ctx,
			"[cloudagent] interrupt resume decoded: 对话流ID=%s thread_id=%s turn_id=%s interrupt_id=%s checkpoint_id=%s kind=%s info_type=%s resume_data_type=%T",
			t.sessionID, t.threadID, payload.TurnID, payload.InterruptID, payload.CheckpointID, payload.Interrupt.Kind, payload.Interrupt.InfoType, resumeData[payload.InterruptID],
		)
	}
	if payload.Approval != nil && payload.Approval.AllowInSession && payload.Approval.Approved && t.approvalRemember != nil {
		t.approvalRemember.RememberApproval(ctx, payload)
	}

	opts := agentthread.ResumeTurnOptions{
		CheckpointID:       payload.CheckpointID,
		ResumeInterruptIDs: []string{payload.InterruptID},
		ResumeData:         resumeData,
		OnTurnStart: func(runCtx context.Context, req agentthread.TurnStartRequest) context.Context {
			return runtimectx.ContextWithTurnIdentity(runCtx, runtimectx.TurnIdentity{
				ThreadID:  t.threadInfo.ThreadID,
				TurnID:    req.TurnID,
				MessageID: workerMessageID(cmd.message),
			})
		},
	}
	if t.turnConfig != nil {
		opts.ConfigProvider = func(ctx context.Context, req agentthread.TurnStartRequest) (*agentthread.TurnConfig, error) {
			return t.runConfig(ctx, req.TurnID, cmd.message, cmd.mode, true)
		}
	}
	curTurn, err := t.thread.ResumeTurn(ctx, payload.TurnID, opts)
	if err != nil {
		return nil, fmt.Errorf("resume turn: %w", err)
	}
	t.waitSubmittedTurn(ctx, curTurn)
	return &agentworker.PostMessageResult{TurnID: payload.TurnID}, nil
}

func (t *Runtime) waitSubmittedTurn(ctx context.Context, curTurn *agentthread.TurnHandle) {
	if curTurn == nil {
		logs.CtxError(ctx, "[cloudagent] missing current turn")
		return
	}
	t.mu.Lock()
	claimCtx := t.claimCtx
	t.mu.Unlock()
	if claimCtx == nil {
		claimCtx = ctx
	}
	go func() {
		if err := curTurn.Wait(claimCtx); err != nil && claimCtx.Err() == nil {
			logs.CtxError(claimCtx, "[cloudagent] turn failed: 对话流ID=%s thread_id=%s turn_id=%s err=%v", t.sessionID, t.threadID, curTurn.TurnID(), err)
		}
	}()
}

func (t *Runtime) resumeData(ctx context.Context, payload protoinput.ResumeTurnPayload) (map[string]any, error) {
	return resumeData(ctx, payload, t.interruptResume)
}

func (t *Runtime) emitCancelTurnEvents(ctx context.Context, payload protoinput.ResumeTurnPayload) {
	reason := strings.TrimSpace(payload.Approval.Reason)
	if reason == "" {
		reason = string(agentworker.ThreadInterruptKindCancelInput)
	}
	consumed := compactConsumedInputsFromIDs(payload.ConsumedMessageIDs)
	t.emitAgentEvent(ctx, agentthread.Event{
		ID:       t.eventID(payload.TurnID),
		TS:       time.Now(),
		ThreadID: t.threadID,
		TurnID:   payload.TurnID,
		Type:     agentthread.EventInterrupted,
		Payload: agentthread.InterruptedPayload{
			Source:       "external",
			InterruptID:  payload.InterruptID,
			CheckpointID: payload.CheckpointID,
			Metadata: map[string]string{
				"kind":   string(agentworker.ThreadInterruptKindCancelInput),
				"reason": reason,
			},
		},
		ConsumedInputs: consumed,
	})
	t.emitAgentEvent(ctx, agentthread.Event{
		ID:             t.eventID(payload.TurnID),
		TS:             time.Now(),
		ThreadID:       t.threadID,
		TurnID:         payload.TurnID,
		Type:           agentthread.EventTurnEnd,
		Payload:        agentthread.TurnEndPayload{},
		ConsumedInputs: consumed,
	})
}

func (t *Runtime) postCompact(ctx context.Context, cmd compactCommand) (err error) {
	turnID := cmd.turnID
	if t.thread.ActiveTurn() != nil || t.activeCompact() != nil {
		logs.CtxInfo(ctx, "[cloudagent] compact rejected because thread is running: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, workerMessageID(cmd.message), turnID)
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
		logs.CtxInfo(ctx, "[cloudagent] compact rejected by active state: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, workerMessageID(cmd.message), turnID)
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

	logs.CtxInfo(ctx, "[cloudagent] compact started: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, workerMessageID(cmd.message), turnID)
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
			logs.CtxInfo(ctx, "[cloudagent] compact interrupted: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s kind=%s", t.sessionID, t.threadID, workerMessageID(cmd.message), turnID, req.Kind)
			t.emitCompactInterruptedEvent(context.WithoutCancel(ctx), op, req)
			return nil
		}
		logs.CtxError(ctx, "[cloudagent] compact failed: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s err=%v", t.sessionID, t.threadID, workerMessageID(cmd.message), turnID, err)
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
		logs.CtxInfo(ctx, "[cloudagent] compact skipped: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, workerMessageID(cmd.message), turnID)
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
	logs.CtxInfo(ctx, "[cloudagent] compact finished: 对话流ID=%s thread_id=%s message_id=%s turn_id=%s", t.sessionID, t.threadID, workerMessageID(cmd.message), turnID)
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

func (t *Runtime) runOutputBridge(ctx context.Context, bridge *threadOutputBridge) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-bridge.agentEvents:
			if !ok {
				return
			}
			if !t.forwardAgentEvent(ctx, ev) {
				return
			}
		case item := <-bridge.inbox:
			if !bridge.deliver(ctx, t, item) {
				return
			}
		}
	}
}

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

func (t *Runtime) forwardAgentEvent(ctx context.Context, ev agentthread.Event) bool {
	usage := t.thread.ContextManager().ContextUsage()
	item, err := threadOutputItem(t.sessionID, t.threadID, ev, &usage, t.outputConfig)
	if err != nil {
		logs.CtxError(ctx, "[cloudagent] map agent event failed: turn_id=%s event_id=%s event_type=%s err=%v", ev.TurnID, ev.ID, ev.Type, err)
		return true
	}
	if item == nil {
		return true
	}
	t.logOutputItem(ctx, ev, *item)
	if ev.Type == agentthread.EventTurnEnd && t.turnFinishedObserver != nil {
		t.turnFinishedObserver(ctx, ev)
	}
	return t.outputBridge.deliver(ctx, t, *item)
}

func (t *Runtime) logOutputItem(ctx context.Context, ev agentthread.Event, item agentworker.ThreadOutputItem) {
	if item.Event != nil {
		switch item.Event.Type {
		case agentworker.EventType(protoevent.EventTypeInterruptRequired.String()):
			var payload protoevent.InterruptRequiredEventPayload
			if err := json.Unmarshal(item.Event.Payload, &payload); err == nil {
				logs.CtxInfo(ctx,
					"[cloudagent] interrupt required output: 对话流ID=%s thread_id=%s turn_id=%s event_id=%s source_event_type=%s interrupt_id=%s checkpoint_id=%s kind=%s info_type=%s",
					t.sessionID, t.threadID, item.Event.TurnID, item.Event.ID, ev.Type, payload.InterruptID, payload.CheckpointID, payload.Kind, payload.InfoType,
				)
			}
		case agentworker.EventType(protoevent.EventTypeTurnInterrupted.String()):
			if payload, ok := ev.Payload.(agentthread.InterruptedPayload); ok && isExternalInterrupt(payload) {
				logs.CtxInfo(ctx,
					"[cloudagent] external interrupt output: 对话流ID=%s thread_id=%s turn_id=%s event_id=%s source=%s reason=%s kind=%s",
					t.sessionID, t.threadID, item.Event.TurnID, item.Event.ID, payload.Source, interruptedMessage(payload), payload.Metadata["kind"],
				)
			}
		}
	}
	if item.Yield != nil && item.Yield.Block != nil {
		logs.CtxInfo(ctx,
			"[cloudagent] block yield output: 对话流ID=%s thread_id=%s turn_id=%s interrupt_id=%s checkpoint_id=%s kind=%s reason=%s",
			t.sessionID, t.threadID, item.Yield.Block.TurnID, item.Yield.Block.InterruptID, item.Yield.Block.CheckpointID, item.Yield.Block.Kind, item.Yield.Reason,
		)
	}
}

func (t *Runtime) emitAgentEvent(ctx context.Context, ev agentthread.Event) {
	t.mu.Lock()
	bridge := t.outputBridge
	t.mu.Unlock()
	if bridge == nil {
		return
	}
	usage := t.thread.ContextManager().ContextUsage()
	item, err := threadOutputItem(t.sessionID, t.threadID, ev, &usage, t.outputConfig)
	if err != nil {
		logs.CtxError(ctx, "[cloudagent] map runtime event failed: turn_id=%s event_id=%s event_type=%s err=%v", ev.TurnID, ev.ID, ev.Type, err)
		return
	}
	if item == nil {
		return
	}
	t.logOutputItem(ctx, ev, *item)
	bridge.send(ctx, *item)
}

func (t *Runtime) eventID(turnID string) string {
	return fmt.Sprintf("evt_%s_%s_%d", t.threadID, turnID, time.Now().UnixNano())
}

func (t *Runtime) runConfig(ctx context.Context, turnID string, message *agentworker.Message, mode protoinput.UserMessageMode, resume bool) (*agentthread.TurnConfig, error) {
	if t == nil || t.turnConfig == nil {
		return nil, nil
	}
	return t.turnConfig(ctx, TurnStartRequest{TurnID: turnID, Mode: mode, Message: message, Resume: resume})
}

func (t *Runtime) withThreadInfo(ctx context.Context) context.Context {
	return runtimectx.ContextWithThreadIdentity(ctx, t.threadInfo)
}

func (t *Runtime) ensureOpen() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return agentworker.ErrThreadClosed
	}
	return nil
}
