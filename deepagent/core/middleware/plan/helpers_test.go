package plan

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func invokeTool(t *testing.T, base tool.BaseTool, payload string) string {
	t.Helper()
	invokable, ok := base.(tool.InvokableTool)
	require.Truef(t, ok, "tool %T is not invokable", base)
	result, err := invokable.InvokableRun(context.Background(), payload)
	require.NoError(t, err)
	return result
}

func findTool(t *testing.T, tools []tool.BaseTool, name string) tool.BaseTool {
	t.Helper()
	for _, candidate := range tools {
		info, _ := candidate.Info(context.Background())
		if info.Name == name {
			return candidate
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func toolInfo(t *testing.T, base tool.BaseTool) *schema.ToolInfo {
	t.Helper()
	info, err := base.Info(context.Background())
	require.NoError(t, err)
	return info
}
