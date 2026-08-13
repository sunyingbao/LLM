//go:build !windows

package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"code.byted.org/gopkg/logs/v2"
	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	acsvc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator/agentcoordinatorservice"
	"eino-cli/deepagent/worker"
)

const (
	defaultConcurrency             = 1
	defaultScanLimit               = int32(20)
	defaultMessageLimit            = int32(50)
	defaultLeaseMS                 = int64(60_000)
	defaultScanInterval            = time.Second
	defaultMessagePollInterval     = 500 * time.Millisecond
	defaultIdleTimeout             = 10 * time.Second
	defaultShutdownDrainTimeout    = 120 * time.Second
	defaultShutdownInterruptDrain  = 120 * time.Second
	defaultInterruptDrainTimeout   = 30 * time.Second
	defaultRuntimeInterruptTimeout = 15 * time.Second
	defaultAppendEventAttempts     = 3
	defaultAppendEventRetryDelay   = 100 * time.Millisecond
	defaultReleaseReason           = "agent thread completed"
	defaultErrorReleaseReason      = "agent thread failed"
	buildThreadFailedReason        = "agent thread build failed"
	initThreadFailedReason         = "agent thread init failed"
	postMessageFailedReason        = "agent thread post message failed"
	ackMessageFailedReason         = "agent thread ack failed"
	controlInputFailedReason       = "agent thread control input failed"
	interruptFailedReason          = "agent thread interrupt failed"
	defaultGracefulReleaseReason   = "worker graceful exit"
	defaultShutdownTimeoutReason   = "worker graceful exit timeout"
	defaultInterruptTimeoutReason  = "agent thread interrupt timeout"
	defaultThreadClosedReason      = "agent thread closed"
	defaultCloseThreadReason       = "user_close"
	maxPullErrorBackoff            = 5 * time.Second
)

// Worker owns the Agent Coordinator worker lifecycle: scan, claim, renew,
// append events, ack messages, and release leases.
type Worker struct {
	Namespace string
	// Env fixes the Agent Coordinator lane this worker scans. Empty scans the
	// default lane.
	Env                string
	Client             acsvc.Client
	AgentThreadFactory AgentThreadFactory

	Concurrency    int
	ScanLimit      int32
	MessageLimit   int32
	LeaseMS        int64
	LeaseOwnerHint string

	ScanInterval        time.Duration
	RenewInterval       time.Duration
	MessagePollInterval time.Duration
	// IdleTimeout is the normal-operation quiet period before a claimed thread
	// is released after it becomes inactive.
	IdleTimeout time.Duration
	// ShutdownDrainTimeout is the maximum time a shutting-down worker waits for
	// an already active turn to finish naturally. During this window the worker
	// keeps renewing the lease and appending runtime output, but it does not pull
	// or deliver new pending messages.
	ShutdownDrainTimeout time.Duration
	// ShutdownInterruptDrainTimeout is the final grace period after shutdown
	// drain timed out and the worker asked the runtime to interrupt the active
	// turn. Runtime output is still consumed during this window so business code
	// can emit its own terminal event.
	ShutdownInterruptDrainTimeout time.Duration
	// InterruptDrainTimeout is the maximum time the worker waits for a runtime
	// to become inactive after a user/control-plane interrupt request. During
	// this window the worker keeps appending runtime output but does not deliver
	// new ordinary input.
	InterruptDrainTimeout time.Duration
	// RuntimeInterruptTimeout is passed to the runtime when interrupting a
	// running turn. If the turn is still blocked after this duration, the
	// runtime may return an interrupted result before the blocked call exits.
	// If unset, the worker uses 15s capped below the active drain window.
	RuntimeInterruptTimeout time.Duration
}

func (w *Worker) normalize() {
	if w.Concurrency <= 0 {
		w.Concurrency = defaultConcurrency
	}
	if w.ScanLimit <= 0 {
		w.ScanLimit = defaultScanLimit
	}
	if w.MessageLimit <= 0 {
		w.MessageLimit = defaultMessageLimit
	}
	if w.LeaseMS <= 0 {
		w.LeaseMS = defaultLeaseMS
	}
	if w.ScanInterval <= 0 {
		w.ScanInterval = defaultScanInterval
	}
	if w.RenewInterval <= 0 {
		w.RenewInterval = time.Duration(w.LeaseMS) * time.Millisecond / 3
		if w.RenewInterval <= 0 {
			w.RenewInterval = defaultScanInterval
		}
	}
	if w.MessagePollInterval <= 0 {
		w.MessagePollInterval = defaultMessagePollInterval
	}
	if w.IdleTimeout <= 0 {
		w.IdleTimeout = defaultIdleTimeout
	}
	if w.ShutdownDrainTimeout <= 0 {
		w.ShutdownDrainTimeout = defaultShutdownDrainTimeout
	}
	if w.ShutdownInterruptDrainTimeout <= 0 {
		w.ShutdownInterruptDrainTimeout = defaultShutdownInterruptDrain
	}
	if w.InterruptDrainTimeout <= 0 {
		w.InterruptDrainTimeout = defaultInterruptDrainTimeout
	}
}

func (w *Worker) Validate() error {
	if w == nil {
		return errors.New("agentworker: worker is nil")
	}
	if w.Namespace == "" {
		return ErrMissingNamespace
	}
	if w.Client == nil {
		return ErrMissingClient
	}
	if w.AgentThreadFactory == nil {
		return ErrMissingAgentThreadFactory
	}
	return nil
}

// Run starts the worker loop. It keeps running through scan, claim, and
// per-thread processing errors; those errors are logged and the worker retries
// until ctx is canceled.
func (w *Worker) Run(ctx context.Context) error {
	w.normalize()
	if err := w.Validate(); err != nil {
		return err
	}

	sem := make(chan struct{}, w.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		scanCtx := w.newScanLogContext(ctx)
		scanStartedAt := time.Now()
		logs.CtxInfo(scanCtx, "[agentworker] scan runnable threads start: limit=%d env=%q", w.ScanLimit, w.Env)
		threads, err := w.scanRunnableThreads(scanCtx)
		if err != nil {
			recordWorkerError(w.Namespace, "scan_threads", err)
			logs.CtxError(scanCtx, "[agentworker] scan runnable threads failed: elapsed=%s err=%v", time.Since(scanStartedAt), err)
			if err := sleepContext(ctx, w.ScanInterval); err != nil {
				return err
			}
			continue
		}
		recordScanResult(w.Namespace, len(threads))
		logs.CtxInfo(scanCtx, "[agentworker] scan runnable threads finished: count=%d elapsed=%s threads=%v truncated=%t",
			len(threads), time.Since(scanStartedAt), threadSummaries(threads), len(threads) > logThreadSummaryLimit)

		for _, thread := range threads {
			if thread == nil {
				continue
			}
			if err := acquireSlot(ctx, sem); err != nil {
				return err
			}
			threadID := thread.GetThreadId()
			claimCtx := w.newClaimLogContext(scanCtx, thread)
			claimStartedAt := time.Now()
			claimedCtx, claim, err := w.claimThreadWithLog(claimCtx, threadID)
			if err != nil {
				releaseSlot(sem)
				recordWorkerError(w.Namespace, "claim_thread", err)
				recordClaimFinished(w.Namespace, "error", time.Since(claimStartedAt))
				logs.CtxError(claimCtx, "[agentworker] claim scanned thread failed: %v", err)
				continue
			}
			runCtx := context.WithoutCancel(w.newRunLogContext(claimedCtx, claim))
			wg.Add(1)
			go func(runCtx context.Context, acceptCtx context.Context, claim *claimResult) {
				defer wg.Done()
				defer releaseSlot(sem)
				if err := w.runClaim(runCtx, acceptCtx, claim); err != nil {
					logs.CtxError(runCtx, "[agentworker] process thread failed: %v", err)
				}
			}(runCtx, ctx, claim)
		}
		if err := sleepContext(ctx, w.ScanInterval); err != nil {
			return err
		}
	}
}

