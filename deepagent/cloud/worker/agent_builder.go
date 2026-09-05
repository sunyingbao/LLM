//go:build !windows

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	cloudbackend "eino-cli/deepagent/cloud/backend"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/worker/policy"
	"eino-cli/deepagent/coordinator"
	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/compact"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/memory"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/baseprompt"
	execmw "eino-cli/deepagent/core/middleware/execute"
	"eino-cli/deepagent/core/middleware/planmode"
	skillmw "eino-cli/deepagent/core/middleware/skill"
	deeptools "eino-cli/deepagent/core/tools"
	agentworker "eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/cloud"
	"eino-cli/deepagent/worker/tasktool"
	runtimethread "eino-cli/deepagent/worker/thread"
	"eino-cli/deepagent/worker/thread/runtimectx"

	"code.byted.org/gopkg/logs/v2"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/google/uuid"
)

const defaultCloudAgentEventBusSize = 4096

// threadSpec is the normalized thread identity used while building one claimed
// Coordinator thread.
type threadSpec struct {
	Info     *coordinator.Thread
	ThreadID string
	Profile  ResolvedThreadProfile
}

type threadResources struct {
	EventBus   chan agentthread.Event
	Backend    backends.SandboxBackend
	WorkDir    string
	History    agentthread.HistoryRolloutStore
	Checkpoint compose.CheckPointStore
}

// threadInfoFromSpec maps the CloudAgent thread spec onto the runtime-readable
// thread identity. It only reads stable values from the AC thread spec; it never
// pulls thread/turn identity from message metadata. Namespace and Env are passed
// through as-is, including empty values.
func threadInfoFromSpec(spec threadSpec) runtimectx.ThreadIdentity {
	return runtimectx.ThreadIdentity{
		ThreadID:  spec.ThreadID,
		SessionID: spec.Info.SessionID,
		UserID:    spec.Info.UserID,
		Namespace: spec.Info.Namespace,
		Env:       spec.Info.Env,
	}
}

func threadProjectName(threadInfo *coordinator.Thread, profile tasktool.ThreadProfile) (projectName string) {
	if threadInfo == nil {
		return "sessionless"
	}
	if name, err := cloudbackend.CleanProjectName(threadInfo.Metadata[MetadataProjectName]); err == nil {
		return name
	}
	if cwd := strings.TrimSpace(profile.Cwd); cwd != "" {
		base := filepath.Base(filepath.Clean(cwd))
		if base != "." && base != string(filepath.Separator) {
			if name := safePathSegment(base, 96); name != "" {
				return name
			}
		}
	}
	if name := safePathSegment(threadInfo.SessionID, 96); name != "" {
		return name
	}
	return safePathSegment(strconv.FormatInt(threadInfo.ThreadID, 10), 96)
}

type threadBuilder struct {
	cfg  Config
	deps Deps
}

func newThreadBuilder(cfg Config, deps Deps) (*threadBuilder, error) {
	b := &threadBuilder{cfg: cfg, deps: deps}
	if err := b.validateDeps(); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *threadBuilder) validateDeps() error {
	if b.deps.Coordinator == nil {
		return fmt.Errorf("cloudagent: coordinator client is required")
	}
	if b.deps.HistoryStore == nil {
		return fmt.Errorf("cloudagent: history store provider is required")
	}
	if b.deps.CheckpointStore == nil {
		return fmt.Errorf("cloudagent: checkpoint store provider is required")
	}
	return nil
}

func (b *threadBuilder) baseThreadProfile(threadInfo *coordinator.Thread) ResolvedThreadProfile {
	profile := cloud.ThreadProfileFromCoordinator(threadInfo)
	roleID := strings.TrimSpace(profile.Role)
	if roleID == "" {
		roleID = DefaultRoleID
	}
	return ResolvedThreadProfile{
		RoleID:        roleID,
		WorkDir:       b.threadWorkDir(threadInfo, profile),
		Project:       threadProjectName(threadInfo, profile),
		Backend:       b.cfg.Thread.Backend,
		Compaction:    b.cfg.Thread.Compaction,
		Collaboration: b.cfg.Thread.Collaboration,
	}
}

func (b *threadBuilder) resolveThreadProfile(ctx context.Context, threadInfo *coordinator.Thread) (ResolvedThreadProfile, error) {
	base := b.baseThreadProfile(threadInfo)
	if b.cfg.Thread.ResolveProfile == nil {
		return b.validateThreadProfile(base)
	}
	profile, err := b.cfg.Thread.ResolveProfile(ctx, ThreadProfileRequest{
		ThreadInfo: threadInfoFromCoordinator(threadInfo),
		Base:       base,
	})
	if err != nil {
		return ResolvedThreadProfile{}, err
	}
	return b.validateThreadProfile(profile)
}

