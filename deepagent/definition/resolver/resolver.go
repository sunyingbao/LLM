package resolver

import (
	"context"
	"fmt"
	"strings"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/skill"
	"eino-cli/deepagent/definition"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
)

type CapabilityKind string

const (
	CapabilityModel       CapabilityKind = "model"
	CapabilityTool        CapabilityKind = "tool"
	CapabilityMiddleware  CapabilityKind = "middleware"
	CapabilitySkillLoader CapabilityKind = "skill_loader"
	CapabilityMemory      CapabilityKind = "memory"
	CapabilitySandbox     CapabilityKind = "sandbox"
)

type CapabilityError struct {
	Kind    CapabilityKind
	Binding string
}

func (err *CapabilityError) Error() (message string) {
	if err == nil {
		return ""
	}
	message = fmt.Sprintf("%s capability %q is unavailable", err.Kind, err.Binding)
	return message
}

type Resolved struct {
	Definition  agentdefinition.Definition
	Model       model.ToolCallingChatModel
	Tools       []tool.BaseTool
	Middleware  []middleware.Middleware
	SkillLoader skill.Loader
	Backend     backends.Backend
}

type Resolver struct{ registry *Registry }

func NewResolver(registry *Registry) (resolver *Resolver) {
	if registry == nil {
		registry = NewRegistry()
	}
	resolver = &Resolver{registry: registry}
	return resolver
}

func (resolver *Resolver) Resolve(ctx context.Context, definition agentdefinition.Definition) (resolved *Resolved, err error) {
	if err = agentdefinition.Validate(definition); err != nil {
		return nil, err
	}
	resolved = &Resolved{Definition: definition}
	registry := resolver.registry.snapshot()
	if name := strings.TrimSpace(definition.Model.Provider); name != "" {
		provider := registry.models[name]
		if provider == nil {
			return nil, missingCapability(CapabilityModel, name)
		}
		if resolved.Model, err = provider(ctx, definition.Model); err != nil {
			return nil, fmt.Errorf("resolve model %q: %w", name, err)
		}
	}
	for _, binding := range definition.Tools {
		provider := registry.tools[binding.Name]
		if provider == nil {
			return nil, missingCapability(CapabilityTool, binding.Name)
		}
		item, providerErr := provider(ctx, binding)
		if providerErr != nil {
			return nil, fmt.Errorf("resolve tool %q: %w", binding.Name, providerErr)
		}
		resolved.Tools = append(resolved.Tools, item)
	}
	for _, binding := range definition.Middleware {
		provider := registry.middleware[binding.Name]
		if provider == nil {
			return nil, missingCapability(CapabilityMiddleware, binding.Name)
		}
		item, providerErr := provider(ctx, binding)
		if providerErr != nil {
			return nil, fmt.Errorf("resolve middleware %q: %w", binding.Name, providerErr)
		}
		resolved.Middleware = append(resolved.Middleware, item)
	}
	if name := strings.TrimSpace(definition.Skills.Loader); name != "" {
		provider := registry.skillLoaders[name]
		if provider == nil {
			return nil, missingCapability(CapabilitySkillLoader, name)
		}
		if resolved.SkillLoader, err = provider(ctx, definition.Skills); err != nil {
			return nil, fmt.Errorf("resolve skill loader %q: %w", name, err)
		}
	}
	if name := strings.TrimSpace(definition.Memory.Provider); name != "" {
		provider := registry.memories[name]
		if provider == nil {
			return nil, missingCapability(CapabilityMemory, name)
		}
		item, providerErr := provider(ctx, definition.Memory)
		if providerErr != nil {
			return nil, fmt.Errorf("resolve memory %q: %w", name, providerErr)
		}
		resolved.Middleware = append(resolved.Middleware, item)
	}
	if name := strings.TrimSpace(definition.Sandbox.Backend); name != "" {
		provider := registry.sandboxes[name]
		if provider == nil {
			return nil, missingCapability(CapabilitySandbox, name)
		}
		if resolved.Backend, err = provider(ctx, definition.Sandbox); err != nil {
			return nil, fmt.Errorf("resolve sandbox %q: %w", name, err)
		}
	}
	return resolved, nil
}

func missingCapability(kind CapabilityKind, binding string) (err error) {
	err = &CapabilityError{Kind: kind, Binding: binding}
	return err
}
