package coordinator

import (
	"context"
	redisstore "eino-cli/deepagent/coordinator/internal/infra/store/redis"
	"eino-cli/deepagent/coordinator/internal/model"
	"eino-cli/deepagent/coordinator/internal/storage"
	"eino-cli/deepagent/coordinator/internal/util"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	redisv6 "code.byted.org/kv/redis-v6"
	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeRedis struct {
	mu    sync.Mutex
	kv    map[string]string
	zsets map[string]map[string]float64
	seqs  map[string]int64
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		kv:    map[string]string{},
		zsets: map[string]map[string]float64{},
		seqs:  map[string]int64{},
	}
}

func (f *fakeRedis) StructGet(_ context.Context, key string, v interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	raw, ok := f.kv[key]
	if !ok {
		return errors.New("not found")
	}
	return sonic.UnmarshalString(raw, v)
}

func (f *fakeRedis) StructGetPrimary(ctx context.Context, key string, v interface{}) error {
	return f.StructGet(ctx, key, v)
}

func (f *fakeRedis) StructMGet(_ context.Context, keys []string, values interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	raws := make([]string, 0, len(keys))
	for _, key := range keys {
		raw, ok := f.kv[key]
		if !ok {
			raws = append(raws, "null")
			continue
		}
		raws = append(raws, raw)
	}
	return sonic.UnmarshalString("["+strings.Join(raws, ",")+"]", values)
}

func (f *fakeRedis) StructMGetPrimary(ctx context.Context, keys []string, values interface{}) error {
	return f.StructMGet(ctx, keys, values)
}

func (f *fakeRedis) StructSet(_ context.Context, key string, v interface{}) error {
	raw, err := sonic.Marshal(v)
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.kv[key] = string(raw)
	return nil
}

func (f *fakeRedis) StructSetTTL(ctx context.Context, key string, v interface{}, _ time.Duration) error {
	return f.StructSet(ctx, key, v)
}

func (f *fakeRedis) StructMSet(ctx context.Context, keys []string, values []interface{}) error {
	if len(keys) != len(values) {
		return fmt.Errorf("length mismatch")
	}
	for idx, key := range keys {
		if err := f.StructSet(ctx, key, values[idx]); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeRedis) GetInt64Primary(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seqs[key], nil
}

func (f *fakeRedis) Incr(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.seqs[key]++
	return f.seqs[key], nil
}

func (f *fakeRedis) ZAdd(_ context.Context, key string, score float64, member string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.zsets[key]; !ok {
		f.zsets[key] = map[string]float64{}
	}
	f.zsets[key][member] = score
	return nil
}

func (f *fakeRedis) ZRange(_ context.Context, key string, start int64, stop int64) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	pairs := sortedPairs(f.zsets[key])
	if len(pairs) == 0 {
		return nil, nil
	}
	if start < 0 {
		start = 0
	}
	if stop < 0 || stop >= int64(len(pairs)) {
		stop = int64(len(pairs) - 1)
	}
	if start > stop || start >= int64(len(pairs)) {
		return nil, nil
	}
	result := make([]string, 0, stop-start+1)
	for idx := start; idx <= stop; idx++ {
		result = append(result, pairs[idx].member)
	}
	return result, nil
}

func (f *fakeRedis) ZRangePrimary(ctx context.Context, key string, start int64, stop int64) ([]string, error) {
	return f.ZRange(ctx, key, start, stop)
}

func (f *fakeRedis) ZRangeByScore(_ context.Context, key string, _ redisv6.ZRangeBy) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	pairs := sortedPairs(f.zsets[key])
	result := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		result = append(result, pair.member)
	}
	return result, nil
}

func (f *fakeRedis) ZRangeByScorePrimary(ctx context.Context, key string, rangeOpt redisv6.ZRangeBy) ([]string, error) {
	return f.ZRangeByScore(ctx, key, rangeOpt)
}

func (f *fakeRedis) ZCard(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return int64(len(f.zsets[key])), nil
}

func (f *fakeRedis) ZCardPrimary(ctx context.Context, key string) (int64, error) {
	return f.ZCard(ctx, key)
}

func (f *fakeRedis) ZScore(_ context.Context, key string, member string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	score, ok := f.zsets[key][member]
	if !ok {
		return 0, errors.New("not found")
	}
	return score, nil
}

func (f *fakeRedis) ZScorePrimary(ctx context.Context, key string, member string) (float64, error) {
	return f.ZScore(ctx, key, member)
}

func (f *fakeRedis) ZRem(_ context.Context, key string, members []interface{}) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var removed int64
	for _, member := range members {
		memberStr := fmt.Sprint(member)
		if _, ok := f.zsets[key][memberStr]; ok {
			delete(f.zsets[key], memberStr)
			removed++
		}
	}
	return removed, nil
}

func (f *fakeRedis) Del(_ context.Context, keys ...string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var removed int64
	for _, key := range keys {
		if _, ok := f.kv[key]; ok {
			delete(f.kv, key)
			removed++
		}
		if _, ok := f.zsets[key]; ok {
			delete(f.zsets, key)
			removed++
		}
		if _, ok := f.seqs[key]; ok {
			delete(f.seqs, key)
			removed++
		}
	}
	return removed, nil
}

type pair struct {
	member string
	score  float64
}

func sortedPairs(values map[string]float64) []pair {
	pairs := make([]pair, 0, len(values))
	for member, score := range values {
		pairs = append(pairs, pair{member: member, score: score})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].score == pairs[j].score {
			return pairs[i].member < pairs[j].member
		}
		return pairs[i].score < pairs[j].score
	})
	return pairs
}

type stubIDGenerator struct {
	next int64
}

func (g *stubIDGenerator) NextID(context.Context) (int64, error) {
	g.next++
	return g.next, nil
}

func messageIDs(messages []*model.TMailboxMessage) []int64 {
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.MessageId)
	}
	return ids
}

func newTestService(t *testing.T) (*Coordinator, *gorm.DB, *fakeRedis) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TThread{}, &model.TMailboxMessage{}))

	fr := newFakeRedis()
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	idgen := &stubIDGenerator{next: 100}
	svc := newTestCoordinator(db, db, fr, idgen, WithClock(func() time.Time { return now }))
	return svc, db, fr
}

func insertThread(t *testing.T, db *gorm.DB, thread *model.TThread) {
	t.Helper()
	require.NoError(t, db.Create(thread).Error)
}

func insertMessage(t *testing.T, db *gorm.DB, message *model.TMailboxMessage) {
	t.Helper()
	require.NoError(t, db.Create(message).Error)
}

