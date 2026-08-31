package agentthread

import (
	"context"
	"sync"
	"time"

	"eino-cli/deepagent/core/graph"

	"code.byted.org/gopkg/logs/v2"
	"github.com/cloudwego/eino/schema"
)

const (
	defaultRunEventBufferSize      = 4096
	threadEventForwardWarnDuration = 50 * time.Millisecond
)

type DeepAgentThread struct {
	ThreadID string

	cfg  *TurnRunnerConfig
	cm   ContextManager
	evCh chan Event

	mu      sync.Mutex
	current *turn

	turnIDProvider TurnIDProvider
}

// NewThread creates a DeepAgentThread with the SDK's default context manager.
func NewThread(threadID string, runnerCfg *TurnRunnerConfig, eventBus chan Event, opts DefaultThreadOptions, threadOpts ...Option) *DeepAgentThread {
	cmOpts := []MemoryContextManagerOption{}
	if opts.ContextWindow > 0 {
		cmOpts = append(cmOpts, WithContextWindow(opts.ContextWindow))
	}
	cm := NewMemoryContextManager(threadID, opts.HistoryStore, opts.CompactionStrategy, opts.TokenCounter, cmOpts...)
	return New(threadID, runnerCfg, cm, eventBus, threadOpts...)
}

// New creates a DeepAgentThread with a caller-provided context manager.
func New(threadID string, cfg *TurnRunnerConfig, cm ContextManager, eventBus chan Event, opts ...Option) *DeepAgentThread {
	if cm == nil {
		panic("agentthread: context manager is nil")
	}
	if eventBus == nil {
		panic("agentthread: event bus is nil")
	}
	t := &DeepAgentThread{
		ThreadID:       threadID,
		cfg:            &TurnRunnerConfig{},
		cm:             cm,
		evCh:           eventBus,
		turnIDProvider: defaultTurnIDProvider,
	}
	if cfg != nil {
		t.cfg = cfg.Clone()
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	if t.cfg == nil {
		t.cfg = &TurnRunnerConfig{}
	}
	return t
}

// Init reloads persisted history before the thread starts processing messages.
// It is not safe to call concurrently with active turn execution.
func (t *DeepAgentThread) Init(ctx context.Context) error {
	return t.cm.ReloadHistory(ctx)
}

func (t *DeepAgentThread) ContextManager() ContextManager {
	return t.cm
}

func (t *DeepAgentThread) Compact(ctx context.Context) (*ContextCompactedPayload, error) {
	return t.CompactWithTurnID(ctx, t.newTurnID(ctx, nil))
}

func (t *DeepAgentThread) CompactWithTurnID(ctx context.Context, turnID string) (*ContextCompactedPayload, error) {
	if turnID == "" {
		return nil, ErrInvalidOp
	}
	if t.ActiveTurn() != nil {
		return nil, ErrThreadRunning
	}
	return t.cm.Compact(ctx, turnID)
}

// ActiveTurn returns the thread's currently active turn, if any.
func (t *DeepAgentThread) ActiveTurn() *TurnHandle {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil || !t.current.isActive() {
		return nil
	}
	return t.curTurnLocked(t.current)
}

// SubmitInput submits one user input to the thread. It starts a new turn when
// no turn is active, or queues the input into the active turn while it still
// accepts pending input. ctx is the lifetime context for a newly started turn.
func (t *DeepAgentThread) SubmitInput(ctx context.Context, input *Message, opts ...SubmitInputOption) (*SubmitInputResult, error) {
	if input == nil {
		return nil, ErrInvalidOp
	}
	submitOpts := buildSubmitInputOptions(opts)
	t.mu.Lock()
	if t.current != nil {
		turnID := t.current.turnID
		if err := t.current.enqueueInput(input, submitOpts.InputMeta); err != nil {
			t.mu.Unlock()
			return nil, err
		}
		result := SubmitInputResult{
			TurnID:             turnID,
			TurnHandle:         t.curTurnLocked(t.current),
			QueuedToActiveTurn: true,
		}
		if submitOpts.OnAccepted != nil {
			submitOpts.OnAccepted(result)
		}
		t.mu.Unlock()
		return &result, nil
	}
	request := turnStartRequest{
		turnID:         t.newTurnID(ctx, input),
		input:          input,
		inputMeta:      submitOpts.InputMeta,
		trigger:        TurnRunnerConfigForSubmit,
		configResolver: submitOpts.TurnRunnerConfig,
		onRunnerStart:  submitOpts.OnTurnRunnerStart,
	}
	runCtx := t.prepareTurnStartContext(ctx, request)
	turn, runnerCfg, err := t.startTurnLocked(runCtx, request)
	if err != nil {
		t.mu.Unlock()
		return nil, err
	}
	result := SubmitInputResult{
		TurnID:       turn.turnID,
		TurnHandle:   t.curTurnLocked(turn),
		StartNewTurn: true,
		RunnerConfig: runnerCfg.Clone(),
	}
	if submitOpts.OnAccepted != nil {
		submitOpts.OnAccepted(result)
	}
	t.mu.Unlock()

	go t.executeTurn(runCtx, turn)
	return &result, nil
}

func (t *DeepAgentThread) resolveTurnRunnerConfig(ctx context.Context, req TurnRunnerConfigRequest, explicit *TurnRunnerConfig, resolver TurnRunnerConfigResolver) (*TurnRunnerConfig, error) {
	if explicit != nil {
		return explicit.Clone(), nil
	}
	base := t.cfg.Clone()
	if resolver == nil {
		return base, nil
	}
	req.ThreadID = t.ThreadID
	req.Input = graph.CopyMessage(req.Input)
	req.Resume = copyResumeTurnConfigRequest(req.Resume)
	req.Base = base.Clone()
	cfg, err := resolver(ctx, req)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return base, nil
	}
	return cfg.Clone(), nil
}

