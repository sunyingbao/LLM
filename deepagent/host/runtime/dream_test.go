package runtime

import (
	"context"
	"testing"

	"eino-cli/backend/config"
)

func TestConsolidateMemoryWithoutTranscriptsIsSuccessfulNoOp(t *testing.T) {
	restore := config.SetRootDirForTest(t.TempDir())
	t.Cleanup(restore)
	runtime := &LocalRuntime{cfg: &config.Config{}}

	result, err := runtime.ConsolidateMemory(context.Background())
	if err != nil {
		t.Fatalf("ConsolidateMemory() error=%v", err)
	}
	if !result.Success || result.Output != "dream: no transcript sessions to consolidate" {
		t.Fatalf("ConsolidateMemory() result=%+v", result)
	}
}
