package sandbox

import (
	"context"
	"testing"

	"eino-cli/deepagent/backend/config"
	"github.com/stretchr/testify/require"
)

type managerStub struct {
	shutdown bool
}

func (m *managerStub) SessionID() (sessionID string) { return "session" }

func (m *managerStub) GetSandboxIdBySessionId(context.Context, string) (sandboxID string, err error) {
	return "sandbox", nil
}

func (m *managerStub) Get(context.Context, string) (sandbox Sandbox, err error) { return nil, nil }

func (m *managerStub) Release(context.Context, string) (err error) { return nil }

func (m *managerStub) Reset() {}

func (m *managerStub) UsesSessionDataMounts() (uses bool) { return false }

func (m *managerStub) AllowsIsolatedExec() (allows bool) { return false }

func (m *managerStub) Shutdown() { m.shutdown = true }

func TestSandboxErrors(t *testing.T) {
	plain := &baseErr{msg: "plain"}
	require.Equal(t, "plain", plain.Error())
	require.Equal(t, "plain", plain.Message())
	require.Nil(t, plain.Details())

	notFound := NewNotFoundError("sandbox")
	require.Equal(t, "sandbox", notFound.SandboxID)
	require.Contains(t, notFound.Error(), "sandbox_id=sandbox")
	require.Equal(t, map[string]any{"sandbox_id": "sandbox"}, notFound.Details())

	runtimeError := NewRuntimeError("runtime failed")
	require.Equal(t, "runtime failed", runtimeError.Error())

	command := NewCommandError("command failed", "", 2)
	require.Equal(t, 2, command.ExitCode)
	require.NotContains(t, command.Details(), "command")
	longCommand := NewCommandError("command failed", string(make([]byte, 101)), 3)
	require.Len(t, longCommand.Details()["command"], 100)
	require.Equal(t, "...", longCommand.Details()["command"].(string)[97:])

	fileError := NewFileError("file failed", "", "")
	require.Empty(t, fileError.Details())
	fileError = NewFileError("file failed", "/file", "read")
	require.Equal(t, "/file", fileError.Path)
	require.Equal(t, "read", fileError.Operation)

	permissionError := NewPermissionError("denied", "/file")
	require.Equal(t, "write", permissionError.Operation)
	missingError := NewFileNotFoundError("/missing")
	require.Equal(t, "file not found", missingError.Message())
	require.Equal(t, "read", missingError.Operation)

	require.Equal(t, "short", truncate("short", 10))
	require.Equal(t, "long...", truncate("long-value", 7))
}

func TestSandboxSecurity(t *testing.T) {
	require.True(t, UsesLocalSandboxManager(nil))
	require.False(t, IsHostBashAllowed(nil))

	cfg := &config.Config{}
	require.True(t, UsesLocalSandboxManager(cfg))
	require.False(t, IsHostBashAllowed(cfg))
	cfg.Sandbox.AllowHostBash = true
	require.True(t, IsHostBashAllowed(cfg))
	cfg.Sandbox.Use = "remote"
	require.False(t, UsesLocalSandboxManager(cfg))
	require.True(t, IsHostBashAllowed(cfg))
}

func TestDefaultManagerLifecycle(t *testing.T) {
	SetDefault(nil)
	require.Nil(t, Default())

	manager := &managerStub{}
	SetDefault(manager)
	require.Same(t, manager, Default())
	ShutdownDefault()
	require.True(t, manager.shutdown)
	require.Nil(t, Default())
	ShutdownDefault()
}
