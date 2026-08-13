package runtimectx

import (
	"context"
	"testing"
)

func TestThreadInfoPresentMissingOverwrite(t *testing.T) {
	ctx := context.Background()

	if _, ok := ThreadIdentityFromContext(ctx); ok {
		t.Fatal("ThreadIdentityFromContext on bare ctx should report missing")
	}

	want := ThreadIdentity{ThreadID: "t1", SessionID: "s1", UserID: 7, Namespace: "ns", Env: "prod"}
	ctx = ContextWithThreadIdentity(ctx, want)
	got, ok := ThreadIdentityFromContext(ctx)
	if !ok {
		t.Fatal("ThreadIdentityFromContext should report present after set")
	}
	if got != want {
		t.Fatalf("ThreadIdentityFromContext=%+v, want %+v", got, want)
	}

	override := ThreadIdentity{ThreadID: "t2", SessionID: "s2"}
	ctx = ContextWithThreadIdentity(ctx, override)
	got, ok = ThreadIdentityFromContext(ctx)
	if !ok || got != override {
		t.Fatalf("ThreadIdentityFromContext after override=%+v ok=%v, want %+v", got, ok, override)
	}
}

func TestThreadInfoEmptyValueStillReportsPresent(t *testing.T) {
	ctx := ContextWithThreadIdentity(context.Background(), ThreadIdentity{})
	got, ok := ThreadIdentityFromContext(ctx)
	if !ok {
		t.Fatal("an explicitly set empty ThreadIdentity must report present")
	}
	if got != (ThreadIdentity{}) {
		t.Fatalf("ThreadIdentityFromContext=%+v, want zero value", got)
	}
}

func TestTurnInfoPresentMissingOverwrite(t *testing.T) {
	ctx := context.Background()

	if _, ok := TurnIdentityFromContext(ctx); ok {
		t.Fatal("TurnIdentityFromContext on bare ctx should report missing")
	}

	want := TurnIdentity{
		ThreadID:  "t1",
		TurnID:    "turn-1",
		MessageID: "message-1",
	}
	ctx = ContextWithTurnIdentity(ctx, want)
	got, ok := TurnIdentityFromContext(ctx)
	if !ok || got != want {
		t.Fatalf("TurnIdentityFromContext=%+v ok=%v, want %+v", got, ok, want)
	}

	override := TurnIdentity{ThreadID: "t1", TurnID: "turn-2", MessageID: "message-2"}
	ctx = ContextWithTurnIdentity(ctx, override)
	got, ok = TurnIdentityFromContext(ctx)
	if !ok || got != override {
		t.Fatalf("TurnIdentityFromContext after override=%+v ok=%v, want %+v", got, ok, override)
	}
}

func TestThreadAndTurnInfoAreIndependentKeys(t *testing.T) {
	ctx := ContextWithThreadIdentity(context.Background(), ThreadIdentity{ThreadID: "t1"})
	if _, ok := TurnIdentityFromContext(ctx); ok {
		t.Fatal("setting thread info must not produce turn info")
	}
	ctx = ContextWithTurnIdentity(ctx, TurnIdentity{TurnID: "turn-1"})
	if ti, ok := ThreadIdentityFromContext(ctx); !ok || ti.ThreadID != "t1" {
		t.Fatalf("thread info lost after setting turn info: %+v ok=%v", ti, ok)
	}
}
