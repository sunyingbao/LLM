package subagent

import (
	"context"
	"strings"
	"testing"

	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubAgentMiddleware_EnglishPromptToolDescSchemaAndParentMarkers(t *testing.T) {
	t.Setenv(constant.PromptLangEnv, "en")

	ctx := context.Background()
	m := New(&SubAgentConfig{
		SubAgents: []*SubAgent{{Name: "worker", Description: "does work"}},
		ContextInjector: &testContextInjector{
			messages: []*schema.Message{schema.UserMessage("parent fact")},
		},
	})

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "## Subagent Delegation")
	assert.False(t, containsHan(msgs[0].Content))

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	taskTool := findTool(t, tools, constant.ToolTask)
	assert.False(t, containsHan(toolInfo(t, taskTool).Desc))
	assert.False(t, schemaContainsHanDescription(toolJSONSchema(t, taskTool)))

	forked, err := m.buildSubAgentMessages(ctx, "worker", "", "run", true)
	require.NoError(t, err)
	require.Len(t, forked, 4)
	assert.False(t, containsHan(forked[0].Content))
	assert.False(t, containsHan(forked[2].Content))
}

func TestSubAgentMiddleware_ToolMaskFiltersOwnToolsAndPrompt(t *testing.T) {
	ctx := context.Background()
	m := New(&SubAgentConfig{
		SubAgents: []*SubAgent{{Name: "worker", Description: "does work"}},
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != constant.ToolTask
		},
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	names := toolNames(tools)
	assert.NotContains(t, names, constant.ToolTask)
	assert.Contains(t, names, constant.ToolListSubAgents)

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.NotContains(t, msgs[0].Content, "- task:")
	assert.Contains(t, msgs[0].Content, "list_subagents")
	assert.False(t, strings.Contains(msgs[0].Content, "可用的子代理："))
}
