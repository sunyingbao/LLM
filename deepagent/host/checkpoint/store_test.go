package checkpoint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := NewStore(dir)

	payload, found, err := store.Get(ctx, "missing")
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, payload)

	require.NoError(t, store.Set(ctx, "checkpoint", []byte("payload")))
	payload, found, err = store.Get(ctx, "checkpoint")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("payload"), payload)
	require.Equal(t, filepath.Join(dir, "checkpoint.json"), store.path("checkpoint"))
}

func TestStoreFailures(t *testing.T) {
	ctx := context.Background()
	rootFile := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(rootFile, []byte("content"), 0o600))
	store := NewStore(filepath.Join(rootFile, "checkpoints"))

	err := store.Set(ctx, "checkpoint", []byte("payload"))
	require.ErrorContains(t, err, "create checkpoints directory")

	dir := t.TempDir()
	store = NewStore(dir)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "directory.json"), 0o700))
	_, _, err = store.Get(ctx, "directory")
	require.ErrorContains(t, err, "read checkpoint")

	require.NoError(t, os.Mkdir(filepath.Join(dir, "blocked.json"), 0o700))
	err = store.Set(ctx, "blocked", []byte("payload"))
	require.ErrorContains(t, err, "write checkpoint")
}
