package local

import (
	"context"
	"fmt"

	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	agentworker "eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/inprocess"
	workerthread "eino-cli/deepagent/worker/thread"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type HistoryStoreFactory func(ctx context.Context, threadID string) (store agentthread.HistoryRolloutStore, err error)
type CheckpointStoreFactory func(ctx context.Context, threadID string) (store compose.CheckPointStore, err error)

type AssemblyDependencies struct {
	AgentConfig     *deepagents.Config
	HistoryStore    HistoryStoreFactory
	CheckpointStore CheckpointStoreFactory
	ContextWindow   int64
	EventBuffer     int
	TurnCompleted   func(ctx context.Context, threadID, turnID string, chatModel model.ToolCallingChatModel, history []*schema.Message)
}

func NewThreadFactory(dependencies AssemblyDependencies) (factory inprocess.ThreadFactory, err error) {
	if dependencies.AgentConfig == nil || dependencies.AgentConfig.Model == nil {
		return nil, fmt.Errorf("agent config with model is required")
	}
	baseConfig := dependencies.AgentConfig.Clone()
	factory = func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (runtime agentworker.AgentThread, err error) {
		agentConfig := baseConfig.Clone()
		if state != nil && state.Profile.Cwd != "" {
			if agentConfig.FilesystemConfig == nil {
				agentConfig.FilesystemConfig = &deepagents.FilesystemConfig{}
			}
			agentConfig.FilesystemConfig.WorkDir = state.Profile.Cwd
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
		runConfig := turnConfigFromAgent(agentConfig, checkpoint)
		runConfig.TurnCompleted = dependencies.TurnCompleted
		thread := agentthread.New(state.ID, runConfig, eventBus, agentthread.ThreadOptions{
			HistoryStore:  history,
			ContextWindow: dependencies.ContextWindow,
		})
		runtime, err = workerthread.NewRuntime(workerthread.AdapterConfig{
			SessionID: state.SessionID,
			ThreadID:  state.ID,
			Thread:    thread,
			EventBus:  eventBus,
		})
		return runtime, err
	}
	return factory, nil
}

func turnConfigFromAgent(config *deepagents.Config, checkpoint compose.CheckPointStore) (turnConfig *agentthread.TurnConfig) {
	agentConfig := config.Clone()
	agentConfig.CheckpointStore = checkpoint
	if _, isSandbox := agentConfig.Backend.(backends.SandboxBackend); !isSandbox {
		agentConfig.Backend = nil
	}
	turnConfig = &agentthread.TurnConfig{Agent: *agentConfig}
	return turnConfig
}
