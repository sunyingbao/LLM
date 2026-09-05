package coordinator

import (
	"context"
	"eino-cli/deepagent/coordinator/internal/model"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type beforeMessageID struct {
	before func()
}

func (g *beforeMessageID) NextID(context.Context) (id int64, err error) {
	g.before()
	return 901, nil
}

func TestSendMessageWakesThreadReleasedBeforeEnqueue(t *testing.T) {
	svc, db, _ := newTestService(t)
	insertThread(t, db, &model.TThread{ThreadId: 75, Namespace: "ns1", Status: model.ThreadStatusRunning})
	svc.idgen = &beforeMessageID{before: func() {
		require.NoError(t, db.Model(&model.TThread{}).Where("thread_id = ?", 75).Update("status", model.ThreadStatusIdle).Error)
	}}
	message, _, err := testSubmitInput(svc.SubmitInput(context.Background(), SubmitInputRequest{Namespace: "ns1", ThreadID: 75, SenderType: SenderType(model.SenderTypeUser), SenderID: "u1", MessageType: "text", Payload: []byte("hi"), Metadata: nil, WakeThread: true}))
	require.NoError(t, err)
	var thread model.TThread
	require.NoError(t, db.Where("thread_id = ?", 75).First(&thread).Error)
	require.Equal(t, model.ThreadStatusReady, thread.Status)
	pending, err := svc.loadPendingInputs(context.Background(), "ns1", 75, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, message.MessageId, pending[0].MessageId)
}

// enqueueTestMessage 直接构造并入队一条 pending 消息，模拟 submitInput 在唤醒
// CAS 之前已完成的入队动作。
func enqueueTestMessage(t *testing.T, svc *Coordinator, namespace string, threadID int64) *model.TMailboxMessage {
	t.Helper()
	message, err := svc.newInput(context.Background(), threadID, model.SenderTypeUser, "u1", "text", []byte("hi"), nil)
	require.NoError(t, err)
	require.NoError(t, svc.enqueueInput(context.Background(), namespace, message))
	return message
}

func TestHandleWakeConflictKeepsMessageWhenConcurrentlyWoken(t *testing.T) {
	svc, db, _ := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	// 线程已被并发的另一次唤醒置为 ready：本次唤醒 CAS 未命中，但消息必须保留。
	insertThread(t, db, &model.TThread{
		ThreadId:  71,
		Namespace: "ns1",
		Status:    model.ThreadStatusReady,
		ReadyAt:   now,
		CreatedAt: now,
		UpdatedAt: now,
	})
	message := enqueueTestMessage(t, svc, "ns1", 71)

	thread, err := svc.handleWakeConflict(context.Background(), "ns1", 71, message.MessageId)
	require.NoError(t, err)
	require.Equal(t, model.ThreadStatusReady, thread.Status)

	hasPending, err := svc.hasPendingInputs(context.Background(), "ns1", 71)
	require.NoError(t, err)
	require.True(t, hasPending, "message must stay pending after wake conflict")
}

func TestHandleWakeConflictRemovesMessageWhenThreadClosed(t *testing.T) {
	svc, db, _ := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	insertThread(t, db, &model.TThread{
		ThreadId:  72,
		Namespace: "ns1",
		Status:    model.ThreadStatusClosed,
		ClosedAt:  now,
		CreatedAt: now,
		UpdatedAt: now,
	})
	message := enqueueTestMessage(t, svc, "ns1", 72)

	_, err := svc.handleWakeConflict(context.Background(), "ns1", 72, message.MessageId)
	require.True(t, errors.Is(err, ErrThreadClosed), "err = %v, want ErrThreadClosed", err)

	hasPending, err := svc.hasPendingInputs(context.Background(), "ns1", 72)
	require.NoError(t, err)
	require.False(t, hasPending, "message must be withdrawn for closed thread")
}

func TestSendMessageWakePullsBackoffReadyAtForward(t *testing.T) {
	svc, db, _ := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	// 线程处于失败退避期：ready 且 ready_at 在未来。新输入应把 ready_at 拉回 now。
	insertThread(t, db, &model.TThread{
		ThreadId:  73,
		Namespace: "ns1",
		Status:    model.ThreadStatusReady,
		ReadyAt:   now.Add(25 * time.Second),
		CreatedAt: now,
		UpdatedAt: now,
	})

	_, _, err := testSubmitInput(svc.SubmitInput(context.Background(), SubmitInputRequest{Namespace: "ns1", ThreadID: 73, SenderType: SenderType(model.SenderTypeUser), SenderID: "u1", MessageType: "text", Payload: []byte("hi"), Metadata: nil, WakeThread: true}))
	require.NoError(t, err)

	var thread model.TThread
	require.NoError(t, db.Where("thread_id = ?", 73).First(&thread).Error)
	require.Equal(t, now.UTC(), thread.ReadyAt.UTC(), "new input should clear failure backoff")
}

func TestSendMessageWithoutWakeKeepsBackoffReadyAt(t *testing.T) {
	svc, db, _ := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	future := now.Add(25 * time.Second)
	insertThread(t, db, &model.TThread{
		ThreadId:  74,
		Namespace: "ns1",
		Status:    model.ThreadStatusReady,
		ReadyAt:   future,
		CreatedAt: now,
		UpdatedAt: now,
	})

	_, _, err := testSubmitInput(svc.SubmitInput(context.Background(), SubmitInputRequest{Namespace: "ns1", ThreadID: 74, SenderType: SenderType(model.SenderTypeUser), SenderID: "u1", MessageType: "text", Payload: []byte("hi"), Metadata: nil, WakeThread: false}))
	require.NoError(t, err)

	var thread model.TThread
	require.NoError(t, db.Where("thread_id = ?", 74).First(&thread).Error)
	require.Equal(t, future.UTC(), thread.ReadyAt.UTC(), "wake=false should not touch ready_at")
}
