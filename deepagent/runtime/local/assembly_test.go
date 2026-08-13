package local

import (
	"context"
	"errors"
	"testing"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/middleware/baseprompt"
	"eino-cli/deepagent/definition"
	definitionresolver "eino-cli/deepagent/definition/resolver"
	"eino-cli/deepagent/mock/mock_model"
	runtimeclient "eino-cli/deepagent/runtime"
	"eino-cli/deepagent/worker/inprocess"
	"github.com/cloudwego/eino/components/model"
	"go.uber.org/mock/gomock"
)

func TestBuildDeepAgentConfig(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	chatModel := mock_model.NewMockToolCallingChatModel(controller)
	resolved := &definitionresolver.Resolved{
		Definition: agentdefinition.Definition{
			Name: "assistant", Version: "v1", Instructions: "identity",
			Limits: agentdefinition.RuntimeLimits{MaxSteps: 8, MaxModelCalls: 3},
		},
		Model: chatModel,
	}
	config, err := BuildDeepAgentConfig(resolved, runtimeclient.WorkspaceSpec{Cwd: "/workspace"})
	if err != nil {
		t.Fatalf("BuildDeepAgentConfig() error = %v", err)
	}
	if config.Model != chatModel || config.WorkDir != "/workspace" || config.MaxSteps != 8 || config.MaxModelCalls != 3 {
		t.Fatalf("BuildDeepAgentConfig() = %+v", config)
	}
	if len(config.Middlewares) != 1 || config.Middlewares[0].Name() != baseprompt.BasePromptMiddlewareName {
		t.Fatalf("middlewares = %+v", config.Middlewares)
	}
}

func TestBuildDeepAgentConfigCanBindBackendWithoutDuplicateFilesystemTools(t *testing.T) {
	controller := gomock.NewController(t)
	resolved := &definitionresolver.Resolved{
		Definition: agentdefinition.Definition{
			Name: "assistant", Version: "v1",
			Sandbox: agentdefinition.SandboxPolicy{Backend: "workspace", Config: agentdefinition.Config{
				"enable_filesystem_tools": false,
			}},
		},
		Model:   mock_model.NewMockToolCallingChatModel(controller),
		Backend: backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: t.TempDir()}),
	}
	config, err := BuildDeepAgentConfig(resolved, runtimeclient.WorkspaceSpec{})
	if err != nil {
		t.Fatalf("BuildDeepAgentConfig() error=%v", err)
	}
	if config.Backend == nil || config.EnableFilesystem {
		t.Fatalf("backend=%T enable_filesystem=%v", config.Backend, config.EnableFilesystem)
	}
}

func TestNewThreadFactoryBuildsSDKThreadRuntime(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	chatModel := mock_model.NewMockToolCallingChatModel(controller)
	registry := definitionresolver.NewRegistry()
	registry.RegisterModel("default", func(ctx context.Context, policy agentdefinition.ModelPolicy) (resolvedModel model.ToolCallingChatModel, err error) {
		return chatModel, nil
	})
	definition := agentdefinition.Definition{Name: "assistant", Version: "v1", Model: agentdefinition.ModelPolicy{Provider: "default"}}
	factory, err := NewThreadFactory(AssemblyDependencies{Definition: definition, Resolver: definitionresolver.NewResolver(registry)})
	if err != nil {
		t.Fatalf("NewThreadFactory() error = %v", err)
	}
	runtime, err := factory(context.Background(), &inprocess.ThreadState{ID: "thread-1", SessionID: "session-1"}, nil)
	if err != nil || runtime == nil {
		t.Fatalf("ThreadFactory() runtime=%T error=%v", runtime, err)
	}
}

func TestMapResolveErrorMapsMissingCapability(t *testing.T) {
	t.Parallel()

	err := mapResolveError("local.resolve", &definitionresolver.CapabilityError{Kind: definitionresolver.CapabilityTool, Binding: "search"})
	if !errors.Is(err, runtimeclient.ErrCapabilityUnavailable) {
		t.Fatalf("mapResolveError() = %v", err)
	}
}
