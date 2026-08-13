package filesystem

import (
	"context"
	"testing"

	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilesystemMiddleware_ToolMaskFiltersToolsAndPrompt(t *testing.T) {
	ctx := context.Background()
	m := New(&FilesystemConfig{
		Backend: newFilesystemTestBackend(t, nil),
		WorkDir: "/workspace",
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != constant.ToolReadFile
		},
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	names := toolNames(tools)
	assert.NotContains(t, names, constant.ToolReadFile)
	assert.Contains(t, names, constant.ToolLs)

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.NotContains(t, msgs[0].Content, "read_file")
	assert.Contains(t, msgs[0].Content, "ls")
}

func TestFilesystemMiddleware_ToolMaskFiltersExecutePrompt(t *testing.T) {
	ctx := context.Background()
	m := New(&FilesystemConfig{
		Backend: &mockSandboxBackend{FilesystemBackend: newFilesystemTestBackend(t, nil)},
		WorkDir: "/workspace",
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != constant.ToolExecute
		},
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	assert.NotContains(t, toolNames(tools), constant.ToolExecute)

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.NotContains(t, msgs[0].Content, "execute")
}
