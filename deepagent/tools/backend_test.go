package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eino-cli/deepagent/backend/consts"
	"eino-cli/deepagent/backend/sandbox"
	"eino-cli/deepagent/core/backends"
	runtimecontext "eino-cli/deepagent/host/executioncontext"
)

type recordingSandbox struct {
	fakeSandbox
	files                           map[string]string
	readPath, writePath, searchPath string
	failSearch                      bool
}

func (s *recordingSandbox) ReadFile(ctx context.Context, path string) (content string, err error) {
	s.readPath = path
	return s.files[path], nil
}

func (s *recordingSandbox) WriteFile(ctx context.Context, path, content string, appendMode bool) (err error) {
	s.writePath = path
	s.files[path] = content
	return nil
}

func (s *recordingSandbox) Grep(ctx context.Context, path, pattern string, opts sandbox.GrepOpts) (matches []sandbox.GrepMatch, truncated bool, err error) {
	s.searchPath = path
	if s.failSearch {
		return nil, false, fmt.Errorf("sandbox search unavailable")
	}
	return []sandbox.GrepMatch{{Path: path + "/remote.txt", LineNumber: 2, Line: "remote match"}}, false, nil
}

type unavailableSandboxManager struct{ fakeSandboxManager }

func (m unavailableSandboxManager) Get(ctx context.Context, id string) (box sandbox.Sandbox, err error) {
	return nil, fmt.Errorf("sandbox unavailable")
}

func TestCoreFilesystemToolsUseCoreSchema(t *testing.T) {
	setToolRoot(t, t.TempDir())
	write := builtinTool(t, nil, "write_file")
	out := invoke(t, write, `{"path":"file.txt","content":"first\nsecond\n"}`)
	var result backends.CommonToolResult
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.Errmsg != "" {
		t.Fatalf("write result %s: %v", out, err)
	}
	read := builtinTool(t, nil, "read_file")
	out = invoke(t, read, `{"path":"file.txt","offset":1,"limit":1}`)
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.Data != "second\n" {
		t.Fatalf("read result %s: %v", out, err)
	}
}

func TestWorkspaceBackendUsesSessionSandbox(t *testing.T) {
	root := t.TempDir()
	setToolRoot(t, root)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("host data"), 0o644); err != nil {
		t.Fatal(err)
	}
	box := &recordingSandbox{files: map[string]string{"/mnt/repo/file.txt": "first\nsecond\n"}}
	acquired := false
	backend := NewWorkspaceBackend(fakeSandboxManager{box: box, acquireCalled: &acquired})
	ctx := runtimecontext.WithSessionID(context.Background(), "session-a")
	offset, limit := 1, 1
	content, err := backend.Read(ctx, "file.txt", &offset, &limit)
	if err != nil || content != "second\n" || box.readPath != "/mnt/repo/file.txt" || !acquired {
		t.Fatalf("sandbox read %q, %q: %v", content, box.readPath, err)
	}
	content, err = backend.Read(ctx, filepath.Join(root, "file.txt"), &offset, &limit)
	if err != nil || content != "second\n" || box.readPath != "/mnt/repo/file.txt" {
		t.Fatalf("advertised workspace path must stay in sandbox: content=%q path=%q error=%v", content, box.readPath, err)
	}
	if _, err = backend.LsInfo(ctx, root); err != nil {
		t.Fatalf("initial directory tree cannot list workspace root: %v", err)
	}
	edit, err := backend.Edit(ctx, "file.txt", "second", "changed", false)
	if err != nil || edit.Occurrences != 1 || box.files["/mnt/repo/file.txt"] != "first\nchanged\n" {
		t.Fatalf("sandbox edit %#v: %v", edit, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "file.txt"))
	if err != nil || string(data) != "host data" {
		t.Fatalf("host file changed: %q, %v", data, err)
	}
	matches, err := backend.GrepRaw(ctx, "match", "/", "*.txt")
	if err != nil || len(matches) != 1 || matches[0].Line != 2 || box.searchPath != "/mnt/repo" {
		t.Fatalf("sandbox grep %#v: %v", matches, err)
	}
	box.failSearch = true
	if _, err := backend.GrepRaw(ctx, "host", "/", ""); err == nil || !strings.Contains(err.Error(), "sandbox search unavailable") {
		t.Fatalf("search silently fell back: %v", err)
	}
}

func TestWorkspaceBackendDoesNotFallbackOnSandboxFailure(t *testing.T) {
	root := t.TempDir()
	setToolRoot(t, root)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("host data"), 0o644); err != nil {
		t.Fatal(err)
	}
	backend := NewWorkspaceBackend(unavailableSandboxManager{})
	if _, err := backend.Read(context.Background(), "file.txt", nil, nil); err == nil || !strings.Contains(err.Error(), "sandbox unavailable") {
		t.Fatalf("read silently fell back: %v", err)
	}
	if _, err := backend.Write(context.Background(), "file.txt", "changed"); err == nil {
		t.Fatal("write silently fell back")
	}
	if _, err := backend.Execute(context.Background(), "echo host"); err == nil {
		t.Fatal("execute silently fell back")
	}
	data, err := os.ReadFile(filepath.Join(root, "file.txt"))
	if err != nil || string(data) != "host data" {
		t.Fatalf("host file changed: %q, %v", data, err)
	}
}

func TestWorkspaceBackendPlanAndSandboxPathGuards(t *testing.T) {
	setToolRoot(t, t.TempDir())
	box := &recordingSandbox{files: map[string]string{}}
	backend := NewWorkspaceBackend(fakeSandboxManager{box: box})
	ctx := runtimecontext.WithPermissionMode(context.Background(), consts.ModePlan)
	if _, err := backend.Write(ctx, "file.txt", "changed"); err == nil {
		t.Fatal("plan write allowed")
	}
	if _, err := backend.Edit(ctx, "file.txt", "a", "b", false); err == nil {
		t.Fatal("plan edit allowed")
	}
	if _, err := backend.Execute(ctx, "echo host"); err == nil {
		t.Fatal("plan execute allowed")
	}
	if box.writePath != "" || box.readPath != "" || box.command != "" {
		t.Fatal("plan action reached sandbox")
	}
	for _, path := range []string{"../outside.txt", "/mnt/skills/SKILL.md", "/etc/passwd"} {
		if _, err := backend.Write(context.Background(), path, "changed"); err == nil {
			t.Fatalf("unsafe write allowed: %s", path)
		}
	}
}
