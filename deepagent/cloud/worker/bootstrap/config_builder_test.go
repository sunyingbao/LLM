package bootstrap

import (
	"testing"
	"time"

	"eino-cli/deepagent/cloud/worker/bootstrap/config"
)

func TestNewCloudAgentConfigMapsMemory(t *testing.T) {
	cfg := config.Default()
	cfg.Memory.Enabled = true
	cfg.Memory.WorkspaceRoot = "/tmp/memory"
	cfg.Memory.Stage1IdleWindow = 2 * time.Minute
	cfg.Memory.Stage2SuccessCooldown = time.Hour
	cfg.Memory.Stage2OutputLimitPerUser = 17
	cfg.Runtime.DisableApplyPatch = true
	cfg.Runtime.MaxSteps = 88
	cfg.Runtime.MaxModelCalls = 12

	got := newCloudAgentConfig(cfg, nil, nil, nil)
	if !got.Memory.Enabled || got.Memory.WorkspaceRoot != "/tmp/memory" {
		t.Fatalf("memory config = %+v", got.Memory)
	}
	if got.Memory.Stage1IdleWindow != 2*time.Minute || got.Memory.Stage2SuccessCooldown != time.Hour {
		t.Fatalf("memory durations = %+v", got.Memory)
	}
	if got.Memory.Stage2OutputLimitPerUser != 17 {
		t.Fatalf("stage2 output limit = %d", got.Memory.Stage2OutputLimitPerUser)
	}
	if !got.Turn.Defaults.Policy.DisableApplyPatch {
		t.Fatalf("turn disable apply patch = false, want true")
	}
	if got.Turn.Defaults.Budget.MaxSteps != 88 {
		t.Fatalf("turn max steps = %d, want 88", got.Turn.Defaults.Budget.MaxSteps)
	}
	if got.Turn.Defaults.Budget.MaxModelCalls != 12 {
		t.Fatalf("turn max model calls = %d, want 12", got.Turn.Defaults.Budget.MaxModelCalls)
	}
	for id, role := range got.Turn.Roles {
		found := false
		for _, mw := range role.Middlewares {
			if mw != nil && mw.Name() == "repair_json" {
				found = true
			}
		}
		if !found {
			t.Fatalf("role %q missing repair_json middleware: %+v", id, role.Middlewares)
		}
	}
}