func (b *threadBuilder) validateThreadProfile(profile ResolvedThreadProfile) (ResolvedThreadProfile, error) {
	if strings.TrimSpace(profile.RoleID) == "" {
		return ResolvedThreadProfile{}, fmt.Errorf("cloudagent: thread profile role id is required")
	}
	if strings.TrimSpace(string(profile.Backend.Type)) == "" {
		profile.Backend.Type = cloudbackend.TypeLocal
	}
	profile.Backend = cloudbackend.Normalize(profile.Backend)
	if profile.Compaction.CompactKeptUserTokens <= 0 {
		profile.Compaction.CompactKeptUserTokens = 4000
	}
	if profile.Collaboration.TaskRolesDescription == "" {
		profile.Collaboration.TaskRolesDescription = defaultTaskRolesDescription
	}
	return profile, nil
}

func (b *threadBuilder) baseTurnProfile(threadProfile ResolvedThreadProfile) (ResolvedTurnProfile, error) {
	roleID := strings.TrimSpace(threadProfile.RoleID)
	if roleID == "" {
		roleID = DefaultRoleID
	}
	rolePreset, ok := b.cfg.Turn.Roles[roleID]
	if !ok {
		return ResolvedTurnProfile{}, fmt.Errorf("cloudagent: role %q is not configured", roleID)
	}
	modelID := strings.TrimSpace(rolePreset.Model.Default)
	modelProfile, ok := b.cfg.Turn.Models[modelID]
	if !ok {
		return ResolvedTurnProfile{}, fmt.Errorf("cloudagent: role %q default model %q is not configured", roleID, modelID)
	}
	capabilities := cloneTurnCapabilities(b.cfg.Turn.Defaults.Capabilities)
	capabilities.Middlewares = append(capabilities.Middlewares, rolePreset.Middlewares...)
	policy := b.cfg.Turn.Defaults.Policy
	if rolePreset.ApprovalPolicy != "" {
		policy.ApprovalPolicy = rolePreset.ApprovalPolicy
	}
	return ResolvedTurnProfile{
		RoleID:       roleID,
		ModelID:      modelID,
		Model:        modelProfile,
		Prompt:       turnPromptProfile(roleID, b.cfg.Turn.Prompt, rolePreset.Prompt),
		Capabilities: capabilities,
		Budget:       b.cfg.Turn.Defaults.Budget,
		Policy:       policy,
	}, nil
}

func (b *threadBuilder) resolveTurnProfile(ctx context.Context, spec threadSpec, turnID string, trigger TurnTrigger) (profile ResolvedTurnProfile, err error) {
	base, err := b.baseTurnProfile(spec.Profile)
	if err != nil {
		if b.cfg.Turn.ResolveProfile == nil {
			return ResolvedTurnProfile{}, err
		}
		base = b.fallbackTurnProfile(spec.Profile)
	}
	if b.cfg.Turn.ResolveProfile == nil {
		return b.validateTurnProfile(base)
	}
	profile, err = b.cfg.Turn.ResolveProfile(ctx, TurnProfileRequest{
		ThreadInfo:    threadInfoFromCoordinator(spec.Info),
		ThreadProfile: spec.Profile,
		TurnID:        turnID,
		Trigger:       trigger,
		Base:          base,
	})
	if err != nil {
		return ResolvedTurnProfile{}, err
	}
	return b.validateTurnProfile(profile)
}

func (b *threadBuilder) fallbackTurnProfile(threadProfile ResolvedThreadProfile) ResolvedTurnProfile {
	roleID := strings.TrimSpace(threadProfile.RoleID)
	if roleID == "" {
		roleID = DefaultRoleID
	}
	return ResolvedTurnProfile{
		RoleID:       roleID,
		Prompt:       turnPromptProfile(roleID, b.cfg.Turn.Prompt, PromptConfig{}),
		Capabilities: cloneTurnCapabilities(b.cfg.Turn.Defaults.Capabilities),
		Budget:       b.cfg.Turn.Defaults.Budget,
		Policy:       b.cfg.Turn.Defaults.Policy,
	}
}

func (b *threadBuilder) validateTurnProfile(profile ResolvedTurnProfile) (ResolvedTurnProfile, error) {
	profile, err := validateTurnProfile(profile)
	if err != nil {
		return ResolvedTurnProfile{}, err
	}
	roleID := strings.TrimSpace(profile.RoleID)
	rolePreset, ok := b.cfg.Turn.Roles[roleID]
	if ok && !roleAllowsModel(rolePreset, strings.TrimSpace(profile.ModelID)) {
		return ResolvedTurnProfile{}, fmt.Errorf("cloudagent: turn profile role %q model %q is not allowed", roleID, profile.ModelID)
	}
	return profile, nil
}

