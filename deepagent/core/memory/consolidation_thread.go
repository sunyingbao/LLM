package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/agentthread"
	agentworker "eino-cli/deepagent/worker"

	"code.byted.org/gopkg/logs/v2"
	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const consolidationDoneReason = "memory_consolidation_done"

// ConsolidationAgentThreadConfig contains the host resources needed to build the
// memory-owned stage2 agent thread.
type ConsolidationAgentThreadConfig struct {
	ThreadID string

	Metadata map[string]string

	ChatModel       model.ToolCallingChatModel
	HistoryStore    agentthread.HistoryRolloutStore
	CheckpointStore compose.CheckPointStore
	Callbacks       []callbacks.Handler
	EventIDProvider func(ctx context.Context, threadID, turnID string) string
	Store           Store
	Workspace       *Workspace

	LeaseTTL   time.Duration
	RetryDelay time.Duration
	Now        func() time.Time
}

// NewConsolidationAgentThread builds the one-turn memory stage2 runtime. It
// satisfies agentworker.AgentThread so hosts can run it like any other thread,
// while all memory-specific execution and completion semantics stay here.
func NewConsolidationAgentThread(cfg ConsolidationAgentThreadConfig) (agentworker.AgentThread, error) {
	if strings.TrimSpace(cfg.ThreadID) == "" {
		return nil, fmt.Errorf("memory: consolidation thread id is required")
	}
	if !IsConsolidationThreadMetadata(cfg.Metadata) {
		return nil, fmt.Errorf("memory: not a consolidation thread")
	}
	if cfg.ChatModel == nil {
		return nil, fmt.Errorf("memory: consolidation chat model is required")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("memory: consolidation store is required")
	}
	if cfg.Workspace == nil {
		return nil, fmt.Errorf("memory: consolidation workspace is required")
	}
	if cfg.Workspace.AgentBackend() == nil {
		return nil, fmt.Errorf("memory: consolidation workspace backend must support sandbox tools")
	}
	metadata, err := ParseStage2Metadata(cfg.Metadata)
	if err != nil {
		return nil, err
	}

	eventBus := make(chan agentthread.Event, 256)
	turnConfig := newConsolidationTurnConfig(cfg)
	thread := agentthread.New(cfg.ThreadID, turnConfig, eventBus, agentthread.ThreadOptions{
		HistoryStore: cfg.HistoryStore,
	})
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	leaseTTL := cfg.LeaseTTL
	if leaseTTL <= 0 {
		leaseTTL = time.Hour
	}
	retryDelay := cfg.RetryDelay
	if retryDelay <= 0 {
		retryDelay = 5 * time.Minute
	}

	return &consolidationAgentThread{
		threadID:            cfg.ThreadID,
		thread:              thread,
		eventBus:            eventBus,
		output:              make(chan agentworker.ThreadOutputItem, 256),
		store:               cfg.Store,
		workspace:           cfg.Workspace,
		userID:              metadata.UserID,
		ownershipToken:      metadata.OwnershipToken,
		watermark:           metadata.InputWatermark,
		baselineHash:        metadata.InputHash,
		startedArtifactHash: metadata.StartedArtifactHash,
		startedMemoryHash:   metadata.StartedMemoryHash,
		startedSummaryHash:  metadata.StartedSummaryHash,
		leaseTTL:            leaseTTL,
		heartbeatEvery:      stage2HeartbeatInterval(leaseTTL),
		retryDelay:          retryDelay,
		now:                 now,
	}, nil
}

func newConsolidationTurnConfig(cfg ConsolidationAgentThreadConfig) (runConfig *agentthread.TurnConfig) {
	workspaceBackend := cfg.Workspace.AgentBackend()
	workDir := cfg.Workspace.AgentWorkDir()
	runConfig = &agentthread.TurnConfig{
		Agent: deepagents.Config{
			Model:       cfg.ChatModel,
			Tools:       nil,
			Callbacks:   append([]callbacks.Handler(nil), cfg.Callbacks...),
			Middlewares: nil,

			FilesystemConfig: &deepagents.FilesystemConfig{
				WorkDir:               workDir,
				DisableExecute:        true,
				DisableUploadDownload: true,
			},
			Backend:         workspaceBackend,
			SkillLoader:     nil,
			CheckpointStore: cfg.CheckpointStore,

			HITLConfig: &deepagents.HITLConfig{},
		},
		EnablePlan: false,

		EventIDProvider: cfg.EventIDProvider,
	}
	return runConfig
}

