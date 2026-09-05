package coordinator

import (
	"context"
	"eino-cli/deepagent/coordinator/internal/infra/idgen"
	redisstore "eino-cli/deepagent/coordinator/internal/infra/store/redis"
	"eino-cli/deepagent/coordinator/internal/model"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type coordinatorIDGen struct {
	ids []int64
}

func (g *coordinatorIDGen) NextID(context.Context) (id int64, err error) {
	id = g.ids[0]
	g.ids = g.ids[1:]
	return id, nil
}

type coordinatorRedis struct {
	redisstore.Client
	operations []string
	members    map[string]map[string]float64
	values     map[string]json.RawMessage
}

func newCoordinatorRedis() (client *coordinatorRedis) {
	return &coordinatorRedis{members: map[string]map[string]float64{}, values: map[string]json.RawMessage{}}
}

func (c *coordinatorRedis) StructSet(_ context.Context, key string, value interface{}) (err error) {
	c.operations = append(c.operations, "set:"+key)
	c.values[key], err = json.Marshal(value)
	return err
}

func (c *coordinatorRedis) StructGetPrimary(_ context.Context, key string, target interface{}) (err error) {
	return json.Unmarshal(c.values[key], target)
}

func (c *coordinatorRedis) StructMGetPrimary(_ context.Context, keys []string, target interface{}) (err error) {
	values := make([]json.RawMessage, len(keys))
	for index, key := range keys {
		values[index] = c.values[key]
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func (c *coordinatorRedis) ZRangePrimary(_ context.Context, key string, start, stop int64) (members []string, err error) {
	for member := range c.members[key] {
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) (less bool) {
		left, right := c.members[key][members[i]], c.members[key][members[j]]
		if left == right {
			return members[i] < members[j]
		}
		return left < right
	})
	count := int64(len(members))
	if start < 0 {
		start += count
	}
	if stop < 0 {
		stop += count
	}
	start = max(start, 0)
	stop = min(stop, count-1)
	if start > stop {
		return nil, nil
	}
	return members[start : stop+1], nil
}

func (c *coordinatorRedis) ZAdd(_ context.Context, key string, score float64, member string) (err error) {
	c.operations = append(c.operations, "zadd:"+member)
	if c.members[key] == nil {
		c.members[key] = map[string]float64{}
	}
	c.members[key][member] = score
	return nil
}

func (c *coordinatorRedis) ZRem(_ context.Context, key string, members []interface{}) (removed int64, err error) {
	for _, member := range members {
		value := fmt.Sprint(member)
		c.operations = append(c.operations, "zrem:"+value)
		if _, ok := c.members[key][value]; ok {
			delete(c.members[key], value)
			removed++
		}
	}
	return removed, nil
}

func (c *coordinatorRedis) Del(_ context.Context, keys ...string) (removed int64, err error) {
	for _, key := range keys {
		c.operations = append(c.operations, "del:"+key)
		delete(c.values, key)
	}
	return int64(len(keys)), nil
}

func (c *coordinatorRedis) ZCardPrimary(_ context.Context, key string) (count int64, err error) {
	return int64(len(c.members[key])), nil
}

func newCoordinatorForTest(t *testing.T, generator idgen.Generator, redisClient redisstore.Client) (coordinator *Coordinator, db *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TAgentNamespace{}, &model.TThread{}, &model.TMailboxMessage{}))
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&model.TAgentNamespace{
		NamespaceId: 1, Namespace: "ns1", MetadataJson: "{}", CreatedAt: now, UpdatedAt: now,
	}).Error)
	coordinator = newTestCoordinator(db, db, redisClient, generator, WithClock(func() time.Time { return now }))
	return coordinator, db
}

