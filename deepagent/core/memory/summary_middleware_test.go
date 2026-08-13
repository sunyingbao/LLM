package memory

import (
	"context"
	"strings"
	"testing"

	"eino-cli/deepagent/core/backends"
)

func TestSummaryMiddlewareBuildInitialContext(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace(backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	}), "memory")
	if err := workspace.ForUser("1234").WriteConsolidated(ctx, "# Memory\n\n- user prefers real E2E.", "v1\n- real E2E over mocks"); err != nil {
		t.Fatal(err)
	}

	mw := NewSummaryMiddleware(SummaryMiddlewareConfig{
		UserID:    "1234",
		Workspace: workspace,
	})
	msgs, err := mw.BuildInitialContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v", msgs)
	}
	if got := msgs[0].Content; !strings.Contains(got, "User Long-Term Memory") || !strings.Contains(got, "real E2E over mocks") {
		t.Fatalf("memory prompt = %q", got)
	}
}

func TestSummaryMiddlewareSkipsMissingSummary(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace(backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	}), "memory")
	mw := NewSummaryMiddleware(SummaryMiddlewareConfig{
		UserID:    "1234",
		Workspace: workspace,
	})
	msgs, err := mw.BuildInitialContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages = %+v", msgs)
	}
}