type consolidationAgentThread struct {
	threadID string
	thread   *agentthread.DeepAgentThread
	eventBus <-chan agentthread.Event
	output   chan agentworker.ThreadOutputItem

	store               Store
	workspace           *Workspace
	userID              string
	ownershipToken      string
	watermark           string
	baselineHash        string
	startedArtifactHash string
	startedMemoryHash   string
	startedSummaryHash  string
	leaseTTL            time.Duration
	heartbeatEvery      time.Duration
	retryDelay          time.Duration
	now                 func() time.Time

	mu              sync.Mutex
	initCancel      context.CancelFunc
	heartbeatCancel context.CancelFunc
	heartbeatDone   chan struct{}
	closed          bool
	active          *consolidationActiveTurn
}

type consolidationActiveTurn struct {
	consumedMessageIDs []string
	curTurn            *agentthread.TurnHandle
	cancel             context.CancelFunc
	done               chan struct{}
}

func (t *consolidationAgentThread) Init(ctx context.Context) (output *agentworker.ThreadOutput, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, agentworker.ErrThreadClosed
	}
	if t.initCancel != nil {
		return &agentworker.ThreadOutput{Items: t.output}, nil
	}
	if err := t.thread.Init(ctx); err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	t.initCancel = cancel
	go t.drainEvents(runCtx)
	t.startHeartbeatLocked(runCtx)

	return &agentworker.ThreadOutput{Items: t.output}, nil
}

func (t *consolidationAgentThread) PostMessage(ctx context.Context, msg *agentworker.Message) (posted *agentworker.PostMessageResult, err error) {
	if msg == nil {
		return nil, fmt.Errorf("memory: consolidation message is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, agentworker.ErrThreadClosed
	}
	if t.active != nil {
		return nil, fmt.Errorf("memory: consolidation thread already has an active turn")
	}
	prompt, err := promptFromConsolidationMessage(msg)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	result, err := t.thread.SubmitInput(runCtx, schema.UserMessage(prompt))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("memory: submit consolidation input: %w", err)
	}
	if result == nil || result.TurnHandle == nil || !result.Started {
		cancel()
		return nil, fmt.Errorf("memory: consolidation input did not start a turn")
	}
	active := &consolidationActiveTurn{
		consumedMessageIDs: []string{strings.TrimSpace(msg.ID)},
		curTurn:            result.TurnHandle,
		cancel:             cancel,
		done:               make(chan struct{}),
	}
	if active.consumedMessageIDs[0] == "" {
		active.consumedMessageIDs = nil
	}
	t.active = active

	go t.waitAndFinalize(runCtx, active)
	return &agentworker.PostMessageResult{TurnID: active.curTurn.TurnID()}, nil
}

func (t *consolidationAgentThread) Interrupt(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
	if t.thread.Interrupt(agentthread.InterruptOptions{Metadata: map[string]string{
		"kind":               string(req.Kind),
		"reason":             req.Reason,
		"control_message_id": req.ControlMessageID,
		"cutoff_message_id":  req.CutoffMessageID,
	}}) {
		return nil
	}
	if t.ActiveTurn() == nil {
		return nil
	}
	return fmt.Errorf("memory: interrupt consolidation turn failed")
}

func (t *consolidationAgentThread) ActiveTurn() (turn *agentworker.ActiveTurn) {
	t.mu.Lock()
	active := t.active
	t.mu.Unlock()
	if active == nil {
		return nil
	}
	return &agentworker.ActiveTurn{
		TurnID:             active.curTurn.TurnID(),
		ConsumedMessageIDs: append([]string(nil), active.consumedMessageIDs...),
	}
}

