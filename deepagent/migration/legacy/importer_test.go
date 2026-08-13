package legacy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eino-cli/backend/session/runs"
	protoevent "eino-cli/deepagent/cloud/protocol/event"
)

type recordingDestination struct{ threads map[string]Thread }

func (destination *recordingDestination) ImportLegacyThread(ctx context.Context, thread Thread) (created bool, err error) {
	if destination.threads == nil {
		destination.threads = make(map[string]Thread)
	}
	if _, exists := destination.threads[thread.SourceSessionID]; exists {
		return false, nil
	}
	destination.threads[thread.SourceSessionID] = thread
	return true, nil
}

func TestImporterIsIdempotentIsolatesCorruptionAndLeavesSourcesUntouched(t *testing.T) {
	root := t.TempDir()
	legacyRoot := filepath.Join(root, ".eino-cli")
	created := time.Unix(100, 0).UTC()
	writeLegacyRun(t, legacyRoot, "session-1", runs.Record{ID: "run-complete", SessionID: "session-1", Status: "success", Prompt: "secret prompt", Output: "secret output", CreatedAt: created, UpdatedAt: created.Add(time.Second)})
	writeLegacyRun(t, legacyRoot, "session-1", runs.Record{ID: "run-failed", SessionID: "session-1", Status: "error", Error: "secret failure", CreatedAt: created.Add(2 * time.Second), UpdatedAt: created.Add(3 * time.Second)})
	writeLegacyRun(t, legacyRoot, "session-1", runs.Record{ID: "run-active", SessionID: "session-1", Status: "running", Prompt: "active prompt", CreatedAt: created.Add(4 * time.Second), UpdatedAt: created.Add(5 * time.Second)})
	corruptPath := filepath.Join(legacyRoot, "sessions", "session-1", "runs", "corrupt.json")
	if err := os.WriteFile(corruptPath, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := snapshotFiles(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	destination := &recordingDestination{}
	manifestPath := filepath.Join(root, "unified", "legacy-manifest.json")
	importer := &Importer{SourceRoot: legacyRoot, ManifestPath: manifestPath, Destination: destination}

	first, err := importer.Import(context.Background())
	if err != nil {
		t.Fatalf("Import(first) error=%v", err)
	}
	if first.Imported != 1 || first.Failed != 1 || first.Skipped != 0 {
		t.Fatalf("first report=%+v", first)
	}
	thread := destination.threads["session-1"]
	if !thread.Interrupted {
		t.Fatal("active legacy run was not marked interrupted")
	}
	assertEventOrder(t, thread, protoevent.EventTypeTurnStarted, protoevent.EventTypeAssistantMessage, protoevent.EventTypeTurnFinished, protoevent.EventTypeTurnStarted, protoevent.EventTypeError, protoevent.EventTypeTurnStarted, protoevent.EventTypeTurnInterrupted)

	second, err := importer.Import(context.Background())
	if err != nil {
		t.Fatalf("Import(second) error=%v", err)
	}
	if second.Imported != 0 || second.Skipped != 1 || second.Failed != 1 {
		t.Fatalf("second report=%+v", second)
	}
	after, err := snapshotFiles(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("legacy source files changed during import")
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret prompt", "secret output", "secret failure", "active prompt"} {
		if strings.Contains(string(manifest), secret) {
			t.Fatalf("manifest leaked source content %q", secret)
		}
	}
}

func writeLegacyRun(t *testing.T, root, sessionID string, record runs.Record) {
	t.Helper()
	dir := filepath.Join(root, "sessions", sessionID, "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, record.ID+".json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func snapshotFiles(root string) (payload []byte, err error) {
	entries := make(map[string]string)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		entries[relative] = string(body)
		return nil
	})
	if err != nil {
		return nil, err
	}
	payload, err = json.Marshal(entries)
	return payload, err
}

func assertEventOrder(t *testing.T, thread Thread, expected ...protoevent.EventType) {
	t.Helper()
	if len(thread.Events) != len(expected) {
		t.Fatalf("event count=%d want=%d: %+v", len(thread.Events), len(expected), thread.Events)
	}
	for index, eventType := range expected {
		if thread.Events[index].EventType != eventType.String() {
			t.Fatalf("event[%d]=%s want=%s", index, thread.Events[index].EventType, eventType)
		}
	}
}
