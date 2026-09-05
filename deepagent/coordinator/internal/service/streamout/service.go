package streamout

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/coordinator/internal/infra/idgen"
	redisstore "eino-cli/deepagent/coordinator/internal/infra/store/redis"
	"eino-cli/deepagent/coordinator/internal/service/eventlog"
	"github.com/google/uuid"
)

const (
	defaultLiveTTL               = 30 * time.Minute
	defaultLeaseTTL              = 30 * time.Second
	defaultRecoverWindow         = 2 * time.Minute
	defaultMissingLiveEventGrace = 2 * time.Second
	defaultNoPendingLogInterval  = 10 * time.Second
	defaultPeekLimit             = int32(50)
	maxPeekLimit                 = int32(200)
	queueStatusActive            = "active"
	queueStatusClosed            = "closed"
	liveDeliveryPrefix           = "live:"
	defaultConsumerName          = "streamout"
)

type subscription struct {
	namespace            string
	sessionID            string
	queueID              string
	consumerToken        string
	cursor               int64
	ackedCursor          int64
	connectedAt          time.Time
	leaseExpireAt        time.Time
	recoverUntil         time.Time
	lastDeliveredEventID int64
	lastDeliveredAt      *time.Time
	missingLiveSeqFirsts map[int64]time.Time
	lastNoPendingLogAt   time.Time
}

type StreamOut struct {
	store         liveEventStore
	queues        queueStateStore
	idgen         idgen.Generator
	now           func() time.Time
	newConsumer   func() string
	liveTTL       time.Duration
	leaseTTL      time.Duration
	recoverWindow time.Duration
	defaultLimit  int32
	maxLimit      int32
	mu            sync.Mutex
	subs          map[string]*subscription
}

type Option func(*StreamOut)

