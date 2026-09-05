package local

import (
	"context"
	"testing"

	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/mock/mock_model"
	"eino-cli/deepagent/worker/inprocess"

	"go.uber.org/mock/gomock"
)

func TestTurnConfigFromAgentKeepsSandboxBackendBoundary(t *testing.T) {
	t.Parallel()

	config := &deepagents.Config{
		Backend: backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: t.TempDir()}),
	}
	runConfig := turnConfigFromAgent(config, nil)
	if runConfig.Agent.Backend != nil {
		t.Fatalf("runner backend = %T, want nil for a non-sandbox backend", runConfig.Agent.Backend)
	}
	if config.Backend == nil {
		t.Fatal("source config was mutated")
	}
}

func TestNewThreadFactoryBuildsSDKThreadRuntime(t *testing.T) {
	t.Parallel()

	controller := gomock.NewController(t)
	chatModel := mock_model.NewMockToolCallingChatModel(controller)
	factory, err := NewThreadFactory(AssemblyDependencies{AgentConfig: &deepagents.Config{Model: chatModel}})
	if err != nil {
		t.Fatalf("NewThreadFactory() error = %v", err)
	}
	runtime, err := factory(context.Background(), &inprocess.ThreadState{ID: "thread-1", SessionID: "session-1"}, nil)
	if err != nil || runtime == nil {
		t.Fatalf("ThreadFactory() runtime=%T error=%v", runtime, err)
	}
}
