package session

import (
	"context"
	"testing"

	"code.byted.org/gopkg/metainfo"
)

func TestWithUserEmailSetsPersistentMetainfo(t *testing.T) {
	ctx := WithUserEmail(context.Background(), " alice@example.com ")
	got, ok := metainfo.GetPersistentValue(ctx, userEmailMetaKey)
	if !ok || got != "alice@example.com" {
		t.Fatalf("email metainfo=%q ok=%t, want alice@example.com true", got, ok)
	}
}

func TestWithUserEmailEmptyNoop(t *testing.T) {
	ctx := WithUserEmail(context.Background(), " ")
	if got, ok := metainfo.GetPersistentValue(ctx, userEmailMetaKey); ok || got != "" {
		t.Fatalf("email metainfo=%q ok=%t, want empty false", got, ok)
	}
}

func TestUserEmailFromContextReadsPersistentAndTransientMetainfo(t *testing.T) {
	for _, ctx := range []context.Context{
		metainfo.WithPersistentValue(context.Background(), userEmailMetaKey, " alice@example.com "),
		metainfo.WithValue(context.Background(), userEmailMetaKey, " alice@example.com "),
	} {
		if got := userEmailFromContext(ctx); got != "alice@example.com" {
			t.Fatalf("email=%q, want alice@example.com", got)
		}
	}
}
