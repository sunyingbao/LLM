package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"eino-cli/backend/cli/tui"
	backendconfig "eino-cli/backend/config"
	"eino-cli/backend/sandbox"
	"eino-cli/backend/sandbox/aio"
	"eino-cli/backend/sandbox/local"
	"eino-cli/backend/session"
	clientruntime "eino-cli/deepagent/host/runtime"
)

// runUnifiedCLI starts the same session, sandbox, runtime, and TUI pipeline
// used by the repository-level sgadk command.
func runUnifiedCLI(ctx context.Context, cfg *backendconfig.Config) (err error) {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	sessionID, err := session.StartSession(ctx)
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	sandboxManager, err := buildSandboxManager(cfg, sessionID)
	if err != nil {
		return fmt.Errorf("build sandbox manager: %w", err)
	}
	sandbox.SetDefault(sandboxManager)
	defer sandbox.ShutdownDefault()
	if err := resetAgentMessagesLog(); err != nil {
		return fmt.Errorf("reset agent messages log: %w", err)
	}
	runtime, err := clientruntime.NewInteractiveRuntime(ctx, cfg, sessionID)
	if err != nil {
		return fmt.Errorf("build runtime: %w", err)
	}
	if err := tui.Run(runtime, sessionID, cfg); err != nil {
		return err
	}
	return nil
}

func buildSandboxManager(cfg *backendconfig.Config, sessionID string) (manager sandbox.SandboxManager, err error) {
	use := ""
	if cfg != nil {
		use = strings.TrimSpace(cfg.Sandbox.Use)
	}
	switch use {
	case "", "local":
		manager, err = local.New(sessionID)
		return manager, err
	case "aio":
		manager, err = aio.New(cfg, sessionID)
		return manager, err
	default:
		return nil, fmt.Errorf("sandbox: unknown sandbox.use %q (allowed: local, aio)", use)
	}
}

// resetAgentMessagesLog keeps each interactive session's transcript isolated.
func resetAgentMessagesLog() (err error) {
	path := backendconfig.AgentMessagesLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0o644)
}
