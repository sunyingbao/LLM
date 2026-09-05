package deepagents

import (
	"context"
	"errors"
	"fmt"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/filesystem"
	"eino-cli/deepagent/core/middleware/patchtoolcalls"
	"eino-cli/deepagent/core/middleware/skill"
	"eino-cli/deepagent/core/middleware/subagent"
	"eino-cli/deepagent/core/middleware/web"
	"eino-cli/deepagent/core/tools"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"
)

// ToolNodePreHandler 在非流式 tools 节点执行前处理完整 assistant message。
// 当前仅在 EnableStreamToolCall=false 时生效。
type ToolNodePreHandler func(ctx context.Context, input *schema.Message) (*schema.Message, error)

// ToolNodePostHandler 在非流式 tools 节点执行后处理完整 tool message 数组。
// 当前仅在 EnableStreamToolCall=false 时生效。
type ToolNodePostHandler func(ctx context.Context, output []*schema.Message) ([]*schema.Message, error)

// New 根据 Option 构造并返回 DeepAgent。
func New(ctx context.Context, opts ...Option) (agent *DeepAgent, err error) {
	config := buildCreateConfig(opts...)
	err = validateCreateConfig(config)
	if err != nil {
		return nil, err
	}
	if config.MaxSteps == 0 {
		config.MaxSteps = constant.DefaultMaxSteps
	}

	backend := selectBackend(config)
	err = validateBackendConfig(config, backend)
	if err != nil {
		return nil, err
	}
	loadSubAgentsFromDirs(ctx, config, backend)
	err = validateSubAgentNames(config)
	if err != nil {
		return nil, err
	}

	middlewares := buildCreateMiddlewares(config, backend)
	middlewares = applyMaxModelCalls(config, middlewares)

	chain := middleware.NewMiddlewareChain(middlewares...)
	allTools, err := collectAllTools(ctx, chain, config)
	if err != nil {
		return nil, err
	}

	agentState, err := buildAgentState(ctx, chain, config.CustomGraphState, config.CheckpointStore)
	if err != nil {
		return nil, err
	}

	runnable, err := buildGraphWithConfig(ctx, *config, allTools, chain)
	if err != nil {
		return nil, err
	}

	agent = &DeepAgent{
		runnable:        runnable,
		middlewareChain: chain,
		backend:         backend,
		callbacks:       append([]callbacks.Handler(nil), config.Callbacks...),
		graphState:      agentState,
		depth:           config.Depth,
	}
	return agent, nil
}

func buildCreateConfig(opts ...Option) (config *Config) {
	config = &Config{
		MaxSteps: constant.DefaultMaxSteps,
	}
	for _, opt := range opts {
		opt(config)
	}
	return config
}

func validateCreateConfig(config *Config) (err error) {
	if config.Model == nil {
		return errors.New(constant.ErrMsgModelRequired)
	}
	if config.MaxModelCalls < 0 {
		return errors.New("max model calls must be >= 0")
	}
	for _, name := range config.SubAgentSharedCustomStateNames {
		if config.CustomGraphState == nil || config.CustomGraphState[name] == nil {
			return fmt.Errorf("sub-agent shared custom state %q is not configured", name)
		}
	}
	return nil
}

func validateBackendConfig(config *Config, backend backends.Backend) (err error) {
	if backend != nil {
		return nil
	}
	if config.FilesystemConfig != nil {
		return errors.New("filesystem requires backend or workdir")
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

func selectBackend(config *Config) (backend backends.Backend) {
	if config.Backend != nil {
		return config.Backend
	}
	if workDir := config.filesystemWorkDir(); workDir != "" {
		return backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
			RootDir:     workDir,
			VirtualMode: true,
		})
	}
	return nil
}

func loadSubAgentsFromDirs(ctx context.Context, config *Config, backend backends.Backend) {
	for _, dir := range config.SubAgentsDirs {
		loaded, err := subagent.LoadSubAgentsFromDir(ctx, dir, backend, nil)
		if err == nil {
			config.SubAgents = append(config.SubAgents, loaded...)
		}
	}
}

func buildCreateMiddlewares(config *Config, backend backends.Backend) (middlewares []middleware.Middleware) {
	var subAgentSkillMiddlewareFactory func() middleware.Middleware

	if config.ContextManager != nil {
		middlewares = append(middlewares, config.ContextManager)
	}

	if config.EnablePatchToolCalls {
		middlewares = append(middlewares, patchtoolcalls.New())
	}

	if config.SkillLoader != nil {
		middlewares = append(middlewares, skill.NewWithConfig(config.SkillLoader, &skill.MiddlewareConfig{
			ToolMask: config.ToolMask,
		}))
		skillLoader := config.SkillLoader
		subAgentSkillMiddlewareFactory = func() middleware.Middleware {
			return skill.New(skillLoader)
		}
	}

	if config.FilesystemConfig != nil {
		filesystemCfg := config.filesystemConfig()
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

	if config.WebConfig != nil {
		webConfig := *config.WebConfig
		webConfig.ToolMask = tools.CombineMasks(webConfig.ToolMask, config.ToolMask)
		middlewares = append(middlewares, web.New(&webConfig))
	}

	for _, mw := range config.Middlewares {
		if mw != nil {
			middlewares = append(middlewares, mw)
		}
	}

	return middlewares
}
