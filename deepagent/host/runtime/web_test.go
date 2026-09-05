package runtime

import (
	"testing"

	"eino-cli/deepagent/backend/config"
)

func TestWebSearchUsesCoreProviderConfiguration(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "test-tavily")
	for _, provider := range []string{"", "duckduckgo", "tavily"} {
		cfg, err := buildWebConfig(config.WebSearch{Enabled: true, Provider: provider})
		if err != nil || cfg == nil || !cfg.EnableWebSearch {
			t.Fatalf("provider %q: config=%+v error=%v", provider, cfg, err)
		}
		if cfg.UseDuckDuckGo != (provider != "tavily") {
			t.Fatalf("provider %q uses wrong search engine", provider)
		}
	}
	if _, err := buildWebConfig(config.WebSearch{Enabled: true, Provider: "bocha", APIKey: "wrong-provider-key"}); err == nil {
		t.Fatal("obsolete provider must not send its key to a different service")
	}
	if cfg, err := buildWebConfig(config.WebSearch{}); cfg != nil || err != nil {
		t.Fatalf("disabled web search: config=%+v error=%v", cfg, err)
	}
}
