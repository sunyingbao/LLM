package coordinator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInputDeliveryDoesNotCompleteThread(t *testing.T) {
	ctx := context.Background()
	service, _ := newCoordinatorForTest(t, &coordinatorIDGen{ids: []int64{101, 201}}, newCoordinatorRedis())
	created, err := service.CreateThread(ctx, CreateThreadRequest{Namespace: "ns1", Env: "test"})
	require.NoError(t, err)
	require.Equal(t, ThreadStatusIdle, created.Thread.Status)

	submitted, err := service.SubmitInput(ctx, SubmitInputRequest{
		Namespace: "ns1", ThreadID: created.Thread.ThreadID,
		SenderType: SenderTypeUser, MessageType: "input", Payload: []byte("hello"), WakeThread: true,
	})
	require.NoError(t, err)
	require.Equal(t, ThreadStatusReady, submitted.Thread.Status)

	claimed, err := service.ClaimThread(ctx, ClaimThreadRequest{Namespace: "ns1", ThreadID: created.Thread.ThreadID})
	require.NoError(t, err)
	require.Equal(t, ThreadStatusRunning, claimed.Thread.Status)
	require.Len(t, claimed.PendingMessages, 1)

	pending, err := service.ReadPendingInputs(ctx, ReadPendingInputsRequest{
		Namespace: "ns1", ThreadID: created.Thread.ThreadID, LeaseToken: claimed.Lease.LeaseToken,
	})
	require.NoError(t, err)
	require.Len(t, pending.Messages, 1, "reading input must not remove it")

	delivered, err := service.ConfirmInputDelivery(ctx, ConfirmInputDeliveryRequest{
		Namespace: "ns1", ThreadID: created.Thread.ThreadID, LeaseToken: claimed.Lease.LeaseToken,
		MessageIDs: []int64{submitted.Message.MessageID}, TriggerRunID: "run-1",
	})
	require.NoError(t, err)
	require.Len(t, delivered, 1)
	require.Equal(t, MessageStatusAcked, delivered[0].Status)
	require.Equal(t, "run-1", delivered[0].TriggerRunID)

	thread, err := service.GetThread(ctx, "ns1", created.Thread.ThreadID)
	require.NoError(t, err)
	require.Equal(t, ThreadStatusRunning, thread.Status, "delivery does not release the worker's execution claim")

	pending, err = service.ReadPendingInputs(ctx, ReadPendingInputsRequest{
		Namespace: "ns1", ThreadID: created.Thread.ThreadID, LeaseToken: claimed.Lease.LeaseToken,
	})
	require.NoError(t, err)
	require.Empty(t, pending.Messages)

	released, err := service.ReleaseThread(ctx, ReleaseThreadRequest{
		Namespace: "ns1", ThreadID: created.Thread.ThreadID, LeaseToken: claimed.Lease.LeaseToken,
	})
	require.NoError(t, err)
	require.Equal(t, ThreadStatusIdle, released.Status)
}
