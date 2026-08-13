//go:build !windows

package worker

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	cloudbackend "eino-cli/deepagent/cloud/backend"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/worker/runtimectx"
	runtimethread "eino-cli/deepagent/cloud/worker/thread"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/memory"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/tasktool"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/google/uuid"
)

const defaultCloudAgentEventBusSize = 4096

// threadSpec is the normalized thread identity used while building one claimed
// Coordinator thread.
type threadSpec struct {
	Info     *ac.Thread
	ThreadID string
	WorkDir  string
	Project  string
	RoleID   string
	Profile  ResolvedThreadProfile
}

type threadResources struct {
	EventBus   chan agentthread.Event
	Backend    backends.SandboxBackend
	WorkDir    string
	History    agentthread.HistoryRolloutStore
	Checkpoint compose.CheckPointStore
}

func (b *threadBuilder) newAgentThread(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
	threadProfile, err := b.resolveThreadProfile(ctx, threadInfo)
	if err != nil {
		return nil, err
	}
	spec, err := b.resolveThreadSpec(threadInfo, threadProfile)
	if err != nil {
		return nil, err
	}
	if memory.IsConsolidationThreadMetadata(threadInfo.GetMetadata()) {
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
		turnProfile, err := b.resolveTurnProfile(ctx, spec, threadProfile, "", TurnTrigger{Kind: TurnTriggerInitial})
		if err != nil {
			return nil, err
		}
		return b.newMemoryConsolidationAgentThread(ctx, spec, resources, turnProfile)
	}

	resources, err := b.prepareThreadResources(ctx, spec)
	if err != nil {
		return nil, err
	}
	spec.WorkDir = resources.WorkDir
	threadProfile.WorkDir = resources.WorkDir
	spec.Profile = threadProfile
	turnProfile, err := b.resolveTurnProfile(ctx, spec, threadProfile, "", TurnTrigger{Kind: TurnTriggerInitial})
	if err != nil {
		return nil, err
	}
	threadLevelConfig := b.buildThreadLevelConfig(ctx, spec, resources, threadProfile, turnProfile)
	thread := agentthread.NewDefault(spec.ThreadID, nil, resources.EventBus, threadLevelConfig)
	return b.adaptAgentThreadToWorker(ctx, spec, resources, threadProfile, thread)
}

