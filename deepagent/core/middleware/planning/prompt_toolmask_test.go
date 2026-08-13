package planning

import (
	"context"
	"testing"

	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanningMiddleware_EnglishPromptToolDescAndSchema(t *testing.T) {
	t.Setenv(constant.PromptLangEnv, "en")

	ctx := context.Background()
	m := NewWithConfig(&PlanningConfig{
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != constant.ToolReadTodos
		},
	})

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "## Task Planning")
	assert.NotContains(t, msgs[0].Content, "read_todos")
	assert.False(t, containsHan(msgs[0].Content))

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	writeTool := findTool(t, tools, constant.ToolWriteTodos)
	assert.False(t, containsHan(toolInfo(t, writeTool).Desc))
	assert.False(t, schemaContainsHanDescription(toolJSONSchema(t, writeTool)))
}

func TestPlanningMiddleware_ToolMaskFiltersOwnToolsAndPrompt(t *testing.T) {
	ctx := context.Background()
	m := NewWithConfig(&PlanningConfig{
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != constant.ToolReadTodos
		},
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	assert.NotContains(t, toolNames(tools), constant.ToolReadTodos)

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.NotContains(t, msgs[0].Content, "read_todos")
	assert.Contains(t, msgs[0].Content, "write_todos")
}
