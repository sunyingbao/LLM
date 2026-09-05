package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	cloudbackend "eino-cli/deepagent/cloud/backend"
	"eino-cli/deepagent/core/backends"
	"github.com/stretchr/testify/require"
)

func TestPathHelpers(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "", want: "."},
		{raw: ".", want: "."},
		{raw: "/", want: "."},
		{raw: "dir\\file.txt", want: "dir/file.txt"},
		{raw: "dir/./file.txt", want: "dir/file.txt"},
	}
	for _, test := range tests {
		got, err := cleanRelativePath(test.raw)
		require.NoError(t, err)
		require.Equal(t, test.want, got)
	}

	for _, raw := range []string{"/absolute", "~/secret", "../secret", "dir/../secret"} {
		_, err := cleanRelativePath(raw)
		require.Error(t, err)
	}

	require.Equal(t, ".", cleanSlashPath("/"))
	require.Equal(t, "dir/file", cleanSlashPath("/dir\\file"))
	require.Equal(t, ".", displayPath("/workspace", "/workspace"))
	require.Equal(t, "dir/file", displayPath("/workspace", "/workspace/dir/file"))
	require.Equal(t, "/other/file", displayPath("/workspace", "/other/file"))
}

func TestMediaType(t *testing.T) {
	require.Equal(t, "application/octet-stream", mediaType("file"))
	require.Contains(t, mediaType("file.json"), "application/json")
	require.Equal(t, "text/plain; charset=utf-8", mediaType("file.go"))
	require.Equal(t, "application/octet-stream", mediaType("file.unknown-extension"))
}

func TestRangeFileReadRange(t *testing.T) {
	ctx := context.Background()
	rootDir := t.TempDir()
	filePath := filepath.Join(rootDir, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("abcdef"), 0o600))
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: rootDir, VirtualMode: true})
	workspace := &cloudbackend.Workspace{Backend: backend, WorkDir: rootDir}
	cloudFile, err := cloudbackend.OpenRangeFile(ctx, workspace, "file.txt")
	require.NoError(t, err)
	file := &RangeFile{file: cloudFile}

	content, err := file.ReadRange(1, 3)
	require.NoError(t, err)
	require.Equal(t, []byte("bcd"), content)

	_, err = (*RangeFile)(nil).ReadRange(0, 1)
	require.Error(t, err)
	_, err = (&RangeFile{}).ReadRange(0, 1)
	require.Error(t, err)
	_, err = file.ReadRange(-1, 1)
	require.Error(t, err)

	require.NoError(t, os.Remove(filePath))
	_, err = file.ReadRange(0, 1)
	require.Error(t, err)
	var serviceErr interface{ Error() string }
	require.True(t, errors.As(err, &serviceErr))
}
