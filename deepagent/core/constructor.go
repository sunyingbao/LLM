package deepagents

import (
	"context"
	"errors"
	"fmt"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/filesystem"
	"eino-cli/deepagent/core/middleware/memory"
	"eino-cli/deepagent/core/middleware/patchtoolcalls"
	"eino-cli/deepagent/core/middleware/planning"
	"eino-cli/deepagent/core/middleware/skill"
	"eino-cli/deepagent/core/middleware/subagent"
	"eino-cli/deepagent/core/middleware/web"
	"eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/core/types"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// ToolNodePreHandler 在非流式 tools 节点执行前处理完整 assistant message。
// 当前仅在 EnableStreamToolCall=false 时生效。
type ToolNodePreHandler func(ctx context.Context, input *schema.Message) (*schema.Message, error)

// ToolNodePostHandler 在非流式 tools 节点执行后处理完整 tool message 数组。
// 当前仅在 EnableStreamToolCall=false 时生效。
type ToolNodePostHandler func(ctx context.Context, output []*schema.Message) ([]*schema.Message, error)

// DeepAgentSpec 是 DeepAgent 的核心构造模型。
// 它只描述一个已经装配好的 agent 应该由哪些依赖组成，
// 不承载默认 middleware/feature 的选择策略。
type DeepAgentSpec struct {
	Model model.ToolCallingChatModel

	Middlewares []middleware.Middleware
	Tools       []tool.BaseTool
	ToolMask    tools.Mask
	Backend     backends.Backend

	MaxSteps      int
	MaxModelCalls int

	CheckpointStore compose.CheckPointStore

	InterruptBeforeNodes []string
	InterruptAfterNodes  []string

	// CustomGraphState 上层业务可以通过这个来写入具体需要持久化的状态
	CustomGraphState map[string]types.RunTimeStateful

	EnableStreamToolCall bool
	Callbacks            []callbacks.Handler

	Depth int

	HITLConfig *HITLConfig

	// ToolInfoRewriter 用于重写工具的 ToolInfo（name/desc）
	// 如果设置，所有工具的 Info 将经过此函数处理
	ToolInfoRewriter tools.ToolInfoRewriter

	// ToolNodePreHandler 在非流式 tools 节点执行前处理完整 assistant message。
	// 当前仅在 EnableStreamToolCall=false 时生效。
	ToolNodePreHandler ToolNodePreHandler

	// ToolNodePostHandler 在非流式 tools 节点执行后处理完整 tool message 数组。
	// 当前仅在 EnableStreamToolCall=false 时生效。
	ToolNodePostHandler ToolNodePostHandler

	// ReactLoopBranchPolicy 控制 DeepAgent react loop 分支；为空保持默认行为。
	ReactLoopBranchPolicy ReactLoopBranchPolicy
}

// New 根据 Option 构造并返回 DeepAgent。
func New(ctx context.Context, opts ...Option) (*DeepAgent, error) {
	config := buildCreateConfig(opts...)
	spec, err := buildSpecFromConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	return NewFromSpec(ctx, spec)
}

func NewFromSpec(ctx context.Context, spec *DeepAgentSpec) (*DeepAgent, error) {
	spec, err := normalizeDeepAgentSpec(spec)
	if err != nil {
		return nil, err
	}
	applyMaxModelCallsSpec(spec)

	chain := middleware.NewMiddlewareChain(spec.Middlewares...)
	allTools, err := collectAllTools(ctx, chain, &Config{
		Tools:            spec.Tools,
		ToolMask:         spec.ToolMask,
		HITLConfig:       spec.HITLConfig,
		ToolInfoRewriter: spec.ToolInfoRewriter,
	})
	if err != nil {
		return nil, err
	}

	agentState, err := buildAgentState(ctx, chain, spec.CustomGraphState, spec.CheckpointStore)
	if err != nil {
		return nil, err
	}

	runnable, err := buildGraphWithConfig(ctx, &einoGraphConfig{
		chatModel:             spec.Model,
		tools:                 allTools,
		maxSteps:              spec.MaxSteps,
		checkpointStore:       spec.CheckpointStore,
		interruptBeforeNodes:  spec.InterruptBeforeNodes,
		interruptAfterNodes:   spec.InterruptAfterNodes,
		middlewareChain:       chain,
		enableStreamToolCall:  spec.EnableStreamToolCall,
		toolNodePreHandler:    spec.ToolNodePreHandler,
		toolNodePostHandler:   spec.ToolNodePostHandler,
		reactLoopBranchPolicy: spec.ReactLoopBranchPolicy,
	})
	if err != nil {
		return nil, err
	}

	return &DeepAgent{
		runnable:        runnable,
		middlewareChain: chain,
		backend:         spec.Backend,
		callbacks:       append([]callbacks.Handler(nil), spec.Callbacks...),
		graphState:      agentState,
		depth:           spec.Depth,
	}, nil
}

func normalizeDeepAgentSpec(spec *DeepAgentSpec) (*DeepAgentSpec, error) {
	if spec == nil {
		return nil, errors.New("deep agent spec is required")
	}
	if spec.Model == nil {
		return nil, errors.New(constant.ErrMsgModelRequired)
	}

	middlewares := make([]middleware.Middleware, 0, len(spec.Middlewares))
	for _, mw := range spec.Middlewares {
		if mw != nil {
			middlewares = append(middlewares, mw)
		}
	}

	normalized := *spec
	normalized.Middlewares = middlewares
	if normalized.MaxSteps == 0 {
		normalized.MaxSteps = constant.DefaultMaxSteps
	}
	if normalized.MaxModelCalls < 0 {
		return nil, errors.New("max model calls must be >= 0")
	}
	return &normalized, nil
}

func buildCreateConfig(opts ...Option) *Config {
	config := &Config{
		MaxSteps: constant.DefaultMaxSteps,
	}
	for _, opt := range opts {
		opt(config)
	}
	return config
}

func buildSpecFromConfig(ctx context.Context, config *Config) (*DeepAgentSpec, error) {
	if err := validateCreateConfig(config); err != nil {
		return nil, err
	}

	backend := resolveCreateBackend(config)
	if err := validateBackendConfig(config, backend); err != nil {
		return nil, err
	}
	if err := loadSubAgentsFromDirs(ctx, config, backend); err != nil {
		return nil, err
	}
	if err := validateSubAgentNames(config); err != nil {
		return nil, err
	}

	middlewares, err := buildCreateMiddlewares(config, backend)
	if err != nil {
		return nil, err
	}

	return &DeepAgentSpec{
		Model:                 config.Model,
		Middlewares:           middlewares,
		Tools:                 config.Tools,
		Backend:               backend,
		MaxSteps:              config.MaxSteps,
		MaxModelCalls:         config.MaxModelCalls,
		CheckpointStore:       config.CheckpointStore,
		InterruptBeforeNodes:  config.InterruptBeforeNodes,
		InterruptAfterNodes:   config.InterruptAfterNodes,
		CustomGraphState:      config.CustomGraphState,
		EnableStreamToolCall:  config.EnableStreamToolCall,
		Callbacks:             append([]callbacks.Handler(nil), config.Callbacks...),
		Depth:                 config.Depth,
		HITLConfig:            config.HITLConfig,
		ToolMask:              config.ToolMask,
		ToolInfoRewriter:      config.ToolInfoRewriter,
		ToolNodePreHandler:    config.ToolNodePreHandler,
		ToolNodePostHandler:   config.ToolNodePostHandler,
		ReactLoopBranchPolicy: config.ReactLoopBranchPolicy,
	}, nil
}

func validateCreateConfig(config *Config) error {
	if config.Model == nil {
		return errors.New(constant.ErrMsgModelRequired)
	}
	if config.MaxModelCalls < 0 {
		return errors.New("max model calls must be >= 0")
	}
	if config.ContextManagerCfg == nil || config.ContextManagerCfg.Manager == nil {
		return errors.New(constant.ErrMsgContextManagerRequired)
	}
	for _, name := range config.SubAgentSharedCustomStateNames {
		if config.CustomGraphState == nil || config.CustomGraphState[name] == nil {
			return fmt.Errorf("sub-agent shared custom state %q is not configured", name)
		}
	}
	return nil
}

func validateBackendConfig(config *Config, backend backends.Backend) error {
	if backend != nil {
		return nil
	}
	if config.EnableFilesystem {
		return errors.New("filesystem requires backend or workdir")
	}
	if len(config.MemoryPaths) > 0 {
		return errors.New("memory paths require backend or workdir")
	}
	if len(config.SkillsDirs) > 0 {
		return errors.New("skills dirs require backend or workdir")
	}
	if len(config.SubAgentsDirs) > 0 {
		return errors.New("subagent dirs require backend or workdir")
	}
	for _, sa := range config.SubAgents {
		if sa != nil && sa.EnableFilesystem {
			return errors.New("subagent filesystem requires backend or workdir")
		}
	}
	return nil
}

func resolveCreateBackend(config *Config) backends.Backend {
	if config.Backend != nil {
		return config.Backend
	}
	if workDir := config.effectiveFilesystemWorkDir(); workDir != "" {
		return backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
			RootDir:     workDir,
			VirtualMode: true,
		})
	}
	return nil
}

