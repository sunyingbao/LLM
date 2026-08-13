//go:build !windows

package worker

import (
	"context"
	"strings"

	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	"eino-cli/deepagent/cloud/worker/policy"
	"eino-cli/deepagent/core"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	execmw "eino-cli/deepagent/core/middleware/execute"
	"eino-cli/deepagent/core/middleware/planmode"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/worker/tasktool"
	"github.com/cloudwego/eino/schema"
)

type roleTurnLevelConfig struct {
	Filesystem      *deepagents.FilesystemConfig
	Middlewares     []middleware.Middleware
	ToolMask        deeptools.Mask
	ToolPolicyGates map[string]deeptools.ToolPolicyGate
}

// buildRoleTurnLevelConfig configures tools and HITL policy for one model turn
// under a specific role. Prompt, history, and checkpoint setup live elsewhere.
func (b *threadBuilder) buildRoleTurnLevelConfig(threadInfo *ac.Thread, roleID string, turnPolicy TurnPolicy, backend backends.SandboxBackend, workDir string) roleTurnLevelConfig {
	fsCfg := &deepagents.FilesystemConfig{
		WorkDir:           workDir,
		DisableExecute:    true,
		DisableApplyPatch: turnPolicy.DisableApplyPatch,
	}
	if turnPolicy.ApprovalPolicy == ApprovalPolicyReadOnly {
		fsCfg.ReadOnly = true
	}

	var middlewares []middleware.Middleware
	policyGates := map[string]deeptools.ToolPolicyGate{}
	if execMiddleware := b.newExecuteMiddleware(turnPolicy.ApprovalPolicy, workDir, backend); execMiddleware != nil {
		middlewares = append(middlewares, execMiddleware)
		policyGates[execmw.DefaultToolName] = b.wrapExecutePolicyGate(threadInfo, execMiddleware.PolicyGate())
	}
	return roleTurnLevelConfig{
		Filesystem:      fsCfg,
		Middlewares:     middlewares,
		ToolMask:        roleToolMask(roleID),
		ToolPolicyGates: policyGates,
	}
}

func (b *threadBuilder) newExecuteMiddleware(approvalPolicy ApprovalPolicy, cwd string, backend backends.SandboxBackend) *execmw.ExecuteMiddleware {
	if backend == nil {
		return nil
	}
	return execmw.New(execmw.Config{
		Executor:      backend,
		PolicyProfile: policy.ExecutePolicyProfile(policy.ApprovalPolicy(approvalPolicy)),
		WorkDir:       cwd,
	})
}

func (b *threadBuilder) wrapExecutePolicyGate(threadInfo *ac.Thread, gate deeptools.ToolPolicyGate) deeptools.ToolPolicyGate {
	basePolicy := gate.Policy
	gate.Policy = func(ctx context.Context, info *deeptools.ApprovalInfo) (deeptools.ToolCallDecision, error) {
		if basePolicy == nil {
			return deeptools.ToolCallDecision{Action: deeptools.ToolCallAllow}, nil
		}
		decision, err := basePolicy(ctx, info)
		if err != nil {
			return decision, err
		}
		if decision.Action == deeptools.ToolCallRequireApproval && b.deps.Approvals != nil && b.deps.Approvals.IsAllowed(ctx, threadInfoFromCoordinator(threadInfo), info.ToolName, info.ArgumentsInJSON) {
			return deeptools.ToolCallDecision{Action: deeptools.ToolCallAllow, Reason: decision.Reason}, nil
		}
		return decision, nil
	}
	return gate
}

// buildPlanModeTurnLevelConfig creates the temporary read-only turn config used
// when the user asks the agent to implement an accepted plan.
func (b *threadBuilder) applyPlanModeTurnConfig(ctx context.Context, base *agentthread.TurnRunnerConfig, spec threadSpec, backend backends.SandboxBackend, turnProfile ResolvedTurnProfile) *agentthread.TurnRunnerConfig {
	cfg := base.Clone()
	cfg.EnablePlan = false
	cfg.SkillLoader = nil
	cfg.Middlewares = b.buildPromptMiddlewares(ctx, turnProfile)
	if memoryRead := b.memoryReadMiddleware(spec.Info.GetUserId()); memoryRead != nil {
		cfg.Middlewares = append(cfg.Middlewares, memoryRead)
	}
	cfg.Middlewares = append(cfg.Middlewares, turnProfile.Capabilities.Middlewares...)
	policyGates := map[string]deeptools.ToolPolicyGate{}
	if execMiddleware := b.newExecuteMiddleware(ApprovalPolicyReadOnly, spec.WorkDir, backend); execMiddleware != nil {
		cfg.Middlewares = append(cfg.Middlewares, execMiddleware)
		policyGates[execmw.DefaultToolName] = b.wrapExecutePolicyGate(spec.Info, execMiddleware.PolicyGate())
	}
	cfg.Middlewares = append(cfg.Middlewares, b.collabMiddlewares(ctx, spec.Info, spec.Profile)...)
	cfg.Middlewares = append(cfg.Middlewares, planmode.New(nil))
	if cfg.FilesystemConfig != nil {
		if cfg.FilesystemConfig.WorkDir == "" {
			cfg.FilesystemConfig.WorkDir = spec.WorkDir
		}
		cfg.FilesystemConfig.ReadOnly = true
		cfg.FilesystemConfig.DisableExecute = true
	}
	cfg.ToolMask = deeptools.CombineMasks(cfg.ToolMask, planToolMask)
	cfg.HITLConfig = b.hitlConfig(policyGates, turnProfile.Policy.EnableFollowUpTool)
	return cfg
}

func roleToolMask(role string) deeptools.Mask {
	switch strings.TrimSpace(role) {
	case "explorer", "worker":
		return hideCollabTools
	default:
		return nil
	}
}

func hideCollabTools(_ context.Context, info *schema.ToolInfo) bool {
	if info == nil {
		return true
	}
	switch info.Name {
	case tasktool.ToolSpawnTask, tasktool.ToolSendMessage, tasktool.ToolWaitMessage, tasktool.ToolCloseTask:
		return false
	default:
		return true
	}
}

func planToolMask(_ context.Context, info *schema.ToolInfo) bool {
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
		constant.ToolTask,
		tasktool.ToolSpawnTask,
		tasktool.ToolSendMessage,
		tasktool.ToolWaitMessage,
		tasktool.ToolCloseTask:
		return false
	default:
		return true
	}
}

func (b *threadBuilder) hitlConfig(policyGates map[string]deeptools.ToolPolicyGate, enableFollowUpTool bool) *deepagents.HITLConfig {
	return &deepagents.HITLConfig{ToolPolicyGates: policyGates, NeedFollowUpTool: enableFollowUpTool}
}
