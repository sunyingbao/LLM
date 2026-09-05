package coordinator

import (
	"context"
	redisstore "eino-cli/deepagent/coordinator/internal/infra/store/redis"
	"eino-cli/deepagent/coordinator/internal/model"
	"eino-cli/deepagent/coordinator/internal/storage"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInputQueuePreservesOrderAndRejectsForeignOrMissingMessages(t *testing.T) {
	for _, scenario := range []string{"ordered", "foreign namespace", "foreign thread", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			client := newCoordinatorRedis()
			queue := storage.NewInputQueue(client)
			for _, id := range []int64{21, 22} {
				require.NoError(t, queue.Enqueue(ctx, "ns", &model.TMailboxMessage{MessageId: id, ThreadId: 1, Status: model.MessageStatusPending}, float64(id)))
			}
			switch scenario {
			case "foreign namespace":
				require.NoError(t, client.StructSet(ctx, redisstore.MessageKey(22), storage.CachedMessage("other", &model.TMailboxMessage{MessageId: 22, ThreadId: 1})))
			case "foreign thread":
				require.NoError(t, client.StructSet(ctx, redisstore.MessageKey(22), storage.CachedMessage("ns", &model.TMailboxMessage{MessageId: 22, ThreadId: 2})))
			case "missing":
				delete(client.values, redisstore.MessageKey(22))
			}
			inputs, err := queue.List(ctx, "ns", 1, 0, -1)
			if scenario == "ordered" {
				require.NoError(t, err)
				require.Len(t, inputs, 2)
				require.Equal(t, int64(21), inputs[0].MessageId)
				require.Equal(t, int64(22), inputs[1].MessageId)
			} else {
				require.Error(t, err)
				require.Nil(t, inputs, "invalid batch must not expose partial results")
				if scenario == "missing" {
					require.ErrorIs(t, err, storage.ErrMessageNotFound)
				} else {
					require.Contains(t, err.Error(), "ownership mismatch")
				}
			}
		})
	}
}

func TestInputFinalizationPreservesMetadataAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	queue := storage.NewInputQueue(newCoordinatorRedis())
	createdAt := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	message := &model.TMailboxMessage{MessageId: 21, ThreadId: 1, Status: model.MessageStatusPending, MetadataJson: `{"source":"user"}`, TriggerTurnId: "original", CreatedAt: createdAt}
	require.NoError(t, queue.Enqueue(ctx, "ns", message, 21))
	acked, err := queue.Finalize(ctx, "ns", 1, []int64{21}, model.MessageStatusAcked, "", createdAt.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, acked, 1)
	require.Equal(t, `{"source":"user"}`, acked[0].MetadataJson)
	require.Equal(t, "original", acked[0].TriggerTurnId)
	require.Equal(t, model.MessageStatusAcked, acked[0].Status)
	count, err := queue.Count(ctx, "ns", 1)
	require.NoError(t, err)
	require.Zero(t, count)
	retried, err := queue.Finalize(ctx, "ns", 1, []int64{21}, model.MessageStatusCanceled, "replacement", createdAt.Add(2*time.Minute))
	require.NoError(t, err)
	require.Equal(t, acked, retried, "already acknowledged messages must not be overwritten")
}
