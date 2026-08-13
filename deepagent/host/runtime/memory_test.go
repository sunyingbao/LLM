package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"eino-cli/backend/config"
	"eino-cli/backend/consts"
	memorystore "eino-cli/backend/memory/store"
)

func TestSGADKMemoryMiddlewareReloadsStructuredAndDreamMemoryPerRun(t *testing.T) {
	root := t.TempDir()
	restore := config.SetRootDirForTest(root)
	t.Cleanup(restore)
	store := memorystore.NewStore()
	data := memorystore.GetEmptyMemoryData()
	data.User.WorkContext = memorystore.Section{Summary: "first context"}
	if err := store.Save(consts.DefaultAgentKey, data); err != nil {
		t.Fatalf("save structured memory: %v", err)
	}
	if err := os.MkdirAll(config.DreamMemoryDir(), 0o755); err != nil {
		t.Fatalf("mkdir dream memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(config.DreamMemoryDir(), "MEMORY.md"), []byte("first dream"), 0o644); err != nil {
		t.Fatalf("write dream memory: %v", err)
	}
	middleware := newSGADKMemoryMiddleware()

	first, err := middleware.BuildInitialContext(context.Background())
	if err != nil {
		t.Fatalf("BuildInitialContext(first) error=%v", err)
	}
	if got := joinedMessageContent(first); !strings.Contains(got, "first context") || !strings.Contains(got, "first dream") {
		t.Fatalf("first memory context=%q", got)
	}

	data.User.WorkContext = memorystore.Section{Summary: "second context"}
	if err = store.Save(consts.DefaultAgentKey, data); err != nil {
		t.Fatalf("update structured memory: %v", err)
	}
	if err = os.WriteFile(filepath.Join(config.DreamMemoryDir(), "MEMORY.md"), []byte("second dream"), 0o644); err != nil {
		t.Fatalf("update dream memory: %v", err)
	}
	second, err := middleware.BuildInitialContext(context.Background())
	if err != nil {
		t.Fatalf("BuildInitialContext(second) error=%v", err)
	}
	if got := joinedMessageContent(second); strings.Contains(got, "first context") || strings.Contains(got, "first dream") || !strings.Contains(got, "second context") || !strings.Contains(got, "second dream") {
		t.Fatalf("reloaded memory context=%q", got)
	}
}

func joinedMessageContent(messages []*schema.Message) (content string) {
	for _, message := range messages {
		if message != nil {
			content += message.Content
		}
	}
	return content
}
