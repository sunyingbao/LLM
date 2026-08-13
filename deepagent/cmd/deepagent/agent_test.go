package main

import "testing"

func TestOpenAIModelConfigReadsDeepSeekAPIKey(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-key")
	t.Setenv("OPENAI_MODEL", "gpt-test")
	t.Setenv("OPENAI_BASE_URL", "https://example.com/v1")

	cfg := openAIModelConfigFromEnv()
	if cfg.APIKey != "deepseek-key" {
		t.Fatalf("APIKey = %q, want DEEPSEEK_API_KEY", cfg.APIKey)
	}
	if cfg.Model != "gpt-test" {
		t.Fatalf("Model = %q, want OPENAI_MODEL", cfg.Model)
	}
	if cfg.BaseURL != "https://example.com/v1" {
		t.Fatalf("BaseURL = %q, want OPENAI_BASE_URL", cfg.BaseURL)
	}
}

func TestValidateModelEnvAcceptsDeepSeekAPIKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("KIMI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ARK_MODEL", "")
	t.Setenv("ARK_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-key")

	if err := validateModelEnv(); err != nil {
		t.Fatalf("validateModelEnv() error = %v", err)
	}
}

func TestStatusLineModelNameUsesDeepSeekAPIKey(t *testing.T) {
	t.Setenv("MODEL_NAME", "")
	t.Setenv("OPENROUTER_MODEL", "")
	t.Setenv("KIMI_MODEL", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("ARK_MODEL", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("KIMI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-key")

	if got := statusLineModelNameFromEnv(); got != "gpt-4o" {
		t.Fatalf("statusLineModelNameFromEnv() = %q, want gpt-4o", got)
	}
}
