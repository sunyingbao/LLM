package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"eino-cli/deepagent/backend/config"
	"eino-cli/deepagent/backend/consts"
	"eino-cli/deepagent/backend/sandbox"
	"eino-cli/deepagent/backend/sandboxpaths"
	"eino-cli/deepagent/core/middleware/filesystem"
	runtimecontext "eino-cli/deepagent/host/executioncontext"
)

func buildFilesystemTools(backend *WorkspaceBackend, disableExecute bool) (builtins []tool.BaseTool) {
	mw := filesystem.New(&filesystem.FilesystemConfig{
		Backend: backend, WorkDir: resolveRoot(),
		DisableUploadDownload: true, DisableApplyPatch: true, DisableExecute: disableExecute,
	})
	builtins, err := mw.Tools(context.Background())
	if err != nil {
		panic(err)
	}
	return builtins
}

// invoke is a small helper: every tool here is built via utils.InferTool
// which returns a tool.InvokableTool; we just need to JSON-marshal a Go map
// then call InvokableRun.
func invoke(t *testing.T, bt tool.BaseTool, args string) string {
	t.Helper()
	it, ok := bt.(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool is not InvokableTool")
	}
	out, err := it.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("tool invoke failed: %v", err)
	}
	return out
}

// invokeExpectErr returns the error message; used for negative cases.
func invokeExpectErr(t *testing.T, bt tool.BaseTool, args string) error {
	t.Helper()
	it := bt.(tool.InvokableTool)
	_, err := it.InvokableRun(context.Background(), args)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	return err
}

func invokeWithContext(t *testing.T, ctx context.Context, bt tool.BaseTool, args string) string {
	t.Helper()
	it, ok := bt.(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool is not InvokableTool")
	}
	out, err := it.InvokableRun(ctx, args)
	if err != nil {
		t.Fatalf("tool invoke failed: %v", err)
	}
	return out
}

func setToolRoot(t *testing.T, root string) {
	t.Helper()
	cleanup := config.SetRootDirForTest(root)
	t.Cleanup(cleanup)
}

type fakeSandboxManager struct {
	box           sandbox.Sandbox
	isolatedExec  bool
	acquireCalled *bool
	getCalled     *bool
}

func (m fakeSandboxManager) SessionID() string { return "session-a" }

func (m fakeSandboxManager) GetSandboxIdBySessionId(context.Context, string) (string, error) {
	if m.acquireCalled != nil {
		*m.acquireCalled = true
	}
	return "sandbox", nil
}
func (m fakeSandboxManager) Get(context.Context, string) (sandbox.Sandbox, error) {
	if m.getCalled != nil {
		*m.getCalled = true
	}
	return m.box, nil
}
func (m fakeSandboxManager) Release(context.Context, string) error { return nil }
func (m fakeSandboxManager) Reset()                                {}
func (m fakeSandboxManager) UsesSessionDataMounts() bool           { return true }
func (m fakeSandboxManager) AllowsIsolatedExec() bool              { return m.isolatedExec }

type fakeSandbox struct {
	command string
}

func (s *fakeSandbox) ID() string { return "sandbox" }

