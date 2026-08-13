package main

import (
	"context"
	"strings"
	"testing"

	"eino-cli/deepagent/core"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
	execmw "eino-cli/deepagent/core/middleware/execute"
	"eino-cli/deepagent/core/middleware/planmode"
	deeptools "eino-cli/deepagent/core/tools"
	sdkutils "eino-cli/deepagent/core/utils"
	inprocess "eino-cli/deepagent/worker/inprocess"
	"github.com/cloudwego/eino/schema"
)

func TestCmdDeepAgentTaskRolesDescription(t *testing.T) {
	for _, want := range []string{"default:", "explorer:", "worker:", "read-only file tools", "clear ownership"} {
		if !strings.Contains(cmdDeepAgentTaskRolesDescription, want) {
			t.Fatalf("roles description missing %q: %s", want, cmdDeepAgentTaskRolesDescription)
		}
	}
}

func TestCmdDeepAgentExplorerRoleUsesReadOnlyRuntime(t *testing.T) {
	factory := &DeepAgentRuntimeFactory{
		fsBackend:      backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: t.TempDir()}),
		toolExecPolicy: NewToolExecPolicy(),
	}
	cfg := factory.threadRoleRuntimeConfig(&inprocess.ThreadState{
		ID:        "thread_1",
		SessionID: "sess_1",
		Profile:   inprocess.ThreadProfile{Role: "explorer", Cwd: "/repo"},
	})
	if cfg.Filesystem == nil || !cfg.Filesystem.ReadOnly || !cfg.Filesystem.DisableExecute {
		t.Fatalf("filesystem config = %+v, want read-only with old execute disabled", cfg.Filesystem)
	}
	gate, ok := cfg.ToolPolicyGates[execmw.DefaultToolName]
	if !ok {
		t.Fatalf("missing exec_command policy gate")
	}
	decision, err := gate.Policy(context.Background(), approvalInfoForExec(t, "go test ./..."))
	if err != nil {
		t.Fatalf("policy error: %v", err)
	}
	if decision.Action != deeptools.ToolCallDeny {
		t.Fatalf("explorer go test action = %s, want deny", decision.Action)
	}
	decision, err = gate.Policy(context.Background(), approvalInfoForExec(t, "rg TODO"))
	if err != nil {
		t.Fatalf("policy error: %v", err)
	}
	if decision.Action != deeptools.ToolCallAllow {
		t.Fatalf("explorer rg action = %s, want allow", decision.Action)
	}
}

func TestCmdDeepAgentWorkerRoleUsesDefaultExecutePolicy(t *testing.T) {
	factory := &DeepAgentRuntimeFactory{
		fsBackend:      backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: t.TempDir()}),
		toolExecPolicy: NewToolExecPolicy(),
	}
	cfg := factory.threadRoleRuntimeConfig(&inprocess.ThreadState{
		ID:        "thread_1",
		SessionID: "sess_1",
		Profile:   inprocess.ThreadProfile{Role: "worker", Cwd: "/repo"},
	})
	if cfg.Filesystem == nil || cfg.Filesystem.ReadOnly || !cfg.Filesystem.DisableExecute {
		t.Fatalf("filesystem config = %+v, want writable filesystem with old execute disabled", cfg.Filesystem)
	}
	decision, err := cfg.ToolPolicyGates[execmw.DefaultToolName].Policy(context.Background(), approvalInfoForExec(t, "go test ./..."))
	if err != nil {
		t.Fatalf("policy error: %v", err)
	}
	if decision.Action != deeptools.ToolCallRequireApproval {
		t.Fatalf("worker go test action = %s, want require approval", decision.Action)
	}
}

