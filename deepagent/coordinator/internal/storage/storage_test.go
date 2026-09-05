package storage

import (
	"context"
	"eino-cli/deepagent/coordinator/internal/model"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestFindAndListRows(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC)
	rows := []any{
		&model.TAgentNamespace{NamespaceId: 1, Namespace: "ns", CreatedAt: now, UpdatedAt: now},
		&model.TThread{ThreadId: 11, Namespace: "ns", SessionId: "session", CreatedAt: now, UpdatedAt: now},
		&model.TThread{ThreadId: 12, Namespace: "ns", SessionId: "session", CreatedAt: now, UpdatedAt: now},
		&model.TThread{ThreadId: 13, Namespace: "other", SessionId: "session", CreatedAt: now, UpdatedAt: now},
		&model.TMailboxMessage{MessageId: 21, ThreadId: 11, Status: "pending", CreatedAt: now, UpdatedAt: now},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
	}

	namespace, err := FindNamespace(ctx, db, "ns")
	if err != nil || namespace.Namespace != "ns" {
		t.Fatalf("FindNamespace() = (%+v, %v)", namespace, err)
	}
	thread, err := FindThread(ctx, db, 11)
	if err != nil || thread.SessionId != "session" {
		t.Fatalf("FindThread() = (%+v, %v)", thread, err)
	}
	message, err := FindMessage(ctx, db, 21)
	if err != nil || message.ThreadId != 11 {
		t.Fatalf("FindMessage() = (%+v, %v)", message, err)
	}
	threads, err := ListSessionThreads(ctx, db, "ns", "session", 11, 10)
	if err != nil {
		t.Fatalf("ListSessionThreads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].ThreadId != 12 {
		t.Fatalf("ListSessionThreads() = %+v", threads)
	}

	if _, err = FindNamespace(ctx, db, "missing"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("FindNamespace() missing error = %v", err)
	}
	if _, err = ListSessionThreads(ctx, db, "ns", "missing", 0, 10); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("ListSessionThreads() missing error = %v", err)
	}
}

func TestUpdateInputStatusPreservesTriggerTurnIDUnlessProvided(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 2, 0, 0, 0, time.UTC)
	messages := []*model.TMailboxMessage{
		{MessageId: 31, ThreadId: 11, Status: "pending", TriggerTurnId: "existing", CreatedAt: now, UpdatedAt: now},
		{MessageId: 32, ThreadId: 11, Status: "pending", TriggerTurnId: "existing", CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatalf("insert messages: %v", err)
	}

	rowsAffected, err := UpdateInputStatus(ctx, db, 11, "pending", "canceled", now.Add(time.Minute), nil, []int64{31})
	if err != nil || rowsAffected != 1 {
		t.Fatalf("UpdateInputStatus() without trigger = (%d, %v)", rowsAffected, err)
	}
	triggerTurnID := ""
	rowsAffected, err = UpdateInputStatus(ctx, db, 11, "pending", "acked", now.Add(2*time.Minute), &triggerTurnID, []int64{32})
	if err != nil || rowsAffected != 1 {
		t.Fatalf("UpdateInputStatus() with trigger = (%d, %v)", rowsAffected, err)
	}

	first, err := FindMessage(ctx, db, 31)
	if err != nil {
		t.Fatalf("find first message: %v", err)
	}
	if first.Status != "canceled" || first.TriggerTurnId != "existing" || !first.HandledAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("first message = %+v", first)
	}
	second, err := FindMessage(ctx, db, 32)
	if err != nil {
		t.Fatalf("find second message: %v", err)
	}
	if second.Status != "acked" || second.TriggerTurnId != "" || !second.HandledAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("second message = %+v", second)
	}
}

func TestInsertAndFindEvents(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 3, 0, 0, 0, time.UTC)
	rows := []*model.TEventLog{
		{EventId: 42, Namespace: "ns", ThreadId: 11, TurnId: "turn", EventType: "done", CreatedAt: now},
		{EventId: 41, Namespace: "ns", ThreadId: 11, TurnId: "turn", EventType: "start", CreatedAt: now},
	}
	result := db.WithContext(ctx).CreateInBatches(&rows, 20)
	if result.Error != nil || result.RowsAffected != 2 {
		t.Fatalf("insert event fixtures = (%d, %v)", result.RowsAffected, result.Error)
	}
	events, err := FindEvents(ctx, db, []int64{42, 41})
	if err != nil {
		t.Fatalf("FindEvents() error = %v", err)
	}
	if len(events) != 2 || events[0].EventId != 41 || events[1].EventId != 42 {
		t.Fatalf("FindEvents() = %+v", events)
	}
	if _, err = FindEvents(ctx, db, []int64{99}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("FindEvents() missing error = %v", err)
	}
}

func newTestDB(t *testing.T) (db *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "storage.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite connection: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&model.TAgentNamespace{}, &model.TThread{}, &model.TMailboxMessage{}, &model.TEventLog{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}
