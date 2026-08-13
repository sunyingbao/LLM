package config

type ThinkingType string

const (
	ThinkingTypeEnabled  ThinkingType = "enabled"
	ThinkingTypeDisabled ThinkingType = "disabled"
	ThinkingTypeAuto     ThinkingType = "auto"
)

type SDKType string

const (
	SDKTypeArk    SDKType = "ark"
	SDKTypeOpenAI SDKType = "openai"
	SDKTypeKimi   SDKType = "kimi"
)

type ModelConfig struct {
	ModelName        string        `json:"model_name" yaml:"model_name"`
	SDKType          SDKType       `json:"sdk_type" yaml:"sdk_type"`
	ModelBaseURL     string        `json:"model_base_url" yaml:"model_base_url"`
	ModelAPIKey      string        `json:"model_api_key" yaml:"model_api_key"`
	ModelEndpointID  string        `json:"model_endpoint_id" yaml:"model_endpoint_id"`
	Temperature      *float32      `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	MaxTokens        int32         `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	ContextWindow    int64         `json:"context_window,omitempty" yaml:"context_window,omitempty"`
	TopP             *float32      `json:"top_p,omitempty" yaml:"top_p,omitempty"`
	FrequencyPenalty *float32      `json:"frequency_penalty,omitempty" yaml:"frequency_penalty,omitempty"`
	Thinking         *ThinkingType `json:"thinking,omitempty" yaml:"thinking,omitempty"`
	DisableByAzure   bool          `json:"disable_by_azure,omitempty" yaml:"disable_by_azure,omitempty"`
	APIVersion       string        `json:"api_version,omitempty" yaml:"api_version,omitempty"`
	LogID            string        `json:"log_id,omitempty" yaml:"log_id,omitempty"`
}

type ApprovalPolicy string

const (
	ApprovalPolicyNormal     ApprovalPolicy = "normal"
	ApprovalPolicyReadOnly   ApprovalPolicy = "readonly"
	ApprovalPolicyPermissive ApprovalPolicy = "permissive"
)

type RoleConfig struct {
	Prompt         string         `json:"prompt" yaml:"prompt"`
	Models         []string       `json:"models" yaml:"models"`
	DefaultModel   string         `json:"default_model" yaml:"default_model"`
	ApprovalPolicy ApprovalPolicy `json:"approval_policy" yaml:"approval_policy"`
}
