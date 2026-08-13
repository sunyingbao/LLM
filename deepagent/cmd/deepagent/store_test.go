package main

import (
	"context"
	"path/filepath"
	"testing"

	inprocess "eino-cli/deepagent/worker/inprocess"
)

func TestLocalStoreThreadStateUserIsolation(t *testing.T) {
	ctx := context.Background()
	store := newTestLocalStore(t)

	if _, err := store.threadState.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID:    1,
		SessionID: "sess_1",
		Profile:   inprocess.ThreadProfile{Cwd: "/repo"},
	}); err != nil {
		t.Fatalf("CreateThread(user 1) error = %v", err)
	}
	if _, err := store.threadState.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID:    2,
		SessionID: "sess_2",
		Profile:   inprocess.ThreadProfile{Cwd: "/repo"},
	}); err != nil {
		t.Fatalf("CreateThread(user 2) error = %v", err)
	}
	threads, err := store.threadState.ListThreads(ctx, inprocess.ListThreadsOptions{UserID: 1, Cwd: "/repo"})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].UserID != 1 {
		t.Fatalf("ListThreads() = %+v, want only user 1", threads)
	}
}

func newTestLocalStore(t *testing.T) *LocalStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewLocalStore(context.Background(), filepath.Join(dir, "agentthread.db"), filepath.Join(dir, "checkpoints"))
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	return store
}
