//go:build !windows

package cloud

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/kite/kitutil/logid"
	"eino-cli/deepagent/coordinator"
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
	Client             CoordinatorClient
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

func (w *Worker) newScanLogContext(ctx context.Context) context.Context {
	scanLogID := logid.GetNginxID()
	ctx = contextWithLogID(ctx, scanLogID)
	return logs.CtxAddKVs(ctx,
		"log_context", "scan",
		"logid_source", "generated",
		"namespace", w.Namespace,
		"worker_env", w.Env,
		"scan_limit", w.ScanLimit,
	)
}

func (w *Worker) newClaimLogContext(ctx context.Context, thread *coordinator.Thread) context.Context {
	var threadID int64
	if thread != nil {
		threadID = thread.ThreadID
	}
	ctx = ensureContextLogID(ctx)
	scanLogID := currentContextLogID(ctx)
	return logs.CtxAddKVs(ctx,
		"log_context", "claim",
		"logid_source", "scan",
		"scan_logid", scanLogID,
		"namespace", w.Namespace,
		"thread_id", threadID,
		"thread_logid", threadProducerLogID(thread),
	)
}

func (w *Worker) newThreadLogContext(ctx context.Context, thread *coordinator.Thread) context.Context {
	var threadID int64
	if thread != nil {
		threadID = thread.ThreadID
	}
	threadLogID := strings.TrimSpace(threadProducerLogID(thread))
	logIDSource := "thread"
	if threadLogID == "" {
		threadLogID = logid.GetNginxID()
		logIDSource = "generated"
	}

	ctx = contextWithLogID(ctx, threadLogID)
	ctx = contextWithThreadLogMeta(ctx, thread)
	return logs.CtxAddKVs(ctx,
		"log_context", "thread",
		"logid_source", logIDSource,
		"namespace", w.Namespace,
		"thread_id", threadID,
	)
}

func (w *Worker) newRunLogContext(ctx context.Context, claim *claimResult) context.Context {
	var thread *coordinator.Thread
	var lease *coordinator.Lease
	var pendingMessages []*coordinator.Message
	if claim != nil {
		thread = claim.thread
		lease = claim.lease
		pendingMessages = claim.pendingMessages
	}
	var threadID int64
	if thread != nil {
		threadID = thread.ThreadID
	}
	parentLogID := currentContextLogID(ctx)
	logID := strings.TrimSpace(threadProducerLogID(thread))
	logIDSource := "thread"
	if logID == "" {
		logID = logid.GetNginxID()
		logIDSource = "generated"
	}

	ctx = contextWithLogID(ctx, logID)
	ctx = contextWithThreadLogMeta(ctx, thread)
	return logs.CtxAddKVs(ctx,
		"log_context", "run",
		"logid_source", logIDSource,
		"parent_logid", parentLogID,
		"activation_logid", logID,
		"namespace", w.Namespace,
		"thread_id", threadID,
		"thread_logid", threadProducerLogID(thread),
		"lease_deadline_at_ms", leaseDeadlineAtMS(lease),
		"pending_message_count", len(pendingMessages),
		"first_pending_message_id", firstMessageID(pendingMessages),
		"first_pending_message_logid", firstMessageLogID(pendingMessages),
	)
}

func (w *Worker) newPullLogContext(ctx context.Context, lease *coordinator.Lease) context.Context {
	ctx = ensureContextLogID(ctx)
	runLogID := currentContextLogID(ctx)
	return logs.CtxAddKVs(ctx,
		"log_context", "pull_message",
		"logid_source", "inherited",
		"run_logid", runLogID,
		"namespace", w.Namespace,
		"thread_id", lease.ThreadID,
		"pull_limit", w.MessageLimit,
	)
}

