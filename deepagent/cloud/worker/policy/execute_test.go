//go:build !windows

package policy

import (
	"context"
	"testing"

	execmw "eino-cli/deepagent/core/middleware/execute"
)

func TestExecutePolicyProfileAllowsSafeLookup(t *testing.T) {
	profile := ExecutePolicyProfile(ApprovalPolicyReadOnly)
	got, err := profile.Policy.Decide(context.Background(), execmw.PolicyRequest{
		Command: execmw.CommandSpec{RawCommand: "which gcc"},
	})
	if err != nil {
		t.Fatalf("Decide() error=%v", err)
	}
	if got.Action != execmw.ActionAllow || got.Class != execmw.CommandSafe {
		t.Fatalf("decision=%+v, want safe allow", got)
	}
}

func TestExecutePolicyProfileRejectsUnsafeLookupTarget(t *testing.T) {
	profile := ExecutePolicyProfile(ApprovalPolicyReadOnly)
	got, err := profile.Policy.Decide(context.Background(), execmw.PolicyRequest{
		Command: execmw.CommandSpec{RawCommand: "which ../gcc"},
	})
	if err != nil {
		t.Fatalf("Decide() error=%v", err)
	}
	if got.Action == execmw.ActionAllow {
		t.Fatalf("decision=%+v, want not allow", got)
	}
}