func TestCmdDeepAgentRuntimeFactoryFallsBackToConfigWorkDir(t *testing.T) {
	factory := &DeepAgentRuntimeFactory{
		cfg:       AppConfig{WorkDir: "/flag/repo"},
		fsBackend: backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: t.TempDir()}),
	}
	state := &inprocess.ThreadState{
		ID:        "thread_1",
		SessionID: "sess_1",
		Profile:   inprocess.ThreadProfile{Role: "worker"},
	}

	if got := factory.threadWorkDir(state); got != "/flag/repo" {
		t.Fatalf("threadWorkDir() = %q, want /flag/repo", got)
	}
	cfg := factory.threadRoleRuntimeConfig(state)
	if cfg.Filesystem == nil || cfg.Filesystem.WorkDir != "/flag/repo" {
		t.Fatalf("filesystem config = %+v, want /flag/repo workdir", cfg.Filesystem)
	}

	base := &agentthread.TurnRunnerConfig{
		FilesystemConfig: &deepagents.FilesystemConfig{},
	}
	planCfg := factory.planTurnConfig(base, state)
	if planCfg.FilesystemConfig == nil || planCfg.FilesystemConfig.WorkDir != "/flag/repo" {
		t.Fatalf("plan filesystem config = %+v, want /flag/repo workdir", planCfg.FilesystemConfig)
	}

	state.Profile.Cwd = "/thread/repo"
	if got := factory.threadWorkDir(state); got != "/thread/repo" {
		t.Fatalf("threadWorkDir() = %q, want /thread/repo", got)
	}
}

func TestCmdDeepAgentRGHintIsExplicitlyEnabled(t *testing.T) {
	disabled := cmdDeepAgentExecutePolicyProfile("", false)
	if strings.Contains(disabled.Instructions, "Prefer rg") {
		t.Fatalf("rg instructions should be absent when disabled: %s", disabled.Instructions)
	}

	enabled := cmdDeepAgentExecutePolicyProfile("", true)
	for _, want := range []string{"Repository search:", "Prefer rg", "rg --files", "filesystem grep/glob"} {
		if !strings.Contains(enabled.Instructions, want) {
			t.Fatalf("rg instructions missing %q: %s", want, enabled.Instructions)
		}
	}
}

func TestCmdDeepAgentPlanTurnConfigAddsPlanModeAndReadOnlyExecute(t *testing.T) {
	factory := &DeepAgentRuntimeFactory{
		fsBackend: backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: t.TempDir()}),
	}
	base := &agentthread.TurnRunnerConfig{
		EnablePlan: true,
		FilesystemConfig: &deepagents.FilesystemConfig{
			WorkDir: "/repo",
		},
	}
	cfg := factory.planTurnConfig(base, &inprocess.ThreadState{Profile: inprocess.ThreadProfile{Cwd: "/repo"}})
	if cfg == base {
		t.Fatalf("plan config reused base pointer")
	}
	if cfg.EnablePlan {
		t.Fatalf("plan config should disable Plan")
	}
	if cfg.FilesystemConfig == nil || !cfg.FilesystemConfig.ReadOnly || !cfg.FilesystemConfig.DisableExecute {
		t.Fatalf("plan filesystem config = %+v, want read-only without legacy execute", cfg.FilesystemConfig)
	}
	foundPlanMode := false
	for _, mw := range cfg.Middlewares {
		if mw != nil && mw.Name() == planmode.MiddlewareName {
			foundPlanMode = true
		}
	}
	if !foundPlanMode {
		t.Fatalf("plan mode middleware not added")
	}
	foundExecute := false
	for _, mw := range cfg.Middlewares {
		if mw != nil && mw.Name() == "execute" {
			foundExecute = true
		}
	}
	if !foundExecute {
		t.Fatalf("plan execute middleware not added")
	}
	for _, name := range []string{constant.ToolWriteFile, constant.ToolEditFile, constant.ToolApplyPatch, constant.ToolUpdatePlan} {
		if cfg.ToolMask(context.Background(), &schema.ToolInfo{Name: name}) {
			t.Fatalf("plan mask allowed mutating tool %q", name)
		}
	}
	for _, name := range []string{constant.ToolReadFile, constant.ToolGrep, planmode.ToolRequestUserInput, execmw.DefaultToolName} {
		if !cfg.ToolMask(context.Background(), &schema.ToolInfo{Name: name}) {
			t.Fatalf("plan mask rejected read/planning tool %q", name)
		}
	}
	if base.FilesystemConfig.ReadOnly || base.FilesystemConfig.DisableExecute || !base.EnablePlan {
		t.Fatalf("base config was mutated: %+v", base)
	}
}

func approvalInfoForExec(t *testing.T, cmd string) *deeptools.ApprovalInfo {
	t.Helper()
	return &deeptools.ApprovalInfo{
		ToolName:        execmw.DefaultToolName,
		ArgumentsInJSON: sdkutils.ToString(execmw.ExecCommandInput{Cmd: cmd}),
	}
}