func New(redisClient redisstore.Client, idgen idgen.Generator, opts ...Option) *StreamOut {
	svc := &StreamOut{
		store:         newRedisLiveEventStore(redisClient),
		queues:        newRedisQueueStateStore(redisClient),
		idgen:         idgen,
		now:           time.Now,
		newConsumer:   func() string { return defaultConsumerName + ":" + uuid.NewString() },
		liveTTL:       defaultLiveTTL,
		leaseTTL:      defaultLeaseTTL,
		recoverWindow: defaultRecoverWindow,
		defaultLimit:  defaultPeekLimit,
		maxLimit:      maxPeekLimit,
		subs:          map[string]*subscription{},
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func WithClock(now func() time.Time) Option {
	return func(s *StreamOut) {
		s.now = now
	}
}

func WithLiveTTL(ttl time.Duration) Option {
	return func(s *StreamOut) {
		if ttl > 0 {
			s.liveTTL = ttl
		}
	}
}

func WithConsumerTokenGenerator(fn func() string) Option {
	return func(s *StreamOut) {
		if fn != nil {
			s.newConsumer = fn
		}
	}
}

func WithLeaseConfig(leaseTTL time.Duration, recoverWindow time.Duration) Option {
	return func(s *StreamOut) {
		if leaseTTL > 0 {
			s.leaseTTL = leaseTTL
		}
		if recoverWindow > 0 {
			s.recoverWindow = recoverWindow
		}
	}
}

func (s *StreamOut) OpenSubscription(ctx context.Context, namespace string, sessionID string, queueID string) (queue *redisstore.StreamQueueMeta, err error) {
	if err := s.validateArgs(namespace, sessionID); err != nil {
		return nil, err
	}
	recoverQueueID := strings.TrimSpace(queueID)
	if recoverQueueID == "" {
		nextID, err := s.idgen.NextID(ctx)
		if err != nil {
			logs.CtxError(ctx, "[streamout] next subscription id failed, namespace=%s 对话流ID=%s err=%v", namespace, sessionID, err)
			return nil, err
		}
		queueID = strconv.FormatInt(nextID, 10)
		cursor, err := s.store.CurrentSequence(ctx, namespace, sessionID)
		if err != nil {
			return nil, err
		}
		now := s.now()
		meta := redisstore.StreamQueueMeta{
			QueueID:               queueID,
			Namespace:             namespace,
			SessionID:             sessionID,
			Status:                queueStatusActive,
			ConnectedAt:           now,
			LeaseExpireAt:         now.Add(s.leaseTTL),
			RecoverUntil:          now.Add(s.recoverWindow),
			ConsumerToken:         s.newConsumer(),
			LastDeliveredSequence: cursor,
		}
		if err := s.queues.SaveActive(ctx, meta); err != nil {
			return nil, err
		}
		sub := subscriptionFromMeta(meta)
		s.mu.Lock()
		s.subs[queueID] = sub
		s.mu.Unlock()
		logs.CtxInfo(ctx, "[streamout] subscription opened, namespace=%s 对话流ID=%s queue_id=%s recover_queue_id= initial_cursor=%d", namespace, sessionID, queueID, cursor)
		return s.metaFromSubscription(sub), nil
	}

	meta, err := s.queues.Load(ctx, recoverQueueID)
	if err != nil {
		if errors.Is(err, errQueueStateNotFound) {
			return nil, ErrQueueNotFound
		}
		return nil, err
	}
	if meta.Namespace != namespace || meta.SessionID != sessionID {
		return nil, ErrQueueNotFound
	}
	now := s.now()
	if !meta.RecoverUntil.IsZero() && meta.RecoverUntil.Before(now) {
		return nil, ErrRecoverExpired
	}
	meta.Status = queueStatusActive
	meta.ConnectedAt = now
	meta.LeaseExpireAt = now.Add(s.leaseTTL)
	meta.RecoverUntil = now.Add(s.recoverWindow)
	meta.ConsumerToken = s.newConsumer()
	if err := s.queues.SaveActive(ctx, *meta); err != nil {
		return nil, err
	}
	sub := subscriptionFromMeta(*meta)
	s.mu.Lock()
	s.subs[meta.QueueID] = sub
	s.mu.Unlock()
	logs.CtxInfo(ctx, "[streamout] subscription opened, namespace=%s 对话流ID=%s queue_id=%s recover_queue_id=%s initial_cursor=%d",
		namespace,
		sessionID,
		queueID,
		recoverQueueID,
		sub.cursor,
	)
	return s.metaFromSubscription(sub), nil
}

func (s *StreamOut) RenewQueueLease(ctx context.Context, queueID string, consumerToken string) (*redisstore.StreamQueueMeta, error) {
	sub, err := s.requireConsumer(ctx, queueID, consumerToken)
	if err != nil {
		return nil, err
	}
	now := s.now()
	s.mu.Lock()
	sub.leaseExpireAt = now.Add(s.leaseTTL)
	sub.recoverUntil = now.Add(s.recoverWindow)
	meta := s.metaFromSubscriptionLocked(sub)
	s.mu.Unlock()
	if err := s.queues.SaveActive(ctx, *meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func (s *StreamOut) CloseQueue(ctx context.Context, queueID string, consumerToken string) error {
	sub, err := s.requireConsumer(ctx, queueID, consumerToken)
	if err != nil {
		return err
	}
	meta := s.metaFromSubscription(sub)
	meta.Status = queueStatusClosed
	meta.ConsumerToken = ""
	if err := s.queues.SaveClosed(ctx, *meta); err != nil {
		return err
	}
	s.mu.Lock()
	if current := s.subs[queueID]; current == sub {
		delete(s.subs, queueID)
	}
	s.mu.Unlock()
	logs.Info("[streamout] subscription closed, namespace=%s 对话流ID=%s queue_id=%s cursor=%d", sub.namespace, sub.sessionID, queueID, sub.cursor)
	return nil
}

func (s *StreamOut) FanoutEventRecords(ctx context.Context, namespace string, sessionID string, events []eventlog.Event) error {
	if err := s.validateArgs(namespace, sessionID); err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	start := time.Now()
	var firstSeq, lastSeq int64
	for _, event := range events {
		if event.CreatedAt.IsZero() {
			event.CreatedAt = s.now()
		}
		if event.SessionID == "" {
			event.SessionID = sessionID
		}
		seq, err := s.store.Append(ctx, namespace, sessionID, event, s.liveTTL)
		if err != nil {
			logs.CtxWarn(ctx, "[streamout] append live event failed, namespace=%s 对话流ID=%s event_id=%d err=%v", namespace, sessionID, event.EventID, err)
			return err
		}
		if firstSeq == 0 {
			firstSeq = seq
		}
		lastSeq = seq
	}
	logs.CtxInfo(ctx, "[streamout] fanout live events done, namespace=%s 对话流ID=%s event_count=%d first_seq=%d last_seq=%d first_event_id=%d last_event_id=%d first_event_type=%s last_event_type=%s elapsed_ms=%d",
		namespace,
		sessionID,
		len(events),
		firstSeq,
		lastSeq,
		events[0].EventID,
		events[len(events)-1].EventID,
		events[0].EventType,
		events[len(events)-1].EventType,
		time.Since(start).Milliseconds(),
	)
	return nil
}

func (s *StreamOut) PeekPendingEvents(ctx context.Context, queueID string, consumerToken string, limit int32) ([]eventlog.Event, error) {
	sub, err := s.requireConsumer(ctx, queueID, consumerToken)
	if err != nil {
		return nil, err
	}
	limit = s.normalizedLimit(limit)
	latest, err := s.store.CurrentSequence(ctx, sub.namespace, sub.sessionID)
	if err != nil {
		return nil, err
	}
	if latest <= sub.cursor {
		now := s.now()
		s.mu.Lock()
		if current := s.subs[queueID]; current == sub {
			if current.lastNoPendingLogAt.IsZero() || now.Sub(current.lastNoPendingLogAt) >= defaultNoPendingLogInterval {
				logs.CtxInfo(ctx, "[streamout] no pending live events, namespace=%s 对话流ID=%s queue_id=%s cursor=%d latest=%d limit=%d",
					current.namespace,
					current.sessionID,
					current.queueID,
					current.cursor,
					latest,
					limit,
				)
				current.lastNoPendingLogAt = now
			}
		}
		s.mu.Unlock()
		return nil, nil
	}
	from := sub.cursor + 1
	to := latest
	if maxTo := sub.cursor + int64(limit); to > maxTo {
		to = maxTo
	}
	logs.CtxInfo(ctx, "[streamout] pending live events visible, namespace=%s 对话流ID=%s queue_id=%s cursor=%d latest=%d from=%d to=%d limit=%d key_count=%d",
		sub.namespace,
		sub.sessionID,
		queueID,
		sub.cursor,
		latest,
		from,
		to,
		limit,
		to-from+1,
	)
	values, err := s.store.LoadRange(ctx, sub.namespace, sub.sessionID, from, to)
	if err != nil {
		logs.CtxError(ctx, "[streamout] live event mget failed, namespace=%s 对话流ID=%s from=%d to=%d err=%v", sub.namespace, sub.sessionID, from, to, err)
		return nil, err
	}
	now := s.now()
	events := make([]eventlog.Event, 0, len(values))
	advanceTo := sub.cursor
	s.mu.Lock()
	if current := s.subs[queueID]; current == sub {
		if current.missingLiveSeqFirsts == nil {
			current.missingLiveSeqFirsts = map[int64]time.Time{}
		}
		advanceTo = current.cursor
		for idx, event := range values {
			seq := from + int64(idx)
			if event == nil {
				firstSeen, ok := current.missingLiveSeqFirsts[seq]
				if !ok {
					current.missingLiveSeqFirsts[seq] = now
					logs.CtxWarn(ctx, "[streamout] live event body missing, hold cursor within grace, namespace=%s 对话流ID=%s queue_id=%s missing_seq=%d cursor=%d latest=%d from=%d to=%d limit=%d grace_ms=%d",
						current.namespace,
						current.sessionID,
						current.queueID,
						seq,
						current.cursor,
						latest,
						from,
						to,
						limit,
						defaultMissingLiveEventGrace.Milliseconds(),
					)
					break
				}
				missingAge := now.Sub(firstSeen)
				if missingAge < defaultMissingLiveEventGrace {
					break
				}
				logs.CtxWarn(ctx, "[streamout] skip missing live event after grace, namespace=%s 对话流ID=%s queue_id=%s missing_seq=%d cursor=%d latest=%d missing_age_ms=%d grace_ms=%d",
					current.namespace,
					current.sessionID,
					current.queueID,
					seq,
					current.cursor,
					latest,
					missingAge.Milliseconds(),
					defaultMissingLiveEventGrace.Milliseconds(),
				)
				delete(current.missingLiveSeqFirsts, seq)
				advanceTo = seq
				continue
			}
			if firstSeen, ok := current.missingLiveSeqFirsts[seq]; ok {
				logs.CtxInfo(ctx, "[streamout] live event body recovered after missing gap, namespace=%s 对话流ID=%s queue_id=%s seq=%d wait_ms=%d cursor=%d latest=%d",
					current.namespace,
					current.sessionID,
					current.queueID,
					seq,
					now.Sub(firstSeen).Milliseconds(),
					current.cursor,
					latest,
				)
				delete(current.missingLiveSeqFirsts, seq)
			}
			event.DeliveryID = liveDeliveryID(seq)
			events = append(events, *event)
			advanceTo = seq
		}
		current.cursor = advanceTo
	}
	s.mu.Unlock()
	return events, nil
}

func (s *StreamOut) AckDeliveredEvents(ctx context.Context, queueID string, consumerToken string, events []eventlog.Event) error {
	sub, err := s.requireConsumer(ctx, queueID, consumerToken)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	lastSequence := int64(0)
	lastEventID := int64(0)
	for _, event := range events {
		sequence, parseErr := parseLiveDeliverySequence(event.DeliveryID)
		if parseErr != nil {
			return parseErr
		}
		if sequence > lastSequence {
			lastSequence = sequence
		}
		if event.EventID != 0 {
			lastEventID = event.EventID
		}
	}
	deliveredAt := s.now()
	meta := s.metaFromSubscription(sub)
	if lastSequence > meta.LastDeliveredSequence {
		meta.LastDeliveredSequence = lastSequence
	}
	if lastEventID != 0 {
		meta.LastDeliveredEventID = lastEventID
	}
	meta.LastDeliveredAt = &deliveredAt
	if err := s.queues.SaveActive(ctx, *meta); err != nil {
		return err
	}
	s.mu.Lock()
	if current := s.subs[queueID]; current == sub && current.consumerToken == consumerToken {
		current.ackedCursor = meta.LastDeliveredSequence
		current.lastDeliveredEventID = meta.LastDeliveredEventID
		current.lastDeliveredAt = meta.LastDeliveredAt
	}
	s.mu.Unlock()
	return nil
}

func (s *StreamOut) requireConsumer(ctx context.Context, queueID string, consumerToken string) (*subscription, error) {
	meta, err := s.queues.Load(ctx, queueID)
	if err != nil {
		if errors.Is(err, errQueueStateNotFound) {
			return nil, ErrQueueNotFound
		}
		return nil, err
	}
	if meta.Status != queueStatusActive || meta.ConsumerToken != consumerToken {
		return nil, ErrConsumerMismatch
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := s.subs[queueID]
	if sub == nil || sub.consumerToken != consumerToken {
		sub = subscriptionFromMeta(*meta)
		s.subs[queueID] = sub
	} else {
		sub.leaseExpireAt = meta.LeaseExpireAt
		sub.recoverUntil = meta.RecoverUntil
		if meta.LastDeliveredSequence > sub.ackedCursor {
			sub.ackedCursor = meta.LastDeliveredSequence
		}
		if sub.cursor < sub.ackedCursor {
			sub.cursor = sub.ackedCursor
		}
		sub.lastDeliveredEventID = meta.LastDeliveredEventID
		sub.lastDeliveredAt = meta.LastDeliveredAt
	}
	return sub, nil
}

func (s *StreamOut) metaFromSubscription(sub *subscription) *redisstore.StreamQueueMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.metaFromSubscriptionLocked(sub)
}

func (s *StreamOut) metaFromSubscriptionLocked(sub *subscription) *redisstore.StreamQueueMeta {
	return &redisstore.StreamQueueMeta{
		QueueID:               sub.queueID,
		Namespace:             sub.namespace,
		SessionID:             sub.sessionID,
		Status:                queueStatusActive,
		ConnectedAt:           sub.connectedAt,
		LeaseExpireAt:         sub.leaseExpireAt,
		RecoverUntil:          sub.recoverUntil,
		ConsumerToken:         sub.consumerToken,
		LastDeliveredEventID:  sub.lastDeliveredEventID,
		LastDeliveredSequence: sub.ackedCursor,
		LastDeliveredAt:       sub.lastDeliveredAt,
	}
}

func subscriptionFromMeta(meta redisstore.StreamQueueMeta) *subscription {
	return &subscription{
		namespace:            meta.Namespace,
		sessionID:            meta.SessionID,
		queueID:              meta.QueueID,
		consumerToken:        meta.ConsumerToken,
		cursor:               meta.LastDeliveredSequence,
		ackedCursor:          meta.LastDeliveredSequence,
		connectedAt:          meta.ConnectedAt,
		leaseExpireAt:        meta.LeaseExpireAt,
		recoverUntil:         meta.RecoverUntil,
		lastDeliveredEventID: meta.LastDeliveredEventID,
		lastDeliveredAt:      meta.LastDeliveredAt,
		missingLiveSeqFirsts: map[int64]time.Time{},
	}
}

func parseLiveDeliverySequence(deliveryID string) (int64, error) {
	if !strings.HasPrefix(deliveryID, liveDeliveryPrefix) {
		return 0, ErrInvalidArgument
	}
	sequence, err := strconv.ParseInt(strings.TrimPrefix(deliveryID, liveDeliveryPrefix), 10, 64)
	if err != nil || sequence <= 0 {
		return 0, ErrInvalidArgument
	}
	return sequence, nil
}

func (s *StreamOut) normalizedLimit(limit int32) int32 {
	if limit <= 0 {
		return s.defaultLimit
	}
	if limit > s.maxLimit {
		return s.maxLimit
	}
	return limit
}

func (s *StreamOut) validateArgs(namespace string, sessionID string) error {
	if s.store == nil || s.queues == nil || s.idgen == nil {
		return ErrInvalidArgument
	}
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(sessionID) == "" {
		return ErrInvalidArgument
	}
	return nil
}
