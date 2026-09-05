package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"eino-cli/deepagent/backend/config"
	"eino-cli/deepagent/backend/consts"
	"github.com/stretchr/testify/require"
)

func TestStartSession(t *testing.T) {
	rootDir := t.TempDir()
	restore := config.SetRootDirForTest(rootDir)
	t.Cleanup(restore)

	sessionID, err := StartSession(context.Background())
	require.NoError(t, err)
	require.Equal(t, consts.DefaultSessionID, sessionID)
	require.DirExists(t, config.SandboxWorkDir(sessionID))

	rootFile := filepath.Join(t.TempDir(), "root-file")
	require.NoError(t, os.WriteFile(rootFile, []byte("file"), 0o600))
	restore()
	restore = config.SetRootDirForTest(rootFile)
	t.Cleanup(restore)
	_, err = StartSession(context.Background())
	require.ErrorContains(t, err, "session: ensure dirs")
}
