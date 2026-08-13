package main

import (
	"context"
	"testing"

	"code.byted.org/gopkg/metainfo"
)

func TestUserEmailFromContextReadsPersistentMetainfo(t *testing.T) {
	ctx := metainfo.WithPersistentValue(context.Background(), userEmailMetaKey, " alice@example.com ")
	if got := userEmailFromContext(ctx); got != "alice@example.com" {
		t.Fatalf("userEmailFromContext()=%q, want alice@example.com", got)
	}
}

func TestUserEmailFromContextReadsTransientMetainfo(t *testing.T) {
	ctx := metainfo.WithValue(context.Background(), userEmailMetaKey, " alice@example.com ")
	if got := userEmailFromContext(ctx); got != "alice@example.com" {
		t.Fatalf("userEmailFromContext()=%q, want alice@example.com", got)
	}
}
