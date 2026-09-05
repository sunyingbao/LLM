//go:build !windows

package worker

import (
	"context"
	"strings"
	"testing"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/memory"
	"eino-cli/deepagent/core/middleware"

	"eino-cli/deepagent/coordinator"
)

func TestMemorySummaryMiddlewareBuildInitialContext(t *testing.T) {
	ctx := context.Background()
	workspace := memory.NewWorkspace(backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	}), "memory")
	if err := workspace.ForUser("1234").WriteConsolidated(ctx, "# Memory\n\n- user prefers real E2E.", "v1\n- real E2E over mocks"); err != nil {
		t.Fatal(err)
	}

	mw := (&threadBuilder{
		cfg:  Config{Memory: MemoryConfig{Enabled: true}},
		deps: Deps{MemoryWorkspace: workspace},
	}).memoryReadMiddleware(1234)
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

func TestMemorySummaryMiddlewareSkipsMissingSummary(t *testing.T) {
	ctx := context.Background()
	workspace := memory.NewWorkspace(backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	}), "memory")
	mw := (&threadBuilder{
		cfg:  Config{Memory: MemoryConfig{Enabled: true}},
		deps: Deps{MemoryWorkspace: workspace},
	}).memoryReadMiddleware(1234)
	msgs, err := mw.BuildInitialContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestPlanModeTurnConfigKeepsMemorySummaryMiddleware(t *testing.T) {
	ctx := context.Background()
	workspace := memory.NewWorkspace(backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	}), "memory")
	if err := workspace.ForUser("1234").WriteConsolidated(ctx, "# Memory\n\n- keep implementation mode memory.", "v1\n- implementation mode memory"); err != nil {
		t.Fatal(err)
	}

	builder := &threadBuilder{
		cfg: Config{
			Memory: MemoryConfig{Enabled: true},
			Turn:   testTurnConfig(nil),
		},
		deps: Deps{MemoryWorkspace: workspace},
	}
	turnProfile := mustBaseTurnProfile(t, builder)
	cfg := builder.applyPlanModeTurnConfig(ctx, &agentthread.TurnConfig{}, threadSpec{
		Info:    &coordinator.Thread{UserID: 1234},
		Profile: ResolvedThreadProfile{RoleID: DefaultRoleID},
	}, nil, turnProfile)

	msgs, err := middleware.NewMiddlewareChain(cfg.Agent.Middlewares...).BuildPrompts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 || !strings.Contains(msgs[0].Content, "implementation mode memory") {
		t.Fatalf("plan-mode initial context = %+v", msgs)
	}
}
