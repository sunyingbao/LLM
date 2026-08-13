package memory

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"eino-cli/deepagent/core/backends"
)

func TestMemoryMiddleware_BuildInitialContextUsesConfiguredSourcesInLearningGuide(t *testing.T) {
	ctx := context.Background()
	backend := newFilesystemTestBackend(t, map[string]*backends.FileData{
		"/memory/user.md": {
			Content:    []string{"remembered preference"},
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
	})

	m := New(&MemoryConfig{
		Backend:     backend,
		Sources:     []string{"/memory/user.md", "/memory/project.md"},
		EnableLearn: true,
	})

	require.NoError(t, m.BeforeAgent(ctx))

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	content := msgs[0].Content
	assert.Contains(t, content, "remembered preference")
	assert.Contains(t, content, "- /memory/user.md")
	assert.Contains(t, content, "- /memory/project.md")
	assert.NotContains(t, content, "AGENTS.md")
	assert.NotContains(t, content, "edit_file")
}

func TestMemoryMiddleware_BuildInitialContextOmitsLearningGuideWhenNoEditToolVisible(t *testing.T) {
	ctx := context.Background()
	backend := newFilesystemTestBackend(t, map[string]*backends.FileData{
		"/memory/user.md": {
			Content:    []string{"remembered preference"},
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
	})

	m := New(&MemoryConfig{
		Backend:     backend,
		Sources:     []string{"/memory/user.md"},
		EnableLearn: true,
		ToolMask: func(_ context.Context, _ *schema.ToolInfo) bool {
			return false
		},
	})

	require.NoError(t, m.BeforeAgent(ctx))

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.NotContains(t, msgs[0].Content, "学习指南")
}
