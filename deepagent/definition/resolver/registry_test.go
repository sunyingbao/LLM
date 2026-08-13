package resolver

import (
	"context"
	"errors"
	"testing"

	"eino-cli/deepagent/definition"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
)

func TestResolverReportsMissingCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		definition agentdefinition.Definition
		kind       CapabilityKind
		binding    string
	}{
		{name: "model", definition: agentdefinition.Definition{Name: "a", Version: "v1", Model: agentdefinition.ModelPolicy{Provider: "missing"}}, kind: CapabilityModel, binding: "missing"},
		{name: "tool", definition: agentdefinition.Definition{Name: "a", Version: "v1", Tools: []agentdefinition.ToolBinding{{Name: "missing"}}}, kind: CapabilityTool, binding: "missing"},
		{name: "middleware", definition: agentdefinition.Definition{Name: "a", Version: "v1", Middleware: []agentdefinition.MiddlewareBinding{{Name: "missing"}}}, kind: CapabilityMiddleware, binding: "missing"},
		{name: "skill loader", definition: agentdefinition.Definition{Name: "a", Version: "v1", Skills: agentdefinition.SkillPolicy{Loader: "missing"}}, kind: CapabilitySkillLoader, binding: "missing"},
		{name: "memory", definition: agentdefinition.Definition{Name: "a", Version: "v1", Memory: agentdefinition.MemoryPolicy{Provider: "missing"}}, kind: CapabilityMemory, binding: "missing"},
		{name: "sandbox", definition: agentdefinition.Definition{Name: "a", Version: "v1", Sandbox: agentdefinition.SandboxPolicy{Backend: "missing"}}, kind: CapabilitySandbox, binding: "missing"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewResolver(NewRegistry()).Resolve(context.Background(), tt.definition)
			var capabilityErr *CapabilityError
			if !errors.As(err, &capabilityErr) {
				t.Fatalf("Resolve() error = %v, want CapabilityError", err)
			}
			if capabilityErr.Kind != tt.kind || capabilityErr.Binding != tt.binding {
				t.Fatalf("CapabilityError = %+v", capabilityErr)
			}
		})
	}
}

func TestResolverDoesNotHoldRegistryLockWhileCallingProvider(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.RegisterModel("model", func(ctx context.Context, policy ModelPolicy) (chatModel model.ToolCallingChatModel, err error) {
		registry.RegisterTool("late-tool", func(ctx context.Context, binding ToolBinding) (baseTool tool.BaseTool, err error) {
			return nil, nil
		})
		return nil, nil
	})
	_, err := NewResolver(registry).Resolve(context.Background(), agentdefinition.Definition{Name: "a", Version: "v1", Model: agentdefinition.ModelPolicy{Provider: "model"}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
}
