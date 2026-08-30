package agentthread

import (
	"context"
	"sync"
	"time"

	"eino-cli/deepagent/core"
	"eino-cli/deepagent/core/graph"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

const (
	defaultRunEventBufferSize      = 4096
	threadEventForwardWarnDuration = 50 * time.Millisecond
)

// DefaultThreadOptions contains the context and persistence dependencies used
// by the SDK's default AgentThread assembly path.
type DefaultThreadOptions struct {
	HistoryStore       HistoryRolloutStore
	CompactionStrategy CompactionStrategy
	TokenCounter       TokenCounter
	ContextWindow      int64
}

// ResumeTurnOptions configures one checkpoint/interruption resume run.
type ResumeTurnOptions struct {
	CheckpointID        string
	WriteToCheckpointID string
	ForceNewRun         bool
	ResumeInterruptIDs  []string
	ResumeData          map[string]any
	// RunnerConfig directly supplies the config for this resume run. Prefer
	// TurnRunnerConfig for dynamic per-resume resolution; this field is kept for
	// callers that already materialize the full runner config.
	RunnerConfig *TurnRunnerConfig
	// TurnRunnerConfig can override the thread-level base runner config when
	// this resume starts a run. It is not called if RunnerConfig is set or if
	// the resume is rejected before a run can start.
	TurnRunnerConfig TurnRunnerConfigResolver
	// ConfigMeta carries caller-owned metadata for per-resume config
	// resolution. The thread passes it through as-is.
	ConfigMeta any

	// OnTurnRunnerStart derives the run context for the resumed turn runner. It
	// is called after the turn ID is confirmed and before the TurnRunnerConfig
	// resolver, runner.Init and RunTurn. It is not called if the resume is
	// rejected before a run can start.
	OnTurnRunnerStart OnTurnRunnerStartFunc
}

// SubmitInputResult describes how one user input was accepted by the thread.
type SubmitInputResult struct {
	TurnID             string
	TurnHandle         *TurnHandle
	StartNewTurn       bool
	QueuedToActiveTurn bool
	// RunnerConfig is the config snapshot used when this input starts a new
	// turn. It is nil when the input is queued into an active turn.
	RunnerConfig *TurnRunnerConfig
}

type TurnRunnerConfigTrigger string

const (
	TurnRunnerConfigForSubmit TurnRunnerConfigTrigger = "submit"
	TurnRunnerConfigForResume TurnRunnerConfigTrigger = "resume"
)

// TurnRunnerConfigRequest describes the turn run that is about to start. The
// resolver is called only after DeepAgentThread decides a run can start. It
// is not called when input is queued into an active turn or when resume is
// rejected before a run starts. Keep resolver side effects minimal; it must not
// call back into the same thread.
type TurnRunnerConfigRequest struct {
	ThreadID string
	TurnID   string
	Trigger  TurnRunnerConfigTrigger

	Input     *Message
	InputMeta any

	Resume *ResumeTurnConfigRequest
	Base   *TurnRunnerConfig
}

type ResumeTurnConfigRequest struct {
	CheckpointID        string
	WriteToCheckpointID string
	ForceNewRun         bool
	ResumeInterruptIDs  []string
	ResumeData          map[string]any
	ConfigMeta          any
}

type TurnRunnerConfigResolver func(ctx context.Context, req TurnRunnerConfigRequest) (*TurnRunnerConfig, error)

type SubmitInputOptions struct {
	// TurnRunnerConfig can override the thread-level runner config when this
	// input starts a new turn. It is not called when the input is queued into an
	// already active turn.
	TurnRunnerConfig TurnRunnerConfigResolver

	// InputMeta carries caller-owned metadata for input attribution. The thread
	// stores this value as-is and copies only the containing slice; callers that
	// need immutability should pass an immutable value or a copy.
	InputMeta any

	// OnAccepted is called synchronously after the input is assigned to a turn
	// but before a new turn starts emitting events, or before an active turn can
	// drain the queued input. Do not call back into DeepAgentThread from it.
	OnAccepted func(SubmitInputResult)

	// OnTurnRunnerStart derives the run context for the turn runner when this
	// input starts a new turn. It is called after the turn ID is confirmed and
	// before the TurnRunnerConfig resolver, runner.Init and RunTurn. It is not
	// called when the input is queued into an already active turn.
	OnTurnRunnerStart OnTurnRunnerStartFunc
}

type SubmitInputOption func(*SubmitInputOptions)

func WithSubmitInputAcceptedHook(f func(SubmitInputResult)) SubmitInputOption {
	return func(opts *SubmitInputOptions) {
		opts.OnAccepted = f
	}
}

func WithTurnRunnerConfig(cfg *TurnRunnerConfig) SubmitInputOption {
	cloned := cfg.Clone()
	return WithTurnRunnerConfigResolver(func(context.Context, TurnRunnerConfigRequest) (*TurnRunnerConfig, error) {
		return cloned.Clone(), nil
	})
}

func WithTurnRunnerConfigResolver(resolver TurnRunnerConfigResolver) SubmitInputOption {
	return func(opts *SubmitInputOptions) {
		opts.TurnRunnerConfig = resolver
	}
}

func WithInputMeta(meta any) SubmitInputOption {
	return func(opts *SubmitInputOptions) {
		opts.InputMeta = meta
	}
}

type TurnIDProvider func(ctx context.Context, threadID string, input *Message) string

type Option func(*DeepAgentThread)

func WithTurnIDProvider(provider TurnIDProvider) Option {
	return func(t *DeepAgentThread) {
		if provider != nil {
			t.turnIDProvider = provider
		}
	}
}

func WithBaseTurnRunnerConfig(cfg *TurnRunnerConfig) Option {
	return func(t *DeepAgentThread) {
		if cfg != nil {
			t.cfg = cfg.Clone()
		}
	}
}

// TurnHandle is a read-only view of one current turn owned by DeepAgentThread.
// It can be held after the thread starts another turn; Wait always waits for
// this turn, not for whatever turn becomes current later.
type TurnHandle struct {
	owner *DeepAgentThread
	turn  *turn
}

func (c *TurnHandle) TurnID() string {
	if c == nil || c.turn == nil {
		return ""
	}
	return c.turn.turnID
}

func (c *TurnHandle) Wait(ctx context.Context) error {
	if c == nil || c.turn == nil {
		return ErrInvalidOp
	}
	select {
	case <-c.turn.done:
		return c.turn.err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *TurnHandle) IsActive() bool {
	if c == nil || c.owner == nil || c.turn == nil {
		return false
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	return c.turn.isActive()
}

func (c *TurnHandle) ConsumedInputs() []*schema.Message {
	if c == nil || c.owner == nil || c.turn == nil {
		return nil
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	if len(c.turn.consumed) == 0 {
		return nil
	}
	return copyMessages(c.turn.consumed)
}

func (c *TurnHandle) ConsumedInputsMeta() []any {
	if c == nil || c.owner == nil || c.turn == nil {
		return nil
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	return copyConsumedInputsMeta(c.turn.consumedInputsMeta)
}

type DeepAgentThread struct {
	ThreadID string

	cfg  *TurnRunnerConfig
	cm   ContextManager
	evCh chan Event

	mu      sync.Mutex
	current *turn

	turnIDProvider TurnIDProvider
}

// NewDefault creates a DeepAgentThread with the SDK's default context manager.
func NewDefault(threadID string, runnerCfg *TurnRunnerConfig, eventBus chan Event, opts DefaultThreadOptions, threadOpts ...Option) *DeepAgentThread {
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
	turnID := t.newTurnID(ctx, input)
	runCtx := applyTurnRunnerStart(ctx, submitOpts.OnTurnRunnerStart, TurnRunnerStartRequest{
		ThreadID:  t.ThreadID,
		TurnID:    turnID,
		Trigger:   TurnRunnerConfigForSubmit,
		Input:     input,
		InputMeta: submitOpts.InputMeta,
	})
	runnerCfg, err := t.resolveTurnRunnerConfig(runCtx, TurnRunnerConfigRequest{
		TurnID:    turnID,
		Trigger:   TurnRunnerConfigForSubmit,
		Input:     input,
		InputMeta: submitOpts.InputMeta,
	}, nil, submitOpts.TurnRunnerConfig)
	if err != nil {
		t.mu.Unlock()
		return nil, err
	}
	turn, err := t.startTurnLocked(runCtx, turnID, input, submitOpts.InputMeta, nil, runnerCfg)
	if err != nil {
		t.mu.Unlock()
		return nil, err
	}
	result := SubmitInputResult{
		TurnID:       turnID,
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
	runCtx := applyTurnRunnerStart(ctx, opts.OnTurnRunnerStart, TurnRunnerStartRequest{
		ThreadID: t.ThreadID,
		TurnID:   turnID,
		Trigger:  TurnRunnerConfigForResume,
		Resume:   resumeTurnConfigRequestFromOptions(&opts),
	})
	runnerCfg, err := t.resolveTurnRunnerConfig(runCtx, TurnRunnerConfigRequest{
		TurnID:  turnID,
		Trigger: TurnRunnerConfigForResume,
		Resume:  resumeTurnConfigRequestFromOptions(&opts),
	}, opts.RunnerConfig, opts.TurnRunnerConfig)
	if err != nil {
		t.mu.Unlock()
		return nil, err
	}
	turn, err := t.startTurnLocked(runCtx, turnID, nil, nil, &opts, runnerCfg)
	if err != nil {
		t.mu.Unlock()
		return nil, err
	}
	curTurn := t.curTurnLocked(turn)
	t.mu.Unlock()

	go t.executeTurn(runCtx, turn)
	return curTurn, nil
}

func (t *DeepAgentThread) startTurnLocked(ctx context.Context, turnID string, input *Message, inputMeta any, opts *ResumeTurnOptions, runnerCfg *TurnRunnerConfig) (*turn, error) {
	if turnID == "" {
		return nil, ErrInvalidOp
	}
	if runnerCfg == nil {
		runnerCfg = t.cfg.Clone()
	} else {
		runnerCfg = runnerCfg.Clone()
	}
	turn := newTurn(turnID, input, inputMeta, opts)
	turn.runner = NewTurnRunner(runnerCfg, t.ThreadID, turnID, t.cm, turn.events)
	turn.runner.setPendingInputDrainer(t)
	turn.runner.setReactLoopBranchPolicy(t)
	t.current = turn
	if err := turn.runner.Init(ctx); err != nil {
		turn.complete(err)
		t.current = nil
		return nil, err
	}
	go t.forwardRunEvents(turn)
	return turn, nil
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

func copyConsumedInputsMeta(in []any) []any {
	if len(in) == 0 {
		return nil
	}
	hasValue := false
	for _, meta := range in {
		if meta != nil {
			hasValue = true
			break
		}
	}
	if !hasValue {
		return nil
	}
	out := make([]any, len(in))
	copy(out, in)
	return out
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

func defaultTurnIDProvider(context.Context, string, *Message) string {
	return uuid.NewString()
}

func copyResumeTurnOptions(in *ResumeTurnOptions) *ResumeTurnOptions {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.ResumeInterruptIDs) > 0 {
		out.ResumeInterruptIDs = append([]string(nil), in.ResumeInterruptIDs...)
	}
	if len(in.ResumeData) > 0 {
		out.ResumeData = make(map[string]any, len(in.ResumeData))
		for k, v := range in.ResumeData {
			out.ResumeData[k] = v
		}
	}
	if in.RunnerConfig != nil {
		out.RunnerConfig = in.RunnerConfig.Clone()
	}
	return &out
}

func resumeTurnConfigRequestFromOptions(in *ResumeTurnOptions) *ResumeTurnConfigRequest {
	if in == nil {
		return nil
	}
	out := &ResumeTurnConfigRequest{
		CheckpointID:        in.CheckpointID,
		WriteToCheckpointID: in.WriteToCheckpointID,
		ForceNewRun:         in.ForceNewRun,
		ConfigMeta:          in.ConfigMeta,
	}
	if len(in.ResumeInterruptIDs) > 0 {
		out.ResumeInterruptIDs = append([]string(nil), in.ResumeInterruptIDs...)
	}
	if len(in.ResumeData) > 0 {
		out.ResumeData = make(map[string]any, len(in.ResumeData))
		for k, v := range in.ResumeData {
			out.ResumeData[k] = v
		}
	}
	return out
}

func copyResumeTurnConfigRequest(in *ResumeTurnConfigRequest) *ResumeTurnConfigRequest {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.ResumeInterruptIDs) > 0 {
		out.ResumeInterruptIDs = append([]string(nil), in.ResumeInterruptIDs...)
	}
	if len(in.ResumeData) > 0 {
		out.ResumeData = make(map[string]any, len(in.ResumeData))
		for k, v := range in.ResumeData {
			out.ResumeData[k] = v
		}
	}
	return &out
}

func buildSubmitInputOptions(opts []SubmitInputOption) SubmitInputOptions {
	var out SubmitInputOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&out)
		}
	}
	return out
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

func (t *DeepAgentThread) AfterModel(ctx context.Context, input deepagents.ReactLoopAfterModelInput) (deepagents.ReactLoopBranchDecision, error) {
	_ = ctx
	if input.Default != deepagents.ReactLoopBranchToEnd {
		return deepagents.ReactLoopBranchDefault, nil
	}
	if t.commitEndIfNoPending() {
		return deepagents.ReactLoopBranchDefault, nil
	}
	return deepagents.ReactLoopBranchToExecutor, nil
}

func (t *DeepAgentThread) AfterTools(ctx context.Context, input deepagents.ReactLoopAfterToolsInput) (deepagents.ReactLoopBranchDecision, error) {
	_ = ctx
	_ = input
	return deepagents.ReactLoopBranchDefault, nil
}

func (t *DeepAgentThread) commitEndIfNoPending() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return true
	}
	return t.current.commitEndIfNoPending()
}

func copyMessages(msgs []*schema.Message) []*schema.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]*schema.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = graph.CopyMessage(msg)
	}
	return out
}
