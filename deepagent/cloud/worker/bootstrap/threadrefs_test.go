package bootstrap

import (
	"testing"

	"eino-cli/deepagent/cloud/worker/bootstrap/config"
)

func TestNewThreadRefStoreDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ThreadRefs.Enabled = false

	if got := newThreadRefStore(nil, cfg); got != nil {
		t.Fatalf("thread ref store should be nil when disabled")
	}
}
