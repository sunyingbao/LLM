package memory

import (
	"context"
	"strings"
	"testing"

	"eino-cli/deepagent/core/backends"
	"github.com/stretchr/testify/require"
)

func newFilesystemTestBackend(t *testing.T, files map[string]*backends.FileData) *backends.FilesystemBackend {
	t.Helper()
	backend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	for path, file := range files {
		content := ""
		if file != nil {
			content = strings.Join(file.Content, "\n")
		}
		_, err := backend.Write(context.Background(), path, content)
		require.NoError(t, err)
	}
	return backend
}
