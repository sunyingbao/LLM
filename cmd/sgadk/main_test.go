package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eino-cli/backend/config"
	clientruntime "eino-cli/deepagent/host/runtime"
)

func TestParseFlagsRootPrefersFlag(t *testing.T) {
	t.Setenv("SGADK_ROOT", "from-env")
	root, err := parseFlags([]string{"--root", "from-flag"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	want, _ := filepath.Abs("from-flag")
	if root != want {
		t.Fatalf("root: got %q, want %q", root, want)
	}
}

func TestPrepareLegacyImportPromptsAndEnablesConfirmedImport(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".eino-cli", "sessions", "legacy-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(clientruntime.RuntimeEnvironmentVariable, "local")
	t.Setenv(clientruntime.LegacyImportEnvironmentVariable, "prompt")
	var output bytes.Buffer
	if err := prepareLegacyImport(root, strings.NewReader("yes\n"), &output); err != nil {
		t.Fatalf("prepareLegacyImport() error=%v", err)
	}
	if got := os.Getenv(clientruntime.LegacyImportEnvironmentVariable); got != "auto" {
		t.Fatalf("legacy import policy=%q", got)
	}
	if !strings.Contains(output.String(), "1 个旧 SGADK 会话") || !strings.Contains(output.String(), filepath.Join(root, ".eino-cli", "sessions")) {
		t.Fatalf("output=%q", output.String())
	}
}

func TestPrepareLegacyImportDefaultsToOff(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".eino-cli", "sessions", "legacy-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(clientruntime.RuntimeEnvironmentVariable, "local")
	t.Setenv(clientruntime.LegacyImportEnvironmentVariable, "prompt")
	if err := prepareLegacyImport(root, strings.NewReader("\n"), io.Discard); err != nil {
		t.Fatalf("prepareLegacyImport() error=%v", err)
	}
	if got := os.Getenv(clientruntime.LegacyImportEnvironmentVariable); got != "off" {
		t.Fatalf("legacy import policy=%q", got)
	}
}

func TestParseFlagsRootFallsBackToEnv(t *testing.T) {
	t.Setenv("SGADK_ROOT", "from-env")
	root, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	want, _ := filepath.Abs("from-env")
	if root != want {
		t.Fatalf("got %q, want %q", root, want)
	}
}

func TestParseFlagsRootFallsBackToWorkingDirectory(t *testing.T) {
	t.Setenv("SGADK_ROOT", "")
	root, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want, _ = filepath.Abs(want)
	if root != want {
		t.Fatalf("got %q, want %q", root, want)
	}
}

func TestBuildSandboxManagerDefaultsToLocal(t *testing.T) {
	manager, err := buildSandboxManager(&config.Config{}, "default_session_id")
	if err != nil {
		t.Fatalf("buildSandboxManager: %v", err)
	}
	if manager == nil {
		t.Fatal("manager is nil")
	}
}

func TestBuildSandboxManagerRejectsUnknownUse(t *testing.T) {
	_, err := buildSandboxManager(&config.Config{Sandbox: config.SandboxConfig{Use: "bad"}}, "default_session_id")
	if err == nil {
		t.Fatal("expected unknown sandbox.use error")
	}
	if !strings.Contains(err.Error(), "sandbox.use") {
		t.Fatalf("error should mention sandbox.use, got %v", err)
	}
}

func TestResetAgentMessagesLogClearsExistingFile(t *testing.T) {
	root := t.TempDir()
	restore := config.SetRootDirForTest(root)
	t.Cleanup(restore)

	path := config.AgentMessagesLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old messages"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := resetAgentMessagesLog(); err != nil {
		t.Fatalf("resetAgentMessagesLog: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("log should be empty after reset, got %q", data)
	}
}
