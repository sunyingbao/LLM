package agentthread

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/schema"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeSeqGenerator struct {
	next int64
	err  error
}

func (g *fakeSeqGenerator) Next(context.Context, string) (int64, error) {
	if g.err != nil {
		return 0, g.err
	}
	g.next++
	return g.next, nil
}

func newTestHistoryStore(t *testing.T, seq SeqGenerator) (*GormHistoryRolloutStore, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Table("agentthread_history").AutoMigrate(&historyRow{}); err != nil {
		t.Fatalf("migrate history: %v", err)
	}
	var id int64 = 100
	store := NewGormHistoryRolloutStore(db, "agentthread_history", func(context.Context, string, string) int64 {
		id += 100
		return id
	}, seq)
	return store, db
}

func TestGormHistoryRolloutStoreListsBySeqNotMessageID(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestHistoryStore(t, &fakeSeqGenerator{})

	records := []*HistoryRecord{
		{ThreadID: "t1", TurnID: "r1", MessageID: 300, Type: HistoryRecordMessage, Message: schema.UserMessage("first")},
		{ThreadID: "t1", TurnID: "r1", MessageID: 100, Type: HistoryRecordMessage, Message: schema.AssistantMessage("second", nil)},
		{ThreadID: "t1", TurnID: "r1", MessageID: 200, Type: HistoryRecordMessage, Message: schema.ToolMessage("third", "call_1")},
	}
	for _, rec := range records {
		if err := store.Append(ctx, rec); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := store.List(ctx, ListQuery{ThreadID: "t1", Order: ListOrderASC})
	if err != nil {
		t.Fatalf("list asc: %v", err)
	}
	if gotIDs := messageIDs(got); !reflect.DeepEqual(gotIDs, []int64{300, 100, 200}) {
		t.Fatalf("message ids by seq asc = %v", gotIDs)
	}

	before := int64(3)
	got, err = store.List(ctx, ListQuery{ThreadID: "t1", Order: ListOrderDESC, BeforeID: &before})
	if err != nil {
		t.Fatalf("list desc: %v", err)
	}
	if gotIDs := messageIDs(got); !reflect.DeepEqual(gotIDs, []int64{100, 300}) {
		t.Fatalf("message ids by seq desc before 3 = %v", gotIDs)
	}
}

func TestGormHistoryRolloutStoreSkipsLegacyZeroSeqRows(t *testing.T) {
	ctx := context.Background()
	store, db := newTestHistoryStore(t, &fakeSeqGenerator{})
	if err := db.Table("agentthread_history").Create(&historyRow{
		ThreadID:  "t1",
		MessageID: 1,
		Seq:       0,
		TurnID:    "r1",
		Type:      string(HistoryRecordMessage),
		Message:   `{"role":"user","content":"legacy"}`,
	}).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := store.Append(ctx, &HistoryRecord{
		ThreadID: "t1",
		TurnID:   "r1",
		Type:     HistoryRecordMessage,
		Message:  schema.UserMessage("new"),
	}); err != nil {
		t.Fatalf("append new row: %v", err)
	}
	got, err := store.List(ctx, ListQuery{ThreadID: "t1", Order: ListOrderASC})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if gotIDs := messageIDs(got); len(gotIDs) != 1 || got[0].Seq != 1 {
		t.Fatalf("list should only return seq rows, ids=%v records=%+v", gotIDs, got)
	}
}

func TestGormHistoryRolloutStoreReturnsSeqGeneratorError(t *testing.T) {
	want := errors.New("redis down")
	store, _ := newTestHistoryStore(t, &fakeSeqGenerator{err: want})
	err := store.Append(context.Background(), &HistoryRecord{
		ThreadID: "t1",
		TurnID:   "r1",
		Type:     HistoryRecordMessage,
		Message:  schema.UserMessage("hello"),
	})
	if !errors.Is(err, want) {
		t.Fatalf("Append() err = %v, want %v", err, want)
	}
}

func TestGormHistoryRolloutStorePreservesUniqueKeyAndCreateAtMS(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestHistoryStore(t, &fakeSeqGenerator{})

	if err := store.Append(ctx, &HistoryRecord{
		ThreadID:   "t1",
		TurnID:     "r1",
		Type:       HistoryRecordMessage,
		UniqueKey:  "thread-message-1",
		Message:    schema.UserMessage("hello"),
		CreateAt:   123,
		CreateAtMS: 123456,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := store.List(ctx, ListQuery{ThreadID: "t1", Order: ListOrderASC})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("records len = %d, want 1", len(got))
	}
}

type fakeIncrByClient struct {
	keys []string
	next int64
}

func (c *fakeIncrByClient) IncrBy(_ context.Context, key string, value int64) (int64, error) {
	c.keys = append(c.keys, key)
	c.next += value
	return c.next, nil
}

func TestRedisSeqGeneratorUsesThreadScopedKey(t *testing.T) {
	client := &fakeIncrByClient{}
	gen := NewRedisSeqGenerator(client, "deep_agent_sdk_worker:history_seq")
	got, err := gen.Next(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if got != 1 {
		t.Fatalf("Next() = %d, want 1", got)
	}
	if !reflect.DeepEqual(client.keys, []string{"deep_agent_sdk_worker:history_seq:thread:thread-1"}) {
		t.Fatalf("keys = %v", client.keys)
	}
}

func messageIDs(records []*HistoryRecord) []int64 {
	out := make([]int64, 0, len(records))
	for _, rec := range records {
		if rec != nil {
			out = append(out, rec.MessageID)
		}
	}
	return out
}
