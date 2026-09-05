package backends

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingStore struct {
	getErr    error
	setErr    error
	deleteErr error
	listErr   error
	existsErr error
	closeErr  error
}

func (s *failingStore) Get(context.Context, string) (value []byte, err error) {
	return nil, s.getErr
}

func (s *failingStore) Set(context.Context, string, []byte) (err error) {
	return s.setErr
}

func (s *failingStore) Delete(context.Context, string) (err error) {
	return s.deleteErr
}

func (s *failingStore) List(context.Context, string) (keys []string, err error) {
	return nil, s.listErr
}

func (s *failingStore) Exists(context.Context, string) (exists bool, err error) {
	return false, s.existsErr
}

func (s *failingStore) Close() (err error) {
	return s.closeErr
}

func TestMemoryStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	require.Equal(t, 0, store.Size())
	require.NoError(t, store.Set(ctx, "prefix:key", []byte("value")))
	require.Equal(t, 1, store.Size())

	value, err := store.Get(ctx, "prefix:key")
	require.NoError(t, err)
	require.Equal(t, []byte("value"), value)
	value[0] = 'X'

	storedValue, err := store.Get(ctx, "prefix:key")
	require.NoError(t, err)
	require.Equal(t, []byte("value"), storedValue)

	exists, err := store.Exists(ctx, "prefix:key")
	require.NoError(t, err)
	require.True(t, exists)

	keys, err := store.List(ctx, "prefix:")
	require.NoError(t, err)
	require.Equal(t, []string{"prefix:key"}, keys)

	require.NoError(t, store.Delete(ctx, "prefix:key"))
	_, err = store.Get(ctx, "prefix:key")
	require.ErrorContains(t, err, "key not found")

	require.NoError(t, store.Set(ctx, "one", []byte("1")))
	store.Clear()
	require.Equal(t, 0, store.Size())
	exists, err = store.Exists(ctx, "one")
	require.NoError(t, err)
	require.False(t, exists)
	require.NoError(t, store.Close())
}

func TestFileStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	rootDir := t.TempDir()
	store, err := NewFileStore(&FileStoreConfig{RootDir: rootDir})
	require.NoError(t, err)
	require.Equal(t, rootDir, store.RootDir())
	require.Equal(t, filepath.Join(rootDir, "scope_key"), store.keyToPath("scope:key"))

	require.NoError(t, store.Set(ctx, "scope:key", []byte("value")))
	value, err := store.Get(ctx, "scope:key")
	require.NoError(t, err)
	require.Equal(t, []byte("value"), value)

	exists, err := store.Exists(ctx, "scope:key")
	require.NoError(t, err)
	require.True(t, exists)

	keys, err := store.List(ctx, "scope:")
	require.NoError(t, err)
	require.Equal(t, []string{"scope:key"}, keys)

	require.NoError(t, store.Set(ctx, "nested/item", []byte("nested")))
	keys, err = store.List(ctx, "nested")
	require.NoError(t, err)
	require.Equal(t, []string{"nested/item"}, keys)

	require.NoError(t, store.Delete(ctx, "scope:key"))
	require.NoError(t, store.Delete(ctx, "scope:key"))
	_, err = store.Get(ctx, "scope:key")
	require.ErrorContains(t, err, "key not found")
	exists, err = store.Exists(ctx, "scope:key")
	require.NoError(t, err)
	require.False(t, exists)

	require.NoError(t, store.Clear())
	entries, err := os.ReadDir(rootDir)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NoError(t, store.Close())
}

