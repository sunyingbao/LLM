package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	sdkmiddleware "eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/skill"
	"eino-cli/deepagent/definition"
	definitionresolver "eino-cli/deepagent/definition/resolver"
	sdkruntime "eino-cli/deepagent/runtime"
	localclient "eino-cli/deepagent/runtime/local"
	"eino-cli/deepagent/worker/inprocess"
	inprocessstore "eino-cli/deepagent/worker/inprocess/store"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"eino-cli/backend/config"
	"eino-cli/backend/consts"
	memorystore "eino-cli/backend/memory/store"
	"eino-cli/backend/sandbox"
	host "eino-cli/deepagent/host"
	"eino-cli/deepagent/host/checkpoint"
	agentmemory "eino-cli/deepagent/memory/sgadk"
	agenttools "eino-cli/deepagent/tools/sgadk"
)

const localDefinitionVersion = "v1"

type LocalRuntime struct {
	cfg         *config.Config
	router      *sdkruntime.Router
	sessionID   string
	definition  agentdefinition.Definition
	modelName   string
	runtimeKind sdkruntime.RuntimeKind
	mu          sync.Mutex
	threadRef   sdkruntime.GlobalThreadRef
	planMode    bool
}

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

	definition, resolver, err := buildLocalDefinition(ctx, cfg)
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
		Definition: definition,
		Resolver:   resolver,
		HistoryStore: func(ctx context.Context, threadID string) (store agentthread.HistoryRolloutStore, err error) {
			return historyStore, nil
		},
		CheckpointStore: func(ctx context.Context, threadID string) (store compose.CheckPointStore, err error) {
			store = checkpoint.NewStore(filepath.Join(runtimeDir, "checkpoints", threadID))
			return store, nil
		},
		TurnCompleted: sgadkMemoryTurnCompleted(),
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
	index, err := OpenPersistentThreadIndex(filepath.Join(runtimeDir, "thread-index.json"))
	if err != nil {
		return nil, err
	}
	runtime = &LocalRuntime{
		cfg:    cfg,
		router: &sdkruntime.Router{Local: client, Index: index}, sessionID: sessionID,
		definition: definition, modelName: cfg.DefaultModel, runtimeKind: sdkruntime.RuntimeLocal,
	}
	return runtime, nil
}

func sgadkMemoryTurnCompleted() (completed func(ctx context.Context, threadID, turnID string, chatModel model.ToolCallingChatModel, history []*schema.Message)) {
	updater := agentmemory.NewMemoryUpdater(memorystore.NewStore())
	completed = func(ctx context.Context, threadID, turnID string, chatModel model.ToolCallingChatModel, history []*schema.Message) {
		memoryContext := context.WithoutCancel(ctx)
		messages := append([]*schema.Message(nil), history...)
		go func() {
			if updateErr := updater.Run(memoryContext, chatModel, consts.DefaultAgentKey, messages, false); updateErr != nil {
				logs.CtxWarn(memoryContext, "[sgadk_memory] update failed: thread_id=%s turn_id=%s err=%v", threadID, turnID, updateErr)
			}
		}()
	}
	return completed
}

func NewRemoteRuntime(ctx context.Context, cfg *config.Config, sessionID string, client sdkruntime.Client) (runtime *LocalRuntime, err error) {
	if cfg == nil || client == nil {
		return nil, fmt.Errorf("remote runtime configuration and client are required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session id is required")
	}
	definition, _, err := buildLocalDefinition(ctx, cfg)
	if err != nil {
		return nil, err
	}
	runtimeDir := filepath.Join(config.BaseDir(), "runtime")
	if err = os.MkdirAll(runtimeDir, 0o700); err != nil {
		return nil, err
	}
	index, err := OpenPersistentThreadIndex(filepath.Join(runtimeDir, "thread-index.json"))
	if err != nil {
		return nil, err
	}
	runtime = &LocalRuntime{
		cfg: cfg, router: &sdkruntime.Router{Remote: client, Index: index}, sessionID: sessionID,
		definition: definition, modelName: cfg.DefaultModel, runtimeKind: sdkruntime.RuntimeRemote,
	}
	return runtime, nil
}

func buildLocalDefinition(ctx context.Context, cfg *config.Config) (definition agentdefinition.Definition, resolver *definitionresolver.Resolver, err error) {
	registry := definitionresolver.NewRegistry()
	workspaceBackend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: config.RootDir(), VirtualMode: true})
	registry.RegisterModel("sgadk-default", func(ctx context.Context, policy agentdefinition.ModelPolicy) (chatModel model.ToolCallingChatModel, err error) {
		chatModel, err = host.BuildToolCallingChatModel(ctx, cfg.Models[cfg.DefaultModel])
		return chatModel, err
	})
	registry.RegisterSkillLoader("sgadk-filesystem", func(ctx context.Context, policy agentdefinition.SkillPolicy) (loader skill.Loader, err error) {
		var mask skill.Mask
		if len(policy.Names) > 0 {
			allowed := make(map[string]struct{}, len(policy.Names))
			for _, name := range policy.Names {
				allowed[name] = struct{}{}
			}
			mask = func(ctx context.Context, metadata *skill.SkillMetadata) (included bool) {
				if metadata != nil {
					_, included = allowed[metadata.Name]
				}
				return included
			}
		}
		loader = skill.NewFileSystemSkillLoader([]string{"backend/skills"}, workspaceBackend, false, mask)
		return loader, nil
	})
	registry.RegisterMemory("sgadk-memory", func(ctx context.Context, policy agentdefinition.MemoryPolicy) (memory sdkmiddleware.Middleware, err error) {
		memory = newSGADKMemoryMiddleware()
		return memory, nil
	})
	registry.RegisterSandbox("sgadk-workspace", func(ctx context.Context, policy agentdefinition.SandboxPolicy) (backend backends.Backend, err error) {
		return workspaceBackend, nil
	})
	definition = agentdefinition.Definition{
		Name: consts.DefaultAgentKey, Version: localDefinitionVersion,
		Instructions: host.GetUnifiedSystemPrompt(consts.DefaultAgentKey, true, cfg),
		Model:        agentdefinition.ModelPolicy{Provider: "sgadk-default", Model: cfg.DefaultModel},
		Skills:       agentdefinition.SkillPolicy{Loader: "sgadk-filesystem"},
		Memory:       agentdefinition.MemoryPolicy{Provider: "sgadk-memory"},
		// SGADK keeps its established sandbox-aware tool names. The resolved
		// backend remains available to skills and runtime capabilities without
		// installing the core filesystem tool middleware a second time.
		Sandbox: agentdefinition.SandboxPolicy{Backend: "sgadk-workspace", Config: agentdefinition.Config{
			"enable_filesystem_tools": false,
		}},
		Limits: agentdefinition.RuntimeLimits{MaxSteps: consts.DefaultAgentIterations},
	}
	for _, baseTool := range agenttools.BuildBuiltinTools(cfg, sandbox.Default()) {
		info, infoErr := baseTool.Info(ctx)
		if infoErr != nil {
			return definition, nil, fmt.Errorf("inspect tool: %w", infoErr)
		}
		name := info.Name
		registeredTool := baseTool
		registry.RegisterTool(name, func(ctx context.Context, binding agentdefinition.ToolBinding) (baseTool tool.BaseTool, err error) {
			return registeredTool, nil
		})
		definition.Tools = append(definition.Tools, agentdefinition.ToolBinding{Name: name})
	}
	resolver = definitionresolver.NewResolver(registry)
	return definition, resolver, nil
}
