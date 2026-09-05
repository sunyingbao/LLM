//go:build !windows

package backend

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/base"
	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/pippit/sandbox/gateway"
	"code.byted.org/overpass/pippit_sandbox_gateway/rpc/pippit_sandbox_gateway"
)

func TestResolveProjectLocal(t *testing.T) {
	project, err := ResolveProject(Config{
		Type:  TypeLocal,
		Local: LocalConfig{Root: "/tmp/deep_agent_sdk_test"},
	}, 1234, "cuda_demo")
	if err != nil {
		t.Fatalf("ResolveProject() error = %v", err)
	}
	want := filepath.Join("/tmp/deep_agent_sdk_test", "1234", "cuda_demo")
	if project.Name != "cuda_demo" || project.Path != want {
		t.Fatalf("ResolveProject() = %#v, want name=%q path=%q", project, "cuda_demo", want)
	}
}

func TestResolveProjectAIInfraUsesProjectWorkDirOnly(t *testing.T) {
	project, err := ResolveProject(Config{
		Type: TypeAIInfra,
		AIInfra: AIInfraConfig{
			BizType:         "aic_agent_sdk",
			BizIDTemplate:   "user_{uid}",
			WorkDirTemplate: "/opt/tiger/workspace/{project_name}",
		},
	}, 1234, "dreamina")
	if err != nil {
		t.Fatalf("ResolveProject() error = %v", err)
	}
	if project.Path != "/opt/tiger/workspace/dreamina" {
		t.Fatalf("project path = %q", project.Path)
	}
}

func TestOpenLocalCreatesSandboxFilesystemBackend(t *testing.T) {
	root := t.TempDir()
	workspace, err := Open(context.Background(), Config{
		Type:  TypeLocal,
		Local: LocalConfig{Root: root},
	}, Target{UID: 7, ProjectName: "demo"})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "7", "demo")); err != nil {
		t.Fatalf("local project dir not created: %v", err)
	}
	if _, err := workspace.Backend.Write(context.Background(), "hello.txt", "ok"); err != nil {
		t.Fatalf("backend Write() error = %v", err)
	}
	data, err := ReadFile(context.Background(), workspace.Backend, "hello.txt")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("ReadFile() = %q", data)
	}
}

func TestOpenAIInfraBuildsUserScopedSandbox(t *testing.T) {
	var got *gateway.FileMkdirRequest
	pippit_sandbox_gateway.SetMock.FileMkdir(func(ctx context.Context, req *gateway.FileMkdirRequest) (*gateway.FileMkdirResponse, error) {
		got = req
		return &gateway.FileMkdirResponse{BaseResp: &base.BaseResp{}}, nil
	})
	t.Cleanup(func() {
		pippit_sandbox_gateway.SetMock.FileMkdir(nil)
	})

	workspace, err := Open(context.Background(), Config{
		Type: TypeAIInfra,
		AIInfra: AIInfraConfig{
			BizType:         "aic_agent_sdk",
			BizIDTemplate:   "user_{uid}",
			WorkDirTemplate: "/opt/tiger/workspace/{project_name}",
		},
	}, Target{UID: 1234, ProjectName: "demo"})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if workspace.WorkDir != "/opt/tiger/workspace/demo" {
		t.Fatalf("WorkDir = %q", workspace.WorkDir)
	}
	if got := workspace.Backend.ID(); got != "aic_agent_sdk_user_1234" {
		t.Fatalf("Backend.ID() = %q", got)
	}
	if got == nil {
		t.Fatal("FileMkdir was not called to ensure workdir")
	}
	if got.Dir != "/opt/tiger/workspace/demo" || !got.GetOptionP() || got.IsSetExecDir() {
		t.Fatalf("unexpected ensure workdir request: dir=%q option_p=%t exec_dir=%q", got.Dir, got.GetOptionP(), got.GetExecDir())
	}
}

func TestOpenAIInfraRejectsUnsafeProjectNameBeforeTemplateRendering(t *testing.T) {
	_, err := Open(context.Background(), Config{
		Type: TypeAIInfra,
		AIInfra: AIInfraConfig{
			BizType:         "aic_agent_sdk",
			BizIDTemplate:   "user_{uid}",
			WorkDirTemplate: "/opt/tiger/workspace/{project_name}",
		},
	}, Target{UID: 1234, ProjectName: "../escape"})
	if err == nil {
		t.Fatal("Open() returned nil error for unsafe project name")
	}
}

func TestCleanProjectNameRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../x", "a/b", `a\b`} {
		if _, err := CleanProjectName(name); err == nil {
			t.Fatalf("CleanProjectName(%q) returned nil error", name)
		}
	}
}
