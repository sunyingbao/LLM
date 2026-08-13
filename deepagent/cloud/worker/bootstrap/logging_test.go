package bootstrap

import (
	"testing"

	"eino-cli/deepagent/cloud/worker/bootstrap/config"
)

func TestLogWriterSummaryIncludesAgentOnlyInTCE(t *testing.T) {
	cfg := config.Default()
	cfg.Log.EnableAgent = true

	if got := logWriterSummaryForEnv(cfg, false); got != "file" {
		t.Fatalf("non-TCE writers = %q, want file", got)
	}
	if got := logWriterSummaryForEnv(cfg, true); got != "file,agent" {
		t.Fatalf("TCE writers = %q, want file,agent", got)
	}
}

func TestLogWriterSummaryHonorsConsoleAndAgentSwitch(t *testing.T) {
	cfg := config.Default()
	cfg.Log.EnableAgent = false
	cfg.Log.EnableConsole = true

	if got := logWriterSummaryForEnv(cfg, true); got != "file,console" {
		t.Fatalf("writers = %q, want file,console", got)
	}
}
