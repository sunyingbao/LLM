package coordinator

import (
	"context"
	redisstore "eino-cli/deepagent/coordinator/internal/infra/store/redis"
	"eino-cli/deepagent/coordinator/internal/service/streamout"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type subscriptionQueue struct {
	mu            sync.Mutex
	opened        bool
	recovered     string
	acked         []int64
	ackedPayloads []string
	events        []Event
	openErr       error
	leaseErr      error
	peekErr       error
	ackErr        error
	closed        chan struct{}
}

func (q *subscriptionQueue) OpenSubscription(_ context.Context, _, _, queueID string) (meta *redisstore.StreamQueueMeta, err error) {
	q.recovered = queueID
	if queueID == "" {
		q.opened = true
		queueID = "queue"
	}
	return &redisstore.StreamQueueMeta{QueueID: queueID, ConsumerToken: "owner"}, q.openErr
}

func (q *subscriptionQueue) RenewQueueLease(context.Context, string, string) (meta *redisstore.StreamQueueMeta, err error) {
	return &redisstore.StreamQueueMeta{}, q.leaseErr
}

func (q *subscriptionQueue) PeekPendingEvents(context.Context, string, string, int32) (events []Event, err error) {
	return q.events, q.peekErr
}

func (q *subscriptionQueue) AckDeliveredEvents(ctx context.Context, _, _ string, events []Event) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, event := range events {
		q.acked = append(q.acked, event.EventID)
		q.ackedPayloads = append(q.ackedPayloads, string(event.Payload))
	}
	return q.ackErr
}

func (q *subscriptionQueue) CloseQueue(context.Context, string, string) (err error) {
	close(q.closed)
	return nil
}

func TestSubscriptionCancellationAcknowledgesOnlyDeliveredEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := &subscriptionQueue{events: []Event{{EventID: 1, Payload: []byte("one")}, {EventID: 2}}, closed: make(chan struct{})}
	sub := newSubscription(ctx, queue, SubscribeSessionRequest{Namespace: "ns", SessionID: "session"}, time.Second)
	frame, err := sub.Recv()
	require.NoError(t, err)
	require.Equal(t, "queue", frame.QueueID)
	frame, err = sub.Recv()
	require.NoError(t, err)
	require.Equal(t, int64(1), frame.Event.EventID)
	frame.Event.Payload[0] = 'x'
	cancel()
	select {
	case <-queue.closed:
	case <-time.After(time.Second):
		t.Fatal("canceled subscriber did not release its queue")
	}
	queue.mu.Lock()
	require.Equal(t, []int64{1}, queue.acked)
	require.Equal(t, []string{"one"}, queue.ackedPayloads)
	queue.mu.Unlock()
	_, err = sub.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func TestSubscriptionRecoveryStopsOnOwnershipLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	queue := &subscriptionQueue{leaseErr: streamout.ErrConsumerMismatch, closed: make(chan struct{})}
	sub := newSubscription(ctx, queue, SubscribeSessionRequest{Namespace: "ns", SessionID: "session", RecoverQueueID: "previous"}, time.Second)
	frame, err := sub.Recv()
	require.NoError(t, err)
	require.Equal(t, "previous", frame.QueueID)
	_, err = sub.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.False(t, queue.opened)
	require.Equal(t, "previous", queue.recovered)
	select {
	case <-queue.closed:
	default:
		t.Fatal("subscription did not clean up after losing ownership")
	}
}

func TestSubscriptionReturnsQueueFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	queueErr := errors.New("queue unavailable")
	queue := &subscriptionQueue{peekErr: queueErr, closed: make(chan struct{})}
	sub := newSubscription(ctx, queue, SubscribeSessionRequest{Namespace: "ns", SessionID: "session"}, time.Second)
	_, err := sub.Recv()
	require.NoError(t, err)
	_, err = sub.Recv()
	require.ErrorIs(t, err, queueErr)
	_, err = sub.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func TestSubscriptionDistinguishesOpenFailureFromLoopTermination(t *testing.T) {
	backendErr := errors.New("redis unavailable")
	for _, stage := range []string{"open", "renew", "peek", "ack"} {
		for _, failure := range []error{streamout.ErrQueueNotFound, streamout.ErrConsumerMismatch, backendErr} {
			t.Run(stage+"/"+failure.Error(), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				queue := &subscriptionQueue{closed: make(chan struct{})}
				switch stage {
				case "open":
					queue.openErr = failure
				case "renew":
					queue.leaseErr = failure
				case "peek":
					queue.peekErr = failure
				case "ack":
					queue.ackErr = failure
					queue.events = []Event{{EventID: 7}}
				}
				sub := newSubscription(ctx, queue, SubscribeSessionRequest{Namespace: "ns", SessionID: "session", RecoverQueueID: "previous"}, time.Second)
				if stage != "open" {
					frame, err := sub.Recv()
					require.NoError(t, err)
					require.Equal(t, "previous", frame.QueueID)
				}
				if stage == "ack" {
					frame, err := sub.Recv()
					require.NoError(t, err)
					require.Equal(t, int64(7), frame.Event.EventID)
				}
				_, err := sub.Recv()
				if stage == "open" || failure == backendErr {
					require.ErrorIs(t, err, failure)
				} else {
					require.ErrorIs(t, err, io.EOF)
				}
				if stage != "open" {
					select {
					case <-queue.closed:
					default:
						t.Fatal("queue not closed after loop termination")
					}
				}
			})
		}
	}
}

func TestClonedEventsDoNotShareMutableFields(t *testing.T) {
	flag := true
	source := []Event{{Payload: []byte("hello"), Metadata: map[string]string{"key": "value"}, PersistToEventLog: &flag, FanoutToSession: &flag}}
	cloned := cloneEvents(source)
	cloned[0].Payload[0] = 'x'
	cloned[0].Metadata["key"] = "changed"
	*cloned[0].PersistToEventLog = false
	*cloned[0].FanoutToSession = false
	require.Equal(t, "hello", string(source[0].Payload))
	require.Equal(t, "value", source[0].Metadata["key"])
	require.True(t, flag)
}
