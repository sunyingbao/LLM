package run

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"eino-cli/backend/consts"
	"eino-cli/backend/session/rollback"
	"eino-cli/backend/session/runs"
	runtimecontext "eino-cli/deepagent/host/executioncontext"
)

type Status string

const (
	Pending     Status = "pending"
	Running     Status = "running"
	Success     Status = "success"
	Error       Status = "error"
	Interrupted Status = "interrupted"
)

type Record struct {
	ID            string
	SessionID     string
	Prompt        string
	Status        Status
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Cancel        context.CancelFunc
	Output        string
	Err           error
	TotalTokens   int64
	Rollbackable  bool
	RollbackPath  string
	RollbackError string
}

type Manager struct {
	mu            sync.Mutex
	current       *Record
	store         *runs.Store
	rollbackStore *rollback.Store
}

type Handle struct {
	manager *Manager
	record  *Record
	Context context.Context
}

func NewManagerWithStore(store *runs.Store, rollbackStores ...*rollback.Store) (manager *Manager) {
	var rollbackStore *rollback.Store
	if len(rollbackStores) > 0 {
		rollbackStore = rollbackStores[0]
	}
	manager = &Manager{store: store, rollbackStore: rollbackStore}
	return manager
}

func (manager *Manager) Begin(ctx context.Context, prompt string) (handle *Handle, err error) {
	record, runContext, err := create(ctx, manager, prompt)
	if err != nil {
		return nil, err
	}
	handle = &Handle{manager: manager, record: record, Context: runContext}
	return handle, nil
}

func (manager *Manager) ListRuns(ctx context.Context) (records []runs.Record, err error) {
	manager.mu.Lock()
	store := manager.store
	manager.mu.Unlock()
	if store == nil {
		return nil, nil
	}
	records, err = store.List(ctx)
	return records, err
}

func (manager *Manager) RestoreWorkspaceSnapshot(ctx context.Context, runID string) (err error) {
	manager.mu.Lock()
	store := manager.rollbackStore
	manager.mu.Unlock()
	if store == nil {
		return fmt.Errorf("workspace snapshot store is not configured")
	}
	err = store.RestoreWorkspacePost(ctx, runID)
	return err
}

func (handle *Handle) ID() (id string) {
	if handle != nil && handle.record != nil {
		id = handle.record.ID
	}
	return id
}

func (handle *Handle) Cancel() {
	if handle != nil && handle.record != nil && handle.record.Cancel != nil {
		handle.record.Cancel()
	}
}

func (handle *Handle) Complete(status Status, output string, runErr error) {
	if handle == nil || handle.manager == nil || handle.record == nil {
		return
	}
	finish(handle.manager, handle.record, status, output, runErr)
}

func (handle *Handle) SaveWorkspaceSnapshot(ctx context.Context) (err error) {
	if handle == nil || handle.manager == nil || handle.record == nil {
		return fmt.Errorf("run handle is not initialized")
	}
	handle.manager.mu.Lock()
	store := handle.manager.store
	rollbackStore := handle.manager.rollbackStore
	handle.manager.mu.Unlock()
	if store == nil || rollbackStore == nil {
		return nil
	}
	path, err := rollbackStore.SaveWorkspacePost(ctx, handle.record.ID)
	if err != nil {
		markRollback(handle.manager, handle.record, "", err.Error())
		return err
	}
	markRollback(handle.manager, handle.record, path, "")
	err = store.Save(context.Background(), toRecord(handle.record))
	return err
}

func create(ctx context.Context, manager *Manager, prompt string) (record *Record, runContext context.Context, err error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.current != nil && (manager.current.Status == Pending || manager.current.Status == Running) {
		return nil, nil, fmt.Errorf("run already in progress")
	}
	runContext, cancel := context.WithCancel(ctx)
	now := time.Now()
	record = &Record{ID: fmt.Sprintf("run-%d", now.UnixNano()), SessionID: sessionIDFromContext(ctx), Prompt: prompt, Status: Pending, CreatedAt: now, UpdatedAt: now, Cancel: cancel}
	manager.current = record
	return record, runContext, nil
}

func finish(manager *Manager, record *Record, status Status, output string, runErr error) {
	manager.mu.Lock()
	record.Status = status
	record.Output = output
	record.Err = runErr
	record.UpdatedAt = time.Now()
	store := manager.store
	snapshot := toRecord(record)
	manager.mu.Unlock()
	if store == nil {
		return
	}
	if err := store.Save(context.Background(), snapshot); err != nil {
		slog.Warn("run store: save failed", "run_id", record.ID, "err", err)
	}
}

func sessionIDFromContext(ctx context.Context) (sessionID string) {
	sessionID = runtimecontext.GetSessionID(ctx)
	if sessionID == "" {
		sessionID = consts.DefaultSessionID
	}
	return sessionID
}

func toRecord(record *Record) (persisted runs.Record) {
	persisted = runs.Record{ID: record.ID, SessionID: record.SessionID, Status: string(record.Status), Prompt: record.Prompt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, DurationMS: record.UpdatedAt.Sub(record.CreatedAt).Milliseconds(), Output: record.Output, Tokens: record.TotalTokens, Rollbackable: record.Rollbackable, RollbackPath: record.RollbackPath, RollbackError: record.RollbackError}
	if record.Err != nil {
		persisted.Error = record.Err.Error()
	}
	return persisted
}

func markRollback(manager *Manager, record *Record, path, detail string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	record.RollbackPath = path
	record.Rollbackable = path != "" && detail == ""
	record.RollbackError = detail
}
