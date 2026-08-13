package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
)

func TestMemoryMiddleware_EnglishPromptEnv(t *testing.T) {
	t.Setenv(constant.PromptLangEnv, "en")

	ctx := context.Background()
	backend := newFilesystemTestBackend(t, map[string]*backends.FileData{
		"/memory/user.md": {
			Content:    []string{"remembered preference"},
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
	})
	m := New(&MemoryConfig{
		Backend:     backend,
		Sources:     []string{"/memory/user.md"},
		EnableLearn: true,
	})
	if err := m.BeforeAgent(ctx); err != nil {
		t.Fatalf("BeforeAgent() error = %v", err)
	}

	msgs, err := m.BuildInitialContext(ctx)
	if err != nil {
		t.Fatalf("BuildInitialContext() error = %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "## Persistent Memory") {
		t.Fatalf("missing English memory header: %s", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "remembered preference") {
		t.Fatalf("memory content was not preserved: %s", msgs[0].Content)
	}
	if containsHan(msgs[0].Content) {
		t.Fatalf("English memory prompt contains Chinese: %s", msgs[0].Content)
	}
}

func containsHan(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}