func (w *Worker) newMessageLogContext(ctx context.Context, thread *coordinator.Thread, message *coordinator.Message) context.Context {
	parentLogID := currentContextLogID(ctx)
	messageLogID := messageProducerLogID(message)
	logIDSource := "producer"
	if messageLogID == "" {
		messageLogID = logid.GetNginxID()
		logIDSource = "generated"
	}

	ctx = contextWithLogID(ctx, messageLogID)
	ctx = contextWithMessageLogMeta(ctx, message)
	senderType, senderID := messageSender(message)
	return logs.CtxAddKVs(ctx,
		"log_context", "message",
		"logid_source", logIDSource,
		"parent_logid", parentLogID,
		"namespace", w.Namespace,
		"thread_id", thread.ThreadID,
		"input_message_id", message.MessageID,
		"message_type", message.MessageType,
		"sender_type", senderType,
		"sender_id", senderID,
	)
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
			logs.CtxError(scanCtx, "[agentworker] scan runnable threads failed: elapsed=%s err=%v", time.Since(scanStartedAt), err)
			if err := sleepContext(ctx, w.ScanInterval); err != nil {
				return err
			}
			continue
		}
		logs.CtxInfo(scanCtx, "[agentworker] scan runnable threads finished: count=%d elapsed=%s threads=%v truncated=%t",
			len(threads), time.Since(scanStartedAt), threadSummaries(threads), len(threads) > logThreadSummaryLimit)

		for _, thread := range threads {
			if thread == nil {
				continue
			}
			if err := acquireSlot(ctx, sem); err != nil {
				return err
			}
			threadID := thread.ThreadID
			claimCtx := w.newClaimLogContext(scanCtx, thread)
			claimedCtx, claim, err := w.claimThreadWithLog(claimCtx, threadID)
			if err != nil {
				releaseSlot(sem)
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

func (w *Worker) scanRunnableThreads(ctx context.Context) (threads []*coordinator.Thread, err error) {
	req := coordinator.ScanRunnableThreadsRequest{
		Namespace: w.Namespace,
		Limit:     w.ScanLimit,
		Env:       w.Env,
	}
	result, err := w.Client.ScanRunnableThreads(ctx, req)
	if err = coordinatorError("ScanRunnableThreads", err); err != nil {
		return nil, err
	}
	return result.Threads, nil
}

func (w *Worker) claimThreadWithLog(ctx context.Context, threadID int64) (context.Context, *claimResult, error) {
	logs.CtxInfo(ctx, "[agentworker] claim thread start")
	claim, err := w.claimThread(ctx, threadID)
	if err != nil {
		return ctx, nil, err
	}
	ctx = logs.CtxAddKVs(ctx,
		"thread_status", strings.ToUpper(string(claim.thread.Status)),
		"thread_status_reason", claim.thread.StatusReason,
		"lease_deadline_at_ms", claim.lease.LeaseDeadlineAt.UnixMilli(),
		"pending_message_count", len(claim.pendingMessages),
		"first_pending_message_id", firstMessageID(claim.pendingMessages),
		"first_pending_message_logid", firstMessageLogID(claim.pendingMessages),
	)
	logs.CtxInfo(ctx,
		"[agentworker] claim thread success: thread_status=%s status_reason=%q lease_owner_hint=%q lease_deadline_at_ms=%d pending_message_count=%d first_pending_message=%s",
		strings.ToUpper(string(claim.thread.Status)), claim.thread.StatusReason, claim.thread.LeaseOwnerHint, claim.lease.LeaseDeadlineAt.UnixMilli(),
		len(claim.pendingMessages), messageLogSummary(firstMessage(claim.pendingMessages)),
	)
	return ctx, claim, nil
}

func (w *Worker) claimThread(ctx context.Context, threadID int64) (claim *claimResult, err error) {
	req := coordinator.ClaimThreadRequest{
		Namespace:    w.Namespace,
		ThreadID:     threadID,
		LeaseMS:      w.LeaseMS,
		MessageLimit: w.MessageLimit,
		LeaseOwner:   w.LeaseOwnerHint,
	}
	result, err := w.Client.ClaimThread(ctx, req)
	if err = coordinatorError(fmt.Sprintf("ClaimThread thread_id=%d", threadID), err); err != nil {
		return nil, err
	}
	if result.Thread == nil {
		return nil, ErrMissingThread
	}
	if result.Lease == nil || result.Lease.LeaseToken == "" {
		return nil, ErrMissingLease
	}
	return &claimResult{
		thread:          result.Thread,
		lease:           result.Lease,
		pendingMessages: result.PendingMessages,
	}, nil
}

func (w *Worker) runClaim(ctx context.Context, acceptCtx context.Context, claim *claimResult) (err error) {
	if !claim.lease.LeaseDeadlineAt.IsZero() && !claim.lease.LeaseDeadlineAt.After(time.Now()) {
		return fmt.Errorf("lease expired thread_id=%d: %w", claim.lease.ThreadID, context.DeadlineExceeded)
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	logs.CtxInfo(runCtx,
		"[agentworker] run claim start: thread_id=%d thread_status=%s status_reason=%q pending_message_count=%d lease_deadline_at_ms=%d idle_timeout=%s poll_interval=%s",
		claim.thread.ThreadID, strings.ToUpper(string(claim.thread.Status)), claim.thread.StatusReason, len(claim.pendingMessages),
		claim.lease.LeaseDeadlineAt.UnixMilli(), w.IdleTimeout, w.MessagePollInterval,
	)
	renewDone := make(chan struct{})
	var renewErr error
	go func() {
		defer close(renewDone)
		renewErr = w.renewLoop(runCtx, claim.lease)
		if renewErr != nil {
			cancel(renewErr)
		}
	}()
	defer func() {
		cancel(nil)
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
		err = fmt.Errorf("AgentThreadFactory thread_id=%d: %w", claim.thread.ThreadID, err)
		logs.CtxError(runCtx,
			"[agentworker] build thread failed: elapsed=%s pending_message_count=%d first_pending_message=%s err=%v",
			time.Since(phaseStartedAt), len(claim.pendingMessages), messageLogSummary(firstMessage(claim.pendingMessages)), err,
		)
		if runCtx.Err() != nil {
			return errors.Join(err, context.Cause(runCtx))
		}
		releaseErr := w.releaseThread(runCtx, claim.lease, buildThreadFailedReason, nil)
		return errors.Join(err, releaseErr)
	}
	logs.CtxInfo(runCtx, "[agentworker] build thread success: elapsed=%s", time.Since(phaseStartedAt))

	logs.CtxInfo(runCtx, "[agentworker] init thread start")
	phaseStartedAt = time.Now()
	output, err := agentThread.Init(runtimeCtx)
	if err != nil {
		closeErr := agentThread.Close(runtimeCtx)
		err = errors.Join(fmt.Errorf("AgentThread.Init thread_id=%d: %w", claim.thread.ThreadID, err), closeErr)
		logs.CtxError(runCtx,
			"[agentworker] init thread failed: elapsed=%s pending_message_count=%d first_pending_message=%s close_err=%v err=%v",
			time.Since(phaseStartedAt), len(claim.pendingMessages), messageLogSummary(firstMessage(claim.pendingMessages)), closeErr, err,
		)
		if runCtx.Err() != nil {
			return errors.Join(err, context.Cause(runCtx))
		}
		releaseErr := w.releaseThread(runCtx, claim.lease, initThreadFailedReason, nil)
		return errors.Join(err, releaseErr)
	}
	logs.CtxInfo(runCtx, "[agentworker] init thread success: elapsed=%s", time.Since(phaseStartedAt))
	if output == nil {
		output = &agentworker.ThreadOutput{}
	}

	logs.CtxInfo(runCtx, "[agentworker] message loop start: thread_id=%d pending_message_count=%d", claim.thread.ThreadID, len(claim.pendingMessages))
	pollInterval := w.MessagePollInterval
	if pollInterval <= 0 {
		pollInterval = defaultMessagePollInterval
	}
	coordinator := newClaimCoordinator(w, runCtx, acceptCtx.Done(), claim, agentThread, output.Items, pollInterval)
	end := coordinator.run()
	if end == nil {
		end = newReleaseAction(defaultReleaseReason, nil)
	}
	logs.CtxInfo(runCtx,
		"[agentworker] message loop finished: thread_id=%d action_kind=%d reason=%q err=%v block_present=%t control_message_id=%d",
		claim.thread.ThreadID, end.kind, end.reason, end.err, end.block != nil, end.controlMessageID,
	)
	closeErr := agentThread.Close(runtimeCtx)
	if runCtx.Err() != nil {
		<-renewDone
		if renewErr != nil {
			return errors.Join(renewErr, closeErr)
		}
	}
	switch end.kind {
	case claimActionRelease:
		logs.CtxInfo(runCtx, "[agentworker] claim release action: reason=%q block_present=%t err=%v close_err=%v",
			end.releaseReason(closeErr), end.block != nil, end.err, closeErr)
		releaseErr := w.releaseThread(runCtx, claim.lease, end.releaseReason(closeErr), end.block)
		return errors.Join(end.err, closeErr, releaseErr)
	case claimActionCompleteClose:
		logs.CtxInfo(runCtx, "[agentworker] claim complete close action: control_message_id=%d reason=%q close_err=%v",
			end.controlMessageID, end.reason, closeErr)
		completeErr := w.completeCloseThread(runCtx, claim.lease, end.controlMessageID, end.reason)
		return errors.Join(closeErr, completeErr)
	default:
		err := fmt.Errorf("unknown claim end kind: %d", end.kind)
		releaseErr := w.releaseThread(runCtx, claim.lease, defaultErrorReleaseReason, nil)
		return errors.Join(err, closeErr, releaseErr)
	}
}

func (w *Worker) enqueueMessage(ctx context.Context, agentThread agentworker.AgentThread, thread *coordinator.Thread, message *coordinator.Message) (result *agentworker.PostMessageResult, err error) {
	msg := messageFromCoordinator(message)
	result, err = agentThread.PostMessage(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("AgentThread.PostMessage thread_id=%d message_id=%d: %w", thread.ThreadID, message.MessageID, err)
	}
	return result, nil
}

func (w *Worker) appendEventBestEffort(ctx context.Context, threadID int64, event *agentworker.Event, leaseToken string) {
	req, eventType, payloadBytes, metadata, err := w.newAppendEventRequest(threadID, event)
	if err != nil {
		logs.CtxError(ctx, "[agentworker] drop invalid runtime event: thread_id=%d err=%v", threadID, err)
		return
	}
	var lastErr error
	req.LeaseToken = leaseToken
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
		threadID, req.RunID, eventType, lastErr)
}

func (w *Worker) newAppendEventRequest(threadID int64, event *agentworker.Event) (request *coordinator.PublishEventsRequest, eventType string, payloadBytes int, metadata map[string]string, err error) {
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
	coordinatorEvent := eventToCoordinator(threadID, event)
	req := &coordinator.PublishEventsRequest{
		Namespace: w.Namespace,
		ThreadID:  threadID,
		RunID:     coordinatorEvent.TurnID,
		Events:    []coordinator.Event{*coordinatorEvent},
	}
	return req, string(event.Type), len(event.Payload), event.Metadata, nil
}

func (w *Worker) appendEventRequest(ctx context.Context, req *coordinator.PublishEventsRequest, eventType string, payloadBytes int, metadata map[string]string) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	startedAt := time.Now()
	result, err := w.Client.PublishEvents(ctx, *req)
	elapsed := time.Since(startedAt)
	if err = coordinatorError(fmt.Sprintf("AppendEvents thread_id=%d turn_id=%s event_type=%s", req.ThreadID, req.RunID, eventType), err); err != nil {
		logs.CtxWarn(ctx, "[agentworker] append event request failed: thread_id=%d turn_id=%s event_type=%s payload_bytes=%d elapsed=%s err=%v",
			req.ThreadID, req.RunID, eventType, payloadBytes, elapsed, err)
		return err
	}
	eventID := int64(0)
	if len(result.Events) > 0 {
		eventID = result.Events[0].EventID
	}
	logs.CtxInfo(ctx, "[agentworker] append event success: event_id=%d turn_id=%s event_type=%s payload_bytes=%d elapsed=%s metadata=%v",
		eventID, req.RunID, eventType, payloadBytes, elapsed, metadata)
	return nil
}

func (w *Worker) pullPendingMessages(ctx context.Context, lease *coordinator.Lease) (messages []*coordinator.Message, err error) {
	req := coordinator.ReadPendingInputsRequest{
		Namespace:  w.Namespace,
		ThreadID:   lease.ThreadID,
		LeaseToken: lease.LeaseToken,
		Limit:      w.MessageLimit,
	}
	result, err := w.Client.ReadPendingInputs(ctx, req)
	if err = coordinatorError(fmt.Sprintf("PullPendingMessages thread_id=%d", lease.ThreadID), err); err != nil {
		return nil, err
	}
	return result.Messages, nil
}

func (w *Worker) ackMessage(ctx context.Context, lease *coordinator.Lease, messageID int64, triggerTurnID string) (err error) {
	req := coordinator.ConfirmInputDeliveryRequest{
		Namespace:    w.Namespace,
		ThreadID:     lease.ThreadID,
		LeaseToken:   lease.LeaseToken,
		MessageIDs:   []int64{messageID},
		TriggerRunID: triggerTurnID,
	}
	logs.CtxInfo(ctx, "[agentworker] call ConfirmInputDelivery: thread_id=%d message_id=%d trigger_turn_id=%q", lease.ThreadID, messageID, triggerTurnID)
	_, err = w.Client.ConfirmInputDelivery(ctx, req)
	if err = coordinatorError(fmt.Sprintf("AckThreadMessages thread_id=%d message_id=%d", lease.ThreadID, messageID), err); err != nil {
		logs.CtxError(ctx, "[agentworker] ConfirmInputDelivery failed: thread_id=%d message_id=%d trigger_turn_id=%q err=%v", lease.ThreadID, messageID, triggerTurnID, err)
		return err
	}
	logs.CtxInfo(ctx, "[agentworker] message acked")
	return nil
}

func (w *Worker) releaseThread(ctx context.Context, lease *coordinator.Lease, reason string, block *agentworker.PendingBlock) (err error) {
	if reason == "" {
		reason = defaultReleaseReason
	}
	releaseToStatus := releaseStatusFromBlock(block)
	req := coordinator.ReleaseThreadRequest{
		Namespace:  w.Namespace,
		ThreadID:   lease.ThreadID,
		LeaseToken: lease.LeaseToken,
		Reason:     reason,
		Status:     releaseToStatus,
	}
	logs.CtxInfo(ctx, "[agentworker] call ReleaseThread: thread_id=%d reason=%q release_to_status=%v block_present=%t",
		lease.ThreadID, reason, releaseToStatus, block != nil)
	_, err = w.Client.ReleaseThread(ctx, req)
	if err = coordinatorError(fmt.Sprintf("ReleaseThread thread_id=%d", lease.ThreadID), err); err != nil {
		logs.CtxError(ctx, "[agentworker] ReleaseThread failed: thread_id=%d reason=%q release_to_status=%v err=%v",
			lease.ThreadID, reason, releaseToStatus, err)
		return err
	}
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

func (w *Worker) completeCloseThread(ctx context.Context, lease *coordinator.Lease, controlMessageID int64, reason string) (err error) {
	req := coordinator.ConfirmThreadClosedRequest{
		Namespace:        w.Namespace,
		ThreadID:         lease.ThreadID,
		LeaseToken:       lease.LeaseToken,
		ControlMessageID: controlMessageID,
		Reason:           reason,
	}
	_, err = w.Client.ConfirmThreadClosed(ctx, req)
	if err = coordinatorError(fmt.Sprintf("CompleteCloseThread thread_id=%d control_message_id=%d", lease.ThreadID, controlMessageID), err); err != nil {
		return err
	}
	logs.CtxInfo(ctx, "[agentworker] complete close thread success: reason=%q control_message_id=%d", reason, controlMessageID)
	return nil
}

func (w *Worker) renewLoop(ctx context.Context, lease *coordinator.Lease) (err error) {
	ticker := time.NewTicker(w.RenewInterval)
	defer ticker.Stop()
	deadline := lease.LeaseDeadlineAt
	if deadline.IsZero() {
		deadline = time.Now().Add(time.Duration(defaultLeaseMS) * time.Millisecond)
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	type renewal struct {
		lease *coordinator.Lease
		err   error
	}
	var pending <-chan renewal
	var cancelRenew context.CancelFunc
	defer func() {
		if cancelRenew != nil {
			cancelRenew()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return fmt.Errorf("lease expired thread_id=%d: %w", lease.ThreadID, context.DeadlineExceeded)
		case <-ticker.C:
			if pending != nil {
				continue
			}
			req := coordinator.RenewThreadLeaseRequest{
				Namespace:  w.Namespace,
				ThreadID:   lease.ThreadID,
				LeaseToken: lease.LeaseToken,
				LeaseMS:    w.LeaseMS,
				LeaseOwner: w.LeaseOwnerHint,
			}
			renewCtx, cancel := context.WithDeadline(ctx, deadline)
			cancelRenew = cancel
			response := make(chan renewal, 1)
			pending = response
			go func() {
				renewed, renewErr := w.Client.RenewThreadLease(renewCtx, req)
				response <- renewal{lease: renewed, err: renewErr}
			}()
		case response := <-pending:
			cancelRenew()
			pending = nil
			if !time.Now().Before(deadline) {
				return fmt.Errorf("lease expired thread_id=%d: %w", lease.ThreadID, context.DeadlineExceeded)
			}
			if err := coordinatorError(fmt.Sprintf("RenewThreadLease thread_id=%d", lease.ThreadID), response.err); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				logs.CtxError(ctx, "[agentworker] renew lease failed: %v", err)
				return err
			}
			if response.lease == nil || response.lease.ThreadID != lease.ThreadID || response.lease.LeaseToken != lease.LeaseToken || !response.lease.LeaseDeadlineAt.After(time.Now()) {
				return ErrMissingLease
			}
			deadline = response.lease.LeaseDeadlineAt
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(time.Until(deadline))
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
