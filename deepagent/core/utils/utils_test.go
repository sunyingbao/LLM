package utils

import (
	"context"
	"errors"
	"testing"

	"code.byted.org/gopkg/ctxvalues"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

type infoTool struct {
	info *schema.ToolInfo
	err  error
}

func (t *infoTool) Info(context.Context) (info *schema.ToolInfo, err error) {
	return t.info, t.err
}

func TestTokenCounters(t *testing.T) {
	message := &schema.Message{
		Role:    schema.Tool,
		Content: "12345678",
		ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{
			Name:      "1234",
			Arguments: "12345678",
		}}},
	}

	require.Equal(t, 4, CountToolContentTokens(message))
	require.Equal(t, 5, SimpleTokenCounter([]*schema.Message{message}))
	require.Equal(t, 2, EstimateTokens("12345678"))

	tools := []tool.BaseTool{
		&infoTool{err: errors.New("info failed")},
		&infoTool{info: &schema.ToolInfo{Name: "invalid", Extra: map[string]any{"channel": make(chan int)}}},
		&infoTool{info: &schema.ToolInfo{Name: "valid"}},
	}
	require.Equal(t, len(`{"name":"valid"}`)/4, EstimateToolDefinitionsTokens(context.Background(), tools))
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	withoutLogID := NewCtxWithLogID(ctx)
	_, exists := ctxvalues.LogID(withoutLogID)
	require.False(t, exists)

	withLogID := ctxvalues.SetLogID(ctx, "log-id")
	copied := NewCtxWithLogID(withLogID)
	logID, exists := ctxvalues.LogID(copied)
	require.True(t, exists)
	require.Equal(t, "log-id", logID)

	cancelCtx, cancel := NewCancelCtxWithLogID(withLogID)
	cancel()
	require.ErrorIs(t, cancelCtx.Err(), context.Canceled)

	func() {
		defer PanicGuard(context.Background())
		panic("expected panic")
	}()
	PanicGuard(context.Background())
}
