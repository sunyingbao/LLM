package checkpointer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryStore(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()

	// Test Set and Get
	t.Run("SetAndGet", func(t *testing.T) {
		data := []byte("test checkpoint data")
		err := store.Set(ctx, "test-id-1", data)
		require.NoError(t, err)

		got, existed, err := store.Get(ctx, "test-id-1")
		require.NoError(t, err)
		assert.True(t, existed)
		assert.Equal(t, data, got)
	})

	// Test Get non-existent
	t.Run("GetNonExistent", func(t *testing.T) {
		got, existed, err := store.Get(ctx, "non-existent")
		require.NoError(t, err)
		assert.False(t, existed)
		assert.Nil(t, got)
	})

	// Test data isolation (returned data should be a copy)
	t.Run("DataIsolation", func(t *testing.T) {
		data := []byte("original data")
		err := store.Set(ctx, "test-id-2", data)
		require.NoError(t, err)

		got, _, _ := store.Get(ctx, "test-id-2")
		got[0] = 'X' // Modify returned data

		got2, _, _ := store.Get(ctx, "test-id-2")
		assert.Equal(t, byte('o'), got2[0]) // Original should be unchanged
	})

	// Test Delete
	t.Run("Delete", func(t *testing.T) {
		data := []byte("to be deleted")
		_ = store.Set(ctx, "test-id-3", data)

		err := store.Delete(ctx, "test-id-3")
		require.NoError(t, err)

		_, existed, _ := store.Get(ctx, "test-id-3")
		assert.False(t, existed)
	})

	// Test List
	t.Run("List", func(t *testing.T) {
		store2 := NewInMemoryStore()
		_ = store2.Set(ctx, "id-a", []byte("a"))
		_ = store2.Set(ctx, "id-b", []byte("b"))
		_ = store2.Set(ctx, "id-c", []byte("c"))

		ids, err := store2.List(ctx)
		require.NoError(t, err)
		assert.Len(t, ids, 3)
		assert.Contains(t, ids, "id-a")
		assert.Contains(t, ids, "id-b")
		assert.Contains(t, ids, "id-c")
	})

	// Test Clear
	t.Run("Clear", func(t *testing.T) {
		store3 := NewInMemoryStore()
		_ = store3.Set(ctx, "id-1", []byte("1"))
		_ = store3.Set(ctx, "id-2", []byte("2"))

		err := store3.Clear(ctx)
		require.NoError(t, err)

		assert.Equal(t, 0, store3.Size())
	})

	// Test Size
	t.Run("Size", func(t *testing.T) {
		store4 := NewInMemoryStore()
		assert.Equal(t, 0, store4.Size())

		_ = store4.Set(ctx, "id-1", []byte("1"))
		assert.Equal(t, 1, store4.Size())

		_ = store4.Set(ctx, "id-2", []byte("2"))
		assert.Equal(t, 2, store4.Size())
	})
}