func (b *threadBuilder) newAgentThread(ctx context.Context, threadInfo *coordinator.Thread) (agentThread agentworker.AgentThread, err error) {
	threadProfile, err := b.resolveThreadProfile(ctx, threadInfo)
	if err != nil {
		return nil, err
	}
	spec, err := b.resolveThreadSpec(threadInfo, threadProfile)
	if err != nil {
		return nil, err
	}
	if memory.IsConsolidationThreadMetadata(threadInfo.Metadata) {
		if !b.cfg.Memory.Enabled {
			return nil, fmt.Errorf("cloudagent: memory consolidation thread requires memory enabled")
		}
		if b.deps.MemoryStore == nil {
			return nil, fmt.Errorf("cloudagent: memory consolidation thread requires memory store")
		}
		if b.deps.MemoryWorkspace == nil {
			return nil, fmt.Errorf("cloudagent: memory consolidation thread requires memory workspace")
		}
		resources, err := b.prepareMemoryConsolidationResources(ctx, spec)
		if err != nil {
			return nil, err
		}
		turnProfile, err := b.resolveTurnProfile(ctx, spec, "", TurnTrigger{Kind: TurnTriggerInitial})
		if err != nil {
			return nil, err
		}
		return b.newMemoryConsolidationAgentThread(ctx, spec, resources, turnProfile)
	}

	resources, err := b.prepareThreadResources(ctx, spec)
	if err != nil {
		return nil, err
	}
	spec.Profile.WorkDir = resources.WorkDir
	turnProfile, err := b.resolveTurnProfile(ctx, spec, "", TurnTrigger{Kind: TurnTriggerInitial})
	if err != nil {
		return nil, err
	}
	threadLevelConfig := b.buildThreadLevelConfig(ctx, spec, resources, turnProfile)
	thread := agentthread.New(spec.ThreadID, nil, resources.EventBus, threadLevelConfig)
	return b.adaptAgentThreadToWorker(ctx, spec, resources, thread)
}

func (b *threadBuilder) resolveThreadSpec(threadInfo *coordinator.Thread, profile ResolvedThreadProfile) (spec threadSpec, err error) {
	if threadInfo == nil {
		return threadSpec{}, fmt.Errorf("thread info is required")
	}
	spec = threadSpec{
		Info:     threadInfo,
		ThreadID: strconv.FormatInt(threadInfo.ThreadID, 10),
		Profile:  profile,
	}
	if strings.TrimSpace(profile.WorkDir) == "" && profile.Backend.Type != cloudbackend.TypeAIInfra &&
		!memory.IsConsolidationThreadMetadata(threadInfo.Metadata) {
		return threadSpec{}, fmt.Errorf("thread workdir is required")
	}
	return spec, nil
}

func (b *threadBuilder) threadWorkDir(threadInfo *coordinator.Thread, profile tasktool.ThreadProfile) (workDir string) {
	if profile.Cwd != "" {
		return profile.Cwd
	}
	if b.deps.WorkDirResolver != nil {
		if workDir := strings.TrimSpace(b.deps.WorkDirResolver(threadInfoFromCoordinator(threadInfo), profile)); workDir != "" {
			return workDir
		}
	}
	if b.cfg.Thread.Backend.Type == cloudbackend.TypeAIInfra {
		return ""
	}
	root := b.cfg.Thread.WorkDir
	if b.cfg.Thread.Backend.Type == cloudbackend.TypeLocal && strings.TrimSpace(b.cfg.Thread.Backend.Local.Root) != "" {
		root = b.cfg.Thread.Backend.Local.Root
	}
	if threadInfo == nil {
		return root
	}
	return sessionWorkDir(root, threadInfo.UserID, threadInfo.SessionID)
}

func (b *threadBuilder) prepareThreadResources(ctx context.Context, spec threadSpec) (resources threadResources, err error) {
	history, err := b.deps.HistoryStore(ctx, spec.ThreadID)
	if err != nil {
		return threadResources{}, fmt.Errorf("init history store: %w", err)
	}
	checkpoint, err := b.deps.CheckpointStore(ctx, spec.ThreadID)
	if err != nil {
		return threadResources{}, fmt.Errorf("init checkpoint store: %w", err)
	}
	workspace, err := cloudbackend.Open(ctx, spec.Profile.Backend, cloudbackend.Target{
		UID:         spec.Info.UserID,
		SessionID:   spec.Info.SessionID,
		ProjectName: spec.Profile.Project,
		ProjectPath: spec.Profile.WorkDir,
	})
	if err != nil {
		return threadResources{}, fmt.Errorf("open backend workspace: %w", err)
	}

	return threadResources{
		EventBus:   make(chan agentthread.Event, defaultCloudAgentEventBusSize),
		Backend:    workspace.Backend,
		WorkDir:    workspace.WorkDir,
		History:    history,
		Checkpoint: checkpoint,
	}, nil
}

