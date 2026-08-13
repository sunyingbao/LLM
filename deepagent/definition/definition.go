package agentdefinition

// Config contains JSON-compatible, non-secret provider configuration.
// Runtime hosts resolve credentials and process-local dependencies by binding name.
type Config map[string]any

// Definition describes an Agent independently of its execution environment.
type Definition struct {
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Instructions string              `json:"instructions,omitempty"`
	Model        ModelPolicy         `json:"model,omitempty"`
	Tools        []ToolBinding       `json:"tools,omitempty"`
	Middleware   []MiddlewareBinding `json:"middleware,omitempty"`
	Skills       SkillPolicy         `json:"skills,omitempty"`
	Memory       MemoryPolicy        `json:"memory,omitempty"`
	Sandbox      SandboxPolicy       `json:"sandbox,omitempty"`
	Limits       RuntimeLimits       `json:"limits,omitempty"`
}

// ModelPolicy selects a model registered by the runtime host.
type ModelPolicy struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Config   Config `json:"config,omitempty"`
}

// ToolBinding selects a named tool and its declarative configuration.
type ToolBinding struct {
	Name   string `json:"name"`
	Config Config `json:"config,omitempty"`
}

// MiddlewareBinding selects a named middleware and its declarative configuration.
type MiddlewareBinding struct {
	Name   string `json:"name"`
	Config Config `json:"config,omitempty"`
}

// SkillPolicy selects the runtime skill loader and optional skill allowlist.
type SkillPolicy struct {
	Loader string   `json:"loader,omitempty"`
	Names  []string `json:"names,omitempty"`
}

// MemoryPolicy selects a runtime-managed memory provider.
type MemoryPolicy struct {
	Provider string `json:"provider,omitempty"`
	Config   Config `json:"config,omitempty"`
}

// SandboxPolicy selects a runtime-managed sandbox backend.
type SandboxPolicy struct {
	Backend string `json:"backend,omitempty"`
	Config  Config `json:"config,omitempty"`
}

// RuntimeLimits bounds one logical Agent turn. Zero delegates to runtime defaults.
type RuntimeLimits struct {
	MaxSteps      int `json:"max_steps,omitempty"`
	MaxModelCalls int `json:"max_model_calls,omitempty"`
}
