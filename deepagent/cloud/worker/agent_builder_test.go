//go:build !windows

package worker

import (
	"testing"

	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	"eino-cli/deepagent/cloud/worker/runtimectx"
)

func TestThreadInfoFromSpecMapsACThreadFields(t *testing.T) {
	spec := threadSpec{
		Info: &ac.Thread{
			ThreadId:  42,
			Namespace: "aic_agent_sdk",
			SessionId: stringPtr("session-1"),
			Env:       stringPtr("boe"),
			UserId:    99,
		},
		ThreadID: "42",
	}

	got := threadInfoFromSpec(spec)
	want := runtimectx.ThreadIdentity{
		ThreadID:  "42",
		SessionID: "session-1",
		UserID:    99,
		Namespace: "aic_agent_sdk",
		Env:       "boe",
	}
	if got != want {
		t.Fatalf("threadInfoFromSpec()=%+v, want %+v", got, want)
	}
}

func TestThreadInfoFromSpecPassesThroughEmptyNamespaceAndEnv(t *testing.T) {
	spec := threadSpec{
		Info:     &ac.Thread{ThreadId: 7, SessionId: stringPtr("session-2")},
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