func (b *threadBuilder) prepareMemoryConsolidationResources(ctx context.Context, spec threadSpec) (threadResources, error) {
	if b.deps.HistoryStore == nil {
		return threadResources{}, fmt.Errorf("cloudagent: memory consolidation history store provider is required")
	}
	if b.deps.CheckpointStore == nil {
		return threadResources{}, fmt.Errorf("cloudagent: memory consolidation checkpoint store provider is required")
	}
	history, err := b.deps.HistoryStore(ctx, spec.ThreadID)
	if err != nil {
		return threadResources{}, fmt.Errorf("init memory history store: %w", err)
	}
	checkpoint, err := b.deps.CheckpointStore(ctx, spec.ThreadID)
	if err != nil {
		return threadResources{}, fmt.Errorf("init memory checkpoint store: %w", err)
	}
	return threadResources{
		EventBus:   nil,
		WorkDir:    "",
		History:    history,
		Checkpoint: checkpoint,
	}, nil
}

func (b *threadBuilder) newMemoryConsolidationAgentThread(ctx context.Context, spec threadSpec, resources threadResources, turnProfile ResolvedTurnProfile) (agentworker.AgentThread, error) {
	modelProfile := turnProfile.Model
	metadata := spec.Info.Metadata
	parsedMetadata, err := memory.ParseStage2Metadata(metadata)
	if err != nil {
		return newStaleMemoryConsolidationThread(spec.ThreadID, fmt.Sprintf("parse memory stage2 metadata: %v", err)), nil
	}
	if err := b.deps.MemoryStore.ValidateStage2Thread(ctx, memory.ValidateStage2ThreadRequest{
		UserID:         parsedMetadata.UserID,
		OwnershipToken: parsedMetadata.OwnershipToken,
		ThreadID:       spec.ThreadID,
		ValidatedAt:    time.Now(),
	}); err != nil {
		return newStaleMemoryConsolidationThread(spec.ThreadID, fmt.Sprintf("authenticate memory stage2 thread: %v", err)), nil
	}
	return memory.NewConsolidationAgentThread(memory.ConsolidationAgentThreadConfig{
		ThreadID:        spec.ThreadID,
		Metadata:        metadata,
		ChatModel:       modelProfile.ChatModel,
		HistoryStore:    resources.History,
		CheckpointStore: resources.Checkpoint,
		Callbacks:       turnProfile.Capabilities.Callbacks,
		EventIDProvider: b.eventID,
		Store:           b.deps.MemoryStore,
		Workspace:       b.deps.MemoryWorkspace.ForUser(parsedMetadata.UserID),
		LeaseTTL:        b.stage2LeaseTTL(),
		RetryDelay:      b.stage2RetryDelay(),
	})
}

func (b *threadBuilder) adaptAgentThreadToWorker(ctx context.Context, spec threadSpec, resources threadResources, thread *agentthread.DeepAgentThread) (runtime agentworker.AgentThread, err error) {
	runtime, err = runtimethread.NewRuntime(runtimethread.AdapterConfig{
		SessionID:  spec.Info.SessionID,
		ThreadID:   spec.ThreadID,
		Thread:     thread,
		EventBus:   resources.EventBus,
		ThreadInfo: threadInfoFromSpec(spec),
		TurnConfig: func(ctx context.Context, req runtimethread.TurnStartRequest) (*agentthread.TurnConfig, error) {
			trigger := TurnTrigger{Mode: req.Mode, Message: req.Message}
			if req.Resume {
				trigger.Kind = TurnTriggerResume
			} else {
				trigger.Kind = TurnTriggerUserInput
			}
			turnProfile, err := b.resolveTurnProfile(ctx, spec, req.TurnID, trigger)
			if err != nil {
				return nil, err
			}
			return b.buildTurnConfigForMode(ctx, spec, resources, turnProfile, req.Mode)
		},
		ApprovalRemember: runtimethread.ApprovalRemembererFunc(func(ctx context.Context, payload protoinput.ResumeTurnPayload) {
			if b.deps.Approvals != nil && payload.Approval != nil && payload.Approval.AllowInSession && payload.Approval.Approved {
				b.deps.Approvals.Allow(ctx, threadInfoFromCoordinator(spec.Info), payload.ToolName, payload.ArgumentsInJSON)
			}
		}),
		TurnFinishedObserver: b.buildMemoryTurnObserver(spec.Info),
		ThreadOutputObserver: b.deps.ThreadOutputObserver,
		InterruptResume:      b.deps.InterruptResume,
		Output:               b.cfg.Output,
	})
	if err != nil {
		return nil, err
	}
	return runtime, nil
}

