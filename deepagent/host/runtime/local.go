package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"code.byted.org/gopkg/logs/v2"
	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	sdkmiddleware "eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/baseprompt"
	"eino-cli/deepagent/core/middleware/skill"
	sdkruntime "eino-cli/deepagent/runtime"
	localclient "eino-cli/deepagent/runtime/local"
	"eino-cli/deepagent/worker/inprocess"
	inprocessstore "eino-cli/deepagent/worker/inprocess/store"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"eino-cli/deepagent/backend/config"
	"eino-cli/deepagent/backend/consts"
	memorystore "eino-cli/deepagent/backend/memory/store"
	"eino-cli/deepagent/backend/sandbox"
	host "eino-cli/deepagent/host"
	"eino-cli/deepagent/host/checkpoint"
	agentmemory "eino-cli/deepagent/memory/facts"
	agenttools "eino-cli/deepagent/tools"
)

func NewLocalRuntime(ctx context.Context, cfg *config.Config, sessionID string) (runtime *LocalRuntime, err error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session id is required")
	}
	root := config.RootDir()
	runtimeDir := filepath.Join(root, ".eino-cli", "runtime")
	if err = os.MkdirAll(runtimeDir, 0o700); err != nil {
		return nil, fmt.Errorf("create runtime directory: %w", err)
	}
	databasePath := filepath.Join(runtimeDir, "local.db")
	db, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("open local runtime database: %w", err)
	}
	threadStore := inprocessstore.NewSQLiteThreadStateStore(db, "")
	eventStore := inprocessstore.NewSQLiteEventStore(db, "")
	if err = threadStore.AutoMigrate(ctx); err != nil {
		return nil, err
	}
	if err = eventStore.AutoMigrate(ctx); err != nil {
		return nil, err
	}

	agentConfig, err := buildLocalAgentConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var recordSequence atomic.Int64
	recordSequence.Store(time.Now().UnixMilli() * 1000)
	historyStore := agentthread.NewGormHistoryRolloutStore(db, "", func(ctx context.Context, threadID, turnID string) (id int64) {
		id = recordSequence.Add(1)
		return id
	})
	if err = historyStore.AutoMigrate(ctx); err != nil {
		return nil, err
	}
	threadFactory, err := localclient.NewThreadFactory(localclient.AssemblyDependencies{
		AgentConfig: agentConfig,
		HistoryStore: func(ctx context.Context, threadID string) (store agentthread.HistoryRolloutStore, err error) {
			return historyStore, nil
		},
		CheckpointStore: func(ctx context.Context, threadID string) (store compose.CheckPointStore, err error) {
			store = checkpoint.NewStore(filepath.Join(runtimeDir, "checkpoints", threadID))
			return store, nil
		},
		TurnCompleted: memoryTurnCompleted(),
	})
	if err != nil {
		return nil, err
	}
	worker, err := inprocess.NewWorker(inprocess.Dependencies{ThreadStateStore: threadStore, EventStore: eventStore, ThreadFactory: threadFactory})
	if err != nil {
		return nil, err
	}
	client, err := localclient.New(worker, localclient.Options{UserID: 1, SubscriberBuffer: 256})
	if err != nil {
		return nil, err
	}
	runtime = &LocalRuntime{
		router: &sdkruntime.Router{Local: client}, sessionID: sessionID,
		modelName: cfg.DefaultModel, runtimeKind: sdkruntime.RuntimeLocal,
	}
	return runtime, nil
}

func memoryTurnCompleted() (completed func(ctx context.Context, threadID, turnID string, chatModel model.ToolCallingChatModel, history []*schema.Message)) {
	updater := agentmemory.NewMemoryUpdater(memorystore.NewStore())
	completed = func(ctx context.Context, threadID, turnID string, chatModel model.ToolCallingChatModel, history []*schema.Message) {
		memoryContext := context.WithoutCancel(ctx)
		messages := append([]*schema.Message(nil), history...)
		go func() {
			if updateErr := updater.Run(memoryContext, chatModel, consts.DefaultAgentKey, messages, false); updateErr != nil {
				logs.CtxWarn(memoryContext, "[memory] update failed: thread_id=%s turn_id=%s err=%v", threadID, turnID, updateErr)
			}
		}()
	}
	return completed
}

func buildLocalAgentConfig(ctx context.Context, cfg *config.Config) (agentConfig *deepagents.Config, err error) {
	skillBackend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: config.RootDir(), VirtualMode: true})
	workspaceBackend := agenttools.NewWorkspaceBackend(sandbox.Default())
	chatModel, err := host.BuildToolCallingChatModel(ctx, cfg.Models[cfg.DefaultModel])
	if err != nil {
		return nil, err
	}
	builtinTools := agenttools.BuildExtensionTools(cfg, sandbox.Default())
	webConfig, err := buildWebConfig(cfg.WebSearch)
	if err != nil {
		return nil, err
	}
	for _, baseTool := range builtinTools {
		if _, infoErr := baseTool.Info(ctx); infoErr != nil {
			return nil, fmt.Errorf("inspect tool: %w", infoErr)
		}
	}
	agentConfig = &deepagents.Config{
		Model:            chatModel,
		WebConfig:        webConfig,
		MaxSteps:         consts.DefaultAgentIterations,
		Tools:            builtinTools,
		SkillLoader:      skill.NewFileSystemSkillLoader([]string{"deepagent/backend/skills"}, skillBackend, false, nil),
		Backend:          workspaceBackend,
		FilesystemConfig: &deepagents.FilesystemConfig{WorkDir: config.RootDir(), DisableApplyPatch: true, DisableUploadDownload: true},
		Middlewares: []sdkmiddleware.Middleware{
			baseprompt.New(host.SystemPrompt(consts.DefaultAgentKey, true)),
			newMemoryMiddleware(),
		},
	}
	return agentConfig, nil
}
