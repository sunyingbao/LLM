package resolver

import (
	"context"
	"sync"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/skill"
	"eino-cli/deepagent/definition"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
)

type ModelPolicy = agentdefinition.ModelPolicy
type ToolBinding = agentdefinition.ToolBinding
type MiddlewareBinding = agentdefinition.MiddlewareBinding
type SkillPolicy = agentdefinition.SkillPolicy
type MemoryPolicy = agentdefinition.MemoryPolicy
type SandboxPolicy = agentdefinition.SandboxPolicy

type ModelProvider func(ctx context.Context, policy ModelPolicy) (chatModel model.ToolCallingChatModel, err error)
type ToolProvider func(ctx context.Context, binding ToolBinding) (baseTool tool.BaseTool, err error)
type MiddlewareProvider func(ctx context.Context, binding MiddlewareBinding) (item middleware.Middleware, err error)
type SkillLoaderProvider func(ctx context.Context, policy SkillPolicy) (loader skill.Loader, err error)
type MemoryProvider func(ctx context.Context, policy MemoryPolicy) (item middleware.Middleware, err error)
type SandboxProvider func(ctx context.Context, policy SandboxPolicy) (backend backends.Backend, err error)

type Registry struct {
	mu           sync.RWMutex
	models       map[string]ModelProvider
	tools        map[string]ToolProvider
	middleware   map[string]MiddlewareProvider
	skillLoaders map[string]SkillLoaderProvider
	memories     map[string]MemoryProvider
	sandboxes    map[string]SandboxProvider
}

func NewRegistry() (registry *Registry) {
	registry = &Registry{
		models:       make(map[string]ModelProvider),
		tools:        make(map[string]ToolProvider),
		middleware:   make(map[string]MiddlewareProvider),
		skillLoaders: make(map[string]SkillLoaderProvider),
		memories:     make(map[string]MemoryProvider),
		sandboxes:    make(map[string]SandboxProvider),
	}
	return registry
}

func (registry *Registry) RegisterModel(name string, provider ModelProvider) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.models[name] = provider
}

func (registry *Registry) RegisterTool(name string, provider ToolProvider) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.tools[name] = provider
}

func (registry *Registry) RegisterMiddleware(name string, provider MiddlewareProvider) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.middleware[name] = provider
}

func (registry *Registry) RegisterSkillLoader(name string, provider SkillLoaderProvider) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.skillLoaders[name] = provider
}

func (registry *Registry) RegisterMemory(name string, provider MemoryProvider) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.memories[name] = provider
}

func (registry *Registry) RegisterSandbox(name string, provider SandboxProvider) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.sandboxes[name] = provider
}

type registrySnapshot struct {
	models       map[string]ModelProvider
	tools        map[string]ToolProvider
	middleware   map[string]MiddlewareProvider
	skillLoaders map[string]SkillLoaderProvider
	memories     map[string]MemoryProvider
	sandboxes    map[string]SandboxProvider
}

func (registry *Registry) snapshot() (snapshot registrySnapshot) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	snapshot = registrySnapshot{
		models:       cloneProviderMap(registry.models),
		tools:        cloneProviderMap(registry.tools),
		middleware:   cloneProviderMap(registry.middleware),
		skillLoaders: cloneProviderMap(registry.skillLoaders),
		memories:     cloneProviderMap(registry.memories),
		sandboxes:    cloneProviderMap(registry.sandboxes),
	}
	return snapshot
}

func cloneProviderMap[T any](source map[string]T) (cloned map[string]T) {
	cloned = make(map[string]T, len(source))
	for name, provider := range source {
		cloned[name] = provider
	}
	return cloned
}
