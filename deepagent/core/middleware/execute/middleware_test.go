package execute

import (
	"context"
	"strings"
	"testing"

	"eino-cli/deepagent/core/backends"
	deeptools "eino-cli/deepagent/core/tools"
	sdkutils "eino-cli/deepagent/core/utils"
	"github.com/cloudwego/eino/components/tool"
)

type fakeExecutor struct {
	last backends.CommandRequest
	out  *backends.CommandResult
	err  error
}

func (r *fakeExecutor) Execute(ctx context.Context, command string) (*backends.ExecuteResponse, error) {
	return &backends.ExecuteResponse{Output: "ok\n", ExitCode: 0}, nil
}

func (r *fakeExecutor) ExecuteCommand(ctx context.Context, req backends.CommandRequest) (*backends.CommandResult, error) {
	r.last = req
	if r.err != nil {
		return nil, r.err
	}
	if r.out != nil {
		return r.out, nil
	}
	return &backends.CommandResult{Output: "ok\n", ExitCode: 0}, nil
}

func TestMiddlewareToolsAndPrompt(t *testing.T) {
	mw := New(Config{Executor: &fakeExecutor{}, WorkDir: "/repo"})
	tools, err := mw.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Name != DefaultToolName {
		t.Fatalf("tool name = %q, want %q", info.Name, DefaultToolName)
	}
	msgs, err := mw.BuildInitialContext(context.Background())
	if err != nil {
		t.Fatalf("BuildInitialContext() error = %v", err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, "exec_command") {
		t.Fatalf("prompt missing exec_command: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "default shell access") {
		t.Fatalf("prompt missing default policy instructions: %+v", msgs)
	}
}

func TestReadOnlyPolicyAllowsSafeCommand(t *testing.T) {
	executor := &fakeExecutor{out: &backends.CommandResult{Output: "main.go\n", ExitCode: 0}}
	mw := New(Config{
		Executor:      executor,
		PolicyProfile: NewReadOnlyPolicyProfile(nil),
		WorkDir:       "/repo",
	})
	out := invokeExecTool(t, mw, `{"cmd":"rg main"}`)
	if out.Denied {
		t.Fatalf("expected command allowed, got denied: %+v", out)
	}
	if out.Output != "main.go\n" {
		t.Fatalf("output = %q", out.Output)
	}
	if executor.last.Command != "rg main" {
		t.Fatalf("executor command = %q", executor.last.Command)
	}
}

func TestReadOnlyPolicyGateDeniesUnknownAndDangerousCommands(t *testing.T) {
	mw := New(Config{
		Executor:      &fakeExecutor{},
		PolicyProfile: NewReadOnlyPolicyProfile(nil),
		WorkDir:       "/repo",
	})
	gate := mw.PolicyGate()
	for _, input := range []ExecCommandInput{
		{Cmd: "go test ./..."},
		{Cmd: "rm -rf tmp"},
	} {
		decision, err := gate.Policy(context.Background(), approvalInfo(t, input))
		if err != nil {
			t.Fatalf("Policy() error = %v", err)
		}
		if decision.Action != deeptools.ToolCallDeny {
			t.Fatalf("input %+v: action = %s, want deny", input, decision.Action)
		}
	}
}

func TestDefaultPolicyGateRequiresApprovalBeforeExecution(t *testing.T) {
	executor := &fakeExecutor{}
	mw := New(Config{
		Executor:      executor,
		PolicyProfile: NewDefaultPolicyProfile(nil),
		WorkDir:       "/repo",
	})
	baseTools, err := mw.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	base, ok := baseTools[0].(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool is not invokable")
	}
	wrapped := deeptools.NewInvokablePolicyTool(base, mw.PolicyGate())
	got, err := wrapped.InvokableRun(context.Background(), `{"cmd":"go test ./..."}`)
	if err == nil || got != "" {
		t.Fatalf("InvokableRun() = (%q, %v), want approval interrupt", got, err)
	}
	if executor.last.Command != "" {
		t.Fatalf("executor should not run before approval, got %q", executor.last.Command)
	}
}

func TestPolicyGateDecisions(t *testing.T) {
	defaultGate := New(Config{PolicyProfile: NewDefaultPolicyProfile(nil), WorkDir: "/repo"}).PolicyGate()
	readonlyGate := New(Config{PolicyProfile: NewReadOnlyPolicyProfile(nil), WorkDir: "/repo"}).PolicyGate()

	decision, err := defaultGate.Policy(context.Background(), approvalInfo(t, ExecCommandInput{Cmd: "rg foo"}))
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if decision.Action != deeptools.ToolCallAllow {
		t.Fatalf("safe command action = %s, want allow", decision.Action)
	}

	decision, err = defaultGate.Policy(context.Background(), approvalInfo(t, ExecCommandInput{Cmd: "go test ./..."}))
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if decision.Action != deeptools.ToolCallRequireApproval {
		t.Fatalf("unknown command action = %s, want require approval", decision.Action)
	}

	decision, err = readonlyGate.Policy(context.Background(), approvalInfo(t, ExecCommandInput{Cmd: "go test ./..."}))
	if err != nil {
		t.Fatalf("Policy() error = %v", err)
	}
	if decision.Action != deeptools.ToolCallDeny {
		t.Fatalf("readonly command action = %s, want deny", decision.Action)
	}

	if _, err = defaultGate.Policy(context.Background(), &deeptools.ApprovalInfo{ToolName: DefaultToolName, ArgumentsInJSON: "{"}); err == nil {
		t.Fatalf("malformed args should return policy error")
	}
}

func TestPolicyGateDenyFormatter(t *testing.T) {
	mw := New(Config{PolicyProfile: NewReadOnlyPolicyProfile(nil), WorkDir: "/repo"})
	raw, err := mw.PolicyGate().DenyFormatter(context.Background(), approvalInfo(t, ExecCommandInput{Cmd: "go test ./..."}), deeptools.ToolCallDecision{
		Action: deeptools.ToolCallDeny,
		Reason: "blocked",
	})
	if err != nil {
		t.Fatalf("DenyFormatter() error = %v", err)
	}
	out, err := sdkutils.ToStruct[ExecCommandOutput](raw)
	if err != nil {
		t.Fatalf("decode output %q: %v", raw, err)
	}
	if !out.Denied || out.Reason != "blocked" || out.ExitCode != 1 {
		t.Fatalf("deny output = %+v", out)
	}
}

func TestClassifierCommandClasses(t *testing.T) {
	classifier := NewDefaultClassifier()
	tests := []struct {
		cmd  string
		want CommandClass
	}{
		{"rg foo", CommandSafe},
		{"rg --pre 'cmd' foo", CommandDangerous},
		{"sed -n '1,20p' file", CommandSafe},
		{"sed -i 's/a/b/' file", CommandDangerous},
		{"git diff", CommandSafe},
		{"git diff --output=x", CommandDangerous},
		{"git branch --show-current", CommandSafe},
		{"git branch new_branch", CommandDangerous},
		{"find . -name '*.go'", CommandSafe},
		{"find . -delete", CommandDangerous},
		{"cat a > b", CommandDangerous},
		{"grep foo a | wc -l", CommandSafe},
		{"echo hi", CommandSafe},
		{"python -c 'print(1)'", CommandDangerous},
	}
	for _, tt := range tests {
		got, err := classifier.Classify(context.Background(), CommandSpec{RawCommand: tt.cmd})
		if err != nil {
			t.Fatalf("%q: Classify error = %v", tt.cmd, err)
		}
		if got.Class != tt.want {
			t.Fatalf("%q: class = %s, want %s; reason=%s", tt.cmd, got.Class, tt.want, got.Reason)
		}
	}
}

func TestClassifierParsesSimpleCommands(t *testing.T) {
	classifier := NewDefaultClassifier()
	got, err := classifier.Classify(context.Background(), CommandSpec{RawCommand: "grep foo a | wc -l"})
	if err != nil {
		t.Fatalf("Classify error = %v", err)
	}
	if got.Class != CommandSafe {
		t.Fatalf("class = %s, want safe", got.Class)
	}
	if len(got.SimpleCommands) != 2 {
		t.Fatalf("simple commands len = %d, want 2: %+v", len(got.SimpleCommands), got.SimpleCommands)
	}
	if strings.Join(got.SimpleCommands[0].Argv, " ") != "grep foo a" {
		t.Fatalf("first command = %+v", got.SimpleCommands[0].Argv)
	}
	if strings.Join(got.SimpleCommands[1].Argv, " ") != "wc -l" {
		t.Fatalf("second command = %+v", got.SimpleCommands[1].Argv)
	}
}

func TestClassifierRequiresRawCommand(t *testing.T) {
	classifier := NewDefaultClassifier()
	got, err := classifier.Classify(context.Background(), CommandSpec{
		Argv: []string{"/bin/bash", "-lc", "rg foo"},
	})
	if err != nil {
		t.Fatalf("Classify error = %v", err)
	}
	if got.Class != CommandUnknown || !strings.Contains(got.Reason, "raw shell command") {
		t.Fatalf("classification = %+v, want raw command unknown", got)
	}
}

func invokeExecTool(t *testing.T, mw *ExecuteMiddleware, args string) ExecCommandOutput {
	t.Helper()
	tools, err := mw.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	invokable, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool is not invokable")
	}
	raw, err := invokable.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	out, err := sdkutils.ToStruct[ExecCommandOutput](raw)
	if err != nil {
		t.Fatalf("decode output %q: %v", raw, err)
	}
	return out
}

func approvalInfo(t *testing.T, input ExecCommandInput) *deeptools.ApprovalInfo {
	t.Helper()
	return &deeptools.ApprovalInfo{
		ToolName:        DefaultToolName,
		ArgumentsInJSON: sdkutils.ToString(input),
	}
}