func TestSendMessageTransitionsIdleThreadAndWritesHotstore(t *testing.T) {
	svc, db, fr := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)

	insertThread(t, db, &model.TThread{
		ThreadId:     1,
		Namespace:    "ns1",
		Env:          "ppe_a",
		Title:        "t1",
		Status:       model.ThreadStatusIdle,
		MetadataJson: `{"role":"main","logid":"old-logid","byted_ctx_meta_info":"{\"old\":\"value\"}","K_ENV":"old_env"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
		CreatedBy:    "tester",
	})

	message, thread, err := testSubmitInput(svc.SubmitInput(context.Background(), SubmitInputRequest{Namespace: "ns1", ThreadID: 1, SenderType: SenderType(model.SenderTypeUser), SenderID: "u1", MessageType: "input", Payload: []byte("hello"), Metadata: map[string]string{
		"k":                               "v",
		"logid":                           "wake-logid",
		model.MetadataKeyBytedCtxMetaInfo: `{"persist":"value"}`,
		model.MetadataKeyKEnv:             "wake_env",
	}, WakeThread: true}))
	require.NoError(t, err)
	require.NotNil(t, message)
	require.NotNil(t, thread)
	require.Equal(t, model.MessageStatusPending, message.Status)
	require.Equal(t, model.ThreadStatusReady, thread.Status)
	require.False(t, thread.ReadyAt.IsZero())

	var persisted model.TThread
	require.NoError(t, db.First(&persisted, "thread_id = ?", 1).Error)
	require.Equal(t, model.ThreadStatusReady, persisted.Status)
	require.False(t, persisted.ReadyAt.IsZero())
	persistedMetadata, err := util.ToStruct[map[string]string](persisted.MetadataJson)
	require.NoError(t, err)
	require.Equal(t, "main", (*persistedMetadata)["role"])
	require.Equal(t, "wake-logid", (*persistedMetadata)["logid"])
	require.Equal(t, `{"persist":"value"}`, (*persistedMetadata)[model.MetadataKeyBytedCtxMetaInfo])
	require.Equal(t, "wake_env", (*persistedMetadata)[model.MetadataKeyKEnv])
	require.NotContains(t, *persistedMetadata, "k")

	var handledAtNull int
	require.NoError(t, db.Raw("select handled_at is null from t_mailbox_message where message_id = ?", message.MessageId).
		Row().Scan(&handledAtNull))
	require.Equal(t, 1, handledAtNull)

	var cached redisstore.CachedMessage
	require.NoError(t, fr.StructGet(context.Background(), redisstore.MessageKey(101), &cached))
	require.Equal(t, int64(101), cached.MessageID)
	require.Equal(t, model.MessageStatusPending, cached.Status)
	require.Equal(t, "hello", cached.Payload)

	_, ok := fr.zsets[redisstore.PendingInputKey("ns1", 1)]["101"]
	require.True(t, ok)

	message, thread, err = testSubmitInput(svc.SubmitInput(context.Background(), SubmitInputRequest{Namespace: "ns1", ThreadID: 1, SenderType: SenderType(model.SenderTypeUser), SenderID: "u1", MessageType: "input", Payload: []byte("wrong env"), Metadata: nil, WakeThread: true}))
	require.NoError(t, err)
	require.NotNil(t, message)
	require.Equal(t, model.ThreadStatusReady, thread.Status)
}

func TestThreadMetadataWithActivationClearsStaleActivationContext(t *testing.T) {
	got := threadMetadataWithActivation(
		`{"role":"main","logid":"old-logid","byted_ctx_meta_info":"{\"old\":\"value\"}","K_ENV":"old_env"}`,
		map[string]string{},
	)

	metadata, err := util.ToStruct[map[string]string](got)
	require.NoError(t, err)
	require.Equal(t, "main", (*metadata)["role"])
	require.NotContains(t, *metadata, "logid")
	require.NotContains(t, *metadata, model.MetadataKeyBytedCtxMetaInfo)
	require.NotContains(t, *metadata, model.MetadataKeyKEnv)
}

func TestSendMessageDoesNotWakeBlockedThread(t *testing.T) {
	svc, db, _ := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)

	insertThread(t, db, &model.TThread{
		ThreadId:  2,
		Namespace: "ns1",
		Env:       "ppe_a",
		Title:     "t2",
		Status:    model.ThreadStatusBlocked,
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: "tester",
	})

	message, thread, err := testSubmitInput(svc.SubmitInput(context.Background(), SubmitInputRequest{Namespace: "ns1", ThreadID: 2, SenderType: SenderType(model.SenderTypeSystem), SenderID: "sys", MessageType: "wake", Payload: []byte("go"), Metadata: nil, WakeThread: true}))
	require.NoError(t, err)
	require.NotNil(t, message)
	require.Equal(t, model.ThreadStatusBlocked, thread.Status)
	require.True(t, thread.ReadyAt.IsZero())

	var persisted model.TThread
	require.NoError(t, db.First(&persisted, "thread_id = ?", 2).Error)
	require.Equal(t, model.ThreadStatusBlocked, persisted.Status)
	require.True(t, persisted.ReadyAt.IsZero())
}

func TestEnqueuePendingMessageFrontOrdersBeforeNormalMessages(t *testing.T) {
	svc, db, fr := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	insertThread(t, db, &model.TThread{
		ThreadId:  7,
		Namespace: "ns1",
		Env:       "ppe_a",
		Title:     "front insert stats",
		Status:    model.ThreadStatusReady,
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: "tester",
	})
	messages := []*model.TMailboxMessage{
		{
			MessageId:    201,
			ThreadId:     7,
			SenderType:   model.SenderTypeUser,
			SenderId:     "u1",
			MessageType:  "text",
			Status:       model.MessageStatusPending,
			Payload:      "normal-one",
			MetadataJson: "{}",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			MessageId:    202,
			ThreadId:     7,
			SenderType:   model.SenderTypeUser,
			SenderId:     "u1",
			MessageType:  "text",
			Status:       model.MessageStatusPending,
			Payload:      "normal-two",
			MetadataJson: "{}",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			MessageId:    203,
			ThreadId:     7,
			SenderType:   model.SenderTypeSystem,
			SenderId:     "approval",
			MessageType:  "text",
			Status:       model.MessageStatusPending,
			Payload:      "resume",
			MetadataJson: "{}",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}

	require.NoError(t, svc.enqueueInput(context.Background(), "ns1", messages[0]))
	require.NoError(t, svc.enqueueInput(context.Background(), "ns1", messages[1]))
	require.NoError(t, svc.enqueueInputFirst(context.Background(), "ns1", messages[2]))

	loaded, err := svc.loadPendingInputs(context.Background(), "ns1", 7, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{203, 201, 202}, messageIDs(loaded))

	score, err := fr.ZScore(context.Background(), redisstore.PendingInputKey("ns1", 7), "203")
	require.NoError(t, err)
	require.Less(t, score, float64(0))

}

func TestCancelInputCancelsPendingAndEnqueuesControlFront(t *testing.T) {
	svc, db, fr := newTestService(t)
	svc.idgen = &stubIDGenerator{next: 4000}
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)

	insertThread(t, db, &model.TThread{
		ThreadId:     31,
		Namespace:    "ns1",
		Env:          "ppe_a",
		Status:       model.ThreadStatusIdle,
		MetadataJson: `{"role":"main","logid":"old-logid","byted_ctx_meta_info":"{\"old\":\"value\"}","K_ENV":"old_env"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	for _, msg := range []*model.TMailboxMessage{
		{
			MessageId:    3101,
			ThreadId:     31,
			SenderType:   model.SenderTypeUser,
			SenderId:     "u1",
			MessageType:  "input",
			Status:       model.MessageStatusPending,
			Payload:      "one",
			MetadataJson: "{}",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			MessageId:    3102,
			ThreadId:     31,
			SenderType:   model.SenderTypeUser,
			SenderId:     "u1",
			MessageType:  "input",
			Status:       model.MessageStatusPending,
			Payload:      "two",
			MetadataJson: "{}",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			MessageId:    3103,
			ThreadId:     31,
			SenderType:   model.SenderTypeUser,
			SenderId:     "u1",
			MessageType:  "input",
			Status:       model.MessageStatusPending,
			Payload:      "three",
			MetadataJson: "{}",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			MessageId:    3104,
			ThreadId:     31,
			SenderType:   model.SenderTypeSystem,
			SenderId:     model.AgentCoordinatorSenderID,
			MessageType:  model.MessageTypeControlCancelInput,
			Status:       model.MessageStatusPending,
			Payload:      "{}",
			MetadataJson: "{}",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	} {
		insertMessage(t, db, msg)
		require.NoError(t, fr.StructSet(context.Background(), redisstore.MessageKey(msg.MessageId), storage.CachedMessage("ns1", msg)))
		score := float64(msg.MessageId)
		if model.IsControlMessageType(msg.MessageType) {
			score = -score
		}
		require.NoError(t, fr.ZAdd(context.Background(), redisstore.PendingInputKey("ns1", 31), score, strconv.FormatInt(msg.MessageId, 10)))
	}

	cutoff := int64(3102)
	result, err := testRequestInputCancel(svc.RequestInputCancel(context.Background(), RequestInputCancelRequest{Namespace: "ns1", ThreadID: 31, CutoffMessageID: &cutoff, Reason: "user_cancel", Metadata: map[string]string{
		"logid":                           "log-1",
		model.MetadataKeyBytedCtxMetaInfo: `{"persist":"value"}`,
		model.MetadataKeyKEnv:             "cancel_env",
	}}))
	require.NoError(t, err)
	require.Equal(t, cutoff, result.CutoffMessageID)
	require.Equal(t, model.ThreadStatusReady, result.Thread.Status)
	var threadMetadata *map[string]string
	threadMetadata, err = util.ToStruct[map[string]string](result.Thread.MetadataJson)
	require.NoError(t, err)
	require.Equal(t, "log-1", (*threadMetadata)["logid"])
	require.Equal(t, `{"persist":"value"}`, (*threadMetadata)[model.MetadataKeyBytedCtxMetaInfo])
	require.Equal(t, "cancel_env", (*threadMetadata)[model.MetadataKeyKEnv])
	require.Equal(t, []int64{3101, 3102}, result.CancelledMessageIDs)
	require.NotNil(t, result.ControlMessage)
	require.Equal(t, int64(4001), result.ControlMessage.MessageId)
	require.Equal(t, model.MessageTypeControlCancelInput, result.ControlMessage.MessageType)

	pending, err := svc.loadPendingInputs(context.Background(), "ns1", 31, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{4001, 3104, 3103}, messageIDs(pending))

	for _, id := range []int64{3101, 3102} {
		var cached redisstore.CachedMessage
		require.NoError(t, fr.StructGet(context.Background(), redisstore.MessageKey(id), &cached))
		require.Equal(t, model.MessageStatusCanceled, cached.Status)
		require.NotNil(t, cached.HandledAt)
		_, ok := fr.zsets[redisstore.PendingInputKey("ns1", 31)][strconv.FormatInt(id, 10)]
		require.False(t, ok)
		var persisted model.TMailboxMessage
		require.NoError(t, db.First(&persisted, "message_id = ?", id).Error)
		require.Equal(t, model.MessageStatusCanceled, persisted.Status)
		require.False(t, persisted.HandledAt.IsZero())
	}

	var payload CancelInputControlPayload
	require.NoError(t, sonic.UnmarshalString(result.ControlMessage.Payload, &payload))
	require.Equal(t, model.ControlTypeCancelInput, payload.ControlType)
	require.Equal(t, "4001", payload.RequestID)
	require.Equal(t, int64(31), payload.ThreadID)
	require.Equal(t, cutoff, payload.CutoffMessageID)
	require.Equal(t, "user_cancel", payload.Reason)

	var controlMetadata CancelInputControlMetadata
	require.NoError(t, sonic.UnmarshalString(result.ControlMessage.MetadataJson, &controlMetadata))
	require.Equal(t, "log-1", controlMetadata.LogID)
	require.Equal(t, "3102", controlMetadata.CutoffMessageID)
	require.Equal(t, `{"persist":"value"}`, controlMetadata.BytedCtxMetaInfo)
	require.Equal(t, "cancel_env", controlMetadata.KEnv)
}

func TestCancelInputNoopWhenNoOrdinaryInput(t *testing.T) {
	svc, db, _ := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)

	insertThread(t, db, &model.TThread{
		ThreadId:  32,
		Namespace: "ns1",
		Env:       "ppe_a",
		Status:    model.ThreadStatusIdle,
		CreatedAt: now,
		UpdatedAt: now,
	})

	result, err := testRequestInputCancel(svc.RequestInputCancel(context.Background(), RequestInputCancelRequest{Namespace: "ns1", ThreadID: 32, CutoffMessageID: nil, Reason: "", Metadata: nil}))
	require.NoError(t, err)
	require.Zero(t, result.CutoffMessageID)
	require.Nil(t, result.ControlMessage)
	require.Empty(t, result.CancelledMessageIDs)
}

func TestCancelInputRejectsControlCutoffAndBlockedThread(t *testing.T) {
	svc, db, fr := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)

	insertThread(t, db, &model.TThread{
		ThreadId:  33,
		Namespace: "ns1",
		Env:       "ppe_a",
		Status:    model.ThreadStatusReady,
		CreatedAt: now,
		UpdatedAt: now,
	})
	control := &model.TMailboxMessage{
		MessageId:    3301,
		ThreadId:     33,
		SenderType:   model.SenderTypeSystem,
		SenderId:     model.AgentCoordinatorSenderID,
		MessageType:  model.MessageTypeControlCancelInput,
		Status:       model.MessageStatusPending,
		Payload:      "{}",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	insertMessage(t, db, control)
	require.NoError(t, fr.StructSet(context.Background(), redisstore.MessageKey(control.MessageId), storage.CachedMessage("ns1", control)))

	cutoff := int64(3301)
	_, err := testRequestInputCancel(svc.RequestInputCancel(context.Background(), RequestInputCancelRequest{Namespace: "ns1", ThreadID: 33, CutoffMessageID: &cutoff, Reason: "", Metadata: nil}))
	require.ErrorIs(t, err, ErrInvalidCancel)

	insertThread(t, db, &model.TThread{
		ThreadId:  34,
		Namespace: "ns1",
		Env:       "ppe_a",
		Status:    model.ThreadStatusBlocked,
		CreatedAt: now,
		UpdatedAt: now,
	})
	_, err = testRequestInputCancel(svc.RequestInputCancel(context.Background(), RequestInputCancelRequest{Namespace: "ns1", ThreadID: 34, CutoffMessageID: nil, Reason: "", Metadata: nil}))
	require.ErrorIs(t, err, ErrThreadBlocked)
}

func TestCancelInputIdleWakeRaceRejectsBlockedThread(t *testing.T) {
	svc, db, _ := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	staleThread := &model.TThread{
		ThreadId:     35,
		Namespace:    "ns1",
		Env:          "ppe_a",
		Status:       model.ThreadStatusIdle,
		MetadataJson: `{"role":"main","K_ENV":"old_env"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	insertThread(t, db, staleThread)
	require.NoError(t, db.Model(&model.TThread{}).Where("thread_id = ?", int64(35)).Updates(map[string]interface{}{
		"status":        model.ThreadStatusBlocked,
		"metadata_json": `{"role":"main","K_ENV":"blocked_env"}`,
	}).Error)

	_, err := svc.markIdleThreadReadyWithActivation(context.Background(), "ns1", staleThread, "cancel input", map[string]string{
		model.MetadataKeyKEnv: "cancel_env",
	})
	require.ErrorIs(t, err, ErrThreadBlocked)

	var persisted model.TThread
	require.NoError(t, db.First(&persisted, "thread_id = ?", int64(35)).Error)
	require.Equal(t, model.ThreadStatusBlocked, persisted.Status)
	require.JSONEq(t, `{"role":"main","K_ENV":"blocked_env"}`, persisted.MetadataJson)
}

func TestCancelInputStaleIdleReadRejectsBlockedBeforeMailboxMutation(t *testing.T) {
	writeDSN := fmt.Sprintf("file:%s_write?mode=memory&cache=shared", t.Name())
	writeDB, err := gorm.Open(sqlite.Open(writeDSN), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, writeDB.AutoMigrate(&model.TThread{}, &model.TMailboxMessage{}))
	readDSN := fmt.Sprintf("file:%s_read?mode=memory&cache=shared", t.Name())
	readDB, err := gorm.Open(sqlite.Open(readDSN), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, readDB.AutoMigrate(&model.TThread{}, &model.TMailboxMessage{}))

	fr := newFakeRedis()
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	svc := newTestCoordinator(writeDB, readDB, fr, &stubIDGenerator{next: 5300}, WithClock(func() time.Time { return now }))
	staleThread := &model.TThread{
		ThreadId:     36,
		Namespace:    "ns1",
		Env:          "ppe_a",
		Status:       model.ThreadStatusIdle,
		MetadataJson: `{"K_ENV":"stale_env"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	currentThread := *staleThread
	currentThread.Status = model.ThreadStatusBlocked
	currentThread.MetadataJson = `{"K_ENV":"blocked_env"}`
	insertThread(t, readDB, staleThread)
	insertThread(t, writeDB, &currentThread)
	message := &model.TMailboxMessage{
		MessageId:    3601,
		ThreadId:     36,
		SenderType:   model.SenderTypeUser,
		SenderId:     "u1",
		MessageType:  "input",
		Status:       model.MessageStatusPending,
		Payload:      "one",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	insertMessage(t, writeDB, message)
	require.NoError(t, fr.StructSet(context.Background(), redisstore.MessageKey(message.MessageId), storage.CachedMessage("ns1", message)))
	require.NoError(t, fr.ZAdd(context.Background(), redisstore.PendingInputKey("ns1", 36), float64(message.MessageId), strconv.FormatInt(message.MessageId, 10)))

	_, err = testRequestInputCancel(svc.RequestInputCancel(context.Background(), RequestInputCancelRequest{Namespace: "ns1", ThreadID: 36, CutoffMessageID: nil, Reason: "user_cancel", Metadata: map[string]string{
		model.MetadataKeyKEnv: "cancel_env",
	}}))
	require.ErrorIs(t, err, ErrThreadBlocked)

	var cached redisstore.CachedMessage
	require.NoError(t, fr.StructGet(context.Background(), redisstore.MessageKey(message.MessageId), &cached))
	require.Equal(t, model.MessageStatusPending, cached.Status)
	require.Len(t, fr.zsets[redisstore.PendingInputKey("ns1", 36)], 1)
	require.NotContains(t, fr.kv, redisstore.MessageKey(5301))

	var persisted model.TMailboxMessage
	require.NoError(t, writeDB.First(&persisted, "message_id = ?", message.MessageId).Error)
	require.Equal(t, model.MessageStatusPending, persisted.Status)
}

func TestCancelInputUsesWriteStatusToWakeCurrentIdleThread(t *testing.T) {
	writeDSN := fmt.Sprintf("file:%s_write?mode=memory&cache=shared", t.Name())
	writeDB, err := gorm.Open(sqlite.Open(writeDSN), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, writeDB.AutoMigrate(&model.TThread{}, &model.TMailboxMessage{}))
	readDSN := fmt.Sprintf("file:%s_read?mode=memory&cache=shared", t.Name())
	readDB, err := gorm.Open(sqlite.Open(readDSN), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, readDB.AutoMigrate(&model.TThread{}, &model.TMailboxMessage{}))

	fr := newFakeRedis()
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	svc := newTestCoordinator(writeDB, readDB, fr, &stubIDGenerator{next: 5500}, WithClock(func() time.Time { return now }))
	staleThread := &model.TThread{
		ThreadId:     37,
		Namespace:    "ns1",
		Env:          "ppe_a",
		Status:       model.ThreadStatusReady,
		MetadataJson: `{"K_ENV":"stale_env"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	currentThread := *staleThread
	currentThread.Status = model.ThreadStatusIdle
	currentThread.MetadataJson = `{"K_ENV":"idle_env"}`
	insertThread(t, readDB, staleThread)
	insertThread(t, writeDB, &currentThread)
	message := &model.TMailboxMessage{
		MessageId:    3701,
		ThreadId:     37,
		SenderType:   model.SenderTypeUser,
		SenderId:     "u1",
		MessageType:  "input",
		Status:       model.MessageStatusPending,
		Payload:      "one",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	insertMessage(t, writeDB, message)
	require.NoError(t, fr.StructSet(context.Background(), redisstore.MessageKey(message.MessageId), storage.CachedMessage("ns1", message)))
	require.NoError(t, fr.ZAdd(context.Background(), redisstore.PendingInputKey("ns1", 37), float64(message.MessageId), strconv.FormatInt(message.MessageId, 10)))

	result, err := testRequestInputCancel(svc.RequestInputCancel(context.Background(), RequestInputCancelRequest{Namespace: "ns1", ThreadID: 37, CutoffMessageID: nil, Reason: "user_cancel", Metadata: map[string]string{
		model.MetadataKeyKEnv: "cancel_env",
	}}))
	require.NoError(t, err)
	require.Equal(t, model.ThreadStatusReady, result.Thread.Status)
	require.NotNil(t, result.ControlMessage)

	var persisted model.TThread
	require.NoError(t, writeDB.First(&persisted, "thread_id = ?", int64(37)).Error)
	require.Equal(t, model.ThreadStatusReady, persisted.Status)
	require.JSONEq(t, `{"K_ENV":"cancel_env"}`, persisted.MetadataJson)
}

func TestCancelInputRetriesWakeWithoutOverwritingMetadata(t *testing.T) {
	svc, db, fr := newTestService(t)
	svc.idgen = &stubIDGenerator{next: 5600}
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	insertThread(t, db, &model.TThread{
		ThreadId:     38,
		Namespace:    "ns1",
		Env:          "ppe_a",
		Status:       model.ThreadStatusIdle,
		MetadataJson: `{"K_ENV":"original_env"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	message := &model.TMailboxMessage{
		MessageId:    3801,
		ThreadId:     38,
		SenderType:   model.SenderTypeUser,
		SenderId:     "u1",
		MessageType:  "input",
		Status:       model.MessageStatusPending,
		Payload:      "one",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	insertMessage(t, db, message)
	require.NoError(t, fr.StructSet(context.Background(), redisstore.MessageKey(message.MessageId), storage.CachedMessage("ns1", message)))
	require.NoError(t, fr.ZAdd(context.Background(), redisstore.PendingInputKey("ns1", 38), float64(message.MessageId), strconv.FormatInt(message.MessageId, 10)))

	wakeAttempts := 0
	require.NoError(t, db.Callback().Raw().Before("gorm:raw").Register("test:fail_first_cancel_wake", func(tx *gorm.DB) {
		if !strings.Contains(tx.Statement.SQL.String(), "update t_thread set status =") {
			return
		}
		wakeAttempts++
		if wakeAttempts == 1 {
			tx.AddError(errors.New("transient wake failure"))
		}
	}))

	result, err := testRequestInputCancel(svc.RequestInputCancel(context.Background(), RequestInputCancelRequest{Namespace: "ns1", ThreadID: 38, CutoffMessageID: nil, Reason: "user_cancel", Metadata: map[string]string{
		model.MetadataKeyKEnv: "cancel_env",
	}}))
	require.NoError(t, err)
	require.Equal(t, 2, wakeAttempts)
	require.Equal(t, model.ThreadStatusReady, result.Thread.Status)
	require.NotNil(t, result.ControlMessage)

	var persisted model.TThread
	require.NoError(t, db.First(&persisted, "thread_id = ?", int64(38)).Error)
	require.Equal(t, model.ThreadStatusReady, persisted.Status)
	require.JSONEq(t, `{"K_ENV":"original_env"}`, persisted.MetadataJson)
}

func TestCancelInputAcceptsConcurrentReadyOrRunningAfterRetryFailure(t *testing.T) {
	writeDSN := fmt.Sprintf("file:%s_write?mode=memory&cache=shared", t.Name())
	writeDB, err := gorm.Open(sqlite.Open(writeDSN), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, writeDB.AutoMigrate(&model.TThread{}, &model.TMailboxMessage{}))
	readDSN := fmt.Sprintf("file:%s_read?mode=memory&cache=shared", t.Name())
	readDB, err := gorm.Open(sqlite.Open(readDSN), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, readDB.AutoMigrate(&model.TThread{}, &model.TMailboxMessage{}))

	fr := newFakeRedis()
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	svc := newTestCoordinator(writeDB, readDB, fr, &stubIDGenerator{next: 5700}, WithClock(func() time.Time { return now }))
	idleThread := &model.TThread{ThreadId: 39, Namespace: "ns1", Env: "ppe_a", Status: model.ThreadStatusIdle, CreatedAt: now, UpdatedAt: now}
	runningThread := *idleThread
	runningThread.Status = model.ThreadStatusRunning
	insertThread(t, writeDB, idleThread)
	insertThread(t, readDB, &runningThread)
	message := &model.TMailboxMessage{
		MessageId: 3901, ThreadId: 39, SenderType: model.SenderTypeUser, SenderId: "u1", MessageType: "input",
		Status: model.MessageStatusPending, Payload: "one", MetadataJson: "{}", CreatedAt: now, UpdatedAt: now,
	}
	insertMessage(t, writeDB, message)
	require.NoError(t, fr.StructSet(context.Background(), redisstore.MessageKey(message.MessageId), storage.CachedMessage("ns1", message)))
	require.NoError(t, fr.ZAdd(context.Background(), redisstore.PendingInputKey("ns1", 39), float64(message.MessageId), strconv.FormatInt(message.MessageId, 10)))

	wakeErr := errors.New("wake raced")
	require.NoError(t, writeDB.Callback().Raw().Before("gorm:raw").Register("test:fail_cancel_wakes", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "update t_thread set status =") {
			tx.AddError(wakeErr)
		}
	}))

	result, err := testRequestInputCancel(svc.RequestInputCancel(context.Background(), RequestInputCancelRequest{Namespace: "ns1", ThreadID: 39, CutoffMessageID: nil, Reason: "user_cancel", Metadata: nil}))
	require.NoError(t, err)
	require.Equal(t, model.ThreadStatusRunning, result.Thread.Status)
	require.NotNil(t, result.ControlMessage)
}

func TestCloseThreadEnqueuesControlAndCompleteCloses(t *testing.T) {
	svc, db, fr := newTestService(t)
	svc.idgen = &stubIDGenerator{next: 5000}
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	deadline := now.Add(5 * time.Minute)

	insertThread(t, db, &model.TThread{
		ThreadId:        41,
		Namespace:       "ns1",
		Env:             "ppe_a",
		Status:          model.ThreadStatusRunning,
		LeaseToken:      "lease-1",
		LeaseDeadlineAt: deadline,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	for _, msg := range []*model.TMailboxMessage{
		{
			MessageId:    4101,
			ThreadId:     41,
			SenderType:   model.SenderTypeUser,
			SenderId:     "u1",
			MessageType:  "input",
			Status:       model.MessageStatusPending,
			Payload:      "one",
			MetadataJson: "{}",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			MessageId:    4102,
			ThreadId:     41,
			SenderType:   model.SenderTypeUser,
			SenderId:     "u1",
			MessageType:  "input",
			Status:       model.MessageStatusPending,
			Payload:      "two",
			MetadataJson: "{}",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	} {
		insertMessage(t, db, msg)
		require.NoError(t, fr.StructSet(context.Background(), redisstore.MessageKey(msg.MessageId), storage.CachedMessage("ns1", msg)))
		require.NoError(t, fr.ZAdd(context.Background(), redisstore.PendingInputKey("ns1", 41), float64(msg.MessageId), strconv.FormatInt(msg.MessageId, 10)))
	}

	result, err := testRequestThreadClose(svc.RequestThreadClose(context.Background(), RequestThreadCloseRequest{Namespace: "ns1", ThreadID: 41, Reason: "user close", Metadata: map[string]string{
		"logid":                           "log-close",
		model.MetadataKeyBytedCtxMetaInfo: `{"persist":"close-value"}`,
		model.MetadataKeyKEnv:             "close_env",
	}}))
	require.NoError(t, err)
	require.Equal(t, model.ThreadStatusClosing, result.Thread.Status)
	require.Equal(t, []int64{4101, 4102}, result.CancelledMessageIDs)
	require.NotNil(t, result.ControlMessage)
	require.Equal(t, int64(5001), result.ControlMessage.MessageId)
	require.Equal(t, model.MessageTypeControlCloseThread, result.ControlMessage.MessageType)

	var persistedThread model.TThread
	require.NoError(t, db.First(&persistedThread, "thread_id = ?", int64(41)).Error)
	require.Equal(t, model.ThreadStatusClosing, persistedThread.Status)
	require.Equal(t, "lease-1", persistedThread.LeaseToken)
	require.False(t, persistedThread.ReadyAt.IsZero())

	_, _, err = testSubmitInput(svc.SubmitInput(context.Background(), SubmitInputRequest{Namespace: "ns1", ThreadID: 41, SenderType: SenderType(model.SenderTypeUser), SenderID: "u2", MessageType: "input", Payload: []byte("late"), Metadata: nil, WakeThread: true}))
	require.ErrorIs(t, err, ErrThreadClosed)

	pending, _, err := testReadPendingInputs(svc.ReadPendingInputs(context.Background(), ReadPendingInputsRequest{Namespace: "ns1", ThreadID: 41, LeaseToken: "lease-1", Limit: 10}))
	require.NoError(t, err)
	require.Equal(t, []int64{5001}, messageIDs(pending))

	var payload CloseThreadControlPayload
	require.NoError(t, sonic.UnmarshalString(result.ControlMessage.Payload, &payload))
	require.Equal(t, model.ControlTypeCloseThread, payload.ControlType)
	require.Equal(t, int64(41), payload.ThreadID)
	require.Equal(t, "user close", payload.Reason)

	var controlMetadata CloseThreadControlMetadata
	require.NoError(t, sonic.UnmarshalString(result.ControlMessage.MetadataJson, &controlMetadata))
	require.Equal(t, "log-close", controlMetadata.LogID)
	require.Equal(t, `{"persist":"close-value"}`, controlMetadata.BytedCtxMetaInfo)
	require.Equal(t, "close_env", controlMetadata.KEnv)

	completed, err := testConfirmThreadClosed(svc.ConfirmThreadClosed(context.Background(), ConfirmThreadClosedRequest{Namespace: "ns1", ThreadID: 41, LeaseToken: "lease-1", ControlMessageID: result.ControlMessage.MessageId, Reason: "worker complete close"}))
	require.NoError(t, err)
	require.Equal(t, model.ThreadStatusClosed, completed.Thread.Status)
	require.True(t, completed.Thread.LeaseDeadlineAt.IsZero())
	require.Equal(t, "", completed.Thread.LeaseToken)

	var controlCached redisstore.CachedMessage
	require.NoError(t, fr.StructGet(context.Background(), redisstore.MessageKey(result.ControlMessage.MessageId), &controlCached))
	require.Equal(t, model.MessageStatusAcked, controlCached.Status)
	remaining, err := svc.loadPendingInputs(context.Background(), "ns1", 41, 10)
	require.NoError(t, err)
	require.Empty(t, remaining)

	var controlRow model.TMailboxMessage
	require.NoError(t, db.First(&controlRow, "message_id = ?", result.ControlMessage.MessageId).Error)
	require.Equal(t, model.MessageStatusAcked, controlRow.Status)

	retried, err := testConfirmThreadClosed(svc.ConfirmThreadClosed(context.Background(), ConfirmThreadClosedRequest{Namespace: "ns1", ThreadID: 41, LeaseToken: "expired-lease", ControlMessageID: result.ControlMessage.MessageId, Reason: "retry"}))
	require.NoError(t, err)
	require.Equal(t, model.ThreadStatusClosed, retried.Thread.Status)
}

func TestCloseThreadIdleRefreshesActivationMetadata(t *testing.T) {
	svc, db, _ := newTestService(t)
	svc.idgen = &stubIDGenerator{next: 5100}
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)

	insertThread(t, db, &model.TThread{
		ThreadId:     42,
		Namespace:    "ns1",
		Env:          "ppe_a",
		Status:       model.ThreadStatusIdle,
		MetadataJson: `{"role":"main","logid":"old-logid","byted_ctx_meta_info":"{\"old\":\"value\"}","K_ENV":"old_env"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	result, err := testRequestThreadClose(svc.RequestThreadClose(context.Background(), RequestThreadCloseRequest{Namespace: "ns1", ThreadID: 42, Reason: "user close", Metadata: map[string]string{
		"logid":                           "log-close-idle",
		model.MetadataKeyBytedCtxMetaInfo: `{"persist":"idle-close"}`,
		model.MetadataKeyKEnv:             "close_idle_env",
	}}))
	require.NoError(t, err)
	require.Equal(t, model.ThreadStatusClosing, result.Thread.Status)
	require.False(t, result.Thread.ReadyAt.IsZero())
	threadMetadata, err := util.ToStruct[map[string]string](result.Thread.MetadataJson)
	require.NoError(t, err)
	require.Equal(t, "main", (*threadMetadata)["role"])
	require.Equal(t, "log-close-idle", (*threadMetadata)["logid"])
	require.Equal(t, `{"persist":"idle-close"}`, (*threadMetadata)[model.MetadataKeyBytedCtxMetaInfo])
	require.Equal(t, "close_idle_env", (*threadMetadata)[model.MetadataKeyKEnv])
	require.NotNil(t, result.ControlMessage)
}

func TestCloseThreadBlockedRefreshesActivationMetadata(t *testing.T) {
	svc, db, _ := newTestService(t)
	svc.idgen = &stubIDGenerator{next: 5150}
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)

	insertThread(t, db, &model.TThread{
		ThreadId:     45,
		Namespace:    "ns1",
		Env:          "ppe_a",
		Status:       model.ThreadStatusBlocked,
		MetadataJson: `{"role":"main","logid":"old-logid","K_ENV":"old_env"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	result, err := testRequestThreadClose(svc.RequestThreadClose(context.Background(), RequestThreadCloseRequest{Namespace: "ns1", ThreadID: 45, Reason: "user close", Metadata: map[string]string{
		"logid":               "log-close-blocked",
		model.MetadataKeyKEnv: "close_blocked_env",
	}}))
	require.NoError(t, err)
	require.Equal(t, model.ThreadStatusClosing, result.Thread.Status)
	require.False(t, result.Thread.ReadyAt.IsZero())
	threadMetadata, err := util.ToStruct[map[string]string](result.Thread.MetadataJson)
	require.NoError(t, err)
	require.Equal(t, "main", (*threadMetadata)["role"])
	require.Equal(t, "log-close-blocked", (*threadMetadata)["logid"])
	require.Equal(t, "close_blocked_env", (*threadMetadata)[model.MetadataKeyKEnv])
	require.NotNil(t, result.ControlMessage)
}

func TestCloseThreadNonIdleDoesNotOverwriteWriteMetadataFromLaggingRead(t *testing.T) {
	writeDSN := fmt.Sprintf("file:%s_write?mode=memory&cache=shared", t.Name())
	writeDB, err := gorm.Open(sqlite.Open(writeDSN), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, writeDB.AutoMigrate(&model.TThread{}, &model.TMailboxMessage{}))
	readDSN := fmt.Sprintf("file:%s_read?mode=memory&cache=shared", t.Name())
	readDB, err := gorm.Open(sqlite.Open(readDSN), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, readDB.AutoMigrate(&model.TThread{}, &model.TMailboxMessage{}))

	fr := newFakeRedis()
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	svc := newTestCoordinator(writeDB, readDB, fr, &stubIDGenerator{next: 5200}, WithClock(func() time.Time { return now }))
	staleThread := &model.TThread{
		ThreadId:     43,
		Namespace:    "ns1",
		Env:          "ppe_a",
		Status:       model.ThreadStatusRunning,
		MetadataJson: `{"role":"main","K_ENV":"stale_env"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	currentThread := *staleThread
	currentThread.MetadataJson = `{"role":"main","K_ENV":"current_env","logid":"current-log"}`
	insertThread(t, readDB, staleThread)
	insertThread(t, writeDB, &currentThread)

	result, err := testRequestThreadClose(svc.RequestThreadClose(context.Background(), RequestThreadCloseRequest{Namespace: "ns1", ThreadID: 43, Reason: "user close", Metadata: map[string]string{
		model.MetadataKeyKEnv: "close_env",
	}}))
	require.NoError(t, err)
	require.Equal(t, model.ThreadStatusClosing, result.Thread.Status)

	var persisted model.TThread
	require.NoError(t, writeDB.First(&persisted, "thread_id = ?", int64(43)).Error)
	require.Equal(t, model.ThreadStatusClosing, persisted.Status)
	require.JSONEq(t, `{"role":"main","K_ENV":"current_env","logid":"current-log"}`, persisted.MetadataJson)
	require.NotNil(t, result.ControlMessage)
	require.Equal(t, model.MessageTypeControlCloseThread, result.ControlMessage.MessageType)
}

func TestCloseThreadStaleIdleReadClosesCurrentReadyThread(t *testing.T) {
	writeDSN := fmt.Sprintf("file:%s_write?mode=memory&cache=shared", t.Name())
	writeDB, err := gorm.Open(sqlite.Open(writeDSN), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, writeDB.AutoMigrate(&model.TThread{}, &model.TMailboxMessage{}))
	readDSN := fmt.Sprintf("file:%s_read?mode=memory&cache=shared", t.Name())
	readDB, err := gorm.Open(sqlite.Open(readDSN), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, readDB.AutoMigrate(&model.TThread{}, &model.TMailboxMessage{}))

	fr := newFakeRedis()
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	svc := newTestCoordinator(writeDB, readDB, fr, &stubIDGenerator{next: 5400}, WithClock(func() time.Time { return now }))
	staleThread := &model.TThread{
		ThreadId:     44,
		Namespace:    "ns1",
		Env:          "ppe_a",
		Status:       model.ThreadStatusIdle,
		MetadataJson: `{"role":"main","K_ENV":"stale_env"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	currentThread := *staleThread
	currentThread.Status = model.ThreadStatusReady
	currentThread.MetadataJson = `{"role":"main","K_ENV":"current_env","logid":"current-log"}`
	insertThread(t, readDB, staleThread)
	insertThread(t, writeDB, &currentThread)

	result, err := testRequestThreadClose(svc.RequestThreadClose(context.Background(), RequestThreadCloseRequest{Namespace: "ns1", ThreadID: 44, Reason: "user close", Metadata: map[string]string{
		model.MetadataKeyKEnv: "close_env",
	}}))
	require.NoError(t, err)
	require.Equal(t, model.ThreadStatusClosing, result.Thread.Status)

	var persisted model.TThread
	require.NoError(t, writeDB.First(&persisted, "thread_id = ?", int64(44)).Error)
	require.Equal(t, model.ThreadStatusClosing, persisted.Status)
	require.JSONEq(t, `{"role":"main","K_ENV":"current_env","logid":"current-log"}`, persisted.MetadataJson)
	require.NotNil(t, result.ControlMessage)
	require.Equal(t, model.MessageTypeControlCloseThread, result.ControlMessage.MessageType)
}

func TestPullPendingMessagesRequiresLeaseAndReadsHotstore(t *testing.T) {
	svc, db, fr := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	lease := "lease-pull"

	insertThread(t, db, &model.TThread{
		ThreadId:        6,
		Namespace:       "ns1",
		Env:             "ppe_a",
		Title:           "t6",
		Status:          model.ThreadStatusRunning,
		LeaseToken:      lease,
		LeaseDeadlineAt: now.Add(5 * time.Minute),
		CreatedAt:       now,
		UpdatedAt:       now,
		CreatedBy:       "tester",
	})
	for _, item := range []struct {
		id      int64
		payload string
	}{
		{id: 601, payload: "one"},
		{id: 602, payload: "two"},
	} {
		message := &model.TMailboxMessage{
			MessageId:    item.id,
			ThreadId:     6,
			CreatedAt:    now,
			UpdatedAt:    now,
			SenderType:   model.SenderTypeUser,
			SenderId:     "u1",
			MessageType:  "input",
			Status:       model.MessageStatusPending,
			Payload:      item.payload,
			MetadataJson: "{}",
		}
		require.NoError(t, svc.enqueueInput(context.Background(), "ns1", message))
	}

	var storedRows int64
	require.NoError(t, db.Model(&model.TMailboxMessage{}).Where("thread_id = ?", 6).Count(&storedRows).Error)
	require.Equal(t, int64(0), storedRows)

	messages, serverTimeMS, err := testReadPendingInputs(svc.ReadPendingInputs(context.Background(), ReadPendingInputsRequest{Namespace: "ns1", ThreadID: 6, LeaseToken: lease, Limit: 1}))
	require.NoError(t, err)
	require.Equal(t, now.UnixMilli(), serverTimeMS)
	require.Len(t, messages, 1)
	require.Equal(t, int64(601), messages[0].MessageId)
	require.Equal(t, "one", messages[0].Payload)

	_, ok := fr.zsets[redisstore.PendingInputKey("ns1", 6)]["601"]
	require.True(t, ok)
	_, ok = fr.zsets[redisstore.PendingInputKey("ns1", 6)]["602"]
	require.True(t, ok)

	_, _, err = testReadPendingInputs(svc.ReadPendingInputs(context.Background(), ReadPendingInputsRequest{Namespace: "ns1", ThreadID: 6, LeaseToken: "wrong-lease", Limit: 10}))
	require.ErrorIs(t, err, ErrLeaseMismatch)
}

func TestAckAndFailMessagesUpdateStateAndHotstore(t *testing.T) {
	svc, db, fr := newTestService(t)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	lease := "lease-1"
	triggerTurnID := "turn-ack-301"

	insertThread(t, db, &model.TThread{
		ThreadId:        4,
		Namespace:       "ns1",
		Env:             "ppe_a",
		Title:           "t4",
		Status:          model.ThreadStatusRunning,
		LeaseToken:      lease,
		LeaseDeadlineAt: now.Add(5 * time.Minute),
		CreatedAt:       now,
		UpdatedAt:       now,
		CreatedBy:       "tester",
	})
	for _, id := range []int64{301, 302} {
		message := &model.TMailboxMessage{
			MessageId:    id,
			ThreadId:     4,
			CreatedAt:    now,
			UpdatedAt:    now,
			SenderType:   model.SenderTypeAgent,
			SenderId:     "a1",
			MessageType:  "task",
			Status:       model.MessageStatusPending,
			Payload:      "payload",
			MetadataJson: "{}",
		}
		insertMessage(t, db, message)
		require.NoError(t, svc.enqueueInput(context.Background(), "ns1", message))
	}

	acked, err := testConfirmInputDelivery(svc.ConfirmInputDelivery(context.Background(), ConfirmInputDeliveryRequest{Namespace: "ns1", ThreadID: 4, LeaseToken: lease, MessageIDs: []int64{301}, TriggerRunID: triggerTurnID}))
	require.NoError(t, err)
	require.Len(t, acked, 1)
	require.Equal(t, model.MessageStatusAcked, acked[0].Status)
	require.Equal(t, triggerTurnID, acked[0].TriggerTurnId)

	var ackedCached redisstore.CachedMessage
	require.NoError(t, fr.StructGet(context.Background(), redisstore.MessageKey(301), &ackedCached))
	require.Equal(t, model.MessageStatusAcked, ackedCached.Status)
	require.Equal(t, triggerTurnID, ackedCached.TriggerTurnID)
	_, ok := fr.zsets[redisstore.PendingInputKey("ns1", 4)]["301"]
	require.False(t, ok)

	var ackedTriggerTurnID string
	require.NoError(t, db.Raw("select trigger_turn_id from t_mailbox_message where message_id = ?", int64(301)).Scan(&ackedTriggerTurnID).Error)
	require.Equal(t, triggerTurnID, ackedTriggerTurnID)

	repeatAck, err := testConfirmInputDelivery(svc.ConfirmInputDelivery(context.Background(), ConfirmInputDeliveryRequest{Namespace: "ns1", ThreadID: 4, LeaseToken: lease, MessageIDs: []int64{301}, TriggerRunID: triggerTurnID}))
	require.NoError(t, err)
	require.Len(t, repeatAck, 1)
	require.Equal(t, model.MessageStatusAcked, repeatAck[0].Status)
	require.Equal(t, triggerTurnID, repeatAck[0].TriggerTurnId)

	secondAck, err := testConfirmInputDelivery(svc.ConfirmInputDelivery(context.Background(), ConfirmInputDeliveryRequest{Namespace: "ns1", ThreadID: 4, LeaseToken: lease, MessageIDs: []int64{302}, TriggerRunID: "turn-ack-302"}))
	require.NoError(t, err)
	require.Len(t, secondAck, 1)
	require.Equal(t, model.MessageStatusAcked, secondAck[0].Status)
	require.Equal(t, "turn-ack-302", secondAck[0].TriggerTurnId)

	var secondAckCached redisstore.CachedMessage
	require.NoError(t, fr.StructGet(context.Background(), redisstore.MessageKey(302), &secondAckCached))
	require.Equal(t, model.MessageStatusAcked, secondAckCached.Status)
	require.Equal(t, "turn-ack-302", secondAckCached.TriggerTurnID)
	_, ok = fr.zsets[redisstore.PendingInputKey("ns1", 4)]["302"]
	require.False(t, ok)
}