func TestCoordinatorCreateThreadCleansQueuedMessageWhenReadyTransitionFails(t *testing.T) {
	redisClient := newCoordinatorRedis()
	coordinator, db := newCoordinatorForTest(t, &coordinatorIDGen{ids: []int64{101, 201}}, redisClient)
	readyErr := errors.New("ready transition failed")
	require.NoError(t, db.Callback().Raw().Before("gorm:raw").Register("test:fail_create_ready", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "update t_thread set status =") {
			tx.AddError(readyErr)
		}
	}))

	_, err := coordinator.CreateThread(context.Background(), CreateThreadRequest{
		Namespace: "ns1",
		Env:       "ppe_a",
		InitialMessage: &InitialMessage{
			SenderType: SenderTypeUser, SenderID: "u1", MessageType: "input", Payload: []byte("hello"),
		},
	})
	require.ErrorIs(t, err, readyErr)
	require.Empty(t, redisClient.members[redisstore.PendingInputKey("ns1", 101)])
	require.Equal(t, []string{
		"set:" + redisstore.MessageKey(201),
		"zadd:" + strconv.FormatInt(201, 10),
		"zrem:" + strconv.FormatInt(201, 10),
		"del:" + redisstore.MessageKey(201),
	}, redisClient.operations)
}

func TestCoordinatorCreateThreadReturnsReadyThreadAndPersistsInitialMessage(t *testing.T) {
	redisClient := newCoordinatorRedis()
	coordinator, db := newCoordinatorForTest(t, &coordinatorIDGen{ids: []int64{102, 202}}, redisClient)

	result, err := coordinator.CreateThread(context.Background(), CreateThreadRequest{
		Namespace: "ns1",
		Env:       "ppe_a",
		InitialMessage: &InitialMessage{
			SenderType: SenderTypeUser, SenderID: "u1", MessageType: "input", Payload: []byte("hello"),
		},
	})
	require.NoError(t, err)
	require.Equal(t, ThreadStatusReady, result.Thread.Status)
	require.Equal(t, int64(202), result.InitialMessage.MessageID)
	require.Equal(t, int64(102), result.InitialMessage.ThreadID)
	require.Contains(t, redisClient.members[redisstore.PendingInputKey("ns1", 102)], "202")

	var persisted model.TMailboxMessage
	require.NoError(t, db.First(&persisted, "message_id = ?", int64(202)).Error)
	require.Equal(t, model.MessageStatusPending, persisted.Status)
}

func TestCoordinatorResumeFromBlockMergesMetadataAndRollsBackQueuedMessage(t *testing.T) {
	redisClient := newCoordinatorRedis()
	coordinator, db := newCoordinatorForTest(t, &coordinatorIDGen{ids: []int64{301}}, redisClient)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&model.TThread{
		ThreadId: 41, Namespace: "ns1", Env: "ppe_a", Status: model.ThreadStatusBlocked,
		MetadataJson: `{"keep":"yes","logid":"old","K_ENV":"old"}`, CreatedAt: now, UpdatedAt: now,
	}).Error)
	resumeErr := errors.New("resume failed")
	var resumedMetadata string
	require.NoError(t, db.Callback().Raw().Before("gorm:raw").Register("test:fail_resume", func(tx *gorm.DB) {
		if !strings.Contains(tx.Statement.SQL.String(), "where thread_id = ? and namespace = ? and status = ?") {
			return
		}
		if len(tx.Statement.Vars) > 3 {
			resumedMetadata = fmt.Sprint(tx.Statement.Vars[3])
		}
		tx.AddError(resumeErr)
	}))

	_, err := coordinator.ResumeFromBlock(context.Background(), ResumeFromBlockRequest{
		Namespace: "ns1", ThreadID: 41, Reason: "resume",
		ActivationMetadata: map[string]string{"logid": "new", model.MetadataKeyKEnv: "boe"},
		ResumeMessage:      &InitialMessage{SenderType: SenderTypeUser, SenderID: "u1", MessageType: "input", Payload: []byte("resume")},
	})
	require.ErrorIs(t, err, resumeErr)
	require.JSONEq(t, `{"keep":"yes","logid":"new","K_ENV":"boe"}`, resumedMetadata)
	require.Empty(t, redisClient.members[redisstore.PendingInputKey("ns1", 41)])
}

