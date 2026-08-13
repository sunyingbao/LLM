// Package runtimectx carries stable CloudAgent thread and turn identity through
// the DeepAgent run context. Business middleware, tool wrappers, model
// callbacks and prompt builders can read this identity from ctx instead of
// threading it through closures, globals or message metadata.
//
// This package is value-only. It does not import CloudAgent worker config, the
// thread runtime, agentworker, agentthread, AC thrift or Eino types, so it can
// be imported from any runtime execution code without creating a reverse
// dependency.
package runtimectx

import "context"

// ThreadIdentity is the stable identity of one CloudAgent thread. It only holds
// plain values resolved from the CloudAgent thread spec; it never carries the
// raw AC thread struct, worker runtime objects, profile, cwd, metadata, UI
// fields or business extension fields.
type ThreadIdentity struct {
	ThreadID  string
	SessionID string
	UserID    int64
	Namespace string
	Env       string
}

// TurnIdentity is the stable identity of one CloudAgent turn runner execution.
// MessageID identifies the worker message that started or resumed this run so
// observability integrations can correlate a runtime execution with its input.
type TurnIdentity struct {
	ThreadID  string
	TurnID    string
	MessageID string
}

type threadInfoKey struct{}

type turnInfoKey struct{}

// ContextWithThreadIdentity returns a child context carrying the thread info value.
func ContextWithThreadIdentity(ctx context.Context, info ThreadIdentity) context.Context {
	return context.WithValue(ctx, threadInfoKey{}, info)
}

// ThreadIdentityFromContext reports the thread info attached to ctx. The bool is false when
// no thread info was attached, so an empty value is not mistaken for a real one.
func ThreadIdentityFromContext(ctx context.Context) (ThreadIdentity, bool) {
	info, ok := ctx.Value(threadInfoKey{}).(ThreadIdentity)
	return info, ok
}

// ContextWithTurnIdentity returns a child context carrying the turn info value.
func ContextWithTurnIdentity(ctx context.Context, info TurnIdentity) context.Context {
	return context.WithValue(ctx, turnInfoKey{}, info)
}

// TurnIdentityFromContext reports the turn info attached to ctx. The bool is false when no
// turn info was attached, so an empty value is not mistaken for a real one.
func TurnIdentityFromContext(ctx context.Context) (TurnIdentity, bool) {
	info, ok := ctx.Value(turnInfoKey{}).(TurnIdentity)
	return info, ok
}
