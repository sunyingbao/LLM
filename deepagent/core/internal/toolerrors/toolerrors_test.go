package toolerrors

import (
	"context"
	"errors"
	"testing"

	"eino-cli/deepagent/core/checkpointer"
	"github.com/cloudwego/eino/compose"
	"github.com/stretchr/testify/require"
)

func TestToolErrors(t *testing.T) {
	require.False(t, ShouldReturnAsResult(nil))
	require.False(t, ShouldReturnAsResult(errors.New("runtime failure")))
	require.True(t, ShouldReturnAsResult(errors.New("[LocalFunc] failed to unmarshal arguments: invalid")))
	require.True(t, ShouldReturnAsResult(errors.New("[EnhancedLocalStreamFunc] type err: invalid")))
	require.Equal(t, "[Error] Tool invocation failed: runtime failure", Result(errors.New("runtime failure")))

	require.False(t, IsControlFlow(nil))
	require.False(t, IsControlFlow(errors.New("runtime failure")))
	require.True(t, IsControlFlow(errors.New("interrupt signal: approval")))
	require.True(t, IsControlFlow(compose.NewInterruptAndRerunErr("resume")))
	require.False(t, ShouldReturnAsResult(compose.NewInterruptAndRerunErr("resume")))
}

func TestWrappedInterruptIsControlFlow(t *testing.T) {
	ctx := context.Background()
	graph := compose.NewGraph[string, string]()
	require.NoError(t, graph.AddLambdaNode("interrupt", compose.InvokableLambda(func(ctx context.Context, input string) (output string, err error) {
		return "", compose.Interrupt(ctx, input)
	})))
	require.NoError(t, graph.AddEdge(compose.START, "interrupt"))
	require.NoError(t, graph.AddEdge("interrupt", compose.END))
	runnable, err := graph.Compile(ctx, compose.WithCheckPointStore(checkpointer.NewInMemoryStore()))
	require.NoError(t, err)

	_, err = runnable.Invoke(ctx, "approval", compose.WithCheckPointID("toolerrors"))
	require.Error(t, err)
	_, ok := compose.ExtractInterruptInfo(err)
	require.True(t, ok)
	require.True(t, IsControlFlow(err))
}