func TestCoordinatorResumeFromBlockReturnsReadyThreadAndPersistsFrontMessage(t *testing.T) {
	redisClient := newCoordinatorRedis()
	coordinator, db := newCoordinatorForTest(t, &coordinatorIDGen{ids: []int64{302}}, redisClient)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&model.TThread{
		ThreadId: 42, Namespace: "ns1", Env: "ppe_a", Status: model.ThreadStatusBlocked,
		MetadataJson: `{"keep":"yes","logid":"old","K_ENV":"old"}`, CreatedAt: now, UpdatedAt: now,
	}).Error)

	result, err := coordinator.ResumeFromBlock(context.Background(), ResumeFromBlockRequest{
		Namespace: "ns1", ThreadID: 42, Reason: "resume",
		ActivationMetadata: map[string]string{"logid": "new", model.MetadataKeyKEnv: "boe"},
		ResumeMessage:      &InitialMessage{SenderType: SenderTypeUser, SenderID: "u1", MessageType: "input", Payload: []byte("resume")},
	})
	require.NoError(t, err)
	require.Equal(t, ThreadStatusReady, result.Thread.Status)
	require.Equal(t, map[string]string{"keep": "yes", "logid": "new", model.MetadataKeyKEnv: "boe"}, result.Thread.Metadata)
	require.Equal(t, int64(302), result.Message.MessageID)
	require.Contains(t, redisClient.members[redisstore.PendingInputKey("ns1", 42)], "302")

	var persisted model.TMailboxMessage
	require.NoError(t, db.First(&persisted, "message_id = ?", int64(302)).Error)
	require.Equal(t, model.MessageStatusPending, persisted.Status)
}

func TestCoordinatorResumeReadsPrimaryWhenReplicaIsStale(t *testing.T) {
	redisClient := newCoordinatorRedis()
	generator := &coordinatorIDGen{ids: []int64{401}}
	coordinator, primary := newCoordinatorForTest(t, generator, redisClient)
	replica, err := gorm.Open(sqlite.Open("file:"+t.Name()+"_replica?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, replica.AutoMigrate(&model.TThread{}))
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	thread := model.TThread{ThreadId: 43, Namespace: "ns1", Status: model.ThreadStatusBlocked, MetadataJson: "{}", CreatedAt: now, UpdatedAt: now}
	require.NoError(t, primary.Create(&thread).Error)
	thread.Status = model.ThreadStatusRunning
	require.NoError(t, replica.Create(&thread).Error)
	*coordinator = *newTestCoordinator(primary, replica, redisClient, generator, WithClock(func() (current time.Time) { return now }))

	resumed, err := coordinator.ResumeFromBlock(context.Background(), ResumeFromBlockRequest{
		Namespace: "ns1", ThreadID: 43,
		ResumeMessage: &InitialMessage{SenderType: SenderTypeUser, MessageType: "input", Payload: []byte("approved")},
	})
	require.NoError(t, err)
	require.Equal(t, ThreadStatusReady, resumed.Thread.Status)
	require.Equal(t, int64(401), resumed.Message.MessageID)
}

func TestCoordinatorReleaseChecksInputsArrivingDuringRelease(t *testing.T) {
	redisClient := newCoordinatorRedis()
	coordinator, db := newCoordinatorForTest(t, &coordinatorIDGen{}, redisClient)
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&model.TThread{
		ThreadId: 44, Namespace: "ns1", Status: model.ThreadStatusRunning, LeaseToken: "owner",
		LeaseDeadlineAt: now.Add(time.Minute), MetadataJson: "{}", CreatedAt: now, UpdatedAt: now,
	}).Error)
	require.NoError(t, db.Callback().Raw().After("gorm:raw").Register("test:enqueue_during_release", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "lease_token = ''") {
			require.NoError(t, coordinator.enqueueInput(context.Background(), "ns1", &model.TMailboxMessage{
				MessageId: 402, ThreadId: 44, MessageType: "input", Status: model.MessageStatusPending, MetadataJson: "{}",
			}))
		}
	}))

	released, err := coordinator.ReleaseThread(context.Background(), ReleaseThreadRequest{
		Namespace: "ns1", ThreadID: 44, LeaseToken: "owner",
	})
	require.NoError(t, err)
	require.Equal(t, ThreadStatusReady, released.Status, "input arriving during release must remain runnable")
}

var _ redisstore.Client = (*coordinatorRedis)(nil)
