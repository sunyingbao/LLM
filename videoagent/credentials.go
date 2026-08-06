package videoagent

import (
	"fmt"
	"strings"
)

// CredentialsConfig keeps local service credentials outside tracked runtime configuration.
type CredentialsConfig struct {
	Fornax FornaxConfig               `json:"fornax"`
	Models map[string]ModelCredential `json:"models"`
}

// ModelCredential describes one MaaS endpoint selected by prompt key.
type ModelCredential struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url"`
	Region   string `json:"region"`
	Endpoint string `json:"endpoint"`
}

// ChatModelConfig builds a native Fornax model config from the shared identity and one MaaS endpoint.
func (config CredentialsConfig) ChatModelConfig(promptKey string) (ChatModelConfig, error) {
	credential, exists := config.Models[promptKey]
	if !exists {
		return ChatModelConfig{}, fmt.Errorf("model credentials are missing for prompt %s", promptKey)
	}
	if invalidCredential(credential.APIKey) || invalidCredential(credential.BaseURL) || invalidCredential(credential.Endpoint) {
		return ChatModelConfig{}, fmt.Errorf("model credentials are incomplete for prompt %s", promptKey)
	}
	if err := validateFornaxConfig(&config.Fornax); err != nil {
		return ChatModelConfig{}, err
	}
	fornax := config.Fornax
	if fornax.Region == "" {
		fornax.Region = strings.TrimSpace(credential.Region)
	}
	return ChatModelConfig{
		Provider: "fornax",
		APIKey:   credential.APIKey,
		BaseURL:  credential.BaseURL,
		Model:    credential.Endpoint,
		Fornax:   &fornax,
	}, nil
}