func (b *threadBuilder) buildThreadLevelConfig(ctx context.Context, spec threadSpec, resources threadResources, turnProfile ResolvedTurnProfile) (opts agentthread.ThreadOptions) {
	modelProfile := turnProfile.Model
	contextWindow := modelProfile.ContextWindow
	if contextWindow <= 0 {
		contextWindow = int64(constant.LookupModelContextWindow(ctx, modelProfile.ModelName))
	}
	autoLimit := spec.Profile.Compaction.AutoCompactLimitTokens
	if autoLimit <= 0 && contextWindow > 0 {
		autoLimit = int(float64(contextWindow) * 0.85)
	}
	if autoLimit <= 0 {
		autoLimit = 16000
	}
	keptUserTokens := spec.Profile.Compaction.CompactKeptUserTokens
	logs.CtxInfo(ctx, "[cloudagent] enable compaction: thread_id=%s model=%s context_window=%d auto_limit=%d kept_user_tokens=%d",
		spec.ThreadID, modelProfile.ModelName, contextWindow, autoLimit, keptUserTokens)
	return agentthread.ThreadOptions{
		HistoryStore: resources.History,
		CompactionStrategy: compact.NewCodexStrategy(
			modelProfile.ChatModel,
			autoLimit,
			keptUserTokens,
			nil,
			compact.WithPromptAppend(spec.Profile.Compaction.PromptAppend),
		),
	}
}

func (b *threadBuilder) buildTurnConfig(ctx context.Context, spec threadSpec, resources threadResources, turnProfile ResolvedTurnProfile) (runConfig *agentthread.TurnConfig, err error) {
	roleConfig := b.buildRoleTurnLevelConfig(spec.Info, turnProfile.RoleID, turnProfile.Policy, resources.Backend, spec.Profile.WorkDir)
	middlewares := b.buildPromptMiddlewares(ctx, turnProfile)
	if memoryRead := b.memoryReadMiddleware(spec.Info.UserID); memoryRead != nil {
		middlewares = append(middlewares, memoryRead)
	}
	middlewares = append(middlewares, turnProfile.Capabilities.Middlewares...)
	middlewares = append(middlewares, roleConfig.Middlewares...)
	middlewares = append(middlewares, b.collabMiddlewares(ctx, spec.Info, spec.Profile)...)
	runConfig = &agentthread.TurnConfig{
		Agent: deepagents.Config{
			Model:       turnProfile.Model.ChatModel,
			Tools:       append([]tool.BaseTool(nil), turnProfile.Capabilities.Tools...),
			Callbacks:   append([]callbacks.Handler(nil), turnProfile.Capabilities.Callbacks...),
			Middlewares: middlewares,

			MaxSteps:         turnProfile.Budget.MaxSteps,
			MaxModelCalls:    turnProfile.Budget.MaxModelCalls,
			FilesystemConfig: roleConfig.Filesystem,
			ToolMask:         roleConfig.ToolMask,
			Backend:          resources.Backend,
			SkillLoader:      b.buildSkillLoader(turnProfile, spec.Profile, resources.Backend),
			CheckpointStore:  resources.Checkpoint,

			HITLConfig: b.hitlConfig(roleConfig.ToolPolicyGates, turnProfile.Policy.EnableFollowUpTool),
		},
		EnablePlan: true,

		EventIDProvider: b.eventID,
	}
	return runConfig, nil
}

func (b *threadBuilder) buildTurnConfigForMode(ctx context.Context, spec threadSpec, resources threadResources, turnProfile ResolvedTurnProfile, mode protoinput.UserMessageMode) (runConfig *agentthread.TurnConfig, err error) {
	cfg, err := b.buildTurnConfig(ctx, spec, resources, turnProfile)
	if err != nil {
		return nil, err
	}
	if mode == protoinput.UserMessageModeImplPlan {
		runConfig = b.applyPlanModeTurnConfig(ctx, cfg, spec, resources.Backend, turnProfile)
		return runConfig, nil
	}
	runConfig = cfg
	return runConfig, nil
}

