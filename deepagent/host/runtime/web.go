package runtime

import (
	"fmt"
	"os"
	"strings"
	"time"

	"eino-cli/deepagent/backend/config"
	"eino-cli/deepagent/core/middleware/web"
)

func buildWebConfig(cfg config.WebSearch) (webConfig *web.WebConfig, err error) {
	if !cfg.Enabled {
		return nil, nil
	}
	webConfig = &web.WebConfig{
		EnableWebSearch: true,
		Timeout:         time.Duration(cfg.TimeoutSeconds) * time.Second,
		MaxResults:      cfg.MaxResults,
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "duckduckgo":
		webConfig.UseDuckDuckGo = true
	case "tavily":
		webConfig.TavilyAPIKey = strings.TrimSpace(cfg.APIKey)
		if webConfig.TavilyAPIKey == "" {
			keyEnv := cfg.APIKeyEnv
			if keyEnv == "" {
				keyEnv = "TAVILY_API_KEY"
			}
			webConfig.TavilyAPIKey = os.Getenv(keyEnv)
		}
		if webConfig.TavilyAPIKey == "" {
			return nil, fmt.Errorf("web_search: Tavily API key is required")
		}
	default:
		return nil, fmt.Errorf("web_search: unsupported provider %q (use duckduckgo or tavily)", cfg.Provider)
	}
	return webConfig, nil
}
