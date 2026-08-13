package bootstrap

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"code.byted.org/gdp/env"
	"code.byted.org/gopkg/logs/v2"
	cloudworker "eino-cli/deepagent/cloud/worker"
	"eino-cli/deepagent/cloud/worker/bootstrap/config"
	"eino-cli/deepagent/cloud/worker/bootstrap/internal/chatmodel"
	checkpointstore "eino-cli/deepagent/cloud/worker/bootstrap/internal/checkpoint"
	fornaxinfra "eino-cli/deepagent/cloud/worker/bootstrap/internal/fornax"
	"eino-cli/deepagent/cloud/worker/bootstrap/internal/idgen"
	mcpinfra "eino-cli/deepagent/cloud/worker/bootstrap/internal/mcp"
	mysqlstore "eino-cli/deepagent/cloud/worker/bootstrap/internal/mysql"
	redisstore "eino-cli/deepagent/cloud/worker/bootstrap/internal/redis"
	"eino-cli/deepagent/core/agentthread"
	gormstore "eino-cli/deepagent/core/memory/gorm_store"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
)

// Profile identifies one of the two supported deployment shapes. Profiles
// select configuration files; they do not introduce separate runtime paths.
type Profile string

const (
	ProfileLocal  Profile = "local"
	ProfileRemote Profile = "remote"
)

// ProfileConfigFilename returns the conventional filename for a profile.
func ProfileConfigFilename(profile Profile) string {
	return "worker." + string(profile) + ".yml"
}

// Config is the complete bootstrap configuration.
type Config = config.Config

// Options contains the business-owned extension points. Standard
// infrastructure is assembled by the SDK.
type Options struct {
	Args        []string
	Tools       []tool.BaseTool
	Callbacks   []callbacks.Handler
	SkipLogging bool
}

// LoadConfig loads and validates one YAML file. Environment variables are only
// expanded when the YAML explicitly references them with ${NAME}.
func LoadConfig(args []string) (Config, error) {
	return config.Load(args)
}

// Run loads configuration, assembles the standard Worker dependencies and
// serves until ctx is cancelled.
func Run(ctx context.Context, opts Options) error {
	cfg, err := config.Load(opts.Args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("load config: %w", err)
	}
	if !opts.SkipLogging {
		if err := initLogging(cfg); err != nil {
			return fmt.Errorf("init logging: %w", err)
		}
	}
	return runConfigured(ctx, cfg, opts)
}