func TestFileStoreDefaultsAndFailures(t *testing.T) {
	ctx := context.Background()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	store, err := NewFileStore(nil)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(homeDir, ".deepagents", "store"), store.RootDir())

	rootFile := filepath.Join(t.TempDir(), "root-file")
	require.NoError(t, os.WriteFile(rootFile, []byte("file"), 0o600))
	_, err = NewFileStore(&FileStoreConfig{RootDir: filepath.Join(rootFile, "child")})
	require.ErrorContains(t, err, "failed to create store dir")

	rootDir := t.TempDir()
	store, err = NewFileStore(&FileStoreConfig{RootDir: rootDir})
	require.NoError(t, err)

	require.NoError(t, os.Mkdir(filepath.Join(rootDir, "directory"), 0o700))
	_, err = store.Get(ctx, "directory")
	require.ErrorContains(t, err, "failed to read key")
	err = store.Set(ctx, "directory", []byte("value"))
	require.ErrorContains(t, err, "failed to write key")

	blocker := filepath.Join(rootDir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("file"), 0o600))
	err = store.Set(ctx, "blocker/child", []byte("value"))
	require.ErrorContains(t, err, "failed to create dir")

	require.NoError(t, os.WriteFile(filepath.Join(rootDir, "directory", "child"), []byte("value"), 0o600))
	err = store.Delete(ctx, "directory")
	require.ErrorContains(t, err, "failed to delete key")

	loopPath := filepath.Join(rootDir, "loop")
	require.NoError(t, os.Symlink("loop", loopPath))
	_, err = store.Exists(ctx, "loop")
	require.ErrorContains(t, err, "failed to check key")

	require.NoError(t, os.RemoveAll(rootDir))
	keys, err := store.List(ctx, "")
	require.NoError(t, err)
	require.Empty(t, keys)
	err = store.Clear()
	require.ErrorContains(t, err, "failed to read store dir")
}

func TestStoreBackendFileOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	backend := NewStoreBackend(&StoreBackendConfig{
		Store:       store,
		AssistantID: "assistant",
		Namespace:   "workspace",
	})

	require.Equal(t, "assistant/workspace/", backend.KeyPrefix())
	require.Same(t, store, backend.Store())

	writeResult, err := backend.Write(ctx, "docs/readme.txt", "first\nsecond\nthird")
	require.NoError(t, err)
	require.Equal(t, "docs/readme.txt", writeResult.Path)
	require.Empty(t, writeResult.Error)

	content, err := backend.Read(ctx, "/docs/readme.txt", nil, nil)
	require.NoError(t, err)
	require.Equal(t, "first\nsecond\nthird", content)

	offset, limit := 1, 1
	content, err = backend.Read(ctx, "docs/readme.txt", &offset, &limit)
	require.NoError(t, err)
	require.Equal(t, "second", content)

	offset = 10
	content, err = backend.Read(ctx, "docs/readme.txt", &offset, nil)
	require.NoError(t, err)
	require.Empty(t, content)

	editResult, err := backend.Edit(ctx, "docs/readme.txt", "second", "updated", false)
	require.NoError(t, err)
	require.Equal(t, 1, editResult.Occurrences)

	editResult, err = backend.Edit(ctx, "docs/readme.txt", "missing", "unused", false)
	require.NoError(t, err)
	require.Zero(t, editResult.Occurrences)

	_, err = backend.Write(ctx, "docs/notes.md", "match\nmatch")
	require.NoError(t, err)
	editResult, err = backend.Edit(ctx, "docs/notes.md", "match", "done", true)
	require.NoError(t, err)
	require.Equal(t, 2, editResult.Occurrences)

	_, err = backend.Write(ctx, "docs/nested/item.txt", "needle")
	require.NoError(t, err)

	paths, err := backend.Ls(ctx, "docs")
	require.NoError(t, err)
	sort.Strings(paths)
	require.Equal(t, []string{"/docs/nested", "/docs/notes.md", "/docs/readme.txt"}, paths)

	infos, err := backend.LsInfo(ctx, "/docs")
	require.NoError(t, err)
	require.Len(t, infos, 3)

	paths, err = backend.Glob(ctx, "*.txt", "/docs")
	require.NoError(t, err)
	sort.Strings(paths)
	require.Equal(t, []string{"/docs/nested/item.txt", "/docs/readme.txt"}, paths)

	infos, err = backend.GlobInfo(ctx, "*.md", "/docs")
	require.NoError(t, err)
	require.Len(t, infos, 1)
	require.Equal(t, "/docs/notes.md", infos[0].Path)
	require.False(t, infos[0].IsDir)

	matches, err := backend.GrepRaw(ctx, "needle", "/docs", "*.txt")
	require.NoError(t, err)
	require.Equal(t, []*GrepMatch{{Path: "/docs/nested/item.txt", Line: 1, Text: "needle"}}, matches)

	matches, err = backend.GrepRaw(ctx, "done", "/docs/notes.md", "")
	require.NoError(t, err)
	require.Len(t, matches, 2)

	require.NoError(t, backend.Close())
}