func (b *threadBuilder) buildRoleTurnLevelConfig(threadInfo *coordinator.Thread, roleID string, turnPolicy TurnPolicy, backend backends.SandboxBackend, workDir string) roleTurnLevelConfig {
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

func (b *threadBuilder) wrapExecutePolicyGate(threadInfo *coordinator.Thread, gate deeptools.ToolPolicyGate) deeptools.ToolPolicyGate {
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

func (b *threadBuilder) applyPlanModeTurnConfig(ctx context.Context, base *agentthread.TurnConfig, spec threadSpec, backend backends.SandboxBackend, turnProfile ResolvedTurnProfile) *agentthread.TurnConfig {
	cfg := base.Clone()
	cfg.EnablePlan = false
	cfg.Agent.SkillLoader = nil
	cfg.Agent.Middlewares = b.buildPromptMiddlewares(ctx, turnProfile)
	if memoryRead := b.memoryReadMiddleware(spec.Info.UserID); memoryRead != nil {
		cfg.Agent.Middlewares = append(cfg.Agent.Middlewares, memoryRead)
	}
	cfg.Agent.Middlewares = append(cfg.Agent.Middlewares, turnProfile.Capabilities.Middlewares...)
	policyGates := map[string]deeptools.ToolPolicyGate{}
	if execMiddleware := b.newExecuteMiddleware(ApprovalPolicyReadOnly, spec.Profile.WorkDir, backend); execMiddleware != nil {
		cfg.Agent.Middlewares = append(cfg.Agent.Middlewares, execMiddleware)
		policyGates[execmw.DefaultToolName] = b.wrapExecutePolicyGate(spec.Info, execMiddleware.PolicyGate())
	}
	cfg.Agent.Middlewares = append(cfg.Agent.Middlewares, b.collabMiddlewares(ctx, spec.Info, spec.Profile)...)
	cfg.Agent.Middlewares = append(cfg.Agent.Middlewares, planmode.New(nil))
	if cfg.Agent.FilesystemConfig != nil {
		if cfg.Agent.FilesystemConfig.WorkDir == "" {
			cfg.Agent.FilesystemConfig.WorkDir = spec.Profile.WorkDir
		}
		cfg.Agent.FilesystemConfig.ReadOnly = true
		cfg.Agent.FilesystemConfig.DisableExecute = true
	}
	cfg.Agent.ToolMask = deeptools.CombineMasks(cfg.Agent.ToolMask, planToolMask)
	cfg.Agent.HITLConfig = b.hitlConfig(policyGates, turnProfile.Policy.EnableFollowUpTool)
	return cfg
}

func (b *threadBuilder) hitlConfig(policyGates map[string]deeptools.ToolPolicyGate, enableFollowUpTool bool) *deepagents.HITLConfig {
	return &deepagents.HITLConfig{ToolPolicyGates: policyGates, NeedFollowUpTool: enableFollowUpTool}
}

func (b *threadBuilder) buildPromptMiddlewares(ctx context.Context, profile ResolvedTurnProfile) []middleware.Middleware {
	var out []middleware.Middleware
	for _, source := range profile.Prompt.Sources {
		for _, prompt := range []string{
			source.Text,
			readPromptFile(ctx, source.File),
		} {
			if prompt = strings.TrimSpace(prompt); prompt != "" {
				out = append(out, baseprompt.New(prompt))
			}
		}
	}
	return out
}

func (b *threadBuilder) buildSkillLoader(turnProfile ResolvedTurnProfile, threadProfile ResolvedThreadProfile, backend backends.Backend) skillmw.Loader {
	if turnProfile.Capabilities.Skills.Loader != nil {
		return turnProfile.Capabilities.Skills.Loader
	}
	sources := nonEmptySkillSources(turnProfile.Capabilities.Skills.Sources)
	if len(sources) == 0 {
		return nil
	}
	if threadProfile.Backend.Type == cloudbackend.TypeLocal {
		if len(sources) == 1 {
			return skillmw.NewFileSystemSkillLoader([]string{"."}, backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
				RootDir:     expandLocalUserPath(sources[0]),
				VirtualMode: true,
			}), true, nil)
		}
		return skillmw.NewFileSystemSkillLoader(expandLocalUserPaths(sources), backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
			RootDir:     "/",
			VirtualMode: true,
		}), true, nil)
	}
	if backend == nil {
		return nil
	}
	return skillmw.NewFileSystemSkillLoader(sources, backend, true, nil)
}

func (b *threadBuilder) collabMiddlewares(ctx context.Context, threadInfo *coordinator.Thread, threadProfile ResolvedThreadProfile) []middleware.Middleware {
	taskTool := b.collabTaskTool(ctx, threadInfo, threadProfile)
	if taskTool == nil {
		return nil
	}
	return []middleware.Middleware{
		tasktool.NewCollabMiddleware(tasktool.CollabMiddlewareConfig{
			TaskTool:         taskTool,
			RolesDescription: threadProfile.Collaboration.TaskRolesDescription,
		}),
	}
}

