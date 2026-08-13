package executioncontext

import (
	stdctx "context"
	"sync/atomic"

	"eino-cli/backend/consts"
)

type PermissionMode = consts.PermissionMode
type QuerySource string

const (
	QuerySourceMain      QuerySource = "main"
	QuerySourceAutoDream QuerySource = "auto_dream"
)

type (
	sessionIDKey         struct{}
	sandboxIDKey         struct{}
	permissionModeKey    struct{}
	rollbackProtectedKey struct{}
	rollbackPolicyKey    struct{}
	querySourceKey       struct{}
)

type RollbackPolicyState struct {
	unsafeToolBlocked atomic.Bool
}

func IsKnownMode(mode PermissionMode) (known bool) {
	switch mode {
	case consts.ModeDefault, consts.ModeAcceptEdits, consts.ModePlan, consts.ModeBypass:
		known = true
	}
	return known
}

func WithSessionID(ctx stdctx.Context, sessionID string) (next stdctx.Context) {
	return stdctx.WithValue(ctx, sessionIDKey{}, sessionID)
}

func GetSessionID(ctx stdctx.Context) (sessionID string) {
	sessionID, _ = ctx.Value(sessionIDKey{}).(string)
	return sessionID
}

func WithSandboxID(ctx stdctx.Context, sandboxID string) (next stdctx.Context) {
	return stdctx.WithValue(ctx, sandboxIDKey{}, sandboxID)
}

func GetSandboxID(ctx stdctx.Context) (sandboxID string) {
	sandboxID, _ = ctx.Value(sandboxIDKey{}).(string)
	return sandboxID
}

func WithQuerySource(ctx stdctx.Context, source QuerySource) (next stdctx.Context) {
	return stdctx.WithValue(ctx, querySourceKey{}, source)
}

func GetQuerySource(ctx stdctx.Context) (source QuerySource) {
	source, _ = ctx.Value(querySourceKey{}).(QuerySource)
	if source == "" {
		source = QuerySourceMain
	}
	return source
}

func WithPermissionMode(ctx stdctx.Context, mode PermissionMode) (next stdctx.Context) {
	return stdctx.WithValue(ctx, permissionModeKey{}, mode)
}

func GetPermissionMode(ctx stdctx.Context) (mode PermissionMode) {
	mode, _ = ctx.Value(permissionModeKey{}).(PermissionMode)
	if mode == "" {
		mode = consts.ModeDefault
	}
	return mode
}

func WithRollbackProtected(ctx stdctx.Context, enabled bool) (next stdctx.Context) {
	return stdctx.WithValue(ctx, rollbackProtectedKey{}, enabled)
}

func IsRollbackProtected(ctx stdctx.Context) (enabled bool) {
	enabled, _ = ctx.Value(rollbackProtectedKey{}).(bool)
	return enabled
}

func WithRollbackPolicyState(ctx stdctx.Context, state *RollbackPolicyState) (next stdctx.Context) {
	return stdctx.WithValue(ctx, rollbackPolicyKey{}, state)
}

func MarkRollbackUnsafeToolBlocked(ctx stdctx.Context) {
	if state, ok := ctx.Value(rollbackPolicyKey{}).(*RollbackPolicyState); ok && state != nil {
		state.unsafeToolBlocked.Store(true)
	}
}

func WasRollbackUnsafeToolBlocked(state *RollbackPolicyState) (blocked bool) {
	return state != nil && state.unsafeToolBlocked.Load()
}