func TestStoreBackendFailurePaths(t *testing.T) {
	ctx := context.Background()
	backend := NewStoreBackend(nil)

	require.Empty(t, backend.KeyPrefix())
	_, err := backend.Read(ctx, "file", nil, nil)
	require.ErrorContains(t, err, "store not configured")
	writeResult, err := backend.Write(ctx, "file", "content")
	require.NoError(t, err)
	require.Equal(t, ErrInvalidPath, writeResult.Error)
	_, err = backend.Ls(ctx, "/")
	require.ErrorContains(t, err, "store not configured")
	_, err = backend.Glob(ctx, "*", "/")
	require.ErrorContains(t, err, "store not configured")
	_, err = backend.GrepRaw(ctx, "text", "/", "")
	require.ErrorContains(t, err, "store not configured")
	require.NoError(t, backend.Close())

	storeError := errors.New("store failed")
	failing := &failingStore{
		getErr:   storeError,
		setErr:   storeError,
		listErr:  storeError,
		closeErr: storeError,
	}
	backend.SetStore(failing)

	_, err = backend.Read(ctx, "file", nil, nil)
	require.ErrorIs(t, err, storeError)
	writeResult, err = backend.Write(ctx, "file", "content")
	require.NoError(t, err)
	require.Equal(t, ErrPermissionDenied, writeResult.Error)
	editResult, err := backend.Edit(ctx, "file", "old", "new", false)
	require.NoError(t, err)
	require.Equal(t, ErrFileNotFound, editResult.Error)
	_, err = backend.Ls(ctx, "/")
	require.ErrorIs(t, err, storeError)
	_, err = backend.LsInfo(ctx, "/")
	require.ErrorIs(t, err, storeError)
	_, err = backend.Glob(ctx, "*", "/")
	require.ErrorIs(t, err, storeError)
	_, err = backend.GlobInfo(ctx, "*", "/")
	require.ErrorIs(t, err, storeError)
	_, err = backend.GrepRaw(ctx, "text", "/", "*.txt")
	require.ErrorIs(t, err, storeError)
	_, err = backend.GrepRaw(ctx, "text", "/", "")
	require.ErrorIs(t, err, storeError)
	require.ErrorIs(t, backend.Close(), storeError)
}

func TestStoreBackendMetadataAndMatchLimits(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	backend := NewStoreBackend(&StoreBackendConfig{Store: store})

	require.NoError(t, store.Set(ctx, backend.makeKey("/raw/child.txt"), []byte("content")))
	infos, err := backend.LsInfo(ctx, "/raw")
	require.NoError(t, err)
	require.Equal(t, []*FileInfo{{Path: "/raw/child.txt"}}, infos)

	require.NoError(t, store.Set(ctx, backend.makeMetaKey("/raw/child.txt"), []byte("not-json")))
	_, err = backend.getMeta(ctx, "/raw/child.txt")
	require.Error(t, err)

	invalidTime := time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	err = backend.setMeta(ctx, "/raw/invalid", &FileMetadata{UpdatedAt: invalidTime})
	require.Error(t, err)

	lines := strings.Repeat("match\n", 101)
	require.NoError(t, store.Set(ctx, backend.makeKey("/many.txt"), []byte(lines)))
	matches, err := backend.GrepRaw(ctx, "match", "/", "")
	require.NoError(t, err)
	require.Len(t, matches, 100)

	require.Equal(t, "files/clean.txt", backend.makeKey(filepath.Join("folder", "..", "clean.txt")))
	require.Equal(t, "meta/clean.txt", backend.makeMetaKey(filepath.Join("folder", "..", "clean.txt")))
	assert.NotNil(t, backend.config)
}
