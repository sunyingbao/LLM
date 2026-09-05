package changes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eino-cli/deepagent/core/backends"
)

func TestCollectReturnsTrackedAndUntrackedChanges(t *testing.T) {
	ctx := context.Background()
	backend, root := newGitBackend(t, ctx)
	writeTestFile(t, filepath.Join(root, "tracked.txt"), "old\n")
	runTestCommand(t, ctx, backend, root, "git add tracked.txt && git commit -m initial")
	writeTestFile(t, filepath.Join(root, "tracked.txt"), "new\nline\n")
	writeTestFile(t, filepath.Join(root, "new.txt"), "added\n")

	items, err := Collect(ctx, backend, root)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2: %+v", len(items), items)
	}
	if items[0].Path != "new.txt" || items[0].Status != "untracked" || items[0].Additions != 1 {
		t.Fatalf("items[0] = %+v", items[0])
	}
	if items[1].Path != "tracked.txt" || items[1].Status != "modified" || items[1].Additions != 2 || items[1].Deletions != 1 {
		t.Fatalf("items[1] = %+v", items[1])
	}
}

func TestCollectReturnsEmptyOutsideGitRepository(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: root, VirtualMode: true})

	items, err := Collect(ctx, backend, root)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %+v, want empty", items)
	}
}

func TestCollectDiffRejectsParentPath(t *testing.T) {
	_, err := CollectDiff(context.Background(), nil, "/workspace", "../secret")
	if err == nil {
		t.Fatal("CollectDiff() error = nil")
	}
}

func TestCollectDiffQuotesFileNameBeforeRunningGit(t *testing.T) {
	ctx := context.Background()
	backend, root := newGitBackend(t, ctx)
	path := "name;touch injected"
	writeTestFile(t, filepath.Join(root, path), "safe\n")

	diff, err := CollectDiff(ctx, backend, root, path)
	if err != nil {
		t.Fatalf("CollectDiff() error = %v", err)
	}
	if !strings.Contains(diff.Patch, "+safe") {
		t.Fatalf("Patch = %q", diff.Patch)
	}
	if _, err := os.Stat(filepath.Join(root, "injected")); !os.IsNotExist(err) {
		t.Fatalf("shell injection created a file: %v", err)
	}
}

func TestCollectDiffLimitsUntrackedContent(t *testing.T) {
	ctx := context.Background()
	backend, root := newGitBackend(t, ctx)
	writeTestFile(t, filepath.Join(root, "large.txt"), strings.Repeat("x", maxDiffBytes+1024))

	diff, err := CollectDiff(ctx, backend, root, "large.txt")
	if err != nil {
		t.Fatalf("CollectDiff() error = %v", err)
	}
	if !diff.Truncated {
		t.Fatal("Truncated = false")
	}
	if len(diff.Patch) > maxDiffBytes {
		t.Fatalf("len(Patch) = %d, want <= %d", len(diff.Patch), maxDiffBytes)
	}
}

func newGitBackend(t *testing.T, ctx context.Context) (backend *backends.SandboxFilesystemBackend, root string) {
	t.Helper()
	root = t.TempDir()
	backend = backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: root, VirtualMode: true})
	runTestCommand(t, ctx, backend, root, "git init && git config user.email test@example.com && git config user.name test")
	return backend, root
}

func runTestCommand(t *testing.T, ctx context.Context, backend backends.SandboxBackend, root, command string) {
	t.Helper()
	result, err := backend.ExecuteCommand(ctx, backends.CommandRequest{Command: command, WorkDir: root})
	if err != nil {
		t.Fatalf("ExecuteCommand(%q) error = %v", command, err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExecuteCommand(%q) exit = %d, output = %s", command, result.ExitCode, result.Output)
	}
	return
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return
}
