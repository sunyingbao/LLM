package patchtoolcalls

import (
	"testing"

	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func TestPatchDanglingToolCalls(t *testing.T) {
	middleware := New()
	require.Equal(t, constant.MiddlewarePatchToolCalls, middleware.Name())
	require.Nil(t, PatchDanglingToolCalls(nil))

	messages := []*schema.Message{
		schema.UserMessage("request"),
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{
				{ID: "answered", Function: schema.FunctionCall{Name: "read"}},
				{ID: "missing", Function: schema.FunctionCall{Name: "write"}},
			},
		},
		{Role: schema.Tool, ToolCallID: "answered", Content: "result"},
	}

	patched := PatchDanglingToolCalls(messages)
	require.Len(t, patched, 4)
	require.Equal(t, schema.Tool, patched[2].Role)
	require.Equal(t, "missing", patched[2].ToolCallID)
	require.Contains(t, patched[2].Content, "write")
	require.Contains(t, patched[2].Content, "missing")
	require.Same(t, messages[2], patched[3])
}

func TestPatchSingleMessage(t *testing.T) {
	userMessage := schema.UserMessage("request")
	require.Equal(t, []*schema.Message{userMessage}, PatchSingleMessage(userMessage, nil))

	assistantMessage := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{ID: "answered", Function: schema.FunctionCall{Name: "read"}},
			{ID: "missing", Function: schema.FunctionCall{Name: "write"}},
		},
	}
	patched := PatchSingleMessage(assistantMessage, map[string]bool{"answered": true})
	require.Len(t, patched, 2)
	require.Same(t, assistantMessage, patched[0])
	require.Equal(t, "missing", patched[1].ToolCallID)

	patched = PatchSingleMessage(assistantMessage, nil)
	require.Len(t, patched, 3)
}