func (t *consolidationAgentThread) Close(ctx context.Context) (err error) {
	t.mu.Lock()
	t.closed = true
	active := t.active
	if active != nil {
		active.cancel()
	}
	initCancel := t.initCancel
	t.mu.Unlock()

	if active != nil {
		select {
		case <-active.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.stopHeartbeat()
	if initCancel != nil {
		initCancel()
	}
	return nil
}

func (t *consolidationAgentThread) waitAndFinalize(ctx context.Context, active *consolidationActiveTurn) {
	defer close(active.done)
	defer active.cancel()
	completeCtx := context.WithoutCancel(ctx)
	turnID := active.curTurn.TurnID()
	err := active.curTurn.Wait(completeCtx)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	now := t.now()
	if err != nil && ctx.Err() == nil {
		logs.CtxError(ctx, "[memory] consolidation turn failed: thread_id=%s turn_id=%s err=%v", t.threadID, turnID, err)
	}

	var yieldErr error
	if err != nil {
		yieldErr = t.markStage2Error(completeCtx, err.Error(), now)
		if yieldErr != nil {
			logs.CtxError(completeCtx, "[memory] mark consolidation error failed: user_id=%s thread_id=%s turn_id=%s err=%v", t.userID, t.threadID, turnID, yieldErr)
		}
	} else if completeErr := CompleteStage2Thread(completeCtx, t.store, t.workspace, MarkStage2DoneRequest{
		UserID:                  t.userID,
		OwnershipToken:          t.ownershipToken,
		CompletedInputWatermark: t.watermark,
		BaselineHash:            t.baselineHash,
		StartedArtifactHash:     t.startedArtifactHash,
		StartedMemoryHash:       t.startedMemoryHash,
		StartedSummaryHash:      t.startedSummaryHash,
		CompletedAt:             now,
	}); completeErr != nil {
		logs.CtxError(completeCtx, "[memory] complete consolidation failed: user_id=%s thread_id=%s turn_id=%s err=%v", t.userID, t.threadID, turnID, completeErr)
		yieldErr = completeErr
		if markErr := t.markStage2Error(completeCtx, completeErr.Error(), now); markErr != nil {
			yieldErr = fmt.Errorf("%w; mark stage2 error: %v", completeErr, markErr)
		}
	} else {
		logs.CtxInfo(completeCtx, "[memory] complete consolidation success: user_id=%s thread_id=%s turn_id=%s watermark=%s", t.userID, t.threadID, turnID, t.watermark)
	}

	t.stopHeartbeat()
	t.mu.Lock()
	if t.active == active {
		t.active = nil
	}
	if t.closed && t.initCancel != nil {
		t.initCancel()
	}
	t.mu.Unlock()

	t.emitYield(completeCtx, agentworker.ThreadYield{Reason: consolidationDoneReason, Err: yieldErr})
}

func (t *consolidationAgentThread) markStage2Error(ctx context.Context, summary string, failedAt time.Time) error {
	return t.store.MarkStage2Error(ctx, MarkStage2ErrorRequest{
		UserID:         t.userID,
		OwnershipToken: t.ownershipToken,
		ErrorSummary:   summary,
		RetryAt:        failedAt.Add(t.retryDelay),
		FailedAt:       failedAt,
	})
}

func (t *consolidationAgentThread) emitYield(ctx context.Context, yield agentworker.ThreadYield) {
	select {
	case t.output <- agentworker.ThreadOutputItem{Yield: &yield}:
	case <-ctx.Done():
	}
}

func (t *consolidationAgentThread) drainEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-t.eventBus:
			if !ok {
				return
			}
		}
	}
}

func (t *consolidationAgentThread) startHeartbeatLocked(ctx context.Context) {
	if t.heartbeatCancel != nil || t.store == nil || t.userID == "" || t.ownershipToken == "" {
		return
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	t.heartbeatCancel = cancel
	done := make(chan struct{})
	t.heartbeatDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(t.heartbeatEvery)
		defer ticker.Stop()
		if !t.heartbeat(heartbeatCtx) {
			return
		}
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if !t.heartbeat(heartbeatCtx) {
					return
				}
			}
		}
	}()
}

func (t *consolidationAgentThread) stopHeartbeat() {
	t.mu.Lock()
	cancel := t.heartbeatCancel
	done := t.heartbeatDone
	t.heartbeatCancel = nil
	t.heartbeatDone = nil
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (t *consolidationAgentThread) heartbeat(ctx context.Context) (keepRunning bool) {
	if err := t.store.HeartbeatStage2(ctx, HeartbeatStage2Request{
		UserID:         t.userID,
		OwnershipToken: t.ownershipToken,
		LeaseTTL:       t.leaseTTL,
		HeartbeatAt:    t.now(),
	}); err != nil {
		logs.CtxWarn(ctx, "[memory] consolidation heartbeat failed: user_id=%s thread_id=%s err=%v", t.userID, t.threadID, err)
		if errors.Is(err, ErrStage2JobLeaseLost) {
			t.mu.Lock()
			t.closed = true
			t.mu.Unlock()
			go func() { _ = t.Close(context.Background()) }()
			return false
		}
	}
	return true
}

func stage2HeartbeatInterval(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return time.Minute
	}
	interval := ttl / 3
	if interval < 10*time.Millisecond {
		return 10 * time.Millisecond
	}
	return interval
}

type consolidationInputMessage struct {
	Parts []consolidationInputPart `json:"parts"`
}

type consolidationInputPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func promptFromConsolidationMessage(msg *agentworker.Message) (string, error) {
	var input consolidationInputMessage
	if err := sonic.Unmarshal(msg.Payload, &input); err != nil {
		return "", fmt.Errorf("memory: unmarshal consolidation input: %w", err)
	}
	text := make([]string, 0, len(input.Parts))
	for i, part := range input.Parts {
		if strings.TrimSpace(part.Type) != "text" {
			return "", fmt.Errorf("memory: consolidation input part %d has unsupported type %q", i, part.Type)
		}
		if value := strings.TrimSpace(part.Text); value != "" {
			text = append(text, value)
		}
	}
	prompt := strings.Join(text, "\n")
	if prompt == "" {
		return "", fmt.Errorf("memory: consolidation input text is required")
	}
	return prompt, nil
}
