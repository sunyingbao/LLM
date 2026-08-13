package web

import (
	"context"
	"testing"

	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebMiddleware_EnglishPromptAndSchema(t *testing.T) {
	t.Setenv(constant.PromptLangEnv, "en")

	ctx := context.Background()
	m := New(&WebConfig{
		EnableWebSearch:   true,
		EnableHTTPRequest: true,
		EnableFetchURL:    true,
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != constant.ToolWebSearch
		},
	})

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "## Web Tools")
	assert.NotContains(t, msgs[0].Content, "web_search")
	assert.False(t, containsHan(msgs[0].Content))

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	httpTool := findTool(t, tools, constant.ToolHTTPRequest)
	assert.False(t, schemaContainsHanDescription(toolJSONSchema(t, httpTool)))
}

func TestWebMiddleware_ToolMaskFiltersToolsAndPrompt(t *testing.T) {
	ctx := context.Background()
	m := New(&WebConfig{
		EnableWebSearch:   true,
		EnableHTTPRequest: true,
		EnableFetchURL:    true,
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != constant.ToolWebSearch
		},
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	names := toolNames(tools)
	assert.NotContains(t, names, constant.ToolWebSearch)
	assert.Contains(t, names, constant.ToolHTTPRequest)

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.NotContains(t, msgs[0].Content, "web_search")
	assert.Contains(t, msgs[0].Content, "http_request")
}

func TestWebMiddleware_ToolMaskAllToolsRemovesPrompt(t *testing.T) {
	ctx := context.Background()
	m := New(&WebConfig{
		EnableWebSearch:   true,
		EnableHTTPRequest: true,
		EnableFetchURL:    true,
		ToolMask: func(_ context.Context, _ *schema.ToolInfo) bool {
			return false
		},
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	assert.Empty(t, tools)

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	assert.Empty(t, msgs)
}
