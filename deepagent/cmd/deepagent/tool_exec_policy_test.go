package main

import (
	"context"
	"testing"

	"eino-cli/deepagent/core/constant"
	execmw "eino-cli/deepagent/core/middleware/execute"
	"eino-cli/deepagent/core/tools"
)

func TestToolExecPolicyAllowsWriteFileByDirectoryWithinSession(t *testing.T) {
	policy := NewToolExecPolicy()
	policy.AllowSession("sess_1", constant.ToolWriteFile, `{"path":"cuda_reduce_demo/src/a.cu","content":"a"}`)

	needApproval := policy.BuildNeedApprovalMap("sess_1")[constant.ToolWriteFile]
	if needApproval(context.Background(), &tools.ApprovalInfo{
		ToolName:        constant.ToolWriteFile,
		ArgumentsInJSON: `{"path":"cuda_reduce_demo/src/b.cu","content":"b"}`,
	}) {
		t.Fatalf("same directory write_file should be allowed")
	}
	if !needApproval(context.Background(), &tools.ApprovalInfo{
		ToolName:        constant.ToolWriteFile,
		ArgumentsInJSON: `{"path":"cuda_reduce_demo/include/b.h","content":"b"}`,
	}) {
		t.Fatalf("different directory write_file should still require approval")
	}
}

func TestToolExecPolicyDoesNotLeakAcrossSessions(t *testing.T) {
	policy := NewToolExecPolicy()
	policy.AllowSession("sess_1", constant.ToolWriteFile, `{"path":"cuda_reduce_demo/src/a.cu","content":"a"}`)

	needApproval := policy.BuildNeedApprovalMap("sess_2")[constant.ToolWriteFile]
	if !needApproval(context.Background(), &tools.ApprovalInfo{
		ToolName:        constant.ToolWriteFile,
		ArgumentsInJSON: `{"path":"cuda_reduce_demo/src/b.cu","content":"b"}`,
	}) {
		t.Fatalf("approval should not leak across sessions")
	}
}

func TestToolExecPolicyAllowsExecuteByExactArgumentsWithinSession(t *testing.T) {
	policy := NewToolExecPolicy()
	policy.AllowSession("sess_1", execmw.DefaultToolName, `{"cmd":"mkdir -p cuda_reduce_demo/src"}`)

	gate := policy.WrapPolicyGate("sess_1", tools.ToolPolicyGate{
		Policy: func(ctx context.Context, info *tools.ApprovalInfo) (tools.ToolCallDecision, error) {
			return tools.ToolCallDecision{Action: tools.ToolCallRequireApproval}, nil
		},
	})
	decision, err := gate.Policy(context.Background(), &tools.ApprovalInfo{
		ToolName:        execmw.DefaultToolName,
		ArgumentsInJSON: `{"cmd":"mkdir -p cuda_reduce_demo/src"}`,
	})
	if err != nil {
		t.Fatalf("policy gate returned error: %v", err)
	}
	if decision.Action != tools.ToolCallAllow {
		t.Fatalf("same execute arguments should be allowed")
	}
	decision, err = gate.Policy(context.Background(), &tools.ApprovalInfo{
		ToolName:        execmw.DefaultToolName,
		ArgumentsInJSON: `{"cmd":"mkdir -p cuda_reduce_demo/include"}`,
	})
	if err != nil {
		t.Fatalf("policy gate returned error: %v", err)
	}
	if decision.Action != tools.ToolCallRequireApproval {
		t.Fatalf("different execute arguments should still require approval")
	}
}

func TestToolExecPolicyDoesNotBypassBasePolicyDecision(t *testing.T) {
	policy := NewToolExecPolicy()
	policy.AllowSession("sess_1", execmw.DefaultToolName, `{"cmd":"cd cuda_reduce_demo && ls -la"}`)

	gate := policy.WrapPolicyGate("sess_1", tools.ToolPolicyGate{
		Policy: func(ctx context.Context, info *tools.ApprovalInfo) (tools.ToolCallDecision, error) {
			return tools.ToolCallDecision{Action: tools.ToolCallDeny, Reason: "blocked"}, nil
		},
	})
	decision, err := gate.Policy(context.Background(), &tools.ApprovalInfo{
		ToolName:        execmw.DefaultToolName,
		ArgumentsInJSON: `{"cmd":"cd cuda_reduce_demo && ls -la"}`,
	})
	if err != nil {
		t.Fatalf("policy gate returned error: %v", err)
	}
	if decision.Action != tools.ToolCallDeny {
		t.Fatalf("base deny must not be overridden by approval reuse: %s", decision.Action)
	}
}

func TestToolExecPolicyDoesNotTreatCdPrefixAsExecuteReuse(t *testing.T) {
	policy := NewToolExecPolicy()
	policy.AllowSession("sess_1", execmw.DefaultToolName, `{"cmd":"cd cuda_reduce_demo && ls -la"}`)

	gate := policy.WrapPolicyGate("sess_1", tools.ToolPolicyGate{
		Policy: func(ctx context.Context, info *tools.ApprovalInfo) (tools.ToolCallDecision, error) {
			return tools.ToolCallDecision{Action: tools.ToolCallRequireApproval}, nil
		},
	})
	decision, err := gate.Policy(context.Background(), &tools.ApprovalInfo{
		ToolName:        execmw.DefaultToolName,
		ArgumentsInJSON: `{"cmd":"cd cuda_reduce_demo && make"}`,
	})
	if err != nil {
		t.Fatalf("policy gate returned error: %v", err)
	}
	if decision.Action != tools.ToolCallRequireApproval {
		t.Fatalf("different command with same cd prefix should still require approval")
	}
}