func (w *Worker) scanRunnableThreads(ctx context.Context) ([]*ac.Thread, error) {
	req := &ac.ScanRunnableThreadsRequest{
		Namespace: w.Namespace,
		Limit:     &w.ScanLimit,
	}
	if w.Env != "" {
		req.Env = &w.Env
	}
	req.GetOrSetBase()
	resp, err := w.Client.ScanRunnableThreads(ctx, req)
	if err := rpcError("ScanRunnableThreads", resp, err); err != nil {
		return nil, err
	}
	return resp.GetThreads(), nil
}

func (w *Worker) claimThreadWithLog(ctx context.Context, threadID int64) (context.Context, *claimResult, error) {
	logs.CtxInfo(ctx, "[agentworker] claim thread start")
	claim, err := w.claimThread(ctx, threadID)
	if err != nil {
		return ctx, nil, err
	}
	ctx = logs.CtxAddKVs(ctx,
		"thread_status", fmt.Sprint(claim.thread.GetStatus()),
		"thread_status_reason", claim.thread.GetStatusReason(),
		"lease_deadline_at_ms", claim.lease.GetLeaseDeadlineAtMs(),
		"pending_message_count", len(claim.pendingMessages),
		"first_pending_message_id", firstMessageID(claim.pendingMessages),
		"first_pending_message_logid", firstMessageLogID(claim.pendingMessages),
	)
	logs.CtxInfo(ctx,
		"[agentworker] claim thread success: thread_status=%s status_reason=%q lease_owner_hint=%q lease_deadline_at_ms=%d pending_message_count=%d first_pending_message=%s",
		claim.thread.GetStatus(), claim.thread.GetStatusReason(), claim.thread.GetLeaseOwnerHint(), claim.lease.GetLeaseDeadlineAtMs(),
		len(claim.pendingMessages), messageLogSummary(firstMessage(claim.pendingMessages)),
	)
	return ctx, claim, nil
}

type claimResult struct {
	thread          *ac.Thread
	lease           *ac.Lease
	pendingMessages []*ac.Message
}

func (w *Worker) claimThread(ctx context.Context, threadID int64) (*claimResult, error) {
	req := &ac.ClaimThreadRequest{
		Namespace:      w.Namespace,
		ThreadId:       threadID,
		LeaseMs:        &w.LeaseMS,
		MessageLimit:   &w.MessageLimit,
		LeaseOwnerHint: &w.LeaseOwnerHint,
	}
	req.GetOrSetBase()
	resp, err := w.Client.ClaimThread(ctx, req)
	if err := rpcError(fmt.Sprintf("ClaimThread thread_id=%d", threadID), resp, err); err != nil {
		return nil, err
	}
	if resp.GetThread() == nil {
		return nil, ErrMissingThread
	}
	if resp.GetLease() == nil {
		return nil, ErrMissingLease
	}
	return &claimResult{
		thread:          resp.GetThread(),
		lease:           resp.GetLease(),
		pendingMessages: resp.GetPendingMessages(),
	}, nil
}

func (w *Worker) runClaim(ctx context.Context, acceptCtx context.Context, claim *claimResult) error {
	startedAt := time.Now()
	claimResult := "error"
	recordActiveClaim(w.Namespace, 1)
	defer func() {
		recordActiveClaim(w.Namespace, -1)
		recordClaimFinished(w.Namespace, claimResult, time.Since(startedAt))
	}()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	logs.CtxInfo(runCtx,
		"[agentworker] run claim start: thread_id=%d thread_status=%s status_reason=%q pending_message_count=%d lease_deadline_at_ms=%d idle_timeout=%s poll_interval=%s",
		claim.thread.GetThreadId(), claim.thread.GetStatus(), claim.thread.GetStatusReason(), len(claim.pendingMessages),
		claim.lease.GetLeaseDeadlineAtMs(), w.IdleTimeout, w.MessagePollInterval,
	)
	renewDone := make(chan struct{})
	go w.renewLoop(runCtx, claim.lease, renewDone)
	defer func() {
		cancel()
		<-renewDone
	}()

	runtimeCtx := contextWithThreadRequestMeta(runCtx, claim.thread)
	logs.CtxInfo(runCtx,
		"[agentworker] build thread start: pending_message_count=%d first_pending_message=%s",
		len(claim.pendingMessages), messageLogSummary(firstMessage(claim.pendingMessages)),
	)
	phaseStartedAt := time.Now()
	agentThread, err := w.AgentThreadFactory(runtimeCtx, claim.thread)
	if err != nil {
		err = fmt.Errorf("AgentThreadFactory thread_id=%d: %w", claim.thread.GetThreadId(), err)
		recordWorkerError(w.Namespace, "build_thread", err)
		logs.CtxError(runCtx,
			"[agentworker] build thread failed: elapsed=%s pending_message_count=%d first_pending_message=%s err=%v",
			time.Since(phaseStartedAt), len(claim.pendingMessages), messageLogSummary(firstMessage(claim.pendingMessages)), err,
		)
		releaseErr := w.releaseThread(runCtx, claim.lease, buildThreadFailedReason, nil)
		return errors.Join(err, releaseErr)
	}
	logs.CtxInfo(runCtx, "[agentworker] build thread success: elapsed=%s", time.Since(phaseStartedAt))

	logs.CtxInfo(runCtx, "[agentworker] init thread start")
	phaseStartedAt = time.Now()
	output, err := agentThread.Init(runtimeCtx)
	if err != nil {
		closeErr := agentThread.Close(runtimeCtx)
		err = errors.Join(fmt.Errorf("AgentThread.Init thread_id=%d: %w", claim.thread.GetThreadId(), err), closeErr)
		recordWorkerError(w.Namespace, "init_thread", err)
		logs.CtxError(runCtx,
			"[agentworker] init thread failed: elapsed=%s pending_message_count=%d first_pending_message=%s close_err=%v err=%v",
			time.Since(phaseStartedAt), len(claim.pendingMessages), messageLogSummary(firstMessage(claim.pendingMessages)), closeErr, err,
		)
		releaseErr := w.releaseThread(runCtx, claim.lease, initThreadFailedReason, nil)
		return errors.Join(err, releaseErr)
	}
	logs.CtxInfo(runCtx, "[agentworker] init thread success: elapsed=%s", time.Since(phaseStartedAt))
	if output == nil {
		output = &agentworker.ThreadOutput{}
	}
	recordMessagesReceived(w.Namespace, claim.pendingMessages)

	logs.CtxInfo(runCtx, "[agentworker] message loop start: thread_id=%d pending_message_count=%d", claim.thread.GetThreadId(), len(claim.pendingMessages))
	end := w.messageLoop(runCtx, acceptCtx, claim, agentThread, output)
	if end == nil {
		end = newReleaseAction(defaultReleaseReason, nil)
	}
	logs.CtxInfo(runCtx,
		"[agentworker] message loop finished: thread_id=%d action_kind=%d reason=%q err=%v block_present=%t control_message_id=%d",
		claim.thread.GetThreadId(), end.kind, end.reason, end.err, end.block != nil, end.controlMessageID,
	)
	closeErr := agentThread.Close(runtimeCtx)
	switch end.kind {
	case claimActionRelease:
		logs.CtxInfo(runCtx, "[agentworker] claim release action: reason=%q block_present=%t err=%v close_err=%v",
			end.releaseReason(closeErr), end.block != nil, end.err, closeErr)
		releaseErr := w.releaseThread(runCtx, claim.lease, end.releaseReason(closeErr), end.block)
		claimResult = claimReleaseResult(end, closeErr, releaseErr)
		return errors.Join(end.err, closeErr, releaseErr)
	case claimActionCompleteClose:
		logs.CtxInfo(runCtx, "[agentworker] claim complete close action: control_message_id=%d reason=%q close_err=%v",
			end.controlMessageID, end.reason, closeErr)
		completeErr := w.completeCloseThread(runCtx, claim.lease, end.controlMessageID, end.reason)
		claimResult = claimClosedResult(closeErr, completeErr)
		return errors.Join(closeErr, completeErr)
	default:
		err := fmt.Errorf("unknown claim end kind: %d", end.kind)
		recordWorkerError(w.Namespace, "resolve_claim", err)
		releaseErr := w.releaseThread(runCtx, claim.lease, defaultErrorReleaseReason, nil)
		return errors.Join(err, closeErr, releaseErr)
	}
}