func TestFileStore(t *testing.T) {
	ctx := context.Background()

	// Create temp directory for tests
	tmpDir, err := os.MkdirTemp("", "checkpointer-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	store, err := NewFileStore(tmpDir)
	require.NoError(t, err)

	// Test Set and Get
	t.Run("SetAndGet", func(t *testing.T) {
		data := []byte("test checkpoint data for file store")
		err := store.Set(ctx, "file-test-1", data)
		require.NoError(t, err)

		got, existed, err := store.Get(ctx, "file-test-1")
		require.NoError(t, err)
		assert.True(t, existed)
		assert.Equal(t, data, got)

		// Verify file exists
		filePath := filepath.Join(tmpDir, "file-test-1.bin")
		_, err = os.Stat(filePath)
		assert.NoError(t, err)
	})

	// Test Get non-existent
	t.Run("GetNonExistent", func(t *testing.T) {
		got, existed, err := store.Get(ctx, "non-existent")
		require.NoError(t, err)
		assert.False(t, existed)
		assert.Nil(t, got)
	})

	// Test Delete
	t.Run("Delete", func(t *testing.T) {
		data := []byte("to be deleted")
		_ = store.Set(ctx, "file-test-2", data)

		err := store.Delete(ctx, "file-test-2")
		require.NoError(t, err)

		_, existed, _ := store.Get(ctx, "file-test-2")
		assert.False(t, existed)

		// Verify file is deleted
		filePath := filepath.Join(tmpDir, "file-test-2.bin")
		_, err = os.Stat(filePath)
		assert.True(t, os.IsNotExist(err))
	})

	// Test Delete non-existent (should not error)
	t.Run("DeleteNonExistent", func(t *testing.T) {
		err := store.Delete(ctx, "non-existent-delete")
		assert.NoError(t, err)
	})

	// Test List
	t.Run("List", func(t *testing.T) {
		tmpDir2, _ := os.MkdirTemp("", "checkpointer-list-test-*")
		defer os.RemoveAll(tmpDir2)

		store2, _ := NewFileStore(tmpDir2)
		_ = store2.Set(ctx, "list-a", []byte("a"))
		_ = store2.Set(ctx, "list-b", []byte("b"))
		_ = store2.Set(ctx, "list-c", []byte("c"))

		ids, err := store2.List(ctx)
		require.NoError(t, err)
		assert.Len(t, ids, 3)
		// Should be sorted
		assert.Equal(t, []string{"list-a", "list-b", "list-c"}, ids)
	})

	// Test Clear
	t.Run("Clear", func(t *testing.T) {
		tmpDir3, _ := os.MkdirTemp("", "checkpointer-clear-test-*")
		defer os.RemoveAll(tmpDir3)

		store3, _ := NewFileStore(tmpDir3)
		_ = store3.Set(ctx, "clear-1", []byte("1"))
		_ = store3.Set(ctx, "clear-2", []byte("2"))

		err := store3.Clear(ctx)
		require.NoError(t, err)

		ids, _ := store3.List(ctx)
		assert.Len(t, ids, 0)
	})

	// Test path traversal protection
	t.Run("PathTraversalProtection", func(t *testing.T) {
		// Attempt path traversal attack
		maliciousID := "../../../etc/passwd"
		data := []byte("malicious data")
		_ = store.Set(ctx, maliciousID, data)

		// The file should be created in the store directory, not elsewhere
		got, existed, _ := store.Get(ctx, maliciousID)
		if existed {
			assert.Equal(t, data, got)
		}

		// Verify no file was created outside the store directory
		outsidePath := filepath.Join(tmpDir, "..", "..", "..", "etc", "passwd.bin")
		_, err := os.Stat(outsidePath)
		assert.True(t, os.IsNotExist(err) || err != nil)
	})

	// Test Dir method
	t.Run("Dir", func(t *testing.T) {
		assert.Equal(t, tmpDir, store.Dir())
	})
}

func TestSessionHelpers(t *testing.T) {
	// Test GenerateThreadID
	t.Run("GenerateThreadID", func(t *testing.T) {
		id1 := GenerateThreadID()
		id2 := GenerateThreadID()

		assert.Len(t, id1, 8)
		assert.Len(t, id2, 8)
		assert.NotEqual(t, id1, id2)
	})

	// Test GenerateCheckpointID
	t.Run("GenerateCheckpointID", func(t *testing.T) {
		id := GenerateCheckpointID("abc12345", 1)
		assert.Equal(t, "abc12345-v1", id)

		id2 := GenerateCheckpointID("xyz99999", 42)
		assert.Equal(t, "xyz99999-v42", id2)
	})

	// Test GenerateUUID
	t.Run("GenerateUUID", func(t *testing.T) {
		uuid1 := GenerateUUID()
		uuid2 := GenerateUUID()

		assert.Len(t, uuid1, 36)
		assert.Len(t, uuid2, 36)
		assert.NotEqual(t, uuid1, uuid2)
	})
}

// TestExtendedStoreInterface verifies that both stores implement ExtendedStore
func TestExtendedStoreInterface(t *testing.T) {
	var _ ExtendedStore = (*InMemoryStore)(nil)
	var _ ExtendedStore = (*FileStore)(nil)
}
