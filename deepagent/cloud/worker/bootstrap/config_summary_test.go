package bootstrap

import (
	"strings"
	"testing"

	"eino-cli/deepagent/cloud/worker/bootstrap/config"
)

func TestRuntimeConfigSummaryDoesNotLeakSecrets(t *testing.T) {
	cfg := config.Default()
	model := cfg.Models["default"]
	model.ModelAPIKey = "model-secret"
	cfg.Models["default"] = model
	cfg.Fornax = config.FornaxConfig{Enabled: true, AK: "fornax-ak", SK: "fornax-sk"}

	got := runtimeConfigSummary(cfg)
	for _, secret := range []string{"model-secret", "fornax-ak", "fornax-sk"} {
		if strings.Contains(got, secret) {
			t.Fatalf("runtime config summary leaked %q in %q", secret, got)
		}
	}
}