func (b *threadBuilder) collabTaskTool(ctx context.Context, threadInfo *coordinator.Thread, threadProfile ResolvedThreadProfile) *tasktool.TaskTool {
	if b.deps.MessageWaitObserver == nil {
		return nil
	}
	sessionID := ""
	threadID := int64(0)
	userID := int64(0)
	if threadInfo != nil {
		sessionID = threadInfo.SessionID
		threadID = threadInfo.ThreadID
		userID = threadInfo.UserID
	}
	currentRef, err := b.currentThreadRef(ctx, threadInfo, userID, sessionID, threadID)
	if err != nil {
		logs.CtxError(ctx, "[cloudagent] resolve current thread ref failed: 对话流ID=%s thread_id=%d err=%v", sessionID, threadID, err)
	}
	b.registerKnownThreadRefs(ctx, threadInfo)

	metadata := map[string]string{}
	if threadID != 0 {
		parentThreadID := strconv.FormatInt(threadID, 10)
		rootThreadID := threadInfo.Metadata[MetadataRootThreadID]
		if rootThreadID == "" {
			rootThreadID = parentThreadID
		}
		metadata[MetadataThreadRole] = ThreadRoleChild
		metadata[MetadataParentThreadID] = parentThreadID
		metadata[MetadataRootThreadID] = rootThreadID
		metadata[MetadataProjectName] = resolvedThreadProjectName(threadInfo, threadProfile)
	}
	spawnInitialMetadata := map[string]string{}
	if currentRef != "" {
		spawnInitialMetadata[MetadataFromThreadRef] = currentRef
	}
	return &tasktool.TaskTool{
		Host: cloud.CoordinatorTaskHost{
			Namespace: b.cfg.Host.Namespace,
			Env:       b.cfg.Host.Env,
			Client:    b.deps.Coordinator,
			UserID:    userID,
		},
		ThreadID:                    strconv.FormatInt(threadID, 10),
		SessionID:                   sessionID,
		UserID:                      userID,
		WorkerConcurrency:           b.cfg.Host.Concurrency,
		Metadata:                    metadata,
		SpawnProfile:                tasktool.ThreadProfile{Cwd: threadProfile.WorkDir},
		SpawnInitialMessageMetadata: spawnInitialMetadata,
		SpawnMetadataDescription:    threadProfile.Collaboration.SpawnMetadataDescription,
		ResolveTarget: func(ctx context.Context, target string) (string, error) {
			threadID, ok, err := b.resolveThreadRef(ctx, userID, sessionID, target)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", fmt.Errorf("unknown thread target %q", target)
			}
			return strconv.FormatInt(threadID, 10), nil
		},
		OnThreadSpawned: func(ctx context.Context, spawned tasktool.SpawnedThread) (string, error) {
			if b.deps.ThreadRefs == nil {
				return "", nil
			}
			threadID, err := strconv.ParseInt(spawned.ThreadID, 10, 64)
			if err != nil {
				return "", fmt.Errorf("invalid spawned thread id %q: %w", spawned.ThreadID, err)
			}
			return b.deps.ThreadRefs.Allocate(ctx, userID, sessionID, threadID)
		},
		FormatOutbound: func(ctx context.Context, msg tasktool.OutboundMessage) (*tasktool.FormattedOutboundMessage, error) {
			if currentRef == "" {
				return nil, fmt.Errorf("thread ref not found for current thread")
			}
			payload, err := json.Marshal(protoinput.UserMessage{
				Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: msg.Content}},
			})
			if err != nil {
				return nil, fmt.Errorf("marshal outbound message: %w", err)
			}
			return &tasktool.FormattedOutboundMessage{
				MessageType: protoinput.MessageTypeInput,
				Payload:     payload,
				Metadata:    map[string]string{MetadataFromThreadRef: currentRef},
			}, nil
		},
		InputValidator:      b.deps.TaskToolInputValidator,
		TaskResultModifier:  b.deps.TaskResultModifier,
		MessageWaitObserver: b.deps.MessageWaitObserver,
	}
}

func (b *threadBuilder) currentThreadRef(ctx context.Context, threadInfo *coordinator.Thread, userID int64, sessionID string, threadID int64) (string, error) {
	if isMainThread(threadInfo) {
		return ThreadRoleMain, nil
	}
	if b.deps.ThreadRefs == nil {
		return "", nil
	}
	ref, ok, err := b.deps.ThreadRefs.RefForThread(ctx, userID, sessionID, threadID)
	if err != nil || !ok {
		return ref, err
	}
	return ref, nil
}

func (b *threadBuilder) resolveThreadRef(ctx context.Context, userID int64, sessionID string, ref string) (int64, bool, error) {
	if b.deps.ThreadRefs != nil {
		return b.deps.ThreadRefs.Resolve(ctx, userID, sessionID, ref)
	}
	threadID, err := strconv.ParseInt(strings.TrimSpace(ref), 10, 64)
	return threadID, err == nil && threadID != 0, nil
}

func (b *threadBuilder) registerKnownThreadRefs(ctx context.Context, threadInfo *coordinator.Thread) {
	if b.deps.ThreadRefs == nil || threadInfo == nil {
		return
	}
	sessionID := threadInfo.SessionID
	threadID := threadInfo.ThreadID
	if sessionID == "" || threadID == 0 {
		return
	}
	metadata := threadInfo.Metadata
	if isMainThread(threadInfo) {
		_ = b.deps.ThreadRefs.Register(ctx, threadInfo.UserID, sessionID, ThreadRoleMain, threadID)
	}
	if parentThreadID := metadata[MetadataParentThreadID]; parentThreadID != "" {
		if parsed, err := strconv.ParseInt(parentThreadID, 10, 64); err == nil {
			_ = b.deps.ThreadRefs.Register(ctx, threadInfo.UserID, sessionID, "parent", parsed)
		}
	}
	if rootThreadID := metadata[MetadataRootThreadID]; rootThreadID != "" {
		if parsed, err := strconv.ParseInt(rootThreadID, 10, 64); err == nil {
			_ = b.deps.ThreadRefs.Register(ctx, threadInfo.UserID, sessionID, ThreadRoleMain, parsed)
		}
	}
}

