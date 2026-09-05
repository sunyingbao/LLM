package streamout

import (
	"context"
	"errors"
	"strconv"
	"time"

	redisv6 "code.byted.org/kv/redis-v6"
	redisstore "eino-cli/deepagent/coordinator/internal/infra/store/redis"
	"eino-cli/deepagent/coordinator/internal/service/eventlog"
)

var errQueueStateNotFound = errors.New("stream queue state not found")

// liveEventStore is the StreamOut-owned storage seam. StreamOut reasons about
// ordered live events; Redis keys, sequence counters, primary reads and TTLs
// stay inside the adapter.
type liveEventStore interface {
	CurrentSequence(ctx context.Context, namespace string, sessionID string) (int64, error)
	Append(ctx context.Context, namespace string, sessionID string, event eventlog.Event, ttl time.Duration) (int64, error)
	LoadRange(ctx context.Context, namespace string, sessionID string, from int64, to int64) ([]*eventlog.Event, error)
}

// queueStateStore persists reconnectable subscription ownership and the
// acknowledged live sequence. The service owns lifecycle rules; Redis key
// layout and the session-to-queue index stay inside this adapter.
type queueStateStore interface {
	Load(ctx context.Context, queueID string) (*redisstore.StreamQueueMeta, error)
	SaveActive(ctx context.Context, meta redisstore.StreamQueueMeta) error
	SaveClosed(ctx context.Context, meta redisstore.StreamQueueMeta) error
}

type redisLiveEventStore struct {
	client redisstore.Client
}

type redisQueueStateStore struct {
	client redisstore.Client
}

func newRedisLiveEventStore(client redisstore.Client) liveEventStore {
	if client == nil {
		return nil
	}
	return &redisLiveEventStore{client: client}
}

func newRedisQueueStateStore(client redisstore.Client) queueStateStore {
	if client == nil {
		return nil
	}
	return &redisQueueStateStore{client: client}
}

func (s *redisQueueStateStore) Load(ctx context.Context, queueID string) (*redisstore.StreamQueueMeta, error) {
	var meta redisstore.StreamQueueMeta
	if err := s.client.StructGetPrimary(ctx, redisstore.StreamQueueMetaKey(queueID), &meta); err != nil {
		if errors.Is(err, redisv6.Nil) {
			return nil, errQueueStateNotFound
		}
		return nil, err
	}
	return &meta, nil
}

func (s *redisQueueStateStore) SaveActive(ctx context.Context, meta redisstore.StreamQueueMeta) error {
	if err := s.client.StructSet(ctx, redisstore.StreamQueueMetaKey(meta.QueueID), meta); err != nil {
		return err
	}
	return s.client.ZAdd(ctx, redisstore.SessionQueueSetKey(meta.Namespace, meta.SessionID), float64(meta.LeaseExpireAt.UnixMilli()), meta.QueueID)
}

func (s *redisQueueStateStore) SaveClosed(ctx context.Context, meta redisstore.StreamQueueMeta) error {
	if err := s.client.StructSet(ctx, redisstore.StreamQueueMetaKey(meta.QueueID), meta); err != nil {
		return err
	}
	_, err := s.client.ZRem(ctx, redisstore.SessionQueueSetKey(meta.Namespace, meta.SessionID), []interface{}{meta.QueueID})
	return err
}

func (s *redisLiveEventStore) CurrentSequence(ctx context.Context, namespace string, sessionID string) (int64, error) {
	return s.client.GetInt64Primary(ctx, redisstore.SessionLiveSequenceKey(namespace, sessionID))
}

func (s *redisLiveEventStore) Append(ctx context.Context, namespace string, sessionID string, event eventlog.Event, ttl time.Duration) (int64, error) {
	seq, err := s.client.Incr(ctx, redisstore.SessionLiveSequenceKey(namespace, sessionID))
	if err != nil {
		return 0, err
	}
	event.DeliveryID = liveDeliveryID(seq)
	if err := s.client.StructSetTTL(ctx, redisstore.SessionLiveEventKey(namespace, sessionID, seq), event, ttl); err != nil {
		return 0, err
	}
	return seq, nil
}

func (s *redisLiveEventStore) LoadRange(ctx context.Context, namespace string, sessionID string, from int64, to int64) ([]*eventlog.Event, error) {
	if to < from {
		return nil, nil
	}
	keys := make([]string, 0, to-from+1)
	for seq := from; seq <= to; seq++ {
		keys = append(keys, redisstore.SessionLiveEventKey(namespace, sessionID, seq))
	}
	var events []*eventlog.Event
	if err := s.client.StructMGetPrimary(ctx, keys, &events); err != nil {
		return nil, err
	}
	return events, nil
}

func liveDeliveryID(seq int64) string {
	return liveDeliveryPrefix + strconv.FormatInt(seq, 10)
}
