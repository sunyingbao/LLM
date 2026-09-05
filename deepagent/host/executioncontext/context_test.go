package executioncontext

import (
	"context"
	"testing"

	"eino-cli/deepagent/backend/consts"
	"github.com/stretchr/testify/require"
)

func TestExecutionContextValues(t *testing.T) {
	ctx := context.Background()

	require.Equal(t, "", GetSessionID(ctx))
	require.Equal(t, "", GetSandboxID(ctx))
	require.Equal(t, QuerySourceMain, GetQuerySource(ctx))
	require.Equal(t, consts.ModeDefault, GetPermissionMode(ctx))
	require.False(t, IsRollbackProtected(ctx))

	ctx = WithSessionID(ctx, "session")
	ctx = WithSandboxID(ctx, "sandbox")
	ctx = WithQuerySource(ctx, QuerySource("background"))
	ctx = WithPermissionMode(ctx, consts.ModePlan)
	ctx = WithRollbackProtected(ctx, true)

	require.Equal(t, "session", GetSessionID(ctx))
	require.Equal(t, "sandbox", GetSandboxID(ctx))
	require.Equal(t, QuerySource("background"), GetQuerySource(ctx))
	require.Equal(t, consts.ModePlan, GetPermissionMode(ctx))
	require.True(t, IsRollbackProtected(ctx))
}

func TestPermissionModesAndRollbackState(t *testing.T) {
	for _, mode := range []PermissionMode{consts.ModeDefault, consts.ModeAcceptEdits, consts.ModePlan, consts.ModeBypass} {
		require.True(t, IsKnownMode(mode))
	}
	require.False(t, IsKnownMode(PermissionMode("unknown")))

	state := &RollbackPolicyState{}
	require.False(t, WasRollbackUnsafeToolBlocked(nil))
	require.False(t, WasRollbackUnsafeToolBlocked(state))
	MarkRollbackUnsafeToolBlocked(context.Background())
	MarkRollbackUnsafeToolBlocked(WithRollbackPolicyState(context.Background(), nil))
	MarkRollbackUnsafeToolBlocked(WithRollbackPolicyState(context.Background(), state))
	require.True(t, WasRollbackUnsafeToolBlocked(state))
}