func runConfigured(ctx context.Context, cfg Config, opts Options) error {
	logs.CtxInfo(ctx, "[cloud_agent worker] config: namespace=%s env=%s coordinator_psm=%s coordinator_cluster=%s hostports=%t backend=%s workdir=%s checkpoint_store=%s history_table=%s concurrency=%d log_dir=%s log_retention_days=%d log_enable_agent=%t log_agent_active=%t log_enable_console=%t log_writers=%s",
		cfg.Worker.Namespace, env.Env(), cfg.Coordinator.PSM, cfg.Coordinator.Cluster, cfg.Coordinator.DirectHostPorts != "", cfg.Backend.Type, cfg.Runtime.WorkDir, cfg.Checkpoint.Store, cfg.Tables.History, cfg.Worker.Concurrency, cfg.Log.Dir, cfg.Log.RetentionDays, cfg.Log.EnableAgent, logAgentEnabled(cfg), cfg.Log.EnableConsole, logWriterSummary(cfg))
	logs.CtxInfo(ctx, "[cloud_agent worker] runtime config summary: %s", runtimeConfigSummary(cfg))

	fornaxRuntime, err := fornaxinfra.Build(ctx, cfg.Fornax)
	if err != nil {
		return fmt.Errorf("init fornax trace: %w", err)
	}
	defer fornaxRuntime.Close()

	mysqlClient := mysqlstore.New(cfg.MySQL.StoreConfig())
	db, err := mysqlClient.Open(ctx)
	if err != nil {
		logs.CtxError(ctx, "[cloud_agent worker] mysql init failed: %v", err)
		return err
	}
	defer func() {
		if err := mysqlClient.Close(); err != nil {
			logs.Error("[cloud_agent worker] close mysql failed: %v", err)
		}
	}()
	logs.CtxInfo(ctx, "[cloud_agent worker] mysql ready: history_table=%s", cfg.Tables.History)

	redisClient, err := redisstore.New(cfg.Abase.StoreConfig())
	if err != nil {
		return fmt.Errorf("create redis client for history seq: %w", err)
	}
	logs.CtxInfo(ctx, "[cloud_agent worker] redis client ready: mode=%s history_seq=true checkpoint=%t", cfg.Abase.RedisMode(), strings.HasPrefix(strings.TrimSpace(cfg.Checkpoint.Store), "redis://"))
	checkpointFactory := checkpointstore.NewFactory(cfg.Checkpoint.Store, redisClient)

	logs.CtxInfo(ctx, "[cloud_agent worker] building chat models: models=[%s]", modelConfigSummary(cfg.Models))
	chatModels, err := chatmodel.BuildAll(ctx, cfg.Models)
	if err != nil {
		return fmt.Errorf("build chat models: %w", err)
	}
	logs.CtxInfo(ctx, "[cloud_agent worker] chat models ready: count=%d", len(chatModels))

	mcpRuntime, err := mcpinfra.Build(ctx, cfg.MCP)
	if err != nil {
		return fmt.Errorf("init mcp tools: %w", err)
	}
	defer func() {
		if err := mcpRuntime.Close(); err != nil {
			logs.Error("[cloud_agent worker] close mcp tools failed: %v", err)
		}
	}()
	mcpTools := append(mcpRuntime.Tools(), opts.Tools...)
	if cfg.MCP.Enabled {
		logs.CtxInfo(ctx, "[cloud_agent worker] mcp tools ready: count=%d", len(mcpTools))
	}

	staticCallbacks := append([]callbacks.Handler(nil), opts.Callbacks...)
	startupCallbacks := append(fornaxRuntime.Handlers(), staticCallbacks...)
	agentCfg := newCloudAgentConfig(cfg, chatModels, mcpTools, startupCallbacks)
	if cluster := strings.TrimSpace(agentCfg.Host.Coordinator.Cluster); cluster != "" {
		logs.CtxInfo(ctx, "[cloud_agent worker] coordinator cluster enabled: cluster=%s", cluster)
	}
	if hostPorts := agentCfg.Host.Coordinator.DirectHostPorts; len(hostPorts) > 0 {
		logs.CtxInfo(ctx, "[cloud_agent worker] coordinator direct hostports enabled: count=%d", len(hostPorts))
	}
	coordinatorClient, err := cloudworker.NewCoordinatorClient(agentCfg.Host.Coordinator)
	if err != nil {
		return fmt.Errorf("create coordinator client: %w", err)
	}
	logs.CtxInfo(ctx, "[cloud_agent worker] coordinator client ready: psm=%s", cfg.Coordinator.PSM)

	idGenerator, err := idgen.NewGenerator(cfg.IDGen.Namespace)
	if err != nil {
		return fmt.Errorf("create id generator: %w", err)
	}
	historySeqGenerator := agentthread.NewRedisSeqGenerator(redisClient, "aic_agent_sdk_worker:history_seq")
	deps := cloudworker.Deps{
		CoordinatorClient: coordinatorClient,
		HistoryStore: func(_ context.Context, _ string) (agentthread.HistoryRolloutStore, error) {
			return agentthread.NewGormHistoryRolloutStore(db, cfg.Tables.History, idGenerator.MessageID, historySeqGenerator), nil
		},
		CheckpointStore:     checkpointFactory.NewStore,
		ThreadRefs:          newThreadRefStore(db, cfg),
		Approvals:           cloudworker.NewApprovalStore(),
		MessageWaitObserver: cloudworker.TaskMessageWaitObserver,
		EventIDProvider:     idGenerator.EventID,
	}
	if cfg.Memory.Enabled {
		deps.MemoryStore = gormstore.NewGormStore(db, gormstore.GormStoreConfig{
			Tables: gormstore.GormStoreTables{
				Source:       cfg.Tables.MemorySource,
				Stage1Output: cfg.Tables.MemoryStage1Output,
				Stage2Job:    cfg.Tables.MemoryStage2Job,
				Baseline:     cfg.Tables.MemoryBaseline,
			},
			Generator: idGenerator,
		})
		logs.CtxInfo(ctx, "[cloud_agent worker] memory enabled: workspace_root=%s source_table=%s stage1_table=%s stage2_table=%s baseline_table=%s",
			cfg.Memory.WorkspaceRoot, cfg.Tables.MemorySource, cfg.Tables.MemoryStage1Output, cfg.Tables.MemoryStage2Job, cfg.Tables.MemoryBaseline)
	}
	logs.CtxInfo(ctx, "[cloud_agent worker] starting: namespace=%s env=%s workdir=%s", cfg.Worker.Namespace, env.Env(), cfg.Runtime.WorkDir)
	if err := cloudworker.Run(ctx, agentCfg, deps); err != nil && ctx.Err() == nil {
		logs.CtxError(ctx, "[cloud_agent worker] stopped: %v", err)
		return err
	}
	return nil
}
