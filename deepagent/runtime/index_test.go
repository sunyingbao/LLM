package runtime

import (
	"context"
	"testing"
)

func TestMemoryThreadIndexPreservesRuntimeAndOrdering(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	index := NewMemoryThreadIndex()
	local := ThreadIndexEntry{SchemaVersion: ThreadIndexSchemaVersion, Ref: GlobalThreadRef{Runtime: RuntimeLocal, ThreadID: "same"}, Title: "local", UpdatedAtMS: 1}
	remote := ThreadIndexEntry{SchemaVersion: ThreadIndexSchemaVersion, Ref: GlobalThreadRef{Runtime: RuntimeRemote, ThreadID: "same"}, Title: "remote", UpdatedAtMS: 2}
	if err := index.Put(ctx, local); err != nil {
		t.Fatalf("Put(local) error = %v", err)
	}
	if err := index.Put(ctx, remote); err != nil {
		t.Fatalf("Put(remote) error = %v", err)
	}
	remote.Title = "remote updated"
	if err := index.Put(ctx, remote); err != nil {
		t.Fatalf("Put(remote update) error = %v", err)
	}

	got, err := index.Get(ctx, remote.Ref)
	if err != nil {
		t.Fatalf("Get(remote) error = %v", err)
	}
	if got.Ref.Runtime != RuntimeRemote || got.Title != "remote updated" {
		t.Fatalf("Get(remote) = %+v", got)
	}
	entries, err := index.List(ctx, ThreadIndexQuery{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 2 || entries[0].Ref.Runtime != RuntimeRemote || entries[1].Ref.Runtime != RuntimeLocal {
		t.Fatalf("List() = %+v", entries)
	}
}
