package agentthread

import (
	"context"
	"slices"
	"sync"
	"time"

	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/graph"
	"eino-cli/deepagent/core/middleware"

	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/lang/gg/choose"
	"github.com/cloudwego/eino/schema"
)

const (
	defaultRunEventBufferSize      = 4096
	threadEventForwardWarnDuration = 50 * time.Millisecond
)

type DeepAgentThread struct {
	ThreadID string

	turnConfig *TurnConfig
	cm         ContextManager
	evCh       chan Event

	mu      sync.Mutex
	current *run

	turnIDProvider TurnIDProvider
}

// Constructors and lifecycle

func New(
	threadID string,
	turnConfig *TurnConfig,
	eventBus chan Event,
	threadConfig ThreadOptions,
	opts ...Option,
) (thread *DeepAgentThread) {
	contextManager := choose.IfLazyL[ContextManager](
		threadConfig.ContextManager == nil,
		func() (manager ContextManager) {
			manager = NewMemoryContextManager(
				threadID,
				threadConfig.HistoryStore,
				threadConfig.CompactionStrategy,
				threadConfig.TokenCounter,
				WithContextWindow(threadConfig.ContextWindow),
			)
			return manager
		},
		threadConfig.ContextManager,
	)
	if eventBus == nil {
		panic("agentthread: event bus is nil")
	}
	baseTurnConfig := &TurnConfig{}
	if turnConfig != nil {
		baseTurnConfig = turnConfig.Clone()
	}
	thread = &DeepAgentThread{
		ThreadID:       threadID,
		turnConfig:     baseTurnConfig,
		cm:             contextManager,
		evCh:           eventBus,
		turnIDProvider: defaultTurnIDProvider,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(thread)
		}
	}
	return thread
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

// Input and turn control

// ActiveTurn returns the thread's currently active turn, if any.
func (t *DeepAgentThread) ActiveTurn() (handle *TurnHandle) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return nil
	}
	handle = t.currentHandleLocked(t.current)
	return handle
}

// SubmitInput submits one user input to the thread. It starts a new turn when
// no turn is active, or queues the input into the active turn while it still
// accepts pending input. ctx is the lifetime context for a newly started turn.
func (t *DeepAgentThread) SubmitInput(ctx context.Context, input *Message, opts ...SubmitInputOption) (result *SubmitInputResult, err error) {
	if input == nil {
		return nil, ErrInvalidOp
	}
	var submitOpts submitInputOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&submitOpts)
		}
	}
	t.mu.Lock()
	if t.current != nil {
		turnID := t.current.turnID
		if err := t.current.enqueueInput(input, submitOpts.InputMeta); err != nil {
			t.mu.Unlock()
			return nil, err
		}
		accepted := SubmitInputResult{
			TurnID:     turnID,
			TurnHandle: t.currentHandleLocked(t.current),
		}
		t.mu.Unlock()
		return &accepted, nil
	}
	request := TurnStartRequest{
		ThreadID:  t.ThreadID,
		TurnID:    t.newTurnID(ctx, input),
		Input:     input,
		InputMeta: submitOpts.InputMeta,
	}
	runCtx := applyTurnStart(ctx, submitOpts.OnTurnStart, request)
	current, err := t.startRunLocked(runCtx, request, submitOpts.ConfigProvider)
	if err != nil {
		t.mu.Unlock()
		return nil, err
	}
	accepted := SubmitInputResult{
		TurnID:     current.turnID,
		TurnHandle: t.currentHandleLocked(current),
		Started:    true,
	}
	t.mu.Unlock()

	go t.executeRun(runCtx, current)
	return &accepted, nil
}

// ResumeTurn resumes a checkpoint/interruption-bound turn. Unlike SubmitInput,
// resume must target an existing turn ID and is never queued into an active turn.
func (t *DeepAgentThread) ResumeTurn(ctx context.Context, turnID string, opts ResumeTurnOptions) (handle *TurnHandle, err error) {
	if turnID == "" || opts.CheckpointID == "" {
		return nil, ErrInvalidOp
	}
	t.mu.Lock()
	if t.current != nil {
		t.mu.Unlock()
		return nil, ErrThreadRunning
	}
	request := TurnStartRequest{
		ThreadID: t.ThreadID,
		TurnID:   turnID,
		Resume:   copyResumeTurnOptions(&opts),
	}
	runCtx := applyTurnStart(ctx, opts.OnTurnStart, request)
	current, err := t.startRunLocked(runCtx, request, opts.ConfigProvider)
	if err != nil {
		t.mu.Unlock()
		return nil, err
	}
	handle = t.currentHandleLocked(current)
	t.mu.Unlock()

	go t.executeRun(runCtx, current)
	return handle, nil
}

