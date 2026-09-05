//go:build !windows

package worker

import (
	"testing"

	"eino-cli/deepagent/coordinator"
	"eino-cli/deepagent/worker/thread/runtimectx"
)

func TestThreadInfoFromSpecMapsACThreadFields(t *testing.T) {
	spec := threadSpec{
		Info: &coordinator.Thread{
			ThreadID:  42,
			Namespace: "deep_agent_sdk",
			SessionID: "session-1",
			Env:       "boe",
			UserID:    99,
		},
		ThreadID: "42",
	}

	got := threadInfoFromSpec(spec)
	want := runtimectx.ThreadIdentity{
		ThreadID:  "42",
		SessionID: "session-1",
		UserID:    99,
		Namespace: "deep_agent_sdk",
		Env:       "boe",
	}
	if got != want {
		t.Fatalf("threadInfoFromSpec()=%+v, want %+v", got, want)
	}
}

func TestThreadInfoFromSpecPassesThroughEmptyNamespaceAndEnv(t *testing.T) {
	spec := threadSpec{
		Info:     &coordinator.Thread{ThreadID: 7, SessionID: "session-2"},
		ThreadID: "7",
	}

	got := threadInfoFromSpec(spec)
	if got.Namespace != "" || got.Env != "" {
		t.Fatalf("threadInfoFromSpec() namespace=%q env=%q, want empty (no business fallback)", got.Namespace, got.Env)
	}
	if got.ThreadID != "7" || got.SessionID != "session-2" {
		t.Fatalf("threadInfoFromSpec()=%+v", got)
	}
}
