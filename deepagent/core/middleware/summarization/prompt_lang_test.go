package summarization

import (
	"strings"
	"testing"

	"eino-cli/deepagent/core/constant"
)

func TestSummarizationMiddleware_EnglishDefaultPromptAndCustomPrompt(t *testing.T) {
	t.Setenv(constant.PromptLangEnv, "en")

	m := New(nil)
	if containsHan(m.config.SummaryPrompt) {
		t.Fatalf("English summary prompt contains Chinese")
	}
	if !strings.Contains(chainSummaryPrompt(), "Merge the existing summary") {
		t.Fatalf("chain summary prompt did not switch to English")
	}

	custom := "自定义摘要提示词：%s"
	m = New(&SummarizationConfig{SummaryPrompt: custom})
	if m.config.SummaryPrompt != custom {
		t.Fatalf("custom SummaryPrompt = %q, want %q", m.config.SummaryPrompt, custom)
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