// Interrupt requests cancellation of the active turn.
func (t *DeepAgentThread) Interrupt(opts InterruptOptions) (interrupted bool) {
	t.mu.Lock()
	current := t.current
	t.mu.Unlock()
	if current == nil {
		return false
	}
	return current.interrupt(opts)
}

// DrainInput removes pending inputs so the active turn can process them.
func (t *DeepAgentThread) DrainInput(ctx context.Context) (messages []*schema.Message) {
	t.mu.Lock()
	current := t.current
	if current == nil {
		t.mu.Unlock()
		return nil
	}
	messages = current.drainInput()
	t.mu.Unlock()
	if len(messages) > 0 {
		current.events.emit(ctx, EventPendingInputProcessingStarted, PendingInputProcessingStartedPayload{
			Inputs: copyMessages(messages),
		})
	}
	return messages
}

// Run creation

func (t *DeepAgentThread) selectTurnConfig(ctx context.Context, request TurnStartRequest, provider TurnConfigProvider) (turnConfig *TurnConfig, err error) {
	base := t.turnConfig.Clone()
	if provider == nil {
		return base, nil
	}
	request.ThreadID = t.ThreadID
	request.Input = graph.CopyMessage(request.Input)
	request.Resume = copyResumeTurnOptions(request.Resume)
	turnConfig, err = provider(ctx, request)
	if err != nil {
		return nil, err
	}
	if turnConfig == nil {
		return base, nil
	}
	return turnConfig.Clone(), nil
}

func (t *DeepAgentThread) startRunLocked(ctx context.Context, request TurnStartRequest, provider TurnConfigProvider) (current *run, err error) {
	if request.TurnID == "" {
		return nil, ErrInvalidOp
	}
	turnConfig, err := t.selectTurnConfig(ctx, request, provider)
	if err != nil {
		return nil, err
	}
	current, err = t.buildRun(ctx, request, turnConfig, make(chan Event, defaultRunEventBufferSize))
	if err != nil {
		return nil, err
	}
	t.current = current
	go t.forwardRunEvents(current)
	return current, nil
}

func (t *DeepAgentThread) buildRun(
	ctx context.Context,
	request TurnStartRequest,
	turnConfig *TurnConfig,
	eventBus chan Event,
) (current *run, err error) {
	turnID := request.TurnID
	events := newTurnEventRecorder(turnConfig, t.ThreadID, turnID, t.cm, eventBus)
	agentConfig := t.buildRunAgentConfig(ctx, turnID, turnConfig, events)
	workDir := ""
	if agentConfig.FilesystemConfig != nil {
		workDir = agentConfig.FilesystemConfig.WorkDir
	}
	logs.CtxInfo(ctx, "[agentthread::startRun] max_steps=%d max_model_calls=%d fs=%v web=%v workdir=%s skills_loader=%v hitl=%v", agentConfig.MaxSteps, agentConfig.MaxModelCalls, agentConfig.FilesystemConfig != nil, agentConfig.WebConfig != nil, workDir, agentConfig.SkillLoader != nil, agentConfig.HITLConfig != nil)
	agent, err := deepagents.New(ctx, deepagents.WithConfig(agentConfig))
	if err != nil {
		return nil, err
	}

	var onCompleted func(context.Context)
	if turnConfig.TurnCompleted != nil {
		completed := turnConfig.TurnCompleted
		chatModel := agentConfig.Model
		contextManager := t.cm
		threadID := t.ThreadID
		onCompleted = func(ctx context.Context) {
			history := append([]*schema.Message(nil), contextManager.History(ctx)...)
			completed(ctx, threadID, turnID, chatModel, history)
		}
	}

	current = &run{
		threadID:       t.ThreadID,
		turnID:         turnID,
		input:          graph.CopyMessage(request.Input),
		resume:         copyResumeTurnOptions(request.Resume),
		agent:          agent,
		events:         events,
		onCompleted:    onCompleted,
		acceptingInput: true,
		eventsDrained:  make(chan struct{}),
		done:           make(chan struct{}),
	}
	if request.Input != nil {
		current.consumed = append(current.consumed, graph.CopyMessage(request.Input))
		current.consumedInputMeta = append(current.consumedInputMeta, request.InputMeta)
	}
	return current, nil
}