func (c *Config) effectiveFilesystemConfig() *FilesystemConfig {
	if c == nil {
		return &FilesystemConfig{}
	}

	merged := &FilesystemConfig{}
	if c.FilesystemConfig != nil {
		*merged = *c.FilesystemConfig
	}
	if c.workDirExplicit {
		merged.WorkDir = c.WorkDir
	}
	if c.disableUploadDownloadExplicit {
		merged.DisableUploadDownload = c.DisableUploadDownload
	}
	if c.disableExecuteExplicit {
		merged.DisableExecute = c.DisableExecute
	}

	return merged
}

func (c *Config) effectiveFilesystemWorkDir() string {
	return c.effectiveFilesystemConfig().WorkDir
}

func loadSubAgentsFromDirs(ctx context.Context, config *Config, backend backends.Backend) error {
	if len(config.SubAgentsDirs) == 0 {
		return nil
	}
	for _, dir := range config.SubAgentsDirs {
		loaded, err := subagent.LoadSubAgentsFromDir(ctx, dir, backend, nil)
		if err == nil {
			config.SubAgents = append(config.SubAgents, loaded...)
		}
	}
	return nil
}

func buildCreateMiddlewares(config *Config, backend backends.Backend) ([]middleware.Middleware, error) {
	var middlewares []middleware.Middleware
	var subAgentSkillMiddlewareFactory func() middleware.Middleware

	if config.ContextManagerCfg != nil && config.ContextManagerCfg.Manager != nil {
		middlewares = append(middlewares, config.ContextManagerCfg.Manager)
	}

	if config.EnablePatchToolCalls {
		middlewares = append(middlewares, patchtoolcalls.New())
	}

	if config.EnablePlanning {
		middlewares = append(middlewares, planning.NewWithConfig(&planning.PlanningConfig{
			ToolMask: config.ToolMask,
		}))
	}

	if len(config.MemoryPaths) > 0 {
		middlewares = append(middlewares, memory.New(&memory.MemoryConfig{
			Backend:     backend,
			Sources:     config.MemoryPaths,
			EnableLearn: true,
			ToolMask:    config.ToolMask,
		}))
	}

	if config.SkillLoader != nil {
		middlewares = append(middlewares, skill.NewWithConfig(config.SkillLoader, &skill.MiddlewareConfig{
			ToolMask: config.ToolMask,
		}))
		skillLoader := config.SkillLoader
		subAgentSkillMiddlewareFactory = func() middleware.Middleware {
			return skill.New(skillLoader)
		}
	} else if len(config.SkillsDirs) > 0 {
		middlewares = append(middlewares, skill.NewWithConfig(skill.NewFileSystemSkillLoader(config.SkillsDirs, backend, true, config.SkillMask), &skill.MiddlewareConfig{
			ToolMask: config.ToolMask,
		}))
		skillDirs := append([]string(nil), config.SkillsDirs...)
		subAgentSkillMiddlewareFactory = func() middleware.Middleware {
			return skill.New(skill.NewFileSystemSkillLoader(skillDirs, backend, true, config.SkillMask))
		}
	}

	if config.EnableFilesystem {
		filesystemCfg := config.effectiveFilesystemConfig()
		middlewares = append(middlewares, filesystem.New(&filesystem.FilesystemConfig{
			Backend:               backend,
			WorkDir:               filesystemCfg.WorkDir,
			ReadOnly:              filesystemCfg.ReadOnly,
			DisableUploadDownload: filesystemCfg.DisableUploadDownload,
			DisableExecute:        filesystemCfg.DisableExecute,
			DisableApplyPatch:     filesystemCfg.DisableApplyPatch,
			CommandTimeout:        filesystemCfg.CommandTimeout,
			ToolMask:              config.ToolMask,
		}))
	}

	if !config.DisableSubAgent && len(config.SubAgents) > 0 {
		middlewares = append(middlewares, subagent.New(&subagent.SubAgentConfig{
			SubAgents:                      config.SubAgents,
			DefaultModel:                   config.Model,
			DefaultTools:                   config.Tools,
			Factory:                        createSubAgentFactory(config),
			SubAgentSkillMiddlewareFactory: subAgentSkillMiddlewareFactory,
			ContextInjector:                config.SubAgentContextInjector,
			ToolMask:                       config.ToolMask,
			EnableTaskStreaming:            config.EnableSubAgentTaskStreaming,
		}))
	}

	if config.EnableWeb {
		webConfig := web.DefaultConfig()
		if config.WebConfig != nil {
			*webConfig = *config.WebConfig
		}
		webConfig.ToolMask = tools.CombineMasks(webConfig.ToolMask, config.ToolMask)
		middlewares = append(middlewares, web.New(webConfig))
	}

	for _, mw := range config.Middlewares {
		if mw != nil {
			middlewares = append(middlewares, mw)
		}
	}

	return middlewares, nil
}
