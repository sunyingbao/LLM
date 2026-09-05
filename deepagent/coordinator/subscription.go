package coordinator

import (
	"context"
	redisstore "eino-cli/deepagent/coordinator/internal/infra/store/redis"
	"eino-cli/deepagent/coordinator/internal/service/streamout"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"code.byted.org/gopkg/logs/v2"
)

const (
	sessionBatchSize               = int32(50)
	subscriptionPollInterval       = 200 * time.Millisecond
	subscriptionIdleBackoffAfter   = 15 * time.Second
	subscriptionIdlePollInterval   = time.Second
	subscriptionLeaseRenewInterval = 10 * time.Second
	subscriptionAckTimeout         = time.Second
)

type SessionFrame struct {
	QueueID string
	Event   *Event
}

type Subscription struct {
	ctx     context.Context
	queue   sessionQueue
	maxIdle time.Duration
	frames  chan subscriptionItem
}

func newSubscription(ctx context.Context, queue sessionQueue, request SubscribeSessionRequest, maxIdle time.Duration) (subscription *Subscription) {
	if maxIdle <= 0 {
		maxIdle = 2 * time.Minute
	}
	subscription = &Subscription{
		ctx:     ctx,
		queue:   queue,
		maxIdle: maxIdle,
		frames:  make(chan subscriptionItem),
	}
	go func() {
		defer close(subscription.frames)
		err := subscription.run(ctx, request)
		if err == nil || ctx.Err() != nil {
			return
		}
		select {
		case subscription.frames <- subscriptionItem{err: err}:
		case <-ctx.Done():
		}
	}()
	return subscription
}

func (s *Subscription) Recv() (frame *SessionFrame, err error) {
	if s == nil || s.frames == nil {
		return nil, io.EOF
	}
	item, ok := <-s.frames
	if !ok {
		return nil, io.EOF
	}
	return item.frame, item.err
}

