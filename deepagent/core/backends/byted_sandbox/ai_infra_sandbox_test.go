//go:build !windows

package byted_sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"code.byted.org/gopkg/thrift"
	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/base"
	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/capcut/business/common/sandbox_model"
	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/pippit/sandbox/gateway"
	"code.byted.org/overpass/pippit_sandbox_gateway/rpc/pippit_sandbox_gateway"
	"eino-cli/deepagent/core/backends"
)

func TestAIInfraSandboxExecuteCommandUsesBashExec(t *testing.T) {
	var got *gateway.BashExecRequest
	timeoutStatus := sandbox_model.OperationStatus_HARD_TIMEOUT
	pippit_sandbox_gateway.SetMock.BashExec(func(ctx context.Context, req *gateway.BashExecRequest) (*gateway.BashExecResponse, error) {
		got = req
		return &gateway.BashExecResponse{
			ShellSessionID:  thrift.StringPtr("next-session"),
			OperationStatus: &timeoutStatus,
			ExitCode:        thrift.Int32Ptr(7),
			Stdout:          thrift.StringPtr("stdout"),
			Stderr:          thrift.StringPtr("stderr"),
		}, nil
	})
	t.Cleanup(func() {
		pippit_sandbox_gateway.SetMock.BashExec(nil)
	})

	sandbox := NewAIInfraSandbox(&sandbox_model.BizMeta{}, "/workspace")
	sandbox.SetShellID("current-session")

	res, err := sandbox.ExecuteCommand(context.Background(), backends.CommandRequest{
		Command: "pwd",
		WorkDir: "project",
		Timeout: 1500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("ExecuteCommand returned error: %v", err)
	}
	if got == nil {
		t.Fatal("BashExec was not called")
	}
	if got.Command != "pwd" {
		t.Fatalf("unexpected command: %q", got.Command)
	}
	if got.GetShellSessionID() != "current-session" {
		t.Fatalf("unexpected shell session id: %q", got.GetShellSessionID())
	}
	if got.GetExecDir() != "/workspace/project" {
		t.Fatalf("unexpected exec dir: %q", got.GetExecDir())
	}
	if got.GetHardTimeoutSec() != 2 {
		t.Fatalf("unexpected hard timeout: %d", got.GetHardTimeoutSec())
	}
	if res.Output != "stdout\nstderr" {
		t.Fatalf("unexpected output: %q", res.Output)
	}
	if res.ExitCode != 7 {
		t.Fatalf("unexpected exit code: %d", res.ExitCode)
	}
	if res.ShellSessionID != "next-session" {
		t.Fatalf("unexpected response shell session id: %q", res.ShellSessionID)
	}
	if !res.TimedOut {
		t.Fatal("expected timed out result")
	}
}

func TestAIInfraSandboxEnsureWorkDirUsesFileMkdirWithoutExecDir(t *testing.T) {
	var got *gateway.FileMkdirRequest
	pippit_sandbox_gateway.SetMock.FileMkdir(func(ctx context.Context, req *gateway.FileMkdirRequest) (*gateway.FileMkdirResponse, error) {
		got = req
		return &gateway.FileMkdirResponse{BaseResp: &base.BaseResp{}}, nil
	})
	t.Cleanup(func() {
		pippit_sandbox_gateway.SetMock.FileMkdir(nil)
	})

	sandbox := NewAIInfraSandbox(&sandbox_model.BizMeta{}, "/tmp/deep_agent_sdk/demo")
	if err := sandbox.EnsureWorkDir(context.Background()); err != nil {
		t.Fatalf("EnsureWorkDir returned error: %v", err)
	}
	if got == nil {
		t.Fatal("FileMkdir was not called")
	}
	if got.Dir != "/tmp/deep_agent_sdk/demo" {
		t.Fatalf("unexpected dir: %q", got.Dir)
	}
	if !got.GetOptionP() {
		t.Fatal("expected FileMkdir OptionP")
	}
	if got.IsSetExecDir() {
		t.Fatalf("EnsureWorkDir should not set ExecDir, got %q", got.GetExecDir())
	}
}

func TestAIInfraSandboxEnsureWorkDirReturnsFileMkdirBaseRespError(t *testing.T) {
	pippit_sandbox_gateway.SetMock.FileMkdir(func(ctx context.Context, req *gateway.FileMkdirRequest) (*gateway.FileMkdirResponse, error) {
		return &gateway.FileMkdirResponse{BaseResp: &base.BaseResp{
			StatusCode:    4,
			StatusMessage: "blocked",
		}}, nil
	})
	t.Cleanup(func() {
		pippit_sandbox_gateway.SetMock.FileMkdir(nil)
	})

	sandbox := NewAIInfraSandbox(&sandbox_model.BizMeta{}, "/workspace/blocked")
	err := sandbox.EnsureWorkDir(context.Background())
	if err == nil {
		t.Fatal("EnsureWorkDir should return BaseResp error")
	}
	if !strings.Contains(err.Error(), "BizStatusCode:[4]") || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAIInfraSandboxApplyPatchUsesBashExec(t *testing.T) {
	var got *gateway.BashExecRequest
	pippit_sandbox_gateway.SetMock.BashExec(func(ctx context.Context, req *gateway.BashExecRequest) (*gateway.BashExecResponse, error) {
		got = req
		return &gateway.BashExecResponse{
			ExitCode: thrift.Int32Ptr(0),
			Stdout:   thrift.StringPtr("ok"),
		}, nil
	})
	t.Cleanup(func() {
		pippit_sandbox_gateway.SetMock.BashExec(nil)
	})

	sandbox := NewAIInfraSandbox(&sandbox_model.BizMeta{}, "/workspace")
	out, err := sandbox.ApplyPatch(context.Background(), "*** Begin Patch\n*** End Patch")
	if err != nil {
		t.Fatalf("ApplyPatch returned error: %v", err)
	}
	if out != "ok" {
		t.Fatalf("unexpected output: %q", out)
	}
	if got == nil {
		t.Fatal("BashExec was not called")
	}
	if !strings.Contains(got.Command, "/opt/apply_patch_rs/apply_patch") {
		t.Fatalf("unexpected apply_patch command: %q", got.Command)
	}
}

func TestAIInfraSandboxDownloadFilesUsesBinaryDownload(t *testing.T) {
	var got *gateway.FileDownloadRequest
	pippit_sandbox_gateway.SetMock.FileDownload(func(ctx context.Context, req *gateway.FileDownloadRequest) (*gateway.FileDownloadResponse, error) {
		got = req
		return &gateway.FileDownloadResponse{Content: []byte{0, 1, 2, 255}}, nil
	})
	t.Cleanup(func() {
		pippit_sandbox_gateway.SetMock.FileDownload(nil)
	})

	sandbox := NewAIInfraSandbox(&sandbox_model.BizMeta{}, "/workspace")
	res, err := sandbox.DownloadFiles(context.Background(), []string{"assets/image.png"})
	if err != nil {
		t.Fatalf("DownloadFiles returned error: %v", err)
	}
	if got == nil {
		t.Fatal("FileDownload was not called")
	}
	if got.Path != "/workspace/assets/image.png" {
		t.Fatalf("unexpected download path: %q", got.Path)
	}
	if len(res) != 1 || string(res[0].Content) != string([]byte{0, 1, 2, 255}) {
		t.Fatalf("unexpected download response: %+v", res)
	}
}

func TestAIInfraSandboxRejectsWorkspaceEscape(t *testing.T) {
	sandbox := NewAIInfraSandbox(&sandbox_model.BizMeta{}, "/workspace")
	if _, err := sandbox.Read(context.Background(), "../secret.txt", nil, nil); err != backends.ErrInvalidPath {
		t.Fatalf("Read escape error = %v, want %v", err, backends.ErrInvalidPath)
	}

	res, err := sandbox.DownloadFiles(context.Background(), []string{"../secret.txt"})
	if err != nil {
		t.Fatalf("DownloadFiles returned error: %v", err)
	}
	if len(res) != 1 || res[0].Error != backends.ErrInvalidPath {
		t.Fatalf("DownloadFiles escape response = %+v", res)
	}
}

func TestAIInfraSandboxPreservesAbsolutePath(t *testing.T) {
	sandbox := NewAIInfraSandbox(&sandbox_model.BizMeta{}, "/workspace")
	got, err := sandbox.resolvePath("/tmp/file.txt")
	if err != nil {
		t.Fatalf("resolvePath returned error: %v", err)
	}
	if got != "/tmp/file.txt" {
		t.Fatalf("resolvePath = %q, want /tmp/file.txt", got)
	}
}

func TestAIInfraSandboxExpandsHomePathOutsideWorkDir(t *testing.T) {
	sandbox := NewAIInfraSandbox(&sandbox_model.BizMeta{}, "/workspace/project")
	got, err := sandbox.resolvePath("~/.agent/skills")
	if err != nil {
		t.Fatalf("resolvePath returned error: %v", err)
	}
	if got != "/root/.agent/skills" {
		t.Fatalf("resolvePath = %q, want /root/.agent/skills", got)
	}
}

func TestBuildApplyPatchCommand_UsesSingleHeredocCommand(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: 机器人觉醒革命_storyboard.md\n@@\n+\n+ **分镜计时表**\n*** End Patch"

	cmd := buildApplyPatchCommand("/tmp/work dir", patch)

	if !strings.HasPrefix(cmd, "cd '/tmp/work dir' && /opt/apply_patch_rs/apply_patch <<'APPLY_PATCH_EOF'\n") {
		t.Fatalf("unexpected apply_patch command prefix: %q", cmd)
	}
	if !strings.Contains(cmd, patch) {
		t.Fatalf("patch body missing from command: %q", cmd)
	}
	if !strings.HasSuffix(cmd, "\nAPPLY_PATCH_EOF") {
		t.Fatalf("unexpected apply_patch command suffix: %q", cmd)
	}
	if strings.Contains(cmd, "/opt/apply_patch_rs/apply_patch "+patch) {
		t.Fatalf("apply_patch command should not inline patch as argv: %q", cmd)
	}
}

func TestBuildApplyPatchCommand_PicksAlternateDelimiterWhenNeeded(t *testing.T) {
	patch := "*** Begin Patch\nAPPLY_PATCH_EOF\n*** End Patch"

	cmd := buildApplyPatchCommand("", patch)

	if !strings.HasPrefix(cmd, "/opt/apply_patch_rs/apply_patch <<'APPLY_PATCH_EOF_1'\n") {
		t.Fatalf("expected alternate delimiter, got %q", cmd)
	}
	if !strings.HasSuffix(cmd, "\nAPPLY_PATCH_EOF_1") {
		t.Fatalf("expected alternate delimiter suffix, got %q", cmd)
	}
}

func TestShellSingleQuote_EscapesSingleQuotes(t *testing.T) {
	got := shellSingleQuote("/tmp/that's it")
	if got != `'/tmp/that'"'"'s it'` {
		t.Fatalf("unexpected quoted string: %q", got)
	}
}