func (t *DeepAgentThread) buildRunAgentConfig(
	ctx context.Context,
	turnID string,
	turnConfig *TurnConfig,
	events *turnEventRecorder,
) (agentConfig *deepagents.Config) {
	agentConfig = turnConfig.Agent.Clone()
	agentConfig.ContextManager = &ctxMngMiddleware{
		core:    t.cm,
		turnID:  turnID,
		drainer: t,
		emit:    events.emit,
	}
	agentConfig.ContinueAfterModel = t.shouldContinue

	dynamicMiddlewares := []middleware.Middleware(nil)
	if turnConfig.MiddlewaresProvider != nil {
		dynamicMiddlewares = turnConfig.MiddlewaresProvider(ctx, turnID)
	}
	agentConfig.Middlewares = slices.Concat(
		events.middlewares(turnConfig.EnablePlan, agentConfig.ToolMask),
		dynamicMiddlewares,
		agentConfig.Middlewares,
	)
	agentConfig.Middlewares = slices.DeleteFunc(agentConfig.Middlewares, func(mw middleware.Middleware) bool {
		return mw == nil
	})

	if turnConfig.CustomStateBuilder != nil {
		customStates := turnConfig.CustomStateBuilder(ctx, t.ThreadID, turnID)
		if len(customStates) > 0 {
			agentConfig.CustomGraphState = customStates
		}
	}

	return agentConfig
}

// Execution and event forwarding

// executeRun executes one logical turn, drains all turn-local events, and only then
// releases the thread's active-run slot. Keeping this sequence together makes
// the completion boundary explicit: a turn is complete after execution and
// event forwarding have both finished.
func (t *DeepAgentThread) executeRun(ctx context.Context, current *run) {
	err := current.execute(ctx)
	current.events.close()
	<-current.eventsDrained
	t.finishRun(current, err)
}

// forwardRunEvents copies events from one agent execution into the thread-wide
// event channel and attaches the inputs consumed by that logical turn.
func (t *DeepAgentThread) forwardRunEvents(current *run) {
	defer close(current.eventsDrained)
	eventBus := current.events.eventBus
	for event := range eventBus {
		event.ConsumedInputs, event.ConsumedInputsMeta = t.consumedInputsSnapshot(current)
		runQueueLen := len(eventBus)
		runQueueCap := cap(eventBus)
		threadQueueLen := len(t.evCh)
		threadQueueCap := cap(t.evCh)
		startedAt := time.Now()
		t.evCh <- event
		if elapsed := time.Since(startedAt); elapsed > threadEventForwardWarnDuration {
			logs.CtxWarn(context.Background(), "[DeepAgentThread::forwardRunEvents] slow event forward: thread_id=%s turn_id=%s event_type=%s elapsed=%s run_queue_len=%d run_queue_cap=%d thread_queue_len_before=%d thread_queue_cap=%d",
				t.ThreadID, current.turnID, event.Type, elapsed, runQueueLen, runQueueCap, threadQueueLen, threadQueueCap)
		}
	}
}

// Completion and state helpers
func (t *DeepAgentThread) currentHandleLocked(current *run) (handle *TurnHandle) {
	if current == nil {
		return nil
	}
	handle = &TurnHandle{owner: t, run: current}
	return handle
}

func (t *DeepAgentThread) consumedInputsSnapshot(current *run) (messages []*schema.Message, metadata []any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if current == nil || len(current.consumed) == 0 {
		return nil, nil
	}
	messages = copyMessages(current.consumed)
	metadata = copyConsumedInputsMeta(current.consumedInputMeta)
	return messages, metadata
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

// finishRun closes the logical turn and clears the thread's current run only
// when it is still the same turn that was started.
func (t *DeepAgentThread) finishRun(current *run, runErr error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	current.runErr = runErr
	close(current.done)
	if t.current == current {
		t.current = nil
	}
}

func (t *DeepAgentThread) shouldContinue(context.Context) (continueRun bool, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return false, nil
	}
	if len(t.current.pending) > 0 {
		return true, nil
	}
	t.current.acceptingInput = false
	return false, nil
}
