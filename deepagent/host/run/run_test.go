package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"eino-cli/backend/config"
	"eino-cli/backend/consts"
	"eino-cli/backend/session/rollback"
	"eino-cli/backend/session/runs"
	runtimecontext "eino-cli/deepagent/host/executioncontext"
)

func TestHandlePersistsUnifiedRunResultAndSnapshot(t *testing.T) {
	root := t.TempDir()
	restore := config.SetRootDirForTest(root)
	t.Cleanup(restore)
	runStore := runs.NewStore(config.SessionRunsDir(consts.DefaultSessionID))
	manager := NewManagerWithStore(runStore, rollback.NewStore(root, consts.DefaultSessionID))
	ctx := runtimecontext.WithSessionID(context.Background(), "session-1")
	handle, err := manager.Begin(ctx, "prompt")
	if err != nil {
		t.Fatalf("Begin() error=%v", err)
	}
	handle.Complete(Success, "answer", nil)
	workspaceFile := filepath.Join(config.SandboxWorkDir(consts.DefaultSessionID), "state.txt")
	if err = os.MkdirAll(filepath.Dir(workspaceFile), 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err = os.WriteFile(workspaceFile, []byte("before"), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}
	if err = handle.SaveWorkspaceSnapshot(context.Background()); err != nil {
		t.Fatalf("SaveWorkspaceSnapshot() error=%v", err)
	}

	records, err := manager.ListRuns(context.Background())
	if err != nil {
		t.Fatalf("ListRuns() error=%v", err)
	}
	if len(records) != 1 || records[0].Status != "success" || records[0].Output != "answer" || records[0].SessionID != "session-1" || !records[0].Rollbackable {
		t.Fatalf("persisted records=%+v", records)
	}
	if err = os.WriteFile(workspaceFile, []byte("after"), 0o644); err != nil {
		t.Fatalf("mutate workspace: %v", err)
	}
	if err = manager.RestoreWorkspaceSnapshot(context.Background(), handle.ID()); err != nil {
		t.Fatalf("RestoreWorkspaceSnapshot() error=%v", err)
	}
	payload, err := os.ReadFile(workspaceFile)
	if err != nil || string(payload) != "before" {
		t.Fatalf("restored workspace=%q error=%v", payload, err)
	}
}

func TestManagerRejectsConcurrentRunUntilCompletion(t *testing.T) {
	manager := NewManagerWithStore(nil)
	first, err := manager.Begin(context.Background(), "first")
	if err != nil {
		t.Fatalf("Begin(first) error=%v", err)
	}
	if _, err = manager.Begin(context.Background(), "second"); err == nil {
		t.Fatal("Begin(second) accepted concurrent run")
	}
	first.Complete(Interrupted, "", context.Canceled)
	if _, err = manager.Begin(context.Background(), "third"); err != nil {
		t.Fatalf("Begin(third) after completion error=%v", err)
	}
}

func TestHandlePersistsError(t *testing.T) {
	store := runs.NewStore(t.TempDir())
	manager := NewManagerWithStore(store)
	handle, err := manager.Begin(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Begin() error=%v", err)
	}
	wantErr := errors.New("runtime failed")
	handle.Complete(Error, "partial", wantErr)
	records, err := manager.ListRuns(context.Background())
	if err != nil {
		t.Fatalf("ListRuns() error=%v", err)
	}
	if len(records) != 1 || records[0].Error != wantErr.Error() || records[0].Output != "partial" {
		t.Fatalf("persisted records=%+v", records)
	}
}