func (b *threadBuilder) memoryReadMiddleware(threadInfoUserID int64) middleware.Middleware {
	if !b.cfg.Memory.Enabled || b.deps.MemoryWorkspace == nil || threadInfoUserID == 0 {
		return nil
	}
	return memory.NewSummaryMiddleware(memory.SummaryMiddlewareConfig{
		UserID:    strconv.FormatInt(threadInfoUserID, 10),
		Workspace: b.deps.MemoryWorkspace,
	})
}

func (b *threadBuilder) buildMemoryTurnObserver(threadInfo *coordinator.Thread) runtimethread.TurnFinishedObserver {
	if !b.cfg.Memory.Enabled {
		return nil
	}
	if b.deps.MemoryStore == nil {
		logs.Warnf("[cloudagent] memory turn observer disabled: memory store is nil")
		return nil
	}
	if threadInfo == nil {
		logs.Warnf("[cloudagent] memory turn observer disabled: thread info is nil")
		return nil
	}
	if memory.IsConsolidationThreadMetadata(threadInfo.Metadata) {
		logs.Infof("[cloudagent] memory turn observer skipped for consolidation thread: thread_id=%d user_id=%d", threadInfo.ThreadID, threadInfo.UserID)
		return nil
	}
	userID := strconv.FormatInt(threadInfo.UserID, 10)
	sourceThreadID := strconv.FormatInt(threadInfo.ThreadID, 10)
	idleWindow := b.cfg.Memory.Stage1IdleWindow
	if idleWindow <= 0 {
		idleWindow = 10 * time.Minute
	}
	logs.Infof("[cloudagent] memory turn observer enabled: user_id=%s source_thread_id=%s idle_window=%s", userID, sourceThreadID, idleWindow)
	return func(ctx context.Context, ev agentthread.Event) {
		go func() {
			sourceUpdatedAt := ev.TS
			if sourceUpdatedAt.IsZero() {
				sourceUpdatedAt = time.Now()
			}
			eligibleAt := sourceUpdatedAt.Add(idleWindow)
			logs.CtxInfo(ctx, "[cloudagent] memory turn observer fired: user_id=%s source_thread_id=%s event_thread_id=%s event_id=%s turn_id=%s event_type=%s source_updated_at=%s eligible_at=%s",
				userID, sourceThreadID, ev.ThreadID, ev.ID, ev.TurnID, ev.Type, sourceUpdatedAt.Format(time.RFC3339Nano), eligibleAt.Format(time.RFC3339Nano))
			if err := b.deps.MemoryStore.TouchSource(context.WithoutCancel(ctx), memory.TouchSourceRequest{
				Memory: memory.UserMemoryContext{
					UserID:       userID,
					WriteEnabled: true,
				},
				SourceThreadID:  sourceThreadID,
				SourceUpdatedAt: sourceUpdatedAt,
				EligibleAt:      eligibleAt,
				Mode:            memory.SourceModeEnabled,
			}); err != nil {
				logs.CtxWarn(ctx, "[cloudagent] memory touch source failed: user_id=%s source_thread_id=%s event_thread_id=%s event_id=%s turn_id=%s err=%v",
					userID, sourceThreadID, ev.ThreadID, ev.ID, ev.TurnID, err)
				return
			}
			logs.CtxInfo(ctx, "[cloudagent] memory touch source succeeded: user_id=%s source_thread_id=%s event_thread_id=%s event_id=%s turn_id=%s source_updated_at=%s eligible_at=%s",
				userID, sourceThreadID, ev.ThreadID, ev.ID, ev.TurnID, sourceUpdatedAt.Format(time.RFC3339Nano), eligibleAt.Format(time.RFC3339Nano))
		}()
	}
}

func (b *threadBuilder) stage2RetryDelay() time.Duration {
	if b.cfg.Memory.Stage2ScanInterval > 0 {
		return b.cfg.Memory.Stage2ScanInterval
	}
	return 5 * time.Minute
}

func (b *threadBuilder) stage2LeaseTTL() time.Duration {
	if b.cfg.Memory.Stage2LeaseTTL > 0 {
		return b.cfg.Memory.Stage2LeaseTTL
	}
	return time.Hour
}

func (b *threadBuilder) eventID(ctx context.Context, threadID, turnID string) string {
	if b.deps.EventIDProvider != nil {
		return b.deps.EventIDProvider(ctx, threadID, turnID)
	}
	return uuid.New().String()
}