func (b *threadBuilder) prepareThreadResources(ctx context.Context, spec threadSpec) (threadResources, error) {
	history, err := b.deps.HistoryStore(ctx, spec.ThreadID)
	if err != nil {
		return threadResources{}, fmt.Errorf("init history store: %w", err)
	}
	checkpoint, err := b.deps.CheckpointStore(ctx, spec.ThreadID)
	if err != nil {
		return threadResources{}, fmt.Errorf("init checkpoint store: %w", err)
	}
	workspace, err := cloudbackend.Open(ctx, spec.Profile.Backend, cloudbackend.Target{
		UID:         spec.Info.GetUserId(),
		SessionID:   spec.Info.GetSessionId(),
		ProjectName: spec.Project,
		ProjectPath: spec.WorkDir,
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

func (b *threadBuilder) adaptAgentThreadToWorker(ctx context.Context, spec threadSpec, resources threadResources, threadProfile ResolvedThreadProfile, thread *agentthread.DeepAgentThread) (agentworker.AgentThread, error) {
	runtime, err := runtimethread.NewRuntime(runtimethread.AdapterConfig{
		SessionID:  spec.Info.GetSessionId(),
		ThreadID:   spec.ThreadID,
		Thread:     thread,
		EventBus:   resources.EventBus,
		ThreadInfo: threadInfoFromSpec(spec),
		TurnRunnerConfig: func(ctx context.Context, req runtimethread.TurnRunnerConfigRequest) (*agentthread.TurnRunnerConfig, error) {
			trigger := TurnTrigger{Mode: req.Mode, Message: req.Message}
			if req.Resume {
				trigger.Kind = TurnTriggerResume
			} else {
				trigger.Kind = TurnTriggerUserInput
			}
			turnProfile, err := b.resolveTurnProfile(ctx, spec, threadProfile, req.TurnID, trigger)
			if err != nil {
				return nil, err
			}
			return b.buildTurnRunnerConfigForMode(ctx, spec, resources, threadProfile, turnProfile, req.Mode)
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

// threadInfoFromSpec maps the CloudAgent thread spec onto the runtime-readable
// thread identity. It only reads stable values from the AC thread spec; it never
// pulls thread/turn identity from message metadata. Namespace and Env are passed
// through as-is, including empty values.
func threadInfoFromSpec(spec threadSpec) runtimectx.ThreadIdentity {
	return runtimectx.ThreadIdentity{
		ThreadID:  spec.ThreadID,
		SessionID: spec.Info.GetSessionId(),
		UserID:    spec.Info.GetUserId(),
		Namespace: spec.Info.GetNamespace(),
		Env:       spec.Info.GetEnv(),
	}
}

func (b *threadBuilder) resolveThreadSpec(threadInfo *ac.Thread, profile ResolvedThreadProfile) (threadSpec, error) {
	if threadInfo == nil {
		return threadSpec{}, fmt.Errorf("thread info is required")
	}
	spec := threadSpec{
		Info:     threadInfo,
		ThreadID: strconv.FormatInt(threadInfo.GetThreadId(), 10),
		WorkDir:  profile.WorkDir,
		Project:  profile.Project,
		RoleID:   profile.RoleID,
		Profile:  profile,
	}
	if strings.TrimSpace(spec.WorkDir) == "" && profile.Backend.Type != cloudbackend.TypeAIInfra &&
		!memory.IsConsolidationThreadMetadata(threadInfo.GetMetadata()) {
		return threadSpec{}, fmt.Errorf("thread workdir is required")
	}
	return spec, nil
}

func (b *threadBuilder) threadWorkDir(threadInfo *ac.Thread, profile tasktool.ThreadProfile) string {
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
	return sessionWorkDir(root, threadInfo.GetUserId(), threadInfo.GetSessionId())
}

func threadProjectName(threadInfo *ac.Thread, profile tasktool.ThreadProfile) string {
	if threadInfo == nil {
		return "sessionless"
	}
	if name, err := cloudbackend.CleanProjectName(threadInfo.GetMetadata()[MetadataProjectName]); err == nil {
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
	if name := safePathSegment(threadInfo.GetSessionId(), 96); name != "" {
		return name
	}
	return safePathSegment(strconv.FormatInt(threadInfo.GetThreadId(), 10), 96)
}

func (b *threadBuilder) buildTurnRunnerConfig(ctx context.Context, spec threadSpec, resources threadResources, threadProfile ResolvedThreadProfile, turnProfile ResolvedTurnProfile) (*agentthread.TurnRunnerConfig, error) {
	roleConfig := b.buildRoleTurnLevelConfig(spec.Info, turnProfile.RoleID, turnProfile.Policy, resources.Backend, spec.WorkDir)
	middlewares := b.buildPromptMiddlewares(ctx, turnProfile)
	if memoryRead := b.memoryReadMiddleware(spec.Info.GetUserId()); memoryRead != nil {
		middlewares = append(middlewares, memoryRead)
	}
	middlewares = append(middlewares, turnProfile.Capabilities.Middlewares...)
	middlewares = append(middlewares, roleConfig.Middlewares...)
	middlewares = append(middlewares, b.collabMiddlewares(ctx, spec.Info, threadProfile)...)
	return &agentthread.TurnRunnerConfig{
		ChatModel:        turnProfile.Model.ChatModel,
		Tools:            append([]tool.BaseTool(nil), turnProfile.Capabilities.Tools...),
		Callbacks:        append([]callbacks.Handler(nil), turnProfile.Capabilities.Callbacks...),
		Middlewares:      middlewares,
		EnablePlan:       true,
		MaxSteps:         turnProfile.Budget.MaxSteps,
		MaxModelCalls:    turnProfile.Budget.MaxModelCalls,
		EnableFilesystem: true,
		FilesystemConfig: roleConfig.Filesystem,
		ToolMask:         roleConfig.ToolMask,
		WorkDir:          spec.WorkDir,
		SandboxBackend:   resources.Backend,
		SkillLoader:      b.buildSkillLoader(turnProfile, threadProfile, resources.Backend),
		CheckpointStore:  resources.Checkpoint,
		EventIDProvider:  b.eventID,
		HITLConfig:       b.hitlConfig(roleConfig.ToolPolicyGates, turnProfile.Policy.EnableFollowUpTool),
	}, nil
}

func (b *threadBuilder) buildTurnRunnerConfigForMode(ctx context.Context, spec threadSpec, resources threadResources, threadProfile ResolvedThreadProfile, turnProfile ResolvedTurnProfile, mode protoinput.UserMessageMode) (*agentthread.TurnRunnerConfig, error) {
	cfg, err := b.buildTurnRunnerConfig(ctx, spec, resources, threadProfile, turnProfile)
	if err != nil {
		return nil, err
	}
	if mode == protoinput.UserMessageModeImplPlan {
		return b.applyPlanModeTurnConfig(ctx, cfg, spec, resources.Backend, turnProfile), nil
	}
	return cfg, nil
}

func (b *threadBuilder) eventID(ctx context.Context, threadID, turnID string) string {
	if b.deps.EventIDProvider != nil {
		return b.deps.EventIDProvider(ctx, threadID, turnID)
	}
	return uuid.New().String()
}
