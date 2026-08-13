package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type policyTestTool struct {
	name string
	runs int
}

func (t *policyTestTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}

func (t *policyTestTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	t.runs++
	return "ran:" + argumentsInJSON, nil
}

func TestNewInvokableApprovableTool_AllowKeepsOldBehavior(t *testing.T) {
	base := &policyTestTool{name: "counter"}
	wrapped := NewInvokableApprovableTool(base, func(context.Context, *ApprovalInfo) bool {
		return false
	})

	got, err := wrapped.InvokableRun(context.Background(), `{"delta":1}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if got != `ran:{"delta":1}` {
		t.Fatalf("output = %q", got)
	}
	if base.runs != 1 {
		t.Fatalf("runs = %d, want 1", base.runs)
	}
}

func TestNewInvokablePolicyTool_AllowRunsBaseTool(t *testing.T) {
	base := &policyTestTool{name: "counter"}
	wrapped := NewInvokablePolicyTool(base, ToolPolicyGate{
		Policy: func(context.Context, *ApprovalInfo) (ToolCallDecision, error) {
			return ToolCallDecision{Action: ToolCallAllow}, nil
		},
		DenyFormatter: func(ctx context.Context, info *ApprovalInfo, decision ToolCallDecision) (string, error) {
			return "denied", nil
		},
	})

	got, err := wrapped.InvokableRun(context.Background(), `{"delta":1}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if got != `ran:{"delta":1}` {
		t.Fatalf("output = %q", got)
	}
	if base.runs != 1 {
		t.Fatalf("runs = %d, want 1", base.runs)
	}
}

func TestNewInvokablePolicyTool_DenyDoesNotRunBaseTool(t *testing.T) {
	base := &policyTestTool{name: "counter"}
	wrapped := NewInvokablePolicyTool(base, ToolPolicyGate{
		Policy: func(context.Context, *ApprovalInfo) (ToolCallDecision, error) {
			return ToolCallDecision{Action: ToolCallDeny, Reason: "blocked"}, nil
		},
		DenyFormatter: func(ctx context.Context, info *ApprovalInfo, decision ToolCallDecision) (string, error) {
			return fmt.Sprintf("denied:%s:%s", info.ToolName, decision.Reason), nil
		},
	})

	got, err := wrapped.InvokableRun(context.Background(), `{"delta":1}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if got != "denied:counter:blocked" {
		t.Fatalf("output = %q", got)
	}
	if base.runs != 0 {
		t.Fatalf("runs = %d, want 0", base.runs)
	}
}

func TestNewInvokablePolicyTool_RequireApprovalInterrupts(t *testing.T) {
	base := &policyTestTool{name: "counter"}
	wrapped := NewInvokablePolicyTool(base, ToolPolicyGate{
		Policy: func(context.Context, *ApprovalInfo) (ToolCallDecision, error) {
			return ToolCallDecision{Action: ToolCallRequireApproval, Reason: "needs approval"}, nil
		},
		DenyFormatter: func(ctx context.Context, info *ApprovalInfo, decision ToolCallDecision) (string, error) {
			return "denied", nil
		},
	})

	if got, err := wrapped.InvokableRun(context.Background(), `{"delta":1}`); err == nil || got != "" {
		t.Fatalf("InvokableRun() = (%q, %v), want interrupt error", got, err)
	}
	if base.runs != 0 {
		t.Fatalf("runs = %d, want 0", base.runs)
	}
}

func TestNewInvokablePolicyTool_PolicyErrorDenies(t *testing.T) {
	base := &policyTestTool{name: "counter"}
	wrapped := NewInvokablePolicyTool(base, ToolPolicyGate{
		Policy: func(context.Context, *ApprovalInfo) (ToolCallDecision, error) {
			return ToolCallDecision{}, errors.New("classifier failed")
		},
		DenyFormatter: func(ctx context.Context, info *ApprovalInfo, decision ToolCallDecision) (string, error) {
			return "denied:" + decision.Reason, nil
		},
	})

	got, err := wrapped.InvokableRun(context.Background(), `{"delta":1}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if got != "denied:classifier failed" {
		t.Fatalf("output = %q", got)
	}
	if base.runs != 0 {
		t.Fatalf("runs = %d, want 0", base.runs)
	}
}

func TestFormatHumanRejectedToolResultTreatsFeedbackAsInstruction(t *testing.T) {
	reason := "先停止干活"
	got := formatHumanRejectedToolResult("exec_command", &reason)

	for _, want := range []string{
		`Human rejected tool "exec_command".`,
		"Human feedback: 先停止干活.",
		"latest user instruction",
		"Do not retry this tool call",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("result = %q, missing %q", got, want)
		}
	}
}
