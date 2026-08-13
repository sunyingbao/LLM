package local

import (
	"context"
	"errors"
	"fmt"

	workerthread "eino-cli/deepagent/cloud/worker/thread"
	"eino-cli/deepagent/core"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/baseprompt"
	"eino-cli/deepagent/definition"
	definitionresolver "eino-cli/deepagent/definition/resolver"
	runtimeclient "eino-cli/deepagent/runtime"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/inprocess"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// BuildDeepAgentConfig converts a fully resolved definition into the existing
// SDK assembly model. It does not construct providers or read credentials.
func BuildDeepAgentConfig(resolved *definitionresolver.Resolved, workspace runtimeclient.WorkspaceSpec) (config *deepagents.Config, err error) {
	if resolved == nil {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "local.assemble", Runtime: runtimeclient.RuntimeLocal, Message: "resolved definition is required"}
	}
	if resolved.Model == nil {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeCapabilityUnavailable, Op: "local.assemble", Runtime: runtimeclient.RuntimeLocal, Message: "model capability is required"}
	}
	middlewares := make([]middleware.Middleware, 0, len(resolved.Middleware)+1)
	if resolved.Definition.Instructions != "" {
		middlewares = append(middlewares, baseprompt.New(resolved.Definition.Instructions))
	}
	middlewares = append(middlewares, resolved.Middleware...)
	config = &deepagents.Config{
		Model:         resolved.Model,
		MaxSteps:      resolved.Definition.Limits.MaxSteps,
		MaxModelCalls: resolved.Definition.Limits.MaxModelCalls,
		WorkDir:       workspace.Cwd,
		Tools:         append([]tool.BaseTool(nil), resolved.Tools...),
		SkillLoader:   resolved.SkillLoader,
		Backend:       resolved.Backend,
		Middlewares:   middlewares,
	}
	config.EnableFilesystem = resolved.Backend != nil
	if enabled, ok := resolved.Definition.Sandbox.Config["enable_filesystem_tools"].(bool); ok {
		config.EnableFilesystem = enabled
	}
	return config, nil
}

func mapResolveError(operation string, cause error) (err error) {
	var capabilityErr *definitionresolver.CapabilityError
	if errors.As(cause, &capabilityErr) {
		return &runtimeclient.Error{Code: runtimeclient.ErrorCodeCapabilityUnavailable, Op: operation, Runtime: runtimeclient.RuntimeLocal, Message: capabilityErr.Error(), Cause: cause}
	}
	err = &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: operation, Runtime: runtimeclient.RuntimeLocal, Cause: cause}
	return err
}

type HistoryStoreFactory func(ctx context.Context, threadID string) (store agentthread.HistoryRolloutStore, err error)
type CheckpointStoreFactory func(ctx context.Context, threadID string) (store compose.CheckPointStore, err error)

type AssemblyDependencies struct {
	Definition      agentdefinition.Definition
	Resolver        *definitionresolver.Resolver
	HistoryStore    HistoryStoreFactory
	CheckpointStore CheckpointStoreFactory
	ContextWindow   int64
	EventBuffer     int
	TurnCompleted   func(ctx context.Context, threadID, turnID string, chatModel model.ToolCallingChatModel, history []*schema.Message)
}

// NewThreadFactory reuses the SDK AgentThread and CloudAgent protocol adapter
// for local in-process execution.
func NewThreadFactory(dependencies AssemblyDependencies) (factory inprocess.ThreadFactory, err error) {
	if dependencies.Resolver == nil {
		return nil, fmt.Errorf("definition resolver is required")
	}
	if err = agentdefinition.Validate(dependencies.Definition); err != nil {
		return nil, err
	}
	factory = func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (runtime agentworker.AgentThread, err error) {
		resolved, resolveErr := dependencies.Resolver.Resolve(ctx, dependencies.Definition)
		if resolveErr != nil {
			return nil, mapResolveError("local.thread_factory.resolve", resolveErr)
		}
		config, configErr := BuildDeepAgentConfig(resolved, runtimeclient.WorkspaceSpec{Cwd: state.Profile.Cwd})
		if configErr != nil {
			return nil, configErr
		}
		var history agentthread.HistoryRolloutStore
		if dependencies.HistoryStore != nil {
			if history, err = dependencies.HistoryStore(ctx, state.ID); err != nil {
				return nil, fmt.Errorf("open history store: %w", err)
			}
		}
		var checkpoint compose.CheckPointStore
		if dependencies.CheckpointStore != nil {
			if checkpoint, err = dependencies.CheckpointStore(ctx, state.ID); err != nil {
				return nil, fmt.Errorf("open checkpoint store: %w", err)
			}
		}
		buffer := dependencies.EventBuffer
		if buffer <= 0 {
			buffer = 256
		}
		eventBus := make(chan agentthread.Event, buffer)
		runnerConfig := turnRunnerConfigFromResolved(config, checkpoint)
		runnerConfig.TurnCompleted = dependencies.TurnCompleted
		thread := agentthread.NewDefault(state.ID, runnerConfig, eventBus, agentthread.DefaultThreadOptions{HistoryStore: history, ContextWindow: dependencies.ContextWindow})
		runtime, err = workerthread.NewRuntime(workerthread.AdapterConfig{SessionID: state.SessionID, ThreadID: state.ID, Thread: thread, EventBus: eventBus})
		return runtime, err
	}
	return factory, nil
}

func turnRunnerConfigFromResolved(config *deepagents.Config, checkpoint compose.CheckPointStore) (runner *agentthread.TurnRunnerConfig) {
	runner = &agentthread.TurnRunnerConfig{
		ChatModel: config.Model, Tools: config.Tools, Middlewares: config.Middlewares,
		MaxSteps: config.MaxSteps, MaxModelCalls: config.MaxModelCalls,
		EnableFilesystem: config.EnableFilesystem, WorkDir: config.WorkDir,
		SkillLoader: config.SkillLoader, CheckpointStore: checkpoint,
	}
	if sandbox, ok := config.Backend.(backends.SandboxBackend); ok {
		runner.SandboxBackend = sandbox
	}
	return runner
}
