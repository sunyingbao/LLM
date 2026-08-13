package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
)

func TestSQLiteThreadStateStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteThreadStateStore(filepath.Join(t.TempDir(), "threads.sqlite"), "")
	if err != nil {
		t.Fatalf("OpenSQLiteThreadStateStore() error = %v", err)
	}

	root, err := store.CreateThread(ctx, inprocess.CreateThreadSpec{
		ID:        "root",
		UserID:    1001,
		SessionID: "sess_1",
		Title:     "Root",
		Profile:   inprocess.ThreadProfile{Role: "main", Cwd: "/repo"},
		Metadata:  map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("CreateThread(root) error = %v", err)
	}
	if root.RootThreadID != "root" || root.Profile.Cwd != "/repo" || root.Metadata["k"] != "v" {
		t.Fatalf("root = %+v", root)
	}

	child, err := store.CreateThread(ctx, inprocess.CreateThreadSpec{
		ID:             "child",
		ParentThreadID: root.ID,
		Title:          "Child",
	})
	if err != nil {
		t.Fatalf("CreateThread(child) error = %v", err)
	}
	if child.UserID != root.UserID || child.SessionID != root.SessionID || child.RootThreadID != root.ID {
		t.Fatalf("child did not inherit parent identity: %+v", child)
	}

	newTitle := "Renamed"
	newCwd := "/repo/sub"
	block := &agentworker.PendingBlock{TurnID: "turn_1", InterruptID: "interrupt_1", CheckpointID: "cp_1", Kind: "approve"}
	updated, err := store.UpdateThread(ctx, root.ID, inprocess.UpdateThreadStatePatch{
		Title:        &newTitle,
		Cwd:          &newCwd,
		Metadata:     map[string]string{"k": "v2"},
		PendingBlock: block,
	})
	if err != nil {
		t.Fatalf("UpdateThread() error = %v", err)
	}
	if updated.Title != newTitle || updated.Profile.Cwd != newCwd || updated.Metadata["k"] != "v2" {
		t.Fatalf("updated = %+v", updated)
	}
	if updated.PendingBlock == nil || updated.PendingBlock.InterruptID != block.InterruptID {
		t.Fatalf("PendingBlock = %+v, want %+v", updated.PendingBlock, block)
	}

	listed, err := store.ListThreads(ctx, inprocess.ListThreadsOptions{UserID: 1001, Cwd: newCwd, RootOnly: true})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != root.ID {
		t.Fatalf("listed = %+v, want root only", listed)
	}

	closedAt := time.Unix(200, 0)
	closed, err := store.UpdateThread(ctx, root.ID, inprocess.UpdateThreadStatePatch{
		ClearPendingBlock: true,
		ClosedAt:          &closedAt,
	})
	if err != nil {
		t.Fatalf("UpdateThread(close) error = %v", err)
	}
	if closed.PendingBlock != nil || closed.ClosedAt == nil || !closed.ClosedAt.Equal(closedAt) {
		t.Fatalf("closed = %+v", closed)
	}

	openOnly, err := store.ListThreads(ctx, inprocess.ListThreadsOptions{SessionID: root.SessionID})
	if err != nil {
		t.Fatalf("ListThreads(open only) error = %v", err)
	}
	if len(openOnly) != 1 || openOnly[0].ID != child.ID {
		t.Fatalf("openOnly = %+v, want child", openOnly)
	}
}