// ResumeTurn resumes a checkpoint/interruption-bound turn. Unlike SubmitInput,
// resume must target an existing turn ID and is never queued into an active turn.
func (t *DeepAgentThread) ResumeTurn(ctx context.Context, turnID string, opts ResumeTurnOptions) (*TurnHandle, error) {
	if turnID == "" || opts.CheckpointID == "" {
		return nil, ErrInvalidOp
	}
	t.mu.Lock()
	if t.current != nil && t.current.isActive() {
		t.mu.Unlock()
		return nil, ErrThreadRunning
	}
	request := turnStartRequest{
		turnID:         turnID,
		resume:         &opts,
		trigger:        TurnRunnerConfigForResume,
		explicitConfig: opts.RunnerConfig,
		configResolver: opts.TurnRunnerConfig,
		onRunnerStart:  opts.OnTurnRunnerStart,
	}
	runCtx := t.prepareTurnStartContext(ctx, request)
	turn, _, err := t.startTurnLocked(runCtx, request)
	if err != nil {
		t.mu.Unlock()
		return nil, err
	}
	curTurn := t.curTurnLocked(turn)
	t.mu.Unlock()

	go t.executeTurn(runCtx, turn)
	return curTurn, nil
}

func (t *DeepAgentThread) prepareTurnStartContext(ctx context.Context, request turnStartRequest) context.Context {
	return applyTurnRunnerStart(ctx, request.onRunnerStart, TurnRunnerStartRequest{
		ThreadID:  t.ThreadID,
		TurnID:    request.turnID,
		Trigger:   request.trigger,
		Input:     request.input,
		InputMeta: request.inputMeta,
		Resume:    request.configRequest().Resume,
	})
}

func (t *DeepAgentThread) startTurnLocked(ctx context.Context, request turnStartRequest) (*turn, *TurnRunnerConfig, error) {
	if request.turnID == "" {
		return nil, nil, ErrInvalidOp
	}
	runnerCfg, err := t.resolveTurnRunnerConfig(ctx, request.configRequest(), request.explicitConfig, request.configResolver)
	if err != nil {
		return nil, nil, err
	}
	run := newTurn(request.turnID, request.input, request.inputMeta, request.resume)
	run.runner = t.newTurnRunner(run, runnerCfg)
	t.current = run
	if err := run.runner.Init(ctx); err != nil {
		run.complete(err)
		t.current = nil
		return nil, nil, err
	}
	go t.forwardRunEvents(run)
	return run, runnerCfg, nil
}

func (t *DeepAgentThread) newTurnRunner(run *turn, cfg *TurnRunnerConfig) *TurnRunner {
	runner := NewTurnRunner(cfg, t.ThreadID, run.turnID, t.cm, run.events)
	runner.setPendingInputDrainer(t)
	runner.setReactLoopBranchPolicy(t.newReactLoopPolicy())
	return runner
}

func (t *DeepAgentThread) curTurnLocked(turn *turn) *TurnHandle {
	if turn == nil {
		return nil
	}
	return &TurnHandle{owner: t, turn: turn}
}

func (t *DeepAgentThread) Interrupt(opts InterruptOptions) bool {
	t.mu.Lock()
	var runner *TurnRunner
	if t.current != nil {
		runner = t.current.runner
	}
	t.mu.Unlock()
	if runner == nil {
		return false
	}
	return runner.InterruptWithOptions(opts)
}

func (t *DeepAgentThread) consumedInputsSnapshot(turn *turn) ([]*schema.Message, []any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if turn == nil || len(turn.consumed) == 0 {
		return nil, nil
	}
	return copyMessages(turn.consumed), copyConsumedInputsMeta(turn.consumedInputsMeta)
}

func (t *DeepAgentThread) newTurnID(ctx context.Context, input *Message) string {
	if t.turnIDProvider == nil {
		return defaultTurnIDProvider(ctx, t.ThreadID, input)
	}
	if turnID := t.turnIDProvider(ctx, t.ThreadID, graph.CopyMessage(input)); turnID != "" {
		return turnID
	}
	return defaultTurnIDProvider(ctx, t.ThreadID, input)
}

func (t *DeepAgentThread) DrainInput(ctx context.Context) []*schema.Message {
	t.mu.Lock()
	turn := t.current
	if turn == nil {
		t.mu.Unlock()
		return nil
	}
	msgs := turn.drainInput()
	runner := turn.runner
	t.mu.Unlock()
	if len(msgs) > 0 && runner != nil {
		runner.emitEvent(ctx, EventPendingInputProcessingStarted, PendingInputProcessingStartedPayload{
			Inputs: copyMessages(msgs),
		})
	}
	return msgs
}

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

func (t *DeepAgentThread) commitEndIfNoPending() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return true
	}
	return t.current.commitEndIfNoPending()
}
