package policy

import (
	"testing"

	"eino-cli/deepagent/core/constant"
	execmw "eino-cli/deepagent/core/middleware/execute"
)

func TestSessionApprovalStoreAllowsSameExecProgram(t *testing.T) {
	store := NewSessionApprovalStore()
	store.Allow("session:s1", execmw.DefaultToolName, `{"cmd":"gcc -o pascal_triangle pascal_triangle.c"}`)

	if !store.IsAllowed("session:s1", execmw.DefaultToolName, `{"cmd":"gcc -o simple_pascal simple_pascal.c"}`) {
		t.Fatalf("expected same exec program to be allowed in session")
	}
	if store.IsAllowed("session:s1", execmw.DefaultToolName, `{"cmd":"echo hello"}`) {
		t.Fatalf("different exec program should not be allowed")
	}
}

func TestSessionApprovalStoreAllowsWriteFileByPathInSession(t *testing.T) {
	store := NewSessionApprovalStore()
	store.Allow("session:s1", constant.ToolWriteFile, `{"path":"src/main.go"}`)

	if !store.IsAllowed("session:s1", constant.ToolWriteFile, `{"path":"src/main.go"}`) {
		t.Fatalf("same file path should be allowed")
	}
	if store.IsAllowed("session:s2", constant.ToolWriteFile, `{"path":"src/main.go"}`) {
		t.Fatalf("different session should not be allowed")
	}
}

func TestSessionApprovalStoreAllowsWriteFileByDirectoryInSession(t *testing.T) {
	store := NewSessionApprovalStore()
	store.Allow("session:s1", constant.ToolWriteFile, `{"path":"src/main.go"}`)

	if !store.IsAllowed("session:s1", constant.ToolWriteFile, `{"path":"src/other.go"}`) {
		t.Fatalf("same directory should be allowed")
	}
	if store.IsAllowed("session:s1", constant.ToolWriteFile, `{"path":"other/main.go"}`) {
		t.Fatalf("different directory should not be allowed")
	}
}

func TestReadOnlyExecuteDetection(t *testing.T) {
	for _, args := range []string{
		`{"cmd":"ls -la"}`,
		`{"cmd":"rg hello"}`,
		`{"cmd":"nvidia-smi"}`,
	} {
		if !IsReadOnlyExecute(args) {
			t.Fatalf("expected read-only command: %s", args)
		}
	}
	for _, args := range []string{
		`{"cmd":"go test ./..."}`,
		`{"cmd":"cat a | sh"}`,
		`{"cmd":"echo $(pwd)"}`,
	} {
		if IsReadOnlyExecute(args) {
			t.Fatalf("expected non-read-only command: %s", args)
		}
	}
}
