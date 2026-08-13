//go:build !windows

package thread

import (
	"context"
	"fmt"
	"sync"

	"code.byted.org/gopkg/logs/v2"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/worker/runtimectx"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/worker"
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

	turnRunnerConfig     func(context.Context, TurnRunnerConfigRequest) (*agentthread.TurnRunnerConfig, error)
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

type TurnRunnerConfigRequest struct {
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

	TurnRunnerConfig     func(context.Context, TurnRunnerConfigRequest) (*agentthread.TurnRunnerConfig, error)
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
		turnRunnerConfig:     cfg.TurnRunnerConfig,
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
