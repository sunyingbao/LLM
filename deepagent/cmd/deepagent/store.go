package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/checkpointer"
	inprocessstore "eino-cli/deepagent/worker/inprocess/store"
	"github.com/cloudwego/eino/compose"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type LocalStore struct {
	db          *gorm.DB
	history     agentthread.HistoryRolloutStore
	threadState *inprocessstore.SQLiteThreadStateStore
	events      *inprocessstore.SQLiteEventStore

	checkpointDir string
}

func NewLocalStore(ctx context.Context, dbPath, checkpointDir string) (*LocalStore, error) {
	if err := ensureDirs(filepath.Dir(dbPath), checkpointDir); err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open sqlite db failed: %w", err)
	}
	type historyRow struct {
		ThreadID  string `gorm:"column:thread_id;primaryKey;size:128"`
		MessageID int64  `gorm:"column:message_id;primaryKey"`
		Seq       int64  `gorm:"column:seq"`
		TurnID    string `gorm:"column:turn_id;size:128"`
		Type      string `gorm:"column:type;size:32"`
		Message   string `gorm:"column:message"`
		Ext       string `gorm:"column:ext"`
		CreateAt  int64  `gorm:"column:created_at"`
	}
	if err := db.Table("agentthread_history").AutoMigrate(&historyRow{}); err != nil {
		return nil, fmt.Errorf("auto-migrate history table failed: %w", err)
	}
	threadState := inprocessstore.NewSQLiteThreadStateStore(db, "")
	if err := threadState.AutoMigrate(ctx); err != nil {
		return nil, err
	}
	eventStore := inprocessstore.NewSQLiteEventStore(db, "")
	if err := eventStore.AutoMigrate(ctx); err != nil {
		return nil, err
	}
	return &LocalStore{
		db:            db,
		history:       agentthread.NewGormHistoryRolloutStore(db, "agentthread_history", newHistoryRecordIDProvider()),
		threadState:   threadState,
		events:        eventStore,
		checkpointDir: checkpointDir,
	}, nil
}

func (s *LocalStore) CheckpointStore(threadID string) (compose.CheckPointStore, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	return checkpointer.NewFileStore(filepath.Join(s.checkpointDir, threadID))
}

func newHistoryRecordIDProvider() func(ctx context.Context, threadID, turnID string) int64 {
	base := time.Now().UnixMilli() * 1_000_000
	var seq uint64
	return func(_ context.Context, _, _ string) int64 {
		return base + int64(atomic.AddUint64(&seq, 1))
	}
}

func ensureDirs(paths ...string) error {
	for _, path := range paths {
		if path == "" || path == "." {
			continue
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}
	return nil
}
