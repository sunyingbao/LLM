package main

import (
	"context"
	"os"
	"strings"

	"eino-cli/deepagent/core"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/compact"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	execmw "eino-cli/deepagent/core/middleware/execute"
	"eino-cli/deepagent/core/middleware/planmode"
	"eino-cli/deepagent/core/middleware/skill"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type DeepAgentRuntimeFactory struct {
	cfg            AppConfig
	store          *LocalStore
	chatModel      model.ToolCallingChatModel
	skillLoader    skill.Loader
	toolExecPolicy *ToolExecPolicy
	fsBackend      backends.SandboxBackend
	collabBuilder  *CollabBuilder
}

const cmdDeepAgentTaskRolesDescription = `
- default: use for a general child task when no specialized role fits.
- explorer: use for read-only investigation, codebase questions, and concise findings. It has read-only file tools and read-only shell policy; do not assign implementation work to it.
- worker: use for well-scoped implementation or execution tasks with clear ownership. Assign the files or modules it owns, and assume other workers may be changing unrelated parts of the codebase.
`

func (f *DeepAgentRuntimeFactory) NewThreadRuntime(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (agentworker.AgentThread, error) {
	checkpoint, err := f.store.CheckpointStore(state.ID)
	if err != nil {
		return nil, err
	}
	eventBus := make(chan agentthread.Event, 256)
	workDir := f.threadWorkDir(state)
	roleCfg := f.threadRoleRuntimeConfig(state)
	middlewares := append([]middleware.Middleware{}, roleCfg.Middlewares...)
	if f.collabBuilder != nil {
		middlewares = append(middlewares, f.collabBuilder.Middlewares(state, worker)...)
	}
	runnerCfg := &agentthread.TurnRunnerConfig{
		ChatModel:            f.chatModel,
		Middlewares:          middlewares,
		EnableStreamToolCall: true,
		EnablePlan:           true,
		EnableFilesystem:     true,
		FilesystemConfig:     roleCfg.Filesystem,
		WorkDir:              workDir,
		SandboxBackend:       f.fsBackend,
		CheckpointStore:      checkpoint,
		EnableWeb:            f.cfg.EnableWeb,
		SkillLoader:          f.skillLoader,
		EventIDProvider: func(ctx context.Context, threadID, turnID string) string {
			return newEventID()
		},
		HITLConfig: &deepagents.HITLConfig{
			NeedApproveTools: f.toolExecPolicy.BuildNeedApprovalMap(state.SessionID),
			ToolPolicyGates:  roleCfg.ToolPolicyGates,
		},
	}
	return &localThreadRuntime{
		state:            state,
		thread:           agentthread.NewDefault(state.ID, nil, eventBus, f.defaultThreadOptions(), agentthread.WithBaseTurnRunnerConfig(runnerCfg)),
		baseRunnerConfig: runnerCfg,
		planRunnerConfig: func() *agentthread.TurnRunnerConfig {
			return f.planTurnConfig(runnerCfg, state)
		},
		agentEvCh: eventBus,
		items:     make(chan agentworker.ThreadOutputItem, 256),
	}, nil
}

type threadRoleRuntimeConfig struct {
	Filesystem      *deepagents.FilesystemConfig
	Middlewares     []middleware.Middleware
	ToolPolicyGates map[string]deeptools.ToolPolicyGate
}

func (f *DeepAgentRuntimeFactory) threadRoleRuntimeConfig(state *inprocess.ThreadState) threadRoleRuntimeConfig {
	role := strings.TrimSpace(state.Profile.Role)
	workDir := f.threadWorkDir(state)
	fsCfg := &deepagents.FilesystemConfig{
		WorkDir:        workDir,
		DisableExecute: true,
	}
	if role == "explorer" {
		fsCfg.ReadOnly = true
	}

	var middlewares []middleware.Middleware
	policyGates := map[string]deeptools.ToolPolicyGate{}
	if execMiddleware := f.newExecuteMiddleware(role, workDir); execMiddleware != nil {
		middlewares = append(middlewares, execMiddleware)
		gate := execMiddleware.PolicyGate()
		if f.toolExecPolicy != nil {
			gate = f.toolExecPolicy.WrapPolicyGate(state.SessionID, gate)
		}
		policyGates[execmw.DefaultToolName] = gate
	}
	return threadRoleRuntimeConfig{
		Filesystem:      fsCfg,
		Middlewares:     middlewares,
		ToolPolicyGates: policyGates,
	}
}

func (f *DeepAgentRuntimeFactory) threadWorkDir(state *inprocess.ThreadState) string {
	if state != nil && strings.TrimSpace(state.Profile.Cwd) != "" {
		return strings.TrimSpace(state.Profile.Cwd)
	}
	if f != nil {
		return strings.TrimSpace(f.cfg.WorkDir)
	}
	return ""
}

func (f *DeepAgentRuntimeFactory) newExecuteMiddleware(role, cwd string) *execmw.ExecuteMiddleware {
	if f == nil || f.fsBackend == nil {
		return nil
	}
	return execmw.New(execmw.Config{
		Executor:      f.fsBackend,
		PolicyProfile: cmdDeepAgentExecutePolicyProfile(role, f.cfg.EnableRG),
		WorkDir:       cwd,
	})
}

func cmdDeepAgentExecutePolicyProfile(role string, enableRG bool) execmw.PolicyProfile {
	var profile execmw.PolicyProfile
	switch strings.TrimSpace(role) {
	case "explorer", "plan":
		profile = execmw.NewReadOnlyPolicyProfile(nil)
	default:
		profile = execmw.NewDefaultPolicyProfile(nil)
	}
	if enableRG {
		profile.Instructions = strings.TrimSpace(profile.Instructions + "\n\n" + cmdDeepAgentRGInstructions)
	}
	return profile
}

const cmdDeepAgentRGInstructions = `
Repository search:
- rg is available in this CLI environment.
- Prefer rg for searching repository text and rg --files for finding files.
- Use filesystem tools such as grep and glob when you need structured tool output or shell execution is not appropriate.
- Do not duplicate the same search through both rg and filesystem grep/glob unless the first result is insufficient.
`

func (f *DeepAgentRuntimeFactory) planTurnConfig(base *agentthread.TurnRunnerConfig, state *inprocess.ThreadState) *agentthread.TurnRunnerConfig {
	cfg := base.Clone()
	workDir := f.threadWorkDir(state)
	cfg.EnablePlan = false
	cfg.Middlewares = []middleware.Middleware{}
	if execMiddleware := f.newExecuteMiddleware("plan", workDir); execMiddleware != nil {
		cfg.Middlewares = append(cfg.Middlewares, execMiddleware)
	}
	if f.collabBuilder != nil {
		cfg.Middlewares = append(cfg.Middlewares, f.collabBuilder.Middlewares(state, nil)...)
	}
	cfg.Middlewares = append(cfg.Middlewares, planmode.New(nil))
	if cfg.FilesystemConfig != nil {
		if cfg.FilesystemConfig.WorkDir == "" {
			cfg.FilesystemConfig.WorkDir = workDir
		}
		cfg.FilesystemConfig.ReadOnly = true
		cfg.FilesystemConfig.DisableExecute = true
	}
	cfg.ToolMask = deeptools.CombineMasks(cfg.ToolMask, cmdDeepAgentPlanToolMask)
	return cfg
}

func cmdDeepAgentPlanToolMask(_ context.Context, info *schema.ToolInfo) bool {
	if info == nil {
		return true
	}
	switch info.Name {
	case constant.ToolWriteFile,
		constant.ToolEditFile,
		constant.ToolExecute,
		constant.ToolUploadFiles,
		constant.ToolApplyPatch,
		constant.ToolUpdatePlan,
		constant.ToolWriteTodos,
		constant.ToolUpdateTodo,
		constant.ToolDispatchTasks,
		constant.ToolTask:
		return false
	default:
		return true
	}
}

func (f *DeepAgentRuntimeFactory) defaultThreadOptions() agentthread.DefaultThreadOptions {
	autoLimit := 16000
	if contextWindow := constant.LookupModelContextWindow(context.Background(), strings.TrimSpace(os.Getenv("MODEL_NAME"))); contextWindow > 0 {
		autoLimit = int(float64(contextWindow) * 0.7)
	}
	return agentthread.DefaultThreadOptions{
		HistoryStore: f.store.history,
		CompactionStrategy: compact.NewCodexStrategy(
			f.chatModel,
			autoLimit,
			4000,
			nil,
		),
	}
}
