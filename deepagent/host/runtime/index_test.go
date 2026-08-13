package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkruntime "eino-cli/deepagent/runtime"
)

func TestPersistentThreadIndexRoundTripAndRuntimeCollision(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "threads.json")
	index, err := OpenPersistentThreadIndex(path)
	if err != nil {
		t.Fatalf("OpenPersistentThreadIndex() error = %v", err)
	}
	ctx := context.Background()
	for _, entry := range []sdkruntime.ThreadIndexEntry{
		{Ref: sdkruntime.GlobalThreadRef{Runtime: sdkruntime.RuntimeLocal, ThreadID: "same"}, Title: "local", UpdatedAtMS: 1},
		{Ref: sdkruntime.GlobalThreadRef{Runtime: sdkruntime.RuntimeRemote, ThreadID: "same"}, Title: "remote", UpdatedAtMS: 2, TimelineCursor: "event-2"},
	} {
		if err = index.Put(ctx, entry); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
	}
	reopened, err := OpenPersistentThreadIndex(path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	entries, err := reopened.List(ctx, sdkruntime.ThreadIndexQuery{})
	if err != nil || len(entries) != 2 || entries[0].Ref.Runtime != sdkruntime.RuntimeRemote {
		t.Fatalf("List() entries=%+v error=%v", entries, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, secret := range []string{"api_key", "DEEPSEEK_API_KEY", "token", "password"} {
		if strings.Contains(strings.ToLower(string(data)), strings.ToLower(secret)) {
			t.Fatalf("serialized index contains secret marker %q", secret)
		}
	}
}

func TestPersistentThreadIndexSkipsCorruptRecordAndUpgradesSchema(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "threads.json")
	data := []byte(`{"schema_version":0,"entries":[{"schema_version":0,"ref":{"runtime":"local","thread_id":"good"},"title":"kept"},"broken"]}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	index, err := OpenPersistentThreadIndex(path)
	if err != nil {
		t.Fatalf("OpenPersistentThreadIndex() error = %v", err)
	}
	entries, err := index.List(context.Background(), sdkruntime.ThreadIndexQuery{})
	if err != nil || len(entries) != 1 || entries[0].SchemaVersion != sdkruntime.ThreadIndexSchemaVersion {
		t.Fatalf("List() entries=%+v error=%v", entries, err)
	}
}