func claimReleaseResult(end *claimAction, closeErr, releaseErr error) string {
	if end == nil || end.err != nil || closeErr != nil || releaseErr != nil {
		return "error"
	}
	if end.block != nil {
		return "block"
	}
	return "release"
}

func claimClosedResult(closeErr, completeErr error) string {
	if closeErr != nil || completeErr != nil {
		return "error"
	}
	return "closed"
}

func (w *Worker) messageLoop(ctx context.Context, acceptCtx context.Context, claim *claimResult, agentThread agentworker.AgentThread, output *agentworker.ThreadOutput) *claimAction {
	pollInterval := w.MessagePollInterval
	if pollInterval <= 0 {
		pollInterval = defaultMessagePollInterval
	}
	coordinator := newClaimCoordinator(claimCoordinatorConfig{
		worker:      w,
		ctx:         ctx,
		acceptDone:  acceptCtx.Done(),
		claim:       claim,
		agentThread: agentThread,
		items:       output.Items,
		pending:     claim.pendingMessages,
		pollEvery:   pollInterval,
	})
	return coordinator.run()
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

type claimCoordinatorConfig struct {
	worker      *Worker
	ctx         context.Context
	acceptDone  <-chan struct{}
	claim       *claimResult
	agentThread agentworker.AgentThread
	items       <-chan agentworker.ThreadOutputItem
	pending     []*ac.Message
	pollEvery   time.Duration
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

func newClaimCoordinator(cfg claimCoordinatorConfig) *claimCoordinator {
	progress := &claimProgress{}
	stopCh := make(chan struct{})
	output := newOutputItemProcessor(outputItemProcessorConfig{
		worker:   cfg.worker,
		ctx:      cfg.ctx,
		stopCh:   stopCh,
		threadID: cfg.claim.thread.GetThreadId(),
		items:    cfg.items,
	})
	input := newInputMessageProcessor(inputMessageProcessorConfig{
		worker:      cfg.worker,
		ctx:         cfg.ctx,
		stopCh:      stopCh,
		acceptDone:  cfg.acceptDone,
		claim:       cfg.claim,
		agentThread: cfg.agentThread,
		pending:     cfg.pending,
		pollEvery:   cfg.pollEvery,
		progress:    progress,
	})
	return &claimCoordinator{
		worker:      cfg.worker,
		ctx:         cfg.ctx,
		acceptDone:  cfg.acceptDone,
		stopCh:      stopCh,
		claim:       cfg.claim,
		agentThread: cfg.agentThread,
		pollEvery:   cfg.pollEvery,
		input:       input,
		output:      output,
		progress:    progress,
		idleSince:   time.Now(),
		wasActive:   cfg.agentThread.ActiveTurn() != nil,
	}
}

func (c *claimCoordinator) run() *claimAction {
	logs.CtxInfo(c.ctx,
		"[agentworker] claim coordinator start: thread_id=%d pending_message_count=%d poll_interval=%s initially_active=%t",
		c.claim.thread.GetThreadId(), len(c.input.pending), c.pollEvery, c.wasActive,
	)
	c.output.start()
	c.input.start()

	idleTicker := time.NewTicker(c.pollEvery)
	defer idleTicker.Stop()

	for {
		if c.acceptStopped() {
			return c.runDraining("accept context canceled")
		}
		if end := c.idleRelease(); end != nil {
			return c.stopAndResolve(end)
		}

		select {
		case signal := <-c.input.Signal():
			return c.stopAndResolve(c.actionFromInputStop(signal))
		case signal := <-c.output.Signal():
			return c.stopAndResolveAfterYield(signal)
		case <-c.ctx.Done():
			return c.stopAndResolve(newReleaseAction(defaultErrorReleaseReason, c.ctx.Err()))
		case <-c.acceptDone:
			return c.runDraining("accept context canceled")
		case <-idleTicker.C:
			continue
		}
	}
}

func (c *claimCoordinator) stopProcessors() (inputSignal *inputStopReason, outputSignal *runtimeYield) {
	logs.CtxInfo(c.ctx, "[agentworker] claim coordinator stop processors start: thread_id=%d", c.claim.thread.GetThreadId())
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	inputSignal = c.input.wait()
	outputSignal = c.output.wait()
	logs.CtxInfo(c.ctx,
		"[agentworker] claim coordinator stop processors complete: thread_id=%d input_signal=%s output_signal=%s",
		c.claim.thread.GetThreadId(), inputSignalSummary(inputSignal), runtimeYieldSummary(outputSignal),
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

func (c *claimCoordinator) stopAndResolve(end *claimAction) *claimAction {
	inputSignal, outputSignal := c.stopProcessors()
	return c.resolveClaimAction(end, inputSignal, outputSignal)
}

func (c *claimCoordinator) stopAndResolveAfterYield(signal runtimeYield) *claimAction {
	inputSignal, outputSignal := c.stopProcessors()
	if outputSignal == nil {
		outputSignal = &signal
	}
	return c.resolveClaimAction(nil, inputSignal, outputSignal)
}

// resolveClaimAction is the single arbitration point after the coordinator has
// stopped both processors. It orders facts by ownership, not by which goroutine
// reported first: close controls and input commit failures are control-plane
// outcomes, runtime yield is the authoritative turn outcome, and requested
// actions are passive coordinator endings such as idle or graceful shutdown.
func (c *claimCoordinator) resolveClaimAction(requested *claimAction, inputSignal *inputStopReason, outputSignal *runtimeYield) *claimAction {
	logs.CtxInfo(c.ctx,
		"[agentworker] resolve claim action start: thread_id=%d requested=%s input_signal=%s output_signal=%s",
		coordinatorThreadID(c), claimActionSummary(requested), inputSignalSummary(inputSignal), runtimeYieldSummary(outputSignal),
	)
	if inputSignal != nil && isControlPlaneInputStop(inputSignal.kind) {
		action := c.actionFromInputStop(*inputSignal)
		logs.CtxInfo(c.ctx, "[agentworker] resolve claim action result: source=input_control action=%s", claimActionSummary(action))
		return action
	}
	if outputSignal != nil {
		action := newReleaseActionFromYield(*outputSignal)
		logs.CtxInfo(c.ctx, "[agentworker] resolve claim action result: source=runtime_yield action=%s", claimActionSummary(action))
		return action
	}
	if inputSignal != nil {
		action := c.actionFromInputStop(*inputSignal)
		logs.CtxInfo(c.ctx, "[agentworker] resolve claim action result: source=input action=%s", claimActionSummary(action))
		return action
	}
	if requested != nil {
		logs.CtxInfo(c.ctx, "[agentworker] resolve claim action result: source=requested action=%s", claimActionSummary(requested))
		return requested
	}
	action := newReleaseAction(defaultReleaseReason, nil)
	logs.CtxInfo(c.ctx, "[agentworker] resolve claim action result: source=default action=%s", claimActionSummary(action))
	return action
}

func isControlPlaneInputStop(kind inputStopKind) bool {
	switch kind {
	case inputStopFailed, inputStopCloseHandled:
		return true
	default:
		return false
	}
}

func coordinatorThreadID(c *claimCoordinator) int64 {
	if c == nil || c.claim == nil || c.claim.thread == nil {
		return 0
	}
	return c.claim.thread.GetThreadId()
}

func newReleaseActionFromYield(signal runtimeYield) *claimAction {
	return &claimAction{
		kind:   claimActionRelease,
		reason: signal.reason,
		err:    signal.err,
		block:  signal.block,
	}
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

func (c *claimCoordinator) runDraining(reason string) *claimAction {
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
			recordShutdownDrain(c.worker.Namespace, "finished")
			return c.stopAndResolve(newReleaseAction(defaultGracefulReleaseReason, nil))
		}

		select {
		case signal := <-c.input.Signal():
			logs.CtxInfo(c.ctx,
				"[agentworker] worker shutdown drain input signal: elapsed=%s reason=%s err=%v",
				time.Since(start), signal.reason, signal.err,
			)
			return c.stopAndResolve(c.actionFromInputStop(signal))
		case signal := <-c.output.Signal():
			logs.CtxInfo(c.ctx,
				"[agentworker] worker shutdown drain output signal: elapsed=%s reason=%s err=%v block_present=%t",
				time.Since(start), signal.reason, signal.err, signal.block != nil,
			)
			recordShutdownDrain(c.worker.Namespace, "finished")
			return c.stopAndResolveAfterYield(signal)
		case <-c.ctx.Done():
			activeTurnID, consumed := activeTurnForLog(c.agentThread.ActiveTurn())
			logs.CtxInfo(c.ctx,
				"[agentworker] worker shutdown drain context done: elapsed=%s err=%v active_turn=%s consumed_message_ids=%v",
				time.Since(start), c.ctx.Err(), activeTurnID, consumed,
			)
			return c.stopAndResolve(newReleaseAction(defaultErrorReleaseReason, c.ctx.Err()))
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
	recordInterrupt(c.worker.Namespace, agentworker.ThreadInterruptKindWorkerShutdownTimeout)
	runtimeInterruptTimeout := c.worker.runtimeInterruptTimeoutForDrain(c.worker.shutdownInterruptDrainTimeout())
	err := c.agentThread.Interrupt(c.ctx, agentworker.ThreadInterruptRequest{
		Kind:    agentworker.ThreadInterruptKindWorkerShutdownTimeout,
		Reason:  defaultShutdownTimeoutReason,
		Timeout: &runtimeInterruptTimeout,
	})
	if err != nil {
		recordWorkerError(c.worker.Namespace, "shutdown_drain", err)
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

func (c *claimCoordinator) runShutdownInterruptDraining(shutdownStartedAt time.Time) *claimAction {
	timeout := c.worker.shutdownInterruptDrainTimeout()
	logs.CtxInfo(c.ctx, "[agentworker] worker shutdown interrupt drain start: timeout=%s", timeout)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	poll := time.NewTicker(c.pollEvery)
	defer poll.Stop()

	for {
		select {
		case signal := <-c.input.Signal():
			logs.CtxInfo(c.ctx,
				"[agentworker] worker shutdown interrupt drain input signal: elapsed=%s reason=%s err=%v",
				time.Since(shutdownStartedAt), signal.reason, signal.err,
			)
			return c.stopAndResolve(c.actionFromInputStop(signal))
		case signal := <-c.output.Signal():
			logs.CtxInfo(c.ctx,
				"[agentworker] worker shutdown interrupt drain output signal: elapsed=%s reason=%s err=%v block_present=%t",
				time.Since(shutdownStartedAt), signal.reason, signal.err, signal.block != nil,
			)
			recordShutdownDrain(c.worker.Namespace, "interrupted")
			return c.stopAndResolveAfterYield(signal)
		case <-c.ctx.Done():
			activeTurnID, consumed := activeTurnForLog(c.agentThread.ActiveTurn())
			logs.CtxInfo(c.ctx,
				"[agentworker] worker shutdown interrupt drain context done: elapsed=%s err=%v active_turn=%s consumed_message_ids=%v",
				time.Since(shutdownStartedAt), c.ctx.Err(), activeTurnID, consumed,
			)
			return c.stopAndResolve(newReleaseAction(defaultErrorReleaseReason, c.ctx.Err()))
		case <-poll.C:
			continue
		case <-timer.C:
			activeTurnID, consumed := activeTurnForLog(c.agentThread.ActiveTurn())
			logs.CtxInfo(c.ctx,
				"[agentworker] worker shutdown interrupt drain timeout: elapsed=%s active_turn=%s consumed_message_ids=%v",
				time.Since(shutdownStartedAt), activeTurnID, consumed,
			)
			recordShutdownDrain(c.worker.Namespace, "timeout")
			return c.stopAndResolve(newReleaseAction(defaultShutdownTimeoutReason, nil))
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

type inputMessageProcessorConfig struct {
	worker      *Worker
	ctx         context.Context
	stopCh      <-chan struct{}
	acceptDone  <-chan struct{}
	claim       *claimResult
	agentThread agentworker.AgentThread
	pending     []*ac.Message
	pollEvery   time.Duration
	progress    *claimProgress
}

type inputMessageProcessor struct {
	worker      *Worker
	ctx         context.Context
	stopCh      <-chan struct{}
	acceptDone  <-chan struct{}
	claim       *claimResult
	agentThread agentworker.AgentThread
	pending     []*ac.Message
	pollEvery   time.Duration
	progress    *claimProgress
	signal      chan inputStopReason
	done        chan struct{}
	resultMu    sync.Mutex
	result      *inputStopReason
}

func newInputMessageProcessor(cfg inputMessageProcessorConfig) *inputMessageProcessor {
	return &inputMessageProcessor{
		worker:      cfg.worker,
		ctx:         cfg.ctx,
		stopCh:      cfg.stopCh,
		acceptDone:  cfg.acceptDone,
		claim:       cfg.claim,
		agentThread: cfg.agentThread,
		pending:     cfg.pending,
		pollEvery:   cfg.pollEvery,
		progress:    cfg.progress,
		signal:      make(chan inputStopReason, 1),
		done:        make(chan struct{}),
	}
}

func (p *inputMessageProcessor) start() {
	go p.run()
}

func (p *inputMessageProcessor) Signal() <-chan inputStopReason {
	return p.signal
}

func (p *inputMessageProcessor) wait() *inputStopReason {
	<-p.done
	p.resultMu.Lock()
	defer p.resultMu.Unlock()
	if p.result == nil {
		return nil
	}
	signal := *p.result
	return &signal
}

func (p *inputMessageProcessor) run() {
	defer close(p.done)
	logs.CtxInfo(p.ctx, "[agentworker] input processor start: thread_id=%d initial_pending_count=%d poll_interval=%s",
		p.claim.thread.GetThreadId(), len(p.pending), p.pollEvery)
	defer func() {
		logs.CtxInfo(p.ctx, "[agentworker] input processor stopped: thread_id=%d remaining_pending_count=%d", p.claim.thread.GetThreadId(), len(p.pending))
	}()
	pollTicker := time.NewTicker(p.pollEvery)
	defer pollTicker.Stop()
	consecutivePullErrors := 0

	for {
		if signal := p.handlePendingMessages(); signal != nil {
			p.finish(signal)
			return
		}

		select {
		case <-p.ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-p.acceptDone:
			return
		case <-pollTicker.C:
			pullCtx := p.worker.newPullLogContext(p.ctx, p.claim.lease)
			messages, err := p.worker.pullPendingMessages(pullCtx, p.claim.lease)
			if err != nil {
				if p.ctx.Err() != nil {
					return
				}
				consecutivePullErrors++
				recordWorkerError(p.worker.Namespace, "pull_messages", err)
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
			recordMessagesReceived(p.worker.Namespace, messages)
			logs.CtxInfo(pullCtx, "[agentworker] pull pending messages success: count=%d message_ids=%v", len(messages), messageIDs(messages))
			p.pending = append(p.pending, messages...)
		}
	}
}

func (p *inputMessageProcessor) waitPullRetryBackoff(consecutiveErrors int) bool {
	backoff := pullErrorBackoff(p.pollEvery, consecutiveErrors)
	if backoff <= 0 {
		return false
	}
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-p.ctx.Done():
		return true
	case <-p.stopCh:
		return true
	case <-p.acceptDone:
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
	p.resultMu.Lock()
	if p.result == nil {
		copy := *signal
		p.result = &copy
	}
	p.resultMu.Unlock()
	p.sendSignal(*signal)
}

func (p *inputMessageProcessor) sendSignal(signal inputStopReason) {
	select {
	case p.signal <- signal:
	case <-p.ctx.Done():
	case <-p.stopCh:
	}
}

// handlePendingMessages posts locally buffered AC messages to runtime and acks
// them after runtime accepts the post. Once the processor is stopped, remaining
// local pending messages stay unacked so the next worker can claim them.
func (p *inputMessageProcessor) handlePendingMessages() *inputStopReason {
	for len(p.pending) > 0 {
		select {
		case <-p.ctx.Done():
			return nil
		case <-p.stopCh:
			return nil
		case <-p.acceptDone:
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

func (p *inputMessageProcessor) handlePendingMessage(message *ac.Message) *inputStopReason {
	messageCtx := p.worker.newMessageLogContext(p.ctx, p.claim.thread, message)
	logs.CtxInfo(messageCtx, "[agentworker] handle pending message: message_id=%d message_type=%s payload_bytes=%d metadata_keys=%v",
		message.GetMessageId(), message.GetMessageType(), len(message.GetPayload()), metadataKeys(message.GetMetadata()))
	switch message.GetMessageType() {
	case MessageTypeControlCancelInput:
		return p.handleCancelInputControl(messageCtx, message)
	case MessageTypeControlCloseThread:
		return p.handleCloseThreadControl(messageCtx, message)
	}
	logs.CtxInfo(messageCtx, "[agentworker] enqueue message start: message=%s", messagePreview(message))
	messageRuntimeCtx := contextWithMessageRequestMeta(messageCtx, message)
	result, err := p.worker.enqueueMessage(messageRuntimeCtx, p.agentThread, p.claim.thread, p.claim.lease, message)
	if err != nil {
		if p.ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, agentworker.ErrThreadClosed) {
			recordWorkerError(p.worker.Namespace, "post_message", err)
			logs.CtxInfo(messageCtx, "[agentworker] runtime rejected message because thread is closed, leave message pending and release: %v", err)
			return &inputStopReason{kind: inputStopThreadClosed, reason: defaultThreadClosedReason}
		}
		recordWorkerError(p.worker.Namespace, "post_message", err)
		logs.CtxError(messageCtx, "[agentworker] enqueue message failed: %v", err)
		return &inputStopReason{kind: inputStopFailed, reason: postMessageFailedReason, err: err}
	}
	recordMessageAccepted(p.worker.Namespace, message)
	ackCtx := p.worker.newMessageLogContext(p.ctx, p.claim.thread, message)
	triggerTurnID := ""
	if result != nil {
		triggerTurnID = result.TurnID
	}
	logs.CtxInfo(ackCtx, "[agentworker] ack message start: message_id=%d trigger_turn_id=%q", message.GetMessageId(), triggerTurnID)
	if err := p.worker.ackMessage(ackCtx, p.claim.lease, message.GetMessageId(), triggerTurnID); err != nil {
		recordWorkerError(p.worker.Namespace, "ack_messages", err)
		logs.CtxError(ackCtx, "[agentworker] ack message failed: message_id=%d trigger_turn_id=%q err=%v", message.GetMessageId(), triggerTurnID, err)
		return &inputStopReason{kind: inputStopFailed, reason: ackMessageFailedReason, err: err}
	}
	p.progress.markInputActivity()
	logs.CtxInfo(messageCtx, "[agentworker] enqueue message success")
	return nil
}

func (p *inputMessageProcessor) handleCancelInputControl(ctx context.Context, message *ac.Message) *inputStopReason {
	logs.CtxInfo(ctx, "[agentworker] handle cancel input control start")
	var payload CancelInputControlPayload
	parseErr := json.Unmarshal(message.GetPayload(), &payload)
	ackCancelControl := func(phase string) *inputStopReason {
		ackCtx := p.worker.newMessageLogContext(p.ctx, p.claim.thread, message)
		logs.CtxInfo(ackCtx, "[agentworker] ack cancel input control start: phase=%s", phase)
		if err := p.worker.ackMessage(ackCtx, p.claim.lease, message.GetMessageId(), ""); err != nil {
			recordWorkerError(p.worker.Namespace, "ack_messages", err)
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
	if p.agentThread.ActiveTurn() == nil {
		logs.CtxInfo(ctx, "[agentworker] cancel input control complete without active turn")
		if ackSignal := ackCancelControl("no_active_turn"); ackSignal != nil {
			return ackSignal
		}
		return nil
	}
	recordInterrupt(p.worker.Namespace, agentworker.ThreadInterruptKindCancelInput)
	runtimeCtx := contextWithMessageRequestMeta(ctx, message)
	logs.CtxInfo(ctx, "[agentworker] cancel input interrupt start: cutoff_message_id=%d reason=%q", payload.CutoffMessageID, reason)
	runtimeInterruptTimeout := p.worker.runtimeInterruptTimeout()
	if err := p.agentThread.Interrupt(runtimeCtx, agentworker.ThreadInterruptRequest{
		Kind:             agentworker.ThreadInterruptKindCancelInput,
		ControlMessageID: fmt.Sprint(message.GetMessageId()),
		CutoffMessageID:  fmt.Sprint(payload.CutoffMessageID),
		Reason:           reason,
		Timeout:          &runtimeInterruptTimeout,
	}); err != nil {
		if p.ctx.Err() != nil {
			return nil
		}
		err = fmt.Errorf("AgentThread.Interrupt cancel_input: %w", err)
		recordWorkerError(p.worker.Namespace, "interrupt", err)
		logs.CtxError(ctx, "[agentworker] cancel input interrupt failed, leave control pending for retry: err=%v", err)
		return &inputStopReason{kind: inputStopFailed, reason: interruptFailedReason, err: err}
	}
	logs.CtxInfo(ctx, "[agentworker] cancel input interrupt accepted, wait for runtime drain: timeout_ms=%d", p.worker.interruptDrainTimeout().Milliseconds())
	timedOut, err := p.waitForInterruptDrain()
	if err != nil {
		if p.ctx.Err() != nil {
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

func (p *inputMessageProcessor) handleCloseThreadControl(ctx context.Context, message *ac.Message) *inputStopReason {
	logs.CtxInfo(ctx, "[agentworker] handle close thread control start")
	reason := defaultCloseThreadReason
	var payload CloseThreadControlPayload
	if err := json.Unmarshal(message.GetPayload(), &payload); err != nil {
		logs.CtxError(ctx, "[agentworker] close thread control payload invalid, continue with default reason: err=%v", err)
	} else if payload.Reason != "" {
		reason = payload.Reason
	}
	ackCtx := p.worker.newMessageLogContext(p.ctx, p.claim.thread, message)
	if err := p.worker.ackMessage(ackCtx, p.claim.lease, message.GetMessageId(), ""); err != nil {
		recordWorkerError(p.worker.Namespace, "ack_messages", err)
		return &inputStopReason{kind: inputStopFailed, reason: ackMessageFailedReason, err: err}
	}
	p.pending = nil

	if p.agentThread.ActiveTurn() != nil {
		recordInterrupt(p.worker.Namespace, agentworker.ThreadInterruptKindCloseThread)
		runtimeCtx := contextWithMessageRequestMeta(ctx, message)
		runtimeInterruptTimeout := p.worker.runtimeInterruptTimeout()
		if err := p.agentThread.Interrupt(runtimeCtx, agentworker.ThreadInterruptRequest{
			Kind:             agentworker.ThreadInterruptKindCloseThread,
			ControlMessageID: fmt.Sprint(message.GetMessageId()),
			Reason:           reason,
			Timeout:          &runtimeInterruptTimeout,
		}); err != nil {
			recordWorkerError(p.worker.Namespace, "interrupt", err)
			logs.CtxError(ctx, "[agentworker] close thread interrupt failed, continue close: err=%v", err)
		}
		if timedOut, err := p.waitForInterruptDrain(); err != nil {
			logs.CtxError(ctx, "[agentworker] close thread interrupt drain failed, continue close: err=%v", err)
		} else if timedOut {
			logs.CtxError(ctx, "[agentworker] close thread interrupt drain timeout, continue close")
		}
	}

	logs.CtxInfo(ctx, "[agentworker] handle close thread control success")
	return &inputStopReason{kind: inputStopCloseHandled, reason: reason, closeMessageID: message.GetMessageId()}
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
		if isOrdinaryMessage(message) && message.GetMessageId() <= cutoffMessageID {
			continue
		}
		filtered = append(filtered, message)
	}
	p.pending = filtered
}

func isOrdinaryMessage(message *ac.Message) bool {
	return message != nil && !IsControlMessageType(message.GetMessageType())
}

func (p *inputMessageProcessor) waitForInterruptDrain() (bool, error) {
	timeout := p.worker.interruptDrainTimeout()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	pollInterval := p.worker.MessagePollInterval
	if pollInterval <= 0 {
		pollInterval = defaultMessagePollInterval
	}
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()

	for {
		if p.agentThread.ActiveTurn() == nil {
			return false, nil
		}
		select {
		case <-p.ctx.Done():
			return false, p.ctx.Err()
		case <-p.stopCh:
			return false, nil
		case <-poll.C:
			continue
		case <-timer.C:
			return true, nil
		}
	}
}

type outputItemProcessorConfig struct {
	worker   *Worker
	ctx      context.Context
	stopCh   <-chan struct{}
	threadID int64
	items    <-chan agentworker.ThreadOutputItem
}

type outputItemProcessor struct {
	worker   *Worker
	ctx      context.Context
	stopCh   <-chan struct{}
	threadID int64
	items    <-chan agentworker.ThreadOutputItem
	signal   chan runtimeYield
	done     chan struct{}
	resultMu sync.Mutex
	result   *runtimeYield
}

func newOutputItemProcessor(cfg outputItemProcessorConfig) *outputItemProcessor {
	return &outputItemProcessor{
		worker:   cfg.worker,
		ctx:      cfg.ctx,
		stopCh:   cfg.stopCh,
		threadID: cfg.threadID,
		items:    cfg.items,
		signal:   make(chan runtimeYield, 1),
		done:     make(chan struct{}),
	}
}

func (p *outputItemProcessor) start() {
	go p.run()
}

func (p *outputItemProcessor) Signal() <-chan runtimeYield {
	return p.signal
}

func (p *outputItemProcessor) wait() *runtimeYield {
	<-p.done
	p.resultMu.Lock()
	defer p.resultMu.Unlock()
	if p.result == nil {
		return nil
	}
	signal := *p.result
	return &signal
}

func (p *outputItemProcessor) run() {
	defer close(p.done)
	logs.CtxInfo(p.ctx, "[agentworker] output processor start: thread_id=%d", p.threadID)
	defer func() {
		logs.CtxInfo(p.ctx, "[agentworker] output processor stopped: thread_id=%d signal=%s", p.threadID, runtimeYieldSummary(p.result))
	}()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.stopCh:
			p.drainReady()
			return
		case item, ok := <-p.items:
			if !ok {
				return
			}
			p.handleThreadOutput(item)
		}
	}
}

func (p *outputItemProcessor) drainReady() {
	for {
		select {
		case item, ok := <-p.items:
			if !ok {
				return
			}
			p.handleThreadOutput(item)
		default:
			return
		}
	}
}

func (p *outputItemProcessor) handleThreadOutput(item agentworker.ThreadOutputItem) {
	recordOutputItem(p.worker.Namespace, item)
	if item.Event != nil {
		logs.CtxInfo(p.ctx, "[agentworker] output event received: thread_id=%d turn_id=%s event_type=%s payload_bytes=%d",
			p.threadID, item.Event.TurnID, item.Event.Type, len(item.Event.Payload))
		p.worker.appendEventBestEffort(p.ctx, p.threadID, item.Event)
	}

	yield := item.Yield
	if yield == nil {
		return
	}
	logs.CtxInfo(p.ctx, "[agentworker] output yield received: thread_id=%d reason=%q err=%v block_present=%t",
		p.threadID, yield.Reason, yield.Err, yield.Block != nil)
	p.sendSignal(runtimeYield{
		reason: yield.Reason,
		err:    yield.Err,
		block:  yield.Block,
	})
}

func (p *outputItemProcessor) sendSignal(signal runtimeYield) {
	p.resultMu.Lock()
	if p.result == nil {
		copy := signal
		p.result = &copy
	}
	p.resultMu.Unlock()
	select {
	case p.signal <- signal:
	default:
	}
}

func (w *Worker) enqueueMessage(ctx context.Context, agentThread agentworker.AgentThread, thread *ac.Thread, lease *ac.Lease, message *ac.Message) (*agentworker.PostMessageResult, error) {
	msg := messageFromAC(message)
	result, err := agentThread.PostMessage(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("AgentThread.PostMessage thread_id=%d message_id=%d: %w", thread.GetThreadId(), message.GetMessageId(), err)
	}
	return result, nil
}

func (w *Worker) appendEventBestEffort(ctx context.Context, threadID int64, event *agentworker.Event) {
	req, eventType, payloadBytes, metadata, err := w.newAppendEventRequest(threadID, event)
	if err != nil {
		recordWorkerError(w.Namespace, "append_events", err)
		logs.CtxError(ctx, "[agentworker] drop invalid runtime event: thread_id=%d err=%v", threadID, err)
		return
	}
	var lastErr error
	for attempt := 1; attempt <= defaultAppendEventAttempts; attempt++ {
		if err := w.appendEventRequest(ctx, req, eventType, payloadBytes, metadata); err != nil {
			lastErr = err
			logs.CtxWarn(ctx, "[agentworker] append event failed: attempt=%d max_attempts=%d err=%v",
				attempt, defaultAppendEventAttempts, err)
			if attempt < defaultAppendEventAttempts {
				if sleepErr := sleepContext(ctx, defaultAppendEventRetryDelay); sleepErr != nil {
					logs.CtxWarn(ctx, "[agentworker] stop append event retries: err=%v", sleepErr)
					return
				}
			}
			continue
		}
		return
	}
	logs.CtxError(ctx, "[agentworker] drop runtime event after append retries: thread_id=%d turn_id=%s event_type=%s err=%v",
		threadID, req.GetTurnId(), eventType, lastErr)
	recordWorkerError(w.Namespace, "append_events", lastErr)
}

func (w *Worker) newAppendEventRequest(threadID int64, event *agentworker.Event) (*ac.AppendEventsRequest, string, int, map[string]string, error) {
	if event.ThreadID == "" {
		event.ThreadID = fmt.Sprint(threadID)
	}
	eventThreadID, err := parseInt64(event.ThreadID, "event_thread_id")
	if err != nil {
		return nil, "", 0, nil, err
	}
	if eventThreadID != threadID {
		return nil, "", 0, nil, fmt.Errorf("append event thread mismatch: event_thread_id=%d run_thread_id=%d", eventThreadID, threadID)
	}
	if event.TurnID == "" {
		return nil, "", 0, nil, fmt.Errorf("append event turn_id is required: thread_id=%d", threadID)
	}
	if event.Type == "" {
		return nil, "", 0, nil, fmt.Errorf("append event event_type is required: thread_id=%d turn_id=%s", threadID, event.TurnID)
	}
	acEvent := eventToAC(threadID, event)
	req := &ac.AppendEventsRequest{
		Namespace: w.Namespace,
		ThreadId:  threadID,
		TurnId:    &acEvent.TurnId,
		Events:    []*ac.Event{acEvent},
	}
	req.GetOrSetBase()
	return req, string(event.Type), len(event.Payload), event.Metadata, nil
}

func (w *Worker) appendEventRequest(ctx context.Context, req *ac.AppendEventsRequest, eventType string, payloadBytes int, metadata map[string]string) error {
	startedAt := time.Now()
	resp, err := w.Client.AppendEvents(ctx, req)
	elapsed := time.Since(startedAt)
	if err := rpcError(fmt.Sprintf("AppendEvents thread_id=%d turn_id=%s event_type=%s", req.GetThreadId(), req.GetTurnId(), eventType), resp, err); err != nil {
		logs.CtxWarn(ctx, "[agentworker] append event request failed: thread_id=%d turn_id=%s event_type=%s payload_bytes=%d elapsed=%s err=%v",
			req.GetThreadId(), req.GetTurnId(), eventType, payloadBytes, elapsed, err)
		return err
	}
	recordEventAppend(w.Namespace, len(req.GetEvents()))
	eventID := int64(0)
	if events := resp.GetEvents(); len(events) > 0 && events[0] != nil {
		eventID = events[0].GetEventId()
	}
	logs.CtxInfo(ctx, "[agentworker] append event success: event_id=%d turn_id=%s event_type=%s payload_bytes=%d elapsed=%s metadata=%v",
		eventID, req.GetTurnId(), eventType, payloadBytes, elapsed, metadata)
	return nil
}

func (w *Worker) pullPendingMessages(ctx context.Context, lease *ac.Lease) ([]*ac.Message, error) {
	req := &ac.PullPendingMessagesRequest{
		Namespace:  w.Namespace,
		ThreadId:   lease.GetThreadId(),
		LeaseToken: lease.GetLeaseToken(),
		Limit:      &w.MessageLimit,
	}
	req.GetOrSetBase()
	resp, err := w.Client.PullPendingMessages(ctx, req)
	if err := rpcError(fmt.Sprintf("PullPendingMessages thread_id=%d", lease.GetThreadId()), resp, err); err != nil {
		return nil, err
	}
	return resp.GetPendingMessages(), nil
}

func (w *Worker) ackMessage(ctx context.Context, lease *ac.Lease, messageID int64, triggerTurnID string) error {
	req := &ac.AckThreadMessagesRequest{
		Namespace:  w.Namespace,
		ThreadId:   lease.GetThreadId(),
		LeaseToken: lease.GetLeaseToken(),
		MessageIds: []int64{messageID},
	}
	if triggerTurnID != "" {
		req.TriggerTurnId = &triggerTurnID
	}
	req.GetOrSetBase()
	logs.CtxInfo(ctx, "[agentworker] call AckThreadMessages: thread_id=%d message_id=%d trigger_turn_id=%q", lease.GetThreadId(), messageID, triggerTurnID)
	resp, err := w.Client.AckThreadMessages(ctx, req)
	if err := rpcError(fmt.Sprintf("AckThreadMessages thread_id=%d message_id=%d", lease.GetThreadId(), messageID), resp, err); err != nil {
		logs.CtxError(ctx, "[agentworker] AckThreadMessages failed: thread_id=%d message_id=%d trigger_turn_id=%q err=%v", lease.GetThreadId(), messageID, triggerTurnID, err)
		return err
	}
	recordMessageAck(w.Namespace, len(req.GetMessageIds()))
	logs.CtxInfo(ctx, "[agentworker] message acked")
	return nil
}

func (w *Worker) releaseThread(ctx context.Context, lease *ac.Lease, reason string, block *agentworker.PendingBlock) error {
	if reason == "" {
		reason = defaultReleaseReason
	}
	releaseToStatus := releaseStatusFromBlock(block)
	req := &ac.ReleaseThreadRequest{
		Namespace:       w.Namespace,
		ThreadId:        lease.GetThreadId(),
		LeaseToken:      lease.GetLeaseToken(),
		Reason:          &reason,
		ReleaseToStatus: releaseToStatus,
	}
	req.GetOrSetBase()
	logs.CtxInfo(ctx, "[agentworker] call ReleaseThread: thread_id=%d reason=%q release_to_status=%v block_present=%t",
		lease.GetThreadId(), reason, releaseToStatus, block != nil)
	resp, err := w.Client.ReleaseThread(ctx, req)
	if err := rpcError(fmt.Sprintf("ReleaseThread thread_id=%d", lease.GetThreadId()), resp, err); err != nil {
		recordWorkerError(w.Namespace, "release_thread", err)
		logs.CtxError(ctx, "[agentworker] ReleaseThread failed: thread_id=%d reason=%q release_to_status=%v err=%v",
			lease.GetThreadId(), reason, releaseToStatus, err)
		return err
	}
	recordRelease(w.Namespace, block)
	logs.CtxInfo(ctx, "[agentworker] release thread success: reason=%q release_to_status=%v", reason, releaseToStatus)
	return nil
}

func (w *Worker) shutdownDrainTimeout() time.Duration {
	if w.ShutdownDrainTimeout > 0 {
		return w.ShutdownDrainTimeout
	}
	return defaultShutdownDrainTimeout
}

func (w *Worker) shutdownInterruptDrainTimeout() time.Duration {
	if w.ShutdownInterruptDrainTimeout > 0 {
		return w.ShutdownInterruptDrainTimeout
	}
	return defaultShutdownInterruptDrain
}

func (w *Worker) interruptDrainTimeout() time.Duration {
	if w.InterruptDrainTimeout > 0 {
		return w.InterruptDrainTimeout
	}
	return defaultInterruptDrainTimeout
}

func (w *Worker) runtimeInterruptTimeout() time.Duration {
	return w.runtimeInterruptTimeoutForDrain(w.interruptDrainTimeout())
}

func (w *Worker) runtimeInterruptTimeoutForDrain(drain time.Duration) time.Duration {
	timeout := w.RuntimeInterruptTimeout
	if timeout <= 0 {
		timeout = defaultRuntimeInterruptTimeout
	}
	if drain > 0 && timeout >= drain {
		adjusted := drain / 2
		if adjusted > 0 {
			return adjusted
		}
		return drain
	}
	return timeout
}

func (w *Worker) completeCloseThread(ctx context.Context, lease *ac.Lease, controlMessageID int64, reason string) error {
	req := &ac.CompleteCloseThreadRequest{
		Namespace:        w.Namespace,
		ThreadId:         lease.GetThreadId(),
		LeaseToken:       lease.GetLeaseToken(),
		ControlMessageId: controlMessageID,
		Reason:           &reason,
	}
	req.GetOrSetBase()
	resp, err := w.Client.CompleteCloseThread(ctx, req)
	if err := rpcError(fmt.Sprintf("CompleteCloseThread thread_id=%d control_message_id=%d", lease.GetThreadId(), controlMessageID), resp, err); err != nil {
		recordWorkerError(w.Namespace, "complete_close_thread", err)
		return err
	}
	recordClosedRelease(w.Namespace)
	logs.CtxInfo(ctx, "[agentworker] complete close thread success: reason=%q control_message_id=%d", reason, controlMessageID)
	return nil
}

func (w *Worker) renewLoop(ctx context.Context, lease *ac.Lease, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(w.RenewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			req := &ac.RenewThreadLeaseRequest{
				Namespace:  w.Namespace,
				ThreadId:   lease.GetThreadId(),
				LeaseToken: lease.GetLeaseToken(),
				LeaseMs:    w.LeaseMS,
			}
			req.GetOrSetBase()
			resp, err := w.Client.RenewThreadLease(ctx, req)
			if err := rpcError(fmt.Sprintf("RenewThreadLease thread_id=%d", lease.GetThreadId()), resp, err); err != nil {
				logs.CtxError(ctx, "[agentworker] renew lease failed: %v", err)
				continue
			}
			if resp.GetLease() != nil {
				lease.LeaseDeadlineAtMs = resp.GetLease().LeaseDeadlineAtMs
			}
		}
	}
}

// acquireSlot serializes claim processing by using a counting semaphore. The
// send is cancellable so worker shutdown does not get stuck waiting for a slot.
func acquireSlot(ctx context.Context, sem chan<- struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case sem <- struct{}{}:
		return nil
	}
}

func releaseSlot(sem <-chan struct{}) {
	<-sem
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
