package plan

import (
	"context"
	"strings"
	"testing"

	"eino-cli/deepagent/core/constant"
)

func TestPlan_EnglishEnvKeepsEnglishPrompt(t *testing.T) {
	t.Setenv(constant.PromptLangEnv, "en")

	msgs, err := New(nil).BuildInitialContext(context.Background())
	if err != nil {
		t.Fatalf("BuildInitialContext() error = %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "## Planning") {
		t.Fatalf("missing Plan header: %s", msgs[0].Content)
	}
	for _, r := range msgs[0].Content {
		if r >= '\u4e00' && r <= '\u9fff' {
			t.Fatalf("Plan prompt contains Chinese: %s", msgs[0].Content)
		}
	}
}