func (s *fakeSandbox) SessionID() string { return "session-a" }
func (s *fakeSandbox) ExecuteCommand(_ context.Context, command string) (string, error) {
	s.command = command
	return "sandbox: " + command, nil
}
func (s *fakeSandbox) ReadFile(context.Context, string) (string, error) { return "", nil }
func (s *fakeSandbox) WriteFile(context.Context, string, string, bool) error {
	return nil
}
func (s *fakeSandbox) UpdateFile(context.Context, string, []byte) error { return nil }
func (s *fakeSandbox) ListDir(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (s *fakeSandbox) Glob(context.Context, string, string, sandbox.GlobOpts) ([]string, bool, error) {
	return nil, false, nil
}
func (s *fakeSandbox) Grep(context.Context, string, string, sandbox.GrepOpts) ([]sandbox.GrepMatch, bool, error) {
	return nil, false, nil
}

func TestDeleteFile(t *testing.T) {
	root := t.TempDir()
	setToolRoot(t, root)
	path := filepath.Join(root, "old.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bt, _ := GetDeleteFileTool(nil)

	got := invoke(t, bt, `{"file_path":"old.txt"}`)
	if got != "Deleted file "+path {
		t.Fatalf("delete_file:\ngot:  %q", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
	if got := invoke(t, bt, `{"file_path":"missing.txt"}`); !strings.Contains(got, "File does not exist") {
		t.Fatalf("delete missing: %q", got)
	}
}

func TestDeleteFilePathResults(t *testing.T) {
	for _, useSandbox := range []bool{false, true} {
		name := "host"
		if useSandbox {
			name = "sandbox"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			setToolRoot(t, root)
			var manager sandbox.SandboxManager
			displayRoot := root
			if useSandbox {
				manager = fakeSandboxManager{}
				displayRoot = sandboxpaths.VirtualPathPrefixRepo
			}
			bt, err := GetDeleteFileTool(manager)
			if err != nil {
				t.Fatal(err)
			}
			if got := invoke(t, bt, `{"file_path":"missing.txt"}`); got != "File does not exist: "+filepath.Join(displayRoot, "missing.txt") {
				t.Fatalf("missing file result: %q", got)
			}
			if err := os.Mkdir(filepath.Join(root, "directory"), 0o755); err != nil {
				t.Fatal(err)
			}
			err = invokeExpectErr(t, bt, `{"file_path":"directory"}`)
			if !strings.Contains(err.Error(), "refusing to delete directory: "+filepath.Join(displayRoot, "directory")) {
				t.Fatalf("directory error: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "directory")); err != nil {
				t.Fatalf("directory was removed: %v", err)
			}
			path := filepath.Join(root, "file.txt")
			if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := invoke(t, bt, `{"file_path":"file.txt"}`); got != "Deleted file "+filepath.Join(displayRoot, "file.txt") {
				t.Fatalf("deleted file result: %q", got)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("file was not removed: %v", err)
			}
			invokeExpectErr(t, bt, `{"file_path":"../outside.txt"}`)
			invokeExpectErr(t, bt, `{"file_path":""}`)
		})
	}
}

func TestRgContent(t *testing.T) {
	root := t.TempDir()
	setToolRoot(t, root)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bt, _ := GetRgTool(nil)

	got := invoke(t, bt, `{"pattern":"hello","output_mode":"content"}`)
	if !strings.Contains(got, "a.txt:1:hello") {
		t.Fatalf("rg content: %q", got)
	}
	if got := invoke(t, bt, `{"pattern":"missing"}`); got != consts.NoMatchesFound {
		t.Fatalf("rg no match: %q", got)
	}
}

func TestExecuteDeniedWhenRollbackProtected(t *testing.T) {
	setToolRoot(t, t.TempDir())
	bt := builtinTool(t, nil, "execute")
	ctx := runtimecontext.WithRollbackProtected(context.Background(), true)
	got := invokeWithContext(t, ctx, bt, `{"command":"echo hi"}`)
	if !strings.Contains(got, "disabled in rollback-protected runs") {
		t.Fatalf("execute rollback denial: %q", got)
	}
}

func TestExecuteDeniedInNonIsolatedSandboxWhenRollbackProtected(t *testing.T) {
	box := &fakeSandbox{}
	getCalled := false
	bt := builtinTool(t, fakeSandboxManager{box: box, getCalled: &getCalled}, "execute")
	ctx := runtimecontext.WithSandboxID(context.Background(), "sandbox")
	ctx = runtimecontext.WithRollbackProtected(ctx, true)

	got := invokeWithContext(t, ctx, bt, `{"command":"echo hi"}`)
	if !strings.Contains(got, "disabled in rollback-protected runs") {
		t.Fatalf("execute rollback denial: %q", got)
	}
	if getCalled {
		t.Fatal("non-isolated sandbox should not be fetched before rollback denial")
	}
	if box.command != "" {
		t.Fatalf("non-isolated sandbox should not execute command, got %q", box.command)
	}
}

func TestExecuteAllowedInIsolatedSandboxWhenRollbackProtected(t *testing.T) {
	box := &fakeSandbox{}
	bt := builtinTool(t, fakeSandboxManager{box: box, isolatedExec: true}, "execute")
	ctx := runtimecontext.WithSandboxID(context.Background(), "sandbox")
	ctx = runtimecontext.WithRollbackProtected(ctx, true)

	got := invokeWithContext(t, ctx, bt, `{"command":"echo hi"}`)
	if !strings.Contains(got, "sandbox: echo hi") {
		t.Fatalf("execute should use aio sandbox, got %q", got)
	}
	if box.command != "echo hi" {
		t.Fatalf("sandbox command = %q", box.command)
	}
}

func TestExecuteAllowedInIsolatedSandboxWithoutStampedIDWhenRollbackProtected(t *testing.T) {
	box := &fakeSandbox{}
	acquireCalled := false
	bt := builtinTool(t, fakeSandboxManager{box: box, isolatedExec: true, acquireCalled: &acquireCalled}, "execute")
	ctx := runtimecontext.WithSessionID(context.Background(), "session-a")
	ctx = runtimecontext.WithRollbackProtected(ctx, true)

	got := invokeWithContext(t, ctx, bt, `{"command":"echo hi"}`)
	if !strings.Contains(got, "sandbox: echo hi") {
		t.Fatalf("execute should acquire aio sandbox, got %q", got)
	}
	if !acquireCalled {
		t.Fatal("execute should acquire sandbox when ctx has no sandbox id")
	}
	if box.command != "echo hi" {
		t.Fatalf("sandbox command = %q", box.command)
	}
}

func TestShellAndAwaitShell(t *testing.T) {
	root := t.TempDir()
	setToolRoot(t, root)
	bt, _ := GetShellTool(nil, nil)

	got := invoke(t, bt, `{"command":"echo hi","timeout_ms":1000}`)
	if !strings.HasPrefix(got, "hi") {
		t.Fatalf("shell echo: %q", got)
	}

	got = invoke(t, bt, `{"command":"printf ready; sleep 0.2","timeout_ms":1}`)
	if !strings.Contains(got, "task_id=") {
		t.Fatalf("shell background: %q", got)
	}
	taskID := strings.TrimSpace(strings.TrimPrefix(got, "Command is still running in background. task_id="))
	await, _ := GetAwaitShellTool()
	got = invoke(t, await, `{"task_id":"`+taskID+`","pattern":"ready","timeout_ms":1000}`)
	if !strings.Contains(got, "ready") {
		t.Fatalf("await_shell: %q", got)
	}
}

func TestShellDeniedWhenRollbackProtected(t *testing.T) {
	setToolRoot(t, t.TempDir())
	bt, _ := GetShellTool(nil, nil)
	ctx := runtimecontext.WithRollbackProtected(context.Background(), true)
	got := invokeWithContext(t, ctx, bt, `{"command":"echo hi","timeout_ms":1000}`)
	if !strings.Contains(got, "disabled in rollback-protected runs") {
		t.Fatalf("shell rollback denial: %q", got)
	}
}

func TestShellDeniedInNonIsolatedSandboxWhenRollbackProtected(t *testing.T) {
	box := &fakeSandbox{}
	getCalled := false
	bt, _ := GetShellTool(fakeSandboxManager{box: box, getCalled: &getCalled}, nil)
	ctx := runtimecontext.WithSandboxID(context.Background(), "sandbox")
	ctx = runtimecontext.WithRollbackProtected(ctx, true)

	got := invokeWithContext(t, ctx, bt, `{"command":"echo hi","timeout_ms":1000}`)
	if !strings.Contains(got, "disabled in rollback-protected runs") {
		t.Fatalf("shell rollback denial: %q", got)
	}
	if getCalled {
		t.Fatal("non-isolated sandbox should not be fetched before rollback denial")
	}
	if box.command != "" {
		t.Fatalf("non-isolated sandbox should not execute command, got %q", box.command)
	}
}

func TestShellAllowedInIsolatedSandboxWhenRollbackProtected(t *testing.T) {
	box := &fakeSandbox{}
	bt, _ := GetShellTool(fakeSandboxManager{box: box, isolatedExec: true}, nil)
	ctx := runtimecontext.WithSandboxID(context.Background(), "sandbox")
	ctx = runtimecontext.WithRollbackProtected(ctx, true)

	got := invokeWithContext(t, ctx, bt, `{"command":"echo hi","timeout_ms":1000}`)
	if !strings.Contains(got, "sandbox: echo hi") {
		t.Fatalf("shell should use aio sandbox, got %q", got)
	}
	if box.command != "echo hi" {
		t.Fatalf("sandbox command = %q", box.command)
	}
}

func TestShellAllowedInIsolatedSandboxWithoutStampedIDWhenRollbackProtected(t *testing.T) {
	box := &fakeSandbox{}
	acquireCalled := false
	bt, _ := GetShellTool(fakeSandboxManager{box: box, isolatedExec: true, acquireCalled: &acquireCalled}, nil)
	ctx := runtimecontext.WithSessionID(context.Background(), "session-a")
	ctx = runtimecontext.WithRollbackProtected(ctx, true)

	got := invokeWithContext(t, ctx, bt, `{"command":"echo hi","timeout_ms":1000}`)
	if !strings.Contains(got, "sandbox: echo hi") {
		t.Fatalf("shell should acquire aio sandbox, got %q", got)
	}
	if !acquireCalled {
		t.Fatal("shell should acquire sandbox when ctx has no sandbox id")
	}
	if box.command != "echo hi" {
		t.Fatalf("sandbox command = %q", box.command)
	}
}

func TestSemanticSearch(t *testing.T) {
	root := t.TempDir()
	setToolRoot(t, root)
	if err := os.WriteFile(filepath.Join(root, "tool.go"), []byte("func buildToolCall() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bt, _ := GetSemanticSearchTool(nil)

	got := invoke(t, bt, `{"query":"where is tool call built"}`)
	if !strings.Contains(got, "tool.go:1") {
		t.Fatalf("semantic_search: %q", got)
	}
}

func TestSemanticSearchMasksSandboxHostPaths(t *testing.T) {
	root := t.TempDir()
	setToolRoot(t, root)
	if err := os.WriteFile(filepath.Join(root, "tool.go"), []byte("func buildToolCall() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bt, _ := GetSemanticSearchTool(fakeSandboxManager{})
	ctx := runtimecontext.WithSessionID(context.Background(), "session-a")

	got := invokeWithContext(t, ctx, bt, `{"query":"where is tool call built"}`)
	if !strings.Contains(got, sandboxpaths.VirtualPathPrefixRepo+"/tool.go:1") {
		t.Fatalf("semantic_search should show virtual path: %q", got)
	}
	if strings.Contains(got, root) {
		t.Fatalf("semantic_search leaked host path: %q", got)
	}
}

func TestReadLintsTargets(t *testing.T) {
	root := t.TempDir()
	setToolRoot(t, root)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, unsupported, err := getLintTargets([]string{"README.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || len(unsupported) != 1 {
		t.Fatalf("lint targets: packages=%v unsupported=%v", got, unsupported)
	}
}

func TestAskClarificationTool(t *testing.T) {
	bt, err := GetAskClarificationTool()
	if err != nil {
		t.Fatal(err)
	}
	info, err := bt.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Name != consts.AskClarificationToolName {
		t.Fatalf("name: got %q want %q", info.Name, consts.AskClarificationToolName)
	}
	schema, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("ToJSONSchema: %v", err)
	}
	for _, name := range []string{"question", "clarification_type", "context", "options"} {
		if _, ok := schema.Properties.Get(name); !ok {
			t.Fatalf("schema missing %q", name)
		}
	}
	for _, name := range []string{"question", "clarification_type"} {
		if !containsString(schema.Required, name) {
			t.Fatalf("schema required missing %q: %v", name, schema.Required)
		}
	}

	got := invoke(t, bt, `{"question":"Which environment?","clarification_type":"approach_choice","options":["dev","prod"]}`)
	if got != "Clarification request processed by middleware" {
		t.Fatalf("clarification fallback output: %q", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func quoteJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBuildExtensionToolsCount(t *testing.T) {
	cleanup := config.SetRootDirForTest(t.TempDir())
	defer cleanup()
	got := BuildExtensionTools(&config.Config{}, nil)
	if len(got) != 7 {
		t.Fatalf("BuildExtensionTools: got %d tools, want 7", len(got))
	}
	// Names should match eino's expected wire identifiers exactly.
	want := []string{
		"ask_clarification", "delete_file", "rg", "semantic_search", "read_lints", "shell", "await_shell",
	}
	for i, bt := range got {
		info, err := bt.Info(context.Background())
		if err != nil {
			t.Fatalf("tool[%d].Info: %v", i, err)
		}
		if info.Name != want[i] {
			t.Fatalf("tool[%d] name: got %q want %q", i, info.Name, want[i])
		}
	}
}

func builtinTool(t *testing.T, manager sandbox.SandboxManager, name string) (base tool.BaseTool) {
	t.Helper()
	for _, candidate := range buildFilesystemTools(NewWorkspaceBackend(manager), false) {
		info, err := candidate.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name == name {
			return candidate
		}
	}
	t.Fatalf("tool %q not registered", name)
	return nil
}
