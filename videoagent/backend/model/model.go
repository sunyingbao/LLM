package model

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

type ChatModelConfig struct {
	Provider string        `json:"provider"`
	APIKey   string        `json:"api_key"`
	BaseURL  string        `json:"base_url"`
	Model    string        `json:"model"`
	Timeout  time.Duration `json:"timeout"`
	Fornax   *FornaxConfig `json:"fornax,omitempty"`
}

// NewChatModel selects the native Fornax SDK or an OpenAI-compatible client.
func NewChatModel(ctx context.Context, config ChatModelConfig) (model.BaseChatModel, error) {
	provider := strings.ToLower(strings.TrimSpace(config.Provider))
	if provider == "fornax" {
		if err := validateFornaxConfig(config.Fornax); err != nil {
			return nil, err
		}
		if invalidCredential(config.APIKey) || invalidCredential(config.Model) || invalidCredential(config.BaseURL) {
			return nil, fmt.Errorf("Fornax MaaS api_key, model and base_url are required and cannot be placeholders")
		}
		return newFornaxChatModel(ctx, config)
	}
	if provider != "" && provider != "openai" && provider != "maas" {
		return nil, fmt.Errorf("unsupported chat model provider: %s", config.Provider)
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("chat model api key is empty")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("chat model name is empty")
	}
	if config.Timeout <= 0 {
		config.Timeout = 2 * time.Minute
	}
	return openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
		APIKey:     config.APIKey,
		BaseURL:    config.BaseURL,
		Model:      config.Model,
		HTTPClient: &http.Client{Timeout: config.Timeout},
	})
}

func validateFornaxConfig(config *FornaxConfig) error {
	if config == nil || invalidCredential(config.AK) || invalidCredential(config.SK) {
		return fmt.Errorf("Fornax ak and sk are required and cannot be placeholders")
	}
	return nil
}

func invalidCredential(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "" || strings.Contains(value, "replace-with-")
}
