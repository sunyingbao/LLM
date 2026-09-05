package coordinator

import (
	"context"
	"eino-cli/deepagent/coordinator/internal/infra/idgen"
	mysqlstore "eino-cli/deepagent/coordinator/internal/infra/store/mysql"
	redisstore "eino-cli/deepagent/coordinator/internal/infra/store/redis"
	"eino-cli/deepagent/coordinator/internal/model"
	"eino-cli/deepagent/coordinator/internal/service/eventlog"
	"eino-cli/deepagent/coordinator/internal/service/streamout"
	"eino-cli/deepagent/coordinator/internal/storage"
	"eino-cli/deepagent/coordinator/internal/util"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/lang/gg/choose"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Coordinator struct {
	pendingStore            *storage.InputQueue
	writeDB                 *gorm.DB
	readDB                  *gorm.DB
	idgen                   idgen.Generator
	now                     func() time.Time
	newLeaseToken           func() string
	defaultScanLimit        int32
	maxScanLimit            int32
	failureBackoff          time.Duration
	failureReasons          map[string]struct{}
	events                  *eventlog.EventLog
	stream                  *streamout.StreamOut
	subscribeSessionMaxIdle time.Duration
}

func New(ctx context.Context, config Config) (coordinator *Coordinator, err error) {
	mysqlClient := mysqlstore.New(mysqlstore.Config{
		DSN:     config.MySQLDSN,
		ReadDSN: config.MySQLReadDSN,
	})
	if err = mysqlClient.Ping(ctx); err != nil {
		return nil, err
	}

	redisClient, err := redisstore.New(redisstore.Config{
		Addr:     config.RedisAddress,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err != nil {
		return nil, err
	}

	generator := idgen.NewLocalGenerator()
	writeDB := mysqlClient.ForWrite()
	readDB := mysqlClient.ForReadOnly()
	coordinator = newCoordinator(writeDB, readDB, redisClient, generator)
	events := eventlog.NewEventLog(writeDB, readDB, generator)
	stream := streamout.New(redisClient, generator)

	coordinator.events = events
	coordinator.stream = stream
	coordinator.subscribeSessionMaxIdle = config.SubscribeSessionMaxIdle

	return coordinator, nil
}

func newCoordinator(writeDB *gorm.DB, readDB *gorm.DB, redisClient redisstore.Client, generator idgen.Generator) (coordinator *Coordinator) {
	if readDB == nil {
		readDB = writeDB
	}
	return &Coordinator{
		pendingStore: storage.NewInputQueue(redisClient),
		writeDB:      writeDB, readDB: readDB, idgen: generator,
		now: time.Now, newLeaseToken: uuid.NewString,
		defaultScanLimit: defaultScanLimit, maxScanLimit: maxScanLimit,
		failureBackoff: defaultFailureReleaseBackoff, failureReasons: defaultFailureReleaseReasons,
	}
}

func (c *Coordinator) CreateThread(ctx context.Context, request CreateThreadRequest) (result CreateThreadResult, err error) {
	thread, message, err := c.createThreadRow(
		ctx,
		request.Namespace,
		request.Env,
		request.UserID,
		request.SessionID,
		request.Title,
		request.Metadata,
		profileToModel(request.Profile),
		request.InitialMessage,
	)
	if err != nil {
		return CreateThreadResult{}, err
	}
	if message != nil {
		if err = c.enqueueInput(ctx, request.Namespace, message); err != nil {
			return CreateThreadResult{}, err
		}
		thread, err = c.wakeIdleThread(ctx, request.Namespace, message.ThreadId, thread.StatusReason)
		if err != nil {
			c.removeInput(ctx, request.Namespace, message.ThreadId, message.MessageId)
			return CreateThreadResult{}, err
		}
		c.archiveInput(ctx, message, "CreateThread.initial_message")
	}
	result.Thread, err = threadFromModel(thread)
	if err != nil {
		return CreateThreadResult{}, err
	}
	result.InitialMessage, err = messageFromModel(message)
	return result, err
}

func (c *Coordinator) GetThread(ctx context.Context, namespace string, threadID int64) (thread *Thread, err error) {
	row, err := c.readThread(ctx, namespace, threadID)
	if err != nil {
		return nil, err
	}
	return threadFromModel(row)
}

func (c *Coordinator) ListSessionThreads(ctx context.Context, request ListSessionThreadsRequest) (result ListThreadsResult, err error) {
	rows, nextCursor, hasMore, err := c.listSessionThreadRows(
		ctx, request.Namespace, request.SessionID, request.Cursor, request.Limit,
	)
	if err != nil {
		return ListThreadsResult{}, err
	}
	result.Threads, err = threadsFromModel(rows)
	result.NextCursor = nextCursor
	result.HasMore = hasMore
	return result, err
}

func (c *Coordinator) ScanRunnableThreads(ctx context.Context, request ScanRunnableThreadsRequest) (result ScanRunnableThreadsResult, err error) {
	rows, nextCursor, hasMore, err := c.scanRunnableThreadRows(
		ctx, request.Namespace, request.Env, request.Cursor, request.Limit,
	)
	if err != nil {
		return ScanRunnableThreadsResult{}, err
	}
	result.Threads, err = threadsFromModel(rows)
	result.NextCursor = nextCursor
	result.HasMore = hasMore
	return result, err
}

func (c *Coordinator) ClaimThread(ctx context.Context, request ClaimThreadRequest) (result ClaimThreadResult, err error) {
	threadRow, lease, serverTimeMS, err := c.claimThreadLease(
		ctx, request.Namespace, request.ThreadID, request.LeaseMS, request.LeaseOwner,
	)
	if err != nil {
		return result, err
	}
	pendingRows, err := c.loadPendingInputs(ctx, request.Namespace, request.ThreadID, request.MessageLimit)
	if err != nil && lease != nil && lease.LeaseToken != "" {
		_, _ = c.releaseThreadToStatus(ctx, request.Namespace, request.ThreadID, lease.LeaseToken, "mailbox redis load failed", model.ThreadStatusReady, true)
	}
	if err != nil {
		return ClaimThreadResult{}, err
	}
	result.Thread, err = threadFromModel(threadRow)
	if err != nil {
		return ClaimThreadResult{}, err
	}
	result.Lease = lease
	result.PendingMessages, err = messagesFromModel(pendingRows)
	result.ServerTimeMS = serverTimeMS
	return result, err
}

func (c *Coordinator) RenewThreadLease(ctx context.Context, request RenewThreadLeaseRequest) (lease *Lease, err error) {
	now := c.now()
	lease = &Lease{
		ThreadID:        request.ThreadID,
		LeaseToken:      request.LeaseToken,
		LeaseDeadlineAt: now.Add(normalizeLeaseDuration(request.LeaseMS)),
	}
	changed, err := storage.RenewActiveThreadLease(ctx, c.writeDB, request.Namespace, request.ThreadID, request.LeaseToken, lease.LeaseDeadlineAt, request.LeaseOwner, now)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, ErrLeaseMismatch
	}
	return lease, nil
}

func (c *Coordinator) ReleaseThread(ctx context.Context, request ReleaseThreadRequest) (thread *Thread, err error) {
	var hasPending bool
	if request.Status == "" {
		hasPending, err = c.hasPendingInputs(ctx, request.Namespace, request.ThreadID)
		if err != nil {
			return nil, err
		}
	}
	nextStatus := model.ThreadStatusIdle
	if request.Status != "" {
		if request.Status != ThreadStatusBlocked {
			return nil, ErrInvalidStatusTransition
		}
		nextStatus = model.ThreadStatusBlocked
	} else if hasPending {
		nextStatus = model.ThreadStatusReady
	}
	row, err := c.releaseThreadToStatus(ctx, request.Namespace, request.ThreadID, request.LeaseToken, request.Reason, nextStatus, nextStatus == model.ThreadStatusReady)
	if err != nil {
		return nil, err
	}
	if request.Status != "" || row.Status != model.ThreadStatusIdle {
		return threadFromModel(row)
	}
	hasPending, err = c.hasPendingInputs(ctx, request.Namespace, request.ThreadID)
	if err != nil || !hasPending {
		if err != nil {
			return nil, err
		}
		return threadFromModel(row)
	}
	row, err = c.wakeIdleThread(ctx, request.Namespace, request.ThreadID, request.Reason)
	if err != nil {
		return nil, err
	}
	return threadFromModel(row)
}

func (c *Coordinator) SubmitInput(ctx context.Context, request SubmitInputRequest) (result SubmitInputResult, err error) {
	now := c.now()
	// 状态判断决定是否唤醒/拒收，必须读主库：从库延迟下读到旧的 running/closing
	// 会导致该唤醒的不唤醒（消息滞留无人消费）或该拒收的不拒收。
	thread, err := c.readThreadFromPrimary(ctx, request.Namespace, request.ThreadID)
	if err != nil {
		return SubmitInputResult{}, err
	}
	if thread.Status == model.ThreadStatusClosing || thread.Status == model.ThreadStatusClosed {
		return SubmitInputResult{}, ErrThreadClosed
	}

	message, err := c.newInput(ctx, request.ThreadID, normalizeSenderType(request.SenderType), request.SenderID, request.MessageType, request.Payload, request.Metadata)
	if err != nil {
		return SubmitInputResult{}, err
	}
	if err := c.enqueueInput(ctx, request.Namespace, message); err != nil {
		return SubmitInputResult{}, err
	}

	wokeThread := false
	if request.WakeThread {
		threadMetadataJSON := threadMetadataWithActivation(thread.MetadataJson, request.Metadata)
		changed, err := storage.WakeIdleThreadForInput(ctx, c.writeDB, request.Namespace, request.ThreadID, threadMetadataJSON, now)
		if err != nil {
			c.removeInput(ctx, request.Namespace, request.ThreadID, message.MessageId)
			return SubmitInputResult{}, err
		}
		if !changed {
			// idle→ready 的 CAS 没命中说明状态被并发改掉了，重读主库按最新状态
			// 收敛，不能一律删消息报 not_found：并发唤醒（另一条消息刚把线程置
			// ready）时删消息等于把本条用户输入静默丢掉。
			latest, wakeErr := c.handleWakeConflict(ctx, request.Namespace, request.ThreadID, message.MessageId)
			if wakeErr != nil {
				return SubmitInputResult{}, wakeErr
			}
			thread = latest
		} else {
			thread.Status = model.ThreadStatusReady
			thread.ReadyAt = now
			thread.MetadataJson = threadMetadataJSON
			thread.LastActiveAt = now
			thread.UpdatedAt = now
			wokeThread = true
		}
	}
	if !wokeThread {
		if request.WakeThread && thread.Status == model.ThreadStatusReady {
			c.bestEffortPullReadyAtForward(ctx, request.Namespace, request.ThreadID, now)
		}
		c.bestEffortTouchThreadActive(ctx, request.Namespace, request.ThreadID, now)
	}

	c.archiveInput(ctx, message, "submitInput")
	c.observeInputQueueStats(ctx, request.Namespace, request.ThreadID, thread.Status, "send_message")
	result.Message, err = messageFromModel(message)
	if err != nil {
		return SubmitInputResult{}, err
	}
	result.Thread, err = threadFromModel(thread)
	return result, err
}

func (c *Coordinator) ReadPendingInputs(ctx context.Context, request ReadPendingInputsRequest) (result ReadPendingInputsResult, err error) {
	if err := c.ensureActiveLease(ctx, request.Namespace, request.ThreadID, request.LeaseToken); err != nil {
		return ReadPendingInputsResult{}, err
	}
	messages, err := c.loadPendingInputs(ctx, request.Namespace, request.ThreadID, request.Limit)
	if err != nil {
		return ReadPendingInputsResult{}, err
	}
	result.ServerTimeMS = c.now().UnixMilli()
	result.Messages, err = messagesFromModel(messages)
	return result, err
}

func (c *Coordinator) ConfirmInputDelivery(ctx context.Context, request ConfirmInputDeliveryRequest) (delivered []*Message, err error) {
	if err := c.ensureActiveLease(ctx, request.Namespace, request.ThreadID, request.LeaseToken); err != nil {
		return nil, err
	}
	now := c.now()
	messages, err := c.finalizeMessagesInRedis(ctx, request.Namespace, request.ThreadID, request.MessageIDs, model.MessageStatusAcked, request.TriggerRunID, now)
	if err != nil {
		return nil, err
	}
	if _, err := storage.UpdateInputStatus(ctx, c.writeDB, request.ThreadID, model.MessageStatusPending, model.MessageStatusAcked, now, &request.TriggerRunID, request.MessageIDs); err != nil {
		logs.CtxError(ctx, "mailbox mysql best-effort ack failed, thread_id=%d message_ids=%v err=%v", request.ThreadID, request.MessageIDs, err)
	}
	return messagesFromModel(messages)
}

func (c *Coordinator) ResumeFromBlock(ctx context.Context, request ResumeFromBlockRequest) (result ResumeFromBlockResult, err error) {
	current, err := c.readThreadFromPrimary(ctx, request.Namespace, request.ThreadID)
	if err != nil {
		return ResumeFromBlockResult{}, err
	}
	if current.Status != model.ThreadStatusBlocked {
		return ResumeFromBlockResult{}, ErrThreadNotBlocked
	}
	threadMetadata, err := resumeThreadMetadata(current, request.ActivationMetadata)
	if err != nil {
		return ResumeFromBlockResult{}, err
	}

	hasPending := false
	var resumeMessage *model.TMailboxMessage
	if input := request.ResumeMessage; input != nil {
		resumeMessage, err = c.newInput(ctx, request.ThreadID, normalizeSenderType(input.SenderType), input.SenderID, input.MessageType, input.Payload, input.Metadata)
		if err != nil {
			return ResumeFromBlockResult{}, err
		}
		if err = c.enqueueInputFirst(ctx, request.Namespace, resumeMessage); err != nil {
			return ResumeFromBlockResult{}, err
		}
		hasPending = true
	} else {
		hasPending, err = c.hasPendingInputs(ctx, request.Namespace, request.ThreadID)
		if err != nil {
			return ResumeFromBlockResult{}, err
		}
	}

	thread, err := c.resumeBlockedThread(ctx, request.Namespace, request.ThreadID, request.Reason, hasPending, threadMetadata)
	if err != nil {
		if resumeMessage != nil {
			c.removeInput(ctx, request.Namespace, request.ThreadID, resumeMessage.MessageId)
		}
		return ResumeFromBlockResult{}, err
	}
	if resumeMessage != nil {
		c.archiveInput(ctx, resumeMessage, "ResumeFromBlock.resume_message")
	}
	result.Thread, err = threadFromModel(thread)
	if err != nil {
		return ResumeFromBlockResult{}, err
	}
	result.Message, err = messageFromModel(resumeMessage)
	return result, err
}

func (c *Coordinator) RequestInputCancel(ctx context.Context, request RequestInputCancelRequest) (result *RequestInputCancelResult, err error) {
	thread, err := c.readThreadFromPrimary(ctx, request.Namespace, request.ThreadID)
	if err != nil {
		return nil, err
	}
	switch thread.Status {
	case model.ThreadStatusClosing, model.ThreadStatusClosed:
		return nil, ErrThreadClosed
	case model.ThreadStatusBlocked:
		return nil, ErrThreadBlocked
	}
	if request.Reason == "" {
		request.Reason = DefaultCancelInputReason
	}

	cutoff, err := c.cancelCutoff(ctx, request.Namespace, request.ThreadID, request.CutoffMessageID)
	if err != nil {
		return nil, err
	}
	var controlMessage *model.TMailboxMessage
	var cancelled []int64
	if cutoff == 0 {
		return cancelResult(thread, controlMessage, cutoff, cancelled)
	}
	shouldWakeIdle := thread.Status == model.ThreadStatusIdle

	cancelled, err = c.cancelPendingOrdinaryMessages(ctx, request.Namespace, request.ThreadID, cutoff)
	if err != nil {
		return nil, err
	}

	controlMessage, err = c.newCancelInputControlMessage(ctx, request.ThreadID, cutoff, request.Reason, request.Metadata)
	if err != nil {
		return nil, err
	}
	if err := c.enqueueInputFirst(ctx, request.Namespace, controlMessage); err != nil {
		return nil, err
	}
	c.archiveInput(ctx, controlMessage, "requestInputCancel.control_message")
	if shouldWakeIdle {
		if wokenThread, wakeErr := c.markIdleThreadReadyWithActivation(ctx, request.Namespace, thread, "cancel input", request.Metadata); wakeErr == nil {
			thread = wokenThread
		} else {
			logs.CtxWarn(ctx, "cancel input idle wake failed after control message accepted, thread_id=%d err=%v", request.ThreadID, wakeErr)
			if latest, getErr := c.readThreadFromPrimary(ctx, request.Namespace, request.ThreadID); getErr == nil {
				thread = latest
			}
		}
	}
	if thread != nil && thread.Status == model.ThreadStatusIdle {
		wokenThread, wakeErr := c.wakeIdleThread(ctx, request.Namespace, request.ThreadID, "cancel input")
		if wakeErr != nil {
			current, getErr := c.readThread(ctx, request.Namespace, request.ThreadID)
			if getErr == nil && (current.Status == model.ThreadStatusReady || current.Status == model.ThreadStatusRunning) {
				thread = current
			} else {
				return nil, wakeErr
			}
		} else {
			thread = wokenThread
		}
	}

	return cancelResult(thread, controlMessage, cutoff, cancelled)
}

func (c *Coordinator) RequestThreadClose(ctx context.Context, request RequestThreadCloseRequest) (result *RequestThreadCloseResult, err error) {
	thread, err := c.readThread(ctx, request.Namespace, request.ThreadID)
	if err != nil {
		return nil, err
	}
	if request.Reason == "" {
		request.Reason = DefaultCloseThreadReason
	}
	var controlMessage *model.TMailboxMessage
	var cancelled []int64
	if thread.Status == model.ThreadStatusClosed {
		return closeResult(thread, controlMessage, cancelled)
	}

	if thread.Status != model.ThreadStatusClosing {
		thread, err = c.transitionThreadToClosing(ctx, request.Namespace, request.ThreadID, request.Reason, request.Metadata)
		if err != nil {
			return nil, err
		}
		if thread.Status == model.ThreadStatusClosed {
			return closeResult(thread, controlMessage, cancelled)
		}
	}

	cancelled, err = c.cancelPendingOrdinaryMessages(ctx, request.Namespace, request.ThreadID, int64(1<<63-1))
	if err != nil {
		return nil, err
	}

	controlMessage, err = c.findExistingCloseControlMessage(ctx, request.Namespace, request.ThreadID)
	if err != nil {
		return nil, err
	}
	if controlMessage == nil {
		controlMessage, err = c.newCloseThreadControlMessage(ctx, request.ThreadID, request.Reason, request.Metadata)
		if err != nil {
			return nil, err
		}
		if err := c.enqueueInputFirst(ctx, request.Namespace, controlMessage); err != nil {
			return nil, err
		}
		c.archiveInput(ctx, controlMessage, "requestThreadClose.control_message")
	}
	return closeResult(thread, controlMessage, cancelled)
}

func (c *Coordinator) ConfirmThreadClosed(ctx context.Context, request ConfirmThreadClosedRequest) (result *ConfirmThreadClosedResult, err error) {
	if request.ControlMessageID <= 0 {
		return nil, fmt.Errorf("%w: control_message_id must be positive", ErrInvalidClose)
	}
	thread, err := c.readThreadFromPrimary(ctx, request.Namespace, request.ThreadID)
	if err != nil {
		return nil, err
	}
	controlMessage, err := c.lookupCloseControlMessage(ctx, request.Namespace, request.ThreadID, request.ControlMessageID)
	if err != nil {
		return nil, err
	}

	if thread.Status == model.ThreadStatusClosed {
		// 幂等分支：可能是上一次调用置 closed 成功但 ack 控制消息失败的重试，
		// 顺手把残留的 pending 控制消息清掉。
		c.bestEffortFinalizeControlMessage(ctx, request.Namespace, request.ThreadID, request.ControlMessageID, c.now())
		return closedResult(thread, controlMessage)
	}
	if thread.Status != model.ThreadStatusClosing {
		return nil, ErrInvalidClose
	}
	now := c.now()
	if thread.LeaseToken != request.LeaseToken || thread.LeaseDeadlineAt.IsZero() || thread.LeaseDeadlineAt.Before(now) {
		return nil, ErrLeaseMismatch
	}
	if request.Reason == "" {
		request.Reason = DefaultCloseThreadReason
	}

	// 先把 thread 置为 closed 终态，成功后再 ack 控制消息。反过来的话，ack 成功
	// 但下面的条件更新因 lease 恰好过期而未命中时，thread 停在 closing 且邮箱里
	// 已没有 close 控制消息，需要再发一次 requestThreadClose 才能恢复；而现在最坏情况
	// 只是 closed 线程残留一条 pending 控制消息，由幂等分支兜底清理。
	changed, err := storage.CompleteClosingThread(ctx, c.writeDB, request.Namespace, request.ThreadID, request.LeaseToken, request.Reason, now)
	if err != nil {
		return nil, err
	}
	if !changed {
		latest, err := c.readThreadFromPrimary(ctx, request.Namespace, request.ThreadID)
		if err != nil {
			return nil, err
		}
		if latest.Status == model.ThreadStatusClosed {
			c.bestEffortFinalizeControlMessage(ctx, request.Namespace, request.ThreadID, request.ControlMessageID, now)
			thread = latest
			return closedResult(thread, controlMessage)
		}
		return nil, ErrLeaseMismatch
	}
	// closed 已落库，控制消息 ack 失败只记日志：残留的 pending 控制消息由幂等
	// 分支或下一次重试清理，不能让已完成的 close 对调用方表现为失败。
	c.bestEffortFinalizeControlMessage(ctx, request.Namespace, request.ThreadID, request.ControlMessageID, now)
	thread, err = c.readThreadFromPrimary(ctx, request.Namespace, request.ThreadID)
	if err != nil {
		return nil, err
	}
	return closedResult(thread, controlMessage)
}

func (c *Coordinator) PublishEvents(ctx context.Context, request PublishEventsRequest) (result PublishEventsResult, err error) {
	appended, nextCursor, err := eventlog.Publish(ctx, c.events, c.stream, eventlog.AppendEventsRequest{
		LeaseToken: request.LeaseToken,
		Namespace:  request.Namespace,
		ThreadID:   request.ThreadID,
		TurnID:     request.RunID,
		Events:     cloneEvents(request.Events),
	})
	if err != nil {
		return PublishEventsResult{}, err
	}
	return PublishEventsResult{Events: cloneEvents(appended), NextCursor: nextCursor}, nil
}

func (c *Coordinator) ListEvents(ctx context.Context, request ListEventsRequest) (result ListEventsResult, err error) {
	events, nextCursor, nextPageCursor, hasMore, err := c.events.ListEvents(ctx, request.request())
	if err != nil {
		return ListEventsResult{}, err
	}
	return ListEventsResult{
		Events:         cloneEvents(events),
		NextCursor:     nextCursor,
		NextPageCursor: nextPageCursor,
		HasMore:        hasMore,
	}, nil
}

func (c *Coordinator) ListSessionEvents(ctx context.Context, request ListSessionEventsRequest) (result ListSessionEventsResult, err error) {
	events, nextCursor, hasMore, err := c.events.ListSessionEvents(ctx, eventlog.ListSessionEventsRequest{
		Namespace: request.Namespace,
		SessionID: request.SessionID,
		Cursor:    request.Cursor,
		Limit:     request.Limit,
		TurnID:    request.RunID,
		EventType: request.EventType,
		Direction: eventlog.ListDirection(request.Direction),
	})
	if err != nil {
		return ListSessionEventsResult{}, err
	}
	return ListSessionEventsResult{Events: cloneEvents(events), NextCursor: nextCursor, HasMore: hasMore}, nil
}

func (c *Coordinator) SubscribeSession(ctx context.Context, request SubscribeSessionRequest) (subscription *Subscription, err error) {
	if c == nil || c.stream == nil {
		return nil, errors.New("coordinator stream is unavailable")
	}
	return newSubscription(ctx, c.stream, request, c.subscribeSessionMaxIdle), nil
}

func (c *Coordinator) createThreadRow(ctx context.Context, namespace string, env string, userID int64, sessionID string, title string, metadata map[string]string, profile *model.ThreadProfile, initialMessage *InitialMessage) (resultThread *model.TThread, resultMessage *model.TMailboxMessage, resultErr error) {
	if err := c.ensureNamespace(ctx, namespace); err != nil {
		return nil, nil, err
	}

	threadID, err := c.idgen.NextID(ctx)
	if err != nil {
		return nil, nil, err
	}
	now := c.now()

	metadataJSON := choose.If(len(metadata) == 0, "{}", util.ToString(metadata))
	profileJSON := "{}"
	if profile != nil && (profile.Role != "" || profile.Cwd != "") {
		profileJSON = util.ToString(profile)
	}
	if err := c.rejectSessionEnvMismatch(ctx, c.writeDB, namespace, env, sessionID); err != nil {
		return nil, nil, err
	}
	thread := &model.TThread{
		ThreadId:     threadID,
		Namespace:    namespace,
		Env:          env,
		UserId:       userID,
		SessionId:    sessionID,
		Title:        title,
		Status:       model.ThreadStatusIdle,
		StatusReason: "",
		CreatedBy:    defaultCreatedByValue,
		MetadataJson: metadataJSON,
		Profile:      profileJSON,
		LastActiveAt: now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := storage.InsertThread(ctx, c.writeDB, thread); err != nil {
		return nil, nil, err
	}
	if initialMessage == nil {
		return thread, nil, nil
	}

	messageID, err := c.idgen.NextID(ctx)
	if err != nil {
		return nil, nil, err
	}
	initialMetadataJSON := choose.If(len(initialMessage.Metadata) == 0, "{}", util.ToString(initialMessage.Metadata))
	message := &model.TMailboxMessage{
		MessageId:    messageID,
		ThreadId:     threadID,
		SenderType:   normalizeSenderType(initialMessage.SenderType),
		SenderId:     initialMessage.SenderID,
		MessageType:  initialMessage.MessageType,
		Status:       model.MessageStatusPending,
		Payload:      string(initialMessage.Payload),
		MetadataJson: initialMetadataJSON,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return thread, message, nil
}

func (c *Coordinator) readThread(ctx context.Context, namespace string, threadID int64) (thread *model.TThread, err error) {
	return c.getThreadFromDB(ctx, c.readDB, namespace, threadID)
}

// readThreadFromPrimary 从主库读 thread。所有"写主库后回读"的路径必须用它：
// 回读从库在主从延迟窗口内会返回旧状态/旧 lease，worker 拿到的 thread 与刚
// 生效的 lease 不一致。
func (c *Coordinator) readThreadFromPrimary(ctx context.Context, namespace string, threadID int64) (thread *model.TThread, err error) {
	return c.getThreadFromDB(ctx, c.writeDB, namespace, threadID)
}

func (c *Coordinator) listSessionThreadRows(ctx context.Context, namespace string, sessionID string, cursor int64, limit int32) (resultThreads []*model.TThread, count int64, found bool, resultErr error) {
	if err := c.ensureNamespace(ctx, namespace); err != nil {
		return nil, 0, false, err
	}
	if limit <= 0 {
		limit = c.defaultScanLimit
	}
	if limit > c.maxScanLimit {
		limit = c.maxScanLimit
	}
	threads, err := storage.ListSessionThreads(ctx, c.readDB, namespace, sessionID, cursor, limit+1)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, 0, false, err
	}
	if len(threads) == 0 {
		return nil, cursor, false, nil
	}
	hasMore := int32(len(threads)) > limit
	if hasMore {
		threads = threads[:limit]
	}
	nextCursor := threads[len(threads)-1].ThreadId
	return threads, nextCursor, hasMore, nil
}

func (c *Coordinator) scanRunnableThreadRows(ctx context.Context, namespace string, env string, cursor string, limit int32) (resultThreads []*model.TThread, value string, found bool, resultErr error) {
	decoded, err := model.DecodeReadyCursor(cursor)
	if err != nil {
		return nil, "", false, err
	}
	if limit <= 0 {
		limit = c.defaultScanLimit
	}
	if limit > c.maxScanLimit {
		limit = c.maxScanLimit
	}
	cursorTime := model.TimeFromUnixMicrosForDB(decoded.ReadyAtUnixMicros)
	now := c.now()
	readyLike, err := storage.ScanReadyLikeThreads(ctx, c.readDB, namespace, env, cursorTime, decoded.ThreadID, limit+1, now)
	if err != nil {
		return nil, "", false, err
	}
	expiredRunning, err := storage.ScanExpiredRunningThreads(ctx, c.readDB, namespace, env, cursorTime, decoded.ThreadID, limit+1, now)
	if err != nil {
		return nil, "", false, err
	}
	threads := mergeRunnableThreads(readyLike, expiredRunning)
	if len(threads) == 0 {
		return nil, "", false, nil
	}
	hasMore := int32(len(threads)) > limit
	if hasMore {
		threads = threads[:limit]
	}
	nextCursor := ""
	if hasMore {
		last := threads[len(threads)-1]
		nextCursor, err = model.EncodeReadyCursor(model.ReadyCursor{
			ReadyAtUnixMicros: runnableAt(last).UnixMicro(),
			ThreadID:          last.ThreadId,
		})
		if err != nil {
			return nil, "", false, err
		}
	}
	return threads, nextCursor, hasMore, nil
}

func (c *Coordinator) claimThreadLease(ctx context.Context, namespace string, threadID int64, leaseMS int64, leaseOwnerHint string) (resultThread *model.TThread, resultLease *Lease, count int64, resultErr error) {
	leaseDuration := normalizeLeaseDuration(leaseMS)
	now := c.now()
	lease := &Lease{
		ThreadID:        threadID,
		LeaseToken:      c.newLeaseToken(),
		LeaseDeadlineAt: now.Add(leaseDuration),
	}
	claimed, err := storage.ClaimReadyThread(ctx, c.writeDB, namespace, threadID, lease.LeaseToken, lease.LeaseDeadlineAt, leaseOwnerHint, now)
	if err != nil {
		return nil, nil, 0, err
	}
	if !claimed {
		claimed, err = storage.ClaimExpiredRunningThread(ctx, c.writeDB, namespace, threadID, claimReasonExpiredRunningLease, lease.LeaseToken, lease.LeaseDeadlineAt, leaseOwnerHint, now)
		if err != nil {
			return nil, nil, 0, err
		}
	}
	if !claimed {
		claimed, err = storage.ClaimClosingThread(ctx, c.writeDB, namespace, threadID, lease.LeaseToken, lease.LeaseDeadlineAt, leaseOwnerHint, now)
		if err != nil {
			return nil, nil, 0, err
		}
	}
	if !claimed {
		if _, findErr := c.readThreadFromPrimary(ctx, namespace, threadID); findErr != nil {
			return nil, nil, 0, findErr
		}
		return nil, nil, 0, ErrThreadNotRunnable
	}

	thread, err := c.readThreadFromPrimary(ctx, namespace, threadID)
	if err != nil {
		return nil, nil, 0, err
	}
	if thread.StatusReason == claimReasonExpiredRunningLease {
		logs.CtxInfo(ctx, "[Coordinator] claimed expired running thread: namespace=%s thread_id=%d lease_owner_hint=%s lease_deadline_at=%s",
			namespace, threadID, leaseOwnerHint, lease.LeaseDeadlineAt.Format(time.RFC3339Nano))
	}
	return thread, lease, now.UnixMilli(), nil
}

func (c *Coordinator) releaseThreadToStatus(ctx context.Context, namespace string, threadID int64, leaseToken string, reason string, targetStatus string, setReadyAt bool) (resultThread *model.TThread, resultErr error) {
	now := c.now()
	var readyAt any
	if setReadyAt {
		readyAt = c.readyAtForRelease(ctx, namespace, threadID, reason, now)
	}
	changed, err := storage.ReleaseRunningThread(ctx, c.writeDB, namespace, threadID, leaseToken, targetStatus, reason, readyAt, now)
	if err != nil {
		return nil, err
	}
	if !changed {
		// closing 态被 claim 后状态不变（见 claimClosingThread），release 时不能按
		// running 匹配。这里保持 closing 不变、只清 lease 并写 ready_at，让它能被
		// 立即（或退避后）重新 claim 去完成 close，而不是白等 lease 自然过期。
		released, closingErr := storage.ReleaseClosingThread(ctx, c.writeDB, namespace, threadID, leaseToken, reason, c.readyAtForRelease(ctx, namespace, threadID, reason, now), now)
		if closingErr != nil {
			return nil, closingErr
		}
		if !released {
			return nil, ErrLeaseMismatch
		}
	}
	thread, err := c.readThreadFromPrimary(ctx, namespace, threadID)
	if err != nil {
		return nil, err
	}
	return thread, nil
}

func (c *Coordinator) resumeBlockedThread(ctx context.Context, namespace string, threadID int64, reason string, hasPending bool, metadata map[string]string) (resultThread *model.TThread, resultErr error) {
	now := c.now()
	nextStatus := model.ThreadStatusIdle
	var readyAt any
	if hasPending {
		nextStatus = model.ThreadStatusReady
		readyAt = now
	}
	metadataJSON := choose.If(len(metadata) == 0, "{}", util.ToString(metadata))
	changed, err := storage.ResumeBlockedThread(ctx, c.writeDB, namespace, threadID, nextStatus, reason, readyAt, metadataJSON, now)
	if err != nil {
		return nil, err
	}
	if !changed {
		if _, err := c.readThreadFromPrimary(ctx, namespace, threadID); err != nil {
			return nil, err
		}
		return nil, ErrThreadNotBlocked
	}
	thread, err := c.readThreadFromPrimary(ctx, namespace, threadID)
	if err != nil {
		return nil, err
	}
	return thread, nil
}

func (c *Coordinator) wakeIdleThread(ctx context.Context, namespace string, threadID int64, reason string) (resultThread *model.TThread, resultErr error) {
	now := c.now()
	changed, err := storage.WakeIdleThread(ctx, c.writeDB, namespace, threadID, reason, now)
	if err != nil {
		return nil, err
	}
	if !changed {
		thread, err := c.readThreadFromPrimary(ctx, namespace, threadID)
		if err != nil {
			return nil, err
		}
		if thread.Status == model.ThreadStatusReady {
			return thread, nil
		}
		return nil, ErrInvalidStatusTransition
	}
	return c.readThreadFromPrimary(ctx, namespace, threadID)
}

func (c *Coordinator) newInput(ctx context.Context, threadID int64, senderType string, senderID string, messageType string, payload []byte, metadata map[string]string) (message *model.TMailboxMessage, resultErr error) {
	now := c.now()
	metadataJSON := choose.If(len(metadata) == 0, "{}", util.ToString(metadata))
	messageID, err := c.idgen.NextID(ctx)
	if err != nil {
		return nil, err
	}
	return &model.TMailboxMessage{
		MessageId:    messageID,
		ThreadId:     threadID,
		SenderType:   senderType,
		SenderId:     senderID,
		MessageType:  messageType,
		Status:       model.MessageStatusPending,
		Payload:      string(payload),
		MetadataJson: metadataJSON,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func (c *Coordinator) enqueueInput(ctx context.Context, namespace string, message *model.TMailboxMessage) (err error) {
	if message == nil {
		return nil
	}
	return c.enqueuePendingMessage(ctx, namespace, message, float64(message.MessageId))
}

func (c *Coordinator) enqueueInputFirst(ctx context.Context, namespace string, message *model.TMailboxMessage) (err error) {
	if message == nil {
		return nil
	}
	return c.enqueuePendingMessage(ctx, namespace, message, -float64(message.MessageId))
}

func (c *Coordinator) loadPendingInputs(ctx context.Context, namespace string, threadID int64, limit int32) (resultMessages []*model.TMailboxMessage, resultErr error) {
	if err := c.requireRedis(); err != nil {
		logs.CtxError(ctx, "[mailbox] load pending messages redis unavailable, namespace=%s thread_id=%d err=%v", namespace, threadID, err)
		return nil, err
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	messages, err := c.pendingStore.List(ctx, namespace, threadID, 0, int64(limit-1))
	if err != nil {
		logs.CtxError(ctx, "[mailbox] load pending messages failed, namespace=%s thread_id=%d limit=%d err=%v", namespace, threadID, limit, err)
		return nil, err
	}
	c.observeInputQueueStats(ctx, namespace, threadID, "", "load_pending")
	return messages, nil
}

func (c *Coordinator) hasPendingInputs(ctx context.Context, namespace string, threadID int64) (found bool, resultErr error) {
	if err := c.requireRedis(); err != nil {
		return false, err
	}
	items, err := c.pendingStore.List(ctx, namespace, threadID, 0, 0)
	if err != nil {
		return false, err
	}
	return len(items) > 0, nil
}

func (c *Coordinator) archiveInput(ctx context.Context, message *model.TMailboxMessage, stage string) {
	if message == nil {
		return
	}
	if err := storage.ArchiveInput(ctx, c.writeDB, message); err != nil {
		logs.CtxError(ctx, "mailbox mysql best-effort insert failed, stage=%s thread_id=%d message_id=%d err=%v", stage, message.ThreadId, message.MessageId, err)
	}
}

func (c *Coordinator) removeInput(ctx context.Context, namespace string, threadID int64, messageID int64) {
	if c.pendingStore == nil {
		return
	}
	if err := c.pendingStore.Remove(ctx, namespace, threadID, messageID); err != nil {
		logs.CtxWarn(ctx, "mailbox redis best-effort cleanup failed, thread_id=%d message_id=%d err=%v", threadID, messageID, err)
	}
}

func (c *Coordinator) getThreadFromDB(ctx context.Context, db *gorm.DB, namespace string, threadID int64) (resultThread *model.TThread, resultErr error) {
	thread, err := storage.FindThread(ctx, db, threadID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrThreadNotFound
		}
		return nil, err
	}
	if thread.Namespace != namespace {
		return nil, ErrThreadNotFound
	}
	return thread, nil
}

// readyAtForRelease 计算 release 后线程重新可扫描的时间。失败类 reason 推迟
// failureBackoff，避免持续失败的线程以扫描周期为节奏热循环重试；正常交接
// （completed / graceful exit / timeout 类 reason）立即可扫。
func (c *Coordinator) readyAtForRelease(ctx context.Context, namespace string, threadID int64, reason string, now time.Time) (at time.Time) {
	if c.failureBackoff <= 0 || !c.isFailureReleaseReason(reason) {
		return now
	}
	// 首次失败立即可重试：status_reason 保留的是上一次 release/状态转换写入的
	// reason（claim 不改写它），上一次不是失败说明本次是新出现的失败，很可能是
	// 瞬时抖动，立即重试可零等待自愈。只有连续失败（上一次也是失败 reason）才
	// 退避，此时该线程大概率处于持久性故障，降频重试保护控制面与 worker。
	// 读取与更新之间存在竞态窗口，最坏影响是某一次重试的退避档位判错，可接受。
	if prev, err := c.readThreadFromPrimary(ctx, namespace, threadID); err == nil && !c.isFailureReleaseReason(prev.StatusReason) {
		logs.CtxInfo(ctx, "[Coordinator] first failure release, retry immediately: namespace=%s thread_id=%d reason=%q prev_reason=%q",
			namespace, threadID, reason, prev.StatusReason)
		return now
	}
	readyAt := now.Add(c.failureBackoff)
	logs.CtxWarn(ctx, "[Coordinator] consecutive failure release, apply ready backoff: namespace=%s thread_id=%d reason=%q ready_at=%s",
		namespace, threadID, reason, readyAt.Format(time.RFC3339Nano))
	return readyAt
}

func (c *Coordinator) isFailureReleaseReason(reason string) (found bool) {
	_, ok := c.failureReasons[strings.ToLower(strings.TrimSpace(reason))]
	return ok
}

func (c *Coordinator) rejectSessionEnvMismatch(ctx context.Context, tx *gorm.DB, namespace string, env string, sessionID string) (err error) {
	if sessionID == "" {
		return nil
	}
	existing, err := storage.FindSessionEnvironmentMismatch(ctx, tx, namespace, sessionID, env)
	if err != nil {
		return err
	}
	if existing != nil {
		return ErrSessionEnvMismatch
	}
	return nil
}

func (c *Coordinator) ensureNamespace(ctx context.Context, namespace string) (resultErr error) {
	row, err := storage.FindNamespace(ctx, c.readDB, namespace)
	if err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
		// 刚注册的 namespace 可能尚未同步到从库，误报 not found 会让紧随注册的
		// CreateThread 失败；从库未命中时再查一次主库兜底。
		row, err = storage.FindNamespace(ctx, c.writeDB, namespace)
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNamespaceNotFound
		}
		return err
	}
	if row.Namespace == "" {
		return ErrNamespaceNotFound
	}
	return nil
}

// handleWakeConflict 处理 submitInput 唤醒 CAS（where status=idle）未命中的情况。
// 消息此时已入队，按主库最新状态决定去留：
//   - ready/running：线程已被并发唤醒或正在运行，消息保留等待消费，视为成功；
//   - blocked：消息保留，等 resumeBlockedThread 后消费，视为成功；
//   - closing/closed：线程已进入关闭流程，撤回消息并返回 ErrThreadClosed；
//   - 其它（含仍为 idle 的异常竞态）：撤回消息并报错，避免留下无人唤醒的消息。
func (c *Coordinator) handleWakeConflict(ctx context.Context, namespace string, threadID int64, messageID int64) (thread *model.TThread, err error) {
	latest, err := c.readThreadFromPrimary(ctx, namespace, threadID)
	if err != nil {
		c.removeInput(ctx, namespace, threadID, messageID)
		return nil, err
	}
	switch latest.Status {
	case model.ThreadStatusReady, model.ThreadStatusRunning, model.ThreadStatusBlocked:
		logs.CtxInfo(ctx, "[Mailbox] wake races with concurrent transition, keep message: namespace=%s thread_id=%d message_id=%d status=%s",
			namespace, threadID, messageID, latest.Status)
		return latest, nil
	case model.ThreadStatusClosing, model.ThreadStatusClosed:
		c.removeInput(ctx, namespace, threadID, messageID)
		return nil, ErrThreadClosed
	default:
		c.removeInput(ctx, namespace, threadID, messageID)
		return nil, fmt.Errorf("wake thread %d conflict: unexpected status %q", threadID, latest.Status)
	}
}

// bestEffortPullReadyAtForward 把处于失败退避期（ready_at 在未来）的 ready 线程
// 拉回立即可扫描：新输入到达说明用户在等结果，不应再让上一轮的失败退避拖延本轮。
func (c *Coordinator) bestEffortPullReadyAtForward(ctx context.Context, namespace string, threadID int64, now time.Time) {
	changed, err := storage.PullReadyAtForward(ctx, c.writeDB, namespace, threadID, now)
	if err != nil {
		logs.CtxWarn(ctx, "[Mailbox] pull ready_at forward failed: namespace=%s thread_id=%d err=%v", namespace, threadID, err)
		return
	}
	if changed {
		logs.CtxInfo(ctx, "[Mailbox] pulled backoff ready_at forward on new input: namespace=%s thread_id=%d", namespace, threadID)
	}
}

func (c *Coordinator) enqueuePendingMessage(ctx context.Context, namespace string, message *model.TMailboxMessage, score float64) (resultErr error) {
	if err := c.requireRedis(); err != nil {
		return err
	}
	if err := c.pendingStore.Enqueue(ctx, namespace, message, score); err != nil {
		return err
	}
	return nil
}

func (c *Coordinator) redisPendingQueueHeadSnapshot(ctx context.Context, namespace string, threadID int64) (count int64, head *model.TMailboxMessage, err error) {
	if err = c.requireRedis(); err != nil {
		return 0, nil, err
	}
	count, err = c.pendingStore.Count(ctx, namespace, threadID)
	if err != nil {
		return 0, nil, err
	}
	if count == 0 {
		return 0, nil, nil
	}
	items, err := c.pendingStore.List(ctx, namespace, threadID, 0, 0)
	if err != nil {
		return 0, nil, err
	}
	if len(items) == 0 {
		return count, nil, nil
	}
	return count, items[0], nil
}

func (c *Coordinator) findInput(ctx context.Context, namespace string, threadID, messageID int64) (message *model.TMailboxMessage, reason string, err error) {
	message, reason, err = c.readArchivedInput(ctx, namespace, threadID, messageID)
	if err != nil || message != nil || c.pendingStore == nil {
		return message, reason, err
	}
	stored, storeErr := c.pendingStore.Get(ctx, messageID)
	if storeErr != nil {
		return nil, reason, nil
	}
	if stored.Namespace != namespace {
		return nil, MessageLookupReasonNamespaceMismatch, nil
	}
	if stored.Message == nil || stored.Message.ThreadId != threadID {
		return nil, MessageLookupReasonThreadMismatch, nil
	}
	return stored.Message, "", nil
}

func (c *Coordinator) readArchivedInput(ctx context.Context, namespace string, threadID, messageID int64) (message *model.TMailboxMessage, reason string, err error) {
	if threadID <= 0 || messageID <= 0 {
		return nil, MessageLookupReasonInvalidRef, nil
	}
	message, err = storage.FindMessage(ctx, c.readDB, messageID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, MessageLookupReasonNotFound, nil
	}
	if err != nil {
		return nil, "", err
	}
	if message.ThreadId != threadID {
		return nil, MessageLookupReasonThreadMismatch, nil
	}
	if _, err = c.readThread(ctx, namespace, threadID); err != nil {
		if errors.Is(err, ErrThreadNotFound) {
			return nil, MessageLookupReasonNamespaceMismatch, nil
		}
		return nil, "", err
	}
	return message, "", nil
}

func (c *Coordinator) transitionThreadToClosing(ctx context.Context, namespace string, threadID int64, reason string, metadata map[string]string) (thread *model.TThread, err error) {
	now := c.now()
	for attempt := 0; attempt < 2; attempt++ {
		thread, err := c.readThreadFromPrimary(ctx, namespace, threadID)
		if err != nil {
			return nil, err
		}
		switch thread.Status {
		case model.ThreadStatusClosed, model.ThreadStatusClosing:
			return thread, nil
		case model.ThreadStatusIdle, model.ThreadStatusBlocked:
			changed, err := storage.StartClosingIdleOrBlockedThread(ctx, c.writeDB, namespace, threadID, thread.Status, reason, threadMetadataWithActivation(thread.MetadataJson, metadata), now)
			if err != nil {
				return nil, err
			}
			if changed {
				return c.readThreadFromPrimary(ctx, namespace, threadID)
			}
		case model.ThreadStatusReady, model.ThreadStatusRunning:
			changed, err := storage.StartClosingReadyOrRunningThread(ctx, c.writeDB, namespace, threadID, reason, now)
			if err != nil {
				return nil, err
			}
			if changed {
				return c.readThreadFromPrimary(ctx, namespace, threadID)
			}
		default:
			return nil, ErrInvalidClose
		}
	}
	return nil, ErrInvalidClose
}

func (c *Coordinator) markIdleThreadReadyWithActivation(ctx context.Context, namespace string, thread *model.TThread, reason string, metadata map[string]string) (resultThread *model.TThread, resultErr error) {
	now := c.now()
	metadataJSON := threadMetadataWithActivation(thread.MetadataJson, metadata)
	changed, err := storage.WakeIdleThreadWithActivation(ctx, c.writeDB, namespace, thread.ThreadId, reason, metadataJSON, now)
	if err != nil {
		return nil, err
	}
	if !changed {
		latest, err := c.readThreadFromPrimary(ctx, namespace, thread.ThreadId)
		if err != nil {
			return nil, err
		}
		switch latest.Status {
		case model.ThreadStatusReady, model.ThreadStatusRunning:
			return latest, nil
		case model.ThreadStatusBlocked:
			return nil, ErrThreadBlocked
		case model.ThreadStatusClosing, model.ThreadStatusClosed:
			return nil, ErrThreadClosed
		default:
			return nil, fmt.Errorf("invalid idle wake status: %s", latest.Status)
		}
	}
	thread.Status = model.ThreadStatusReady
	thread.StatusReason = reason
	thread.ReadyAt = now
	thread.MetadataJson = metadataJSON
	thread.LastActiveAt = now
	thread.UpdatedAt = now
	return thread, nil
}

func (c *Coordinator) bestEffortFinalizeControlMessage(ctx context.Context, namespace string, threadID int64, controlMessageID int64, now time.Time) {
	if err := c.finalizeControlMessageAcked(ctx, namespace, threadID, controlMessageID, now); err != nil {
		logs.CtxWarn(ctx, "[Mailbox] finalize close control message failed, leave for retry: namespace=%s thread_id=%d control_message_id=%d err=%v",
			namespace, threadID, controlMessageID, err)
	}
}

func (c *Coordinator) cancelCutoff(ctx context.Context, namespace string, threadID int64, cutoffMessageID *int64) (cutoff int64, err error) {
	if cutoffMessageID != nil {
		if *cutoffMessageID <= 0 {
			return 0, fmt.Errorf("%w: cutoff_message_id must be positive", ErrInvalidCancel)
		}
		message, reason, err := c.findInput(ctx, namespace, threadID, *cutoffMessageID)
		if err != nil {
			return 0, err
		}
		if message == nil {
			return 0, fmt.Errorf("%w: cutoff_message_id=%d reason=%s", ErrInvalidCancel, *cutoffMessageID, reason)
		}
		if !model.IsOrdinaryInputMessage(message.MessageType) {
			return 0, fmt.Errorf("%w: cutoff_message_id=%d is control message", ErrInvalidCancel, *cutoffMessageID)
		}
		return *cutoffMessageID, nil
	}

	cutoff, err = storage.LastOrdinaryInputID(ctx, c.readDB, threadID)
	if err != nil {
		return 0, err
	}
	redisCutoff, err := c.maxOrdinaryPendingMessageIDFromRedis(ctx, namespace, threadID)
	if err != nil {
		return 0, err
	}
	if redisCutoff > cutoff {
		cutoff = redisCutoff
	}
	return cutoff, nil
}

func (c *Coordinator) maxOrdinaryPendingMessageIDFromRedis(ctx context.Context, namespace string, threadID int64) (count int64, resultErr error) {
	if c.pendingStore == nil {
		return 0, nil
	}
	messages, err := c.loadAllPendingMessages(ctx, namespace, threadID)
	if err != nil {
		return 0, err
	}
	var maxID int64
	for _, message := range messages {
		if message.Status == model.MessageStatusPending && model.IsOrdinaryInputMessage(message.MessageType) && message.MessageId > maxID {
			maxID = message.MessageId
		}
	}
	return maxID, nil
}

func (c *Coordinator) cancelPendingOrdinaryMessages(ctx context.Context, namespace string, threadID int64, cutoff int64) (resultIds []int64, resultErr error) {
	messages, err := c.loadAllPendingMessages(ctx, namespace, threadID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		if message.Status == model.MessageStatusPending && message.MessageId <= cutoff && model.IsOrdinaryInputMessage(message.MessageType) {
			ids = append(ids, message.MessageId)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	now := c.now()
	cancelled, err := c.finalizeMessagesInRedis(ctx, namespace, threadID, ids, model.MessageStatusCanceled, "", now)
	if err != nil {
		return nil, err
	}
	if _, err := storage.UpdateInputStatus(ctx, c.writeDB, threadID, model.MessageStatusPending, model.MessageStatusCanceled, now, nil, ids); err != nil {
		logs.CtxError(ctx, "mailbox mysql best-effort cancel failed, thread_id=%d message_ids=%v err=%v", threadID, ids, err)
	}
	return messageIDsFromModels(cancelled), nil
}

func (c *Coordinator) loadAllPendingMessages(ctx context.Context, namespace string, threadID int64) (resultMessages []*model.TMailboxMessage, resultErr error) {
	if err := c.requireRedis(); err != nil {
		return nil, err
	}
	return c.pendingStore.List(ctx, namespace, threadID, 0, -1)
}

func (c *Coordinator) newCancelInputControlMessage(ctx context.Context, threadID int64, cutoff int64, reason string, metadata map[string]string) (message *model.TMailboxMessage, resultErr error) {
	now := c.now()
	messageID, err := c.idgen.NextID(ctx)
	if err != nil {
		return nil, err
	}
	requestID := strconv.FormatInt(messageID, 10)
	payload := CancelInputControlPayload{
		ControlType:     model.ControlTypeCancelInput,
		RequestID:       requestID,
		ThreadID:        threadID,
		CutoffMessageID: cutoff,
		Reason:          reason,
	}
	controlMetadata := CancelInputControlMetadata{
		ControlType:      model.ControlTypeCancelInput,
		RequestID:        requestID,
		CutoffMessageID:  strconv.FormatInt(cutoff, 10),
		Reason:           reason,
		LogID:            metadata["logid"],
		BytedCtxMetaInfo: metadata[model.MetadataKeyBytedCtxMetaInfo],
		KEnv:             metadata[model.MetadataKeyKEnv],
	}
	return &model.TMailboxMessage{
		MessageId:    messageID,
		ThreadId:     threadID,
		SenderType:   model.SenderTypeSystem,
		SenderId:     model.AgentCoordinatorSenderID,
		MessageType:  model.MessageTypeControlCancelInput,
		Status:       model.MessageStatusPending,
		Payload:      util.ToString(payload),
		MetadataJson: util.ToString(controlMetadata),
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func (c *Coordinator) newCloseThreadControlMessage(ctx context.Context, threadID int64, reason string, metadata map[string]string) (message *model.TMailboxMessage, resultErr error) {
	now := c.now()
	messageID, err := c.idgen.NextID(ctx)
	if err != nil {
		return nil, err
	}
	requestID := strconv.FormatInt(messageID, 10)
	payload := CloseThreadControlPayload{
		ControlType: model.ControlTypeCloseThread,
		RequestID:   requestID,
		ThreadID:    threadID,
		Reason:      reason,
	}
	controlMetadata := CloseThreadControlMetadata{
		ControlType:      model.ControlTypeCloseThread,
		RequestID:        requestID,
		Reason:           reason,
		LogID:            metadata["logid"],
		BytedCtxMetaInfo: metadata[model.MetadataKeyBytedCtxMetaInfo],
		KEnv:             metadata[model.MetadataKeyKEnv],
	}
	return &model.TMailboxMessage{
		MessageId:    messageID,
		ThreadId:     threadID,
		SenderType:   model.SenderTypeSystem,
		SenderId:     model.AgentCoordinatorSenderID,
		MessageType:  model.MessageTypeControlCloseThread,
		Status:       model.MessageStatusPending,
		Payload:      util.ToString(payload),
		MetadataJson: util.ToString(controlMetadata),
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func (c *Coordinator) findExistingCloseControlMessage(ctx context.Context, namespace string, threadID int64) (resultMessage *model.TMailboxMessage, resultErr error) {
	if c.pendingStore == nil {
		return nil, nil
	}
	messages, err := c.loadAllPendingMessages(ctx, namespace, threadID)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		if message.Status == model.MessageStatusPending && message.MessageType == model.MessageTypeControlCloseThread {
			return message, nil
		}
	}
	return nil, nil
}

func (c *Coordinator) lookupCloseControlMessage(ctx context.Context, namespace string, threadID int64, messageID int64) (resultMessage *model.TMailboxMessage, resultErr error) {
	message, reason, err := c.findInput(ctx, namespace, threadID, messageID)
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, fmt.Errorf("%w: control_message_id=%d reason=%s", ErrInvalidClose, messageID, reason)
	}
	if message.MessageType != model.MessageTypeControlCloseThread {
		return nil, fmt.Errorf("%w: control_message_id=%d is not close control", ErrInvalidClose, messageID)
	}
	return message, nil
}

func (c *Coordinator) finalizeControlMessageAcked(ctx context.Context, namespace string, threadID int64, messageID int64, now time.Time) (resultErr error) {
	if c.pendingStore != nil {
		if _, err := c.finalizeMessagesInRedis(ctx, namespace, threadID, []int64{messageID}, model.MessageStatusAcked, "", now); err != nil {
			return err
		}
	}
	if _, err := storage.UpdateInputStatus(ctx, c.writeDB, threadID, model.MessageStatusPending, model.MessageStatusAcked, now, nil, []int64{messageID}); err != nil {
		logs.CtxError(ctx, "mailbox mysql best-effort close control ack failed, thread_id=%d message_id=%d err=%v", threadID, messageID, err)
	}
	return nil
}

func (c *Coordinator) finalizeMessagesInRedis(ctx context.Context, namespace string, threadID int64, messageIDs []int64, targetStatus string, triggerTurnID string, handledAt time.Time) (messages []*model.TMailboxMessage, err error) {
	if err := c.requireRedis(); err != nil {
		return nil, err
	}
	messages, err = c.pendingStore.Finalize(ctx, namespace, threadID, messageIDs, targetStatus, triggerTurnID, handledAt)
	if err != nil {
		return nil, err
	}
	c.observeInputQueueStats(ctx, namespace, threadID, "", "finalize_"+targetStatus)
	return messages, nil
}

func (c *Coordinator) ensureActiveLease(ctx context.Context, namespace string, threadID int64, leaseToken string) (resultErr error) {
	thread, err := c.readThread(ctx, namespace, threadID)
	if err != nil {
		return err
	}
	now := c.now()
	if (thread.Status != model.ThreadStatusRunning && thread.Status != model.ThreadStatusClosing) || thread.LeaseToken != leaseToken || thread.LeaseDeadlineAt.IsZero() || thread.LeaseDeadlineAt.Before(now) {
		return ErrLeaseMismatch
	}
	return nil
}

func (c *Coordinator) observeInputQueueStats(ctx context.Context, namespace string, threadID int64, threadStatus string, op string) {
	count, head, err := c.redisPendingQueueHeadSnapshot(ctx, namespace, threadID)
	if err != nil {
		logs.CtxWarn(ctx, "mailbox input queue observe failed, namespace=%s thread_id=%d op=%s err=%v", namespace, threadID, op, err)
		return
	}
	checkedAt := c.now()
	var headMessageID, headAgeMillis int64
	if head != nil {
		headMessageID = head.MessageId
		headAgeMillis = max(0, checkedAt.Sub(head.CreatedAt).Milliseconds())
	}
	logs.CtxInfo(ctx, "mailbox input queue stats, namespace=%s thread_id=%d op=%s thread_status=%s queue_len=%d head_message_id=%d head_age_ms=%d",
		namespace, threadID, op, threadStatus, count, headMessageID, headAgeMillis)
}

func (c *Coordinator) bestEffortTouchThreadActive(ctx context.Context, namespace string, threadID int64, activeAt time.Time) {
	_, err := storage.TouchThreadActive(ctx, c.writeDB, namespace, threadID, activeAt)
	if err != nil {
		logs.CtxError(ctx, "mailbox mysql best-effort touch thread active failed, thread_id=%d err=%v", threadID, err)
	}
}

func (c *Coordinator) requireRedis() (err error) {
	if c.pendingStore == nil {
		return ErrRedisUnavailable
	}
	return nil
}