func (s *Subscription) sendFrame(frame *SessionFrame) (err error) {
	select {
	case s.frames <- subscriptionItem{frame: frame}:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *Subscription) run(ctx context.Context, req SubscribeSessionRequest) (runErr error) {
	if strings.TrimSpace(req.Namespace) == "" || strings.TrimSpace(req.SessionID) == "" {
		return streamout.ErrInvalidArgument
	}
	trace := sessionTrace{namespace: req.Namespace, sessionID: req.SessionID, recoverQueueID: req.RecoverQueueID}
	meta, err := s.queue.OpenSubscription(ctx, req.Namespace, req.SessionID, req.RecoverQueueID)
	if err != nil {
		logs.CtxWarn(ctx, "[StreamSession] acquire queue failed, namespace=%s 对话流ID=%s recover_queue_id=%s err=%v", req.Namespace, req.SessionID, req.RecoverQueueID, err)
		return err
	}
	trace.queueID = meta.QueueID
	logs.CtxInfo(ctx, "[StreamSession] queue ready, initial_cursor=%d lease_expire_at=%s recover_until=%s %s",
		meta.LastDeliveredEventID, meta.LeaseExpireAt.Format(time.RFC3339Nano), meta.RecoverUntil.Format(time.RFC3339Nano), trace.String())
	defer s.closeQueue(meta, trace)
	// 获取队列失败仍向调用方报错；获取成功后的队列失效只结束当前订阅。
	defer func() {
		if errors.Is(runErr, streamout.ErrConsumerMismatch) || errors.Is(runErr, streamout.ErrQueueNotFound) {
			runErr = nil
		}
	}()

	if err := s.sendFrame(&SessionFrame{QueueID: meta.QueueID}); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := s.renewLease(ctx, meta, trace); err != nil {
		return err
	}

	leaseRenewInterval := subscriptionLeaseRenewInterval
	nextRenewAt := time.Now().Add(leaseRenewInterval)
	pollTimer := time.NewTimer(0)
	defer stopSessionTimer(pollTimer)
	renewTimer := time.NewTimer(leaseRenewInterval)
	defer stopSessionTimer(renewTimer)
	idleSince := time.Time{}

	for {
		select {
		case <-ctx.Done():
			logs.CtxInfo(ctx, "[StreamSession] stream closed by context, %s", trace.String())
			return nil
		case <-renewTimer.C:
			if renewErr := s.renewLease(ctx, meta, trace); renewErr != nil {
				return renewErr
			}
			nextRenewAt = time.Now().Add(leaseRenewInterval)
			resetSessionTimer(renewTimer, leaseRenewInterval)
		case <-pollTimer.C:
			if ctx.Err() != nil {
				return nil
			}
			if !time.Now().Before(nextRenewAt) {
				if renewErr := s.renewLease(ctx, meta, trace); renewErr != nil {
					return renewErr
				}
				nextRenewAt = time.Now().Add(leaseRenewInterval)
				resetSessionTimer(renewTimer, leaseRenewInterval)
			}
			events, pollErr := s.queue.PeekPendingEvents(ctx, meta.QueueID, meta.ConsumerToken, sessionBatchSize)
			if pollErr != nil {
				logSessionLoopError(ctx, "peek pending events", pollErr, trace)
				return pollErr
			}
			if len(events) > 0 {
				if deliverErr := s.deliverEvents(ctx, meta, events, trace); deliverErr != nil {
					return deliverErr
				}
			}
			nextPoll, nextIdleSince := s.nextPollInterval(time.Now(), idleSince, len(events) > 0)
			idleSince = nextIdleSince
			if s.maxIdleExceeded(time.Now(), idleSince) {
				logs.CtxInfo(ctx, "[StreamSession] close idle stream, idle_ms=%d max_idle_ms=%d %s", time.Since(idleSince).Milliseconds(), s.maxIdle.Milliseconds(), trace.String())
				return nil
			}
			resetSessionTimer(pollTimer, nextPoll)
		}
	}
}

func (s *Subscription) closeQueue(meta *redisstore.StreamQueueMeta, trace sessionTrace) {
	if meta == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.queue.CloseQueue(ctx, meta.QueueID, meta.ConsumerToken); err != nil && !errors.Is(err, streamout.ErrConsumerMismatch) && !errors.Is(err, streamout.ErrQueueNotFound) {
		logs.CtxWarn(ctx, "[StreamSession] close queue failed, err=%v %s", err, trace.String())
	}
}

func (s *Subscription) renewLease(ctx context.Context, meta *redisstore.StreamQueueMeta, trace sessionTrace) (err error) {
	_, err = s.queue.RenewQueueLease(ctx, meta.QueueID, meta.ConsumerToken)
	logSessionLoopError(ctx, "renew queue lease", err, trace)
	return err
}

func (s *Subscription) deliverEvents(ctx context.Context, meta *redisstore.StreamQueueMeta, events []Event, trace sessionTrace) (err error) {
	delivered := make([]Event, 0, len(events))
	for _, event := range events {
		frameEvent := cloneEvent(event)
		if err := s.sendFrame(&SessionFrame{Event: &frameEvent}); err != nil {
			if ackErr := s.ackDelivered(ctx, meta, delivered, trace); ackErr != nil {
				return ackErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		delivered = append(delivered, event)
	}
	return s.ackDelivered(ctx, meta, delivered, trace)
}

func (s *Subscription) ackDelivered(ctx context.Context, meta *redisstore.StreamQueueMeta, events []Event, trace sessionTrace) (err error) {
	if len(events) == 0 {
		return nil
	}
	ackCtx, cancel := context.WithTimeout(context.Background(), subscriptionAckTimeout)
	defer cancel()
	if err := s.queue.AckDeliveredEvents(ackCtx, meta.QueueID, meta.ConsumerToken, events); err != nil {
		logs.CtxWarn(ctx, "[StreamSession] ack delivered failed, event_count=%d err=%v %s", len(events), err, trace.String())
		logSessionLoopError(ctx, "ack delivered", err, trace)
		return err
	}
	return nil
}

func (s *Subscription) nextPollInterval(now time.Time, idleSince time.Time, hasEvents bool) (interval time.Duration, nextIdleSince time.Time) {
	if hasEvents {
		return 0, time.Time{}
	}
	if idleSince.IsZero() {
		idleSince = now
	}
	if subscriptionIdleBackoffAfter > 0 && !now.Before(idleSince.Add(subscriptionIdleBackoffAfter)) {
		return subscriptionIdlePollInterval, idleSince
	}
	return subscriptionPollInterval, idleSince
}

func (s *Subscription) maxIdleExceeded(now time.Time, idleSince time.Time) (exceeded bool) {
	return s.maxIdle > 0 && !idleSince.IsZero() && !now.Before(idleSince.Add(s.maxIdle))
}

func logSessionLoopError(ctx context.Context, stage string, err error, trace sessionTrace) {
	if err == nil {
		return
	}
	if errors.Is(err, streamout.ErrConsumerMismatch) || errors.Is(err, streamout.ErrQueueNotFound) {
		logs.CtxInfo(ctx, "[StreamSession] stop on queue state change, stage=%s err=%v %s", stage, err, trace.String())
		return
	}
	logs.CtxError(ctx, "[StreamSession] %s failed, err=%v %s", stage, err, trace.String())
}

func stopSessionTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func resetSessionTimer(timer *time.Timer, d time.Duration) {
	stopSessionTimer(timer)
	timer.Reset(d)
}

type subscriptionItem struct {
	frame *SessionFrame
	err   error
}

type sessionQueue interface {
	OpenSubscription(ctx context.Context, namespace string, sessionID string, recoverQueueID string) (*redisstore.StreamQueueMeta, error)
	RenewQueueLease(ctx context.Context, queueID string, consumerToken string) (*redisstore.StreamQueueMeta, error)
	PeekPendingEvents(ctx context.Context, queueID string, consumerToken string, limit int32) ([]Event, error)
	AckDeliveredEvents(ctx context.Context, queueID string, consumerToken string, events []Event) error
	CloseQueue(ctx context.Context, queueID string, consumerToken string) error
}

type sessionTrace struct {
	namespace      string
	sessionID      string
	queueID        string
	recoverQueueID string
}

func (t sessionTrace) String() (label string) {
	if t.recoverQueueID == "" {
		return fmt.Sprintf("namespace=%s 对话流ID=%s queue_id=%s", t.namespace, t.sessionID, t.queueID)
	}
	return fmt.Sprintf("namespace=%s 对话流ID=%s queue_id=%s recover_queue_id=%s", t.namespace, t.sessionID, t.queueID, t.recoverQueueID)
}
