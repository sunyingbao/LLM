package constant

import "testing"

func TestIsEnglishPromptLang(t *testing.T) {
	t.Setenv(PromptLangEnv, " en ")
	if !IsEnglishPromptLang() {
		t.Fatalf("IsEnglishPromptLang() = false, want true")
	}
}

func TestIsEnglishPromptLangLegacyDefault(t *testing.T) {
	t.Setenv(PromptLangEnv, "zh")
	if IsEnglishPromptLang() {
		t.Fatalf("IsEnglishPromptLang() = true, want false")
	}
}
