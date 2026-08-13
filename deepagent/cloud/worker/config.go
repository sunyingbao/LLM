//go:build !windows

package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	cloudbackend "eino-cli/deepagent/cloud/backend"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/worker/policy"
	workerthread "eino-cli/deepagent/cloud/worker/thread"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/memory"
	"eino-cli/deepagent/core/middleware"
	skillmw "eino-cli/deepagent/core/middleware/skill"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/cloud"
	"eino-cli/deepagent/worker/tasktool"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

const (
	// DefaultRoleID is used when a thread profile does not specify a role.
	DefaultRoleID      = "main"
	DefaultWorkDirRoot = "./runtime/cloud_agent/workdirs"

	// ThreadRoleMain and ThreadRoleChild are stored in thread metadata so the
	// collaboration tools can resolve parent/root task threads.
	ThreadRoleMain  = "main"
	ThreadRoleChild = "child"

	MetadataThreadRole     = "thread_role"
	MetadataParentThreadID = "parent_thread_id"
	MetadataRootThreadID   = "root_thread_id"
	MetadataFromThreadRef  = "from_thread_ref"
	MetadataProjectName    = "project_name"
)

// ModelProfile binds a named model id to an Eino chat model and its context
// window. RolePreset references these ids.
type ModelProfile struct {
	// ChatModel is the concrete model instance used by DeepAgent turns.
	ChatModel model.ToolCallingChatModel
	// ModelName is used for SDK context-window lookup when ContextWindow is
	// unset. If the name is empty or unknown, the SDK falls back to its generic
	// default context window.
	ModelName string
	// ContextWindow overrides the model context window in tokens. Leave it zero
	// to use the SDK model registry.
	ContextWindow int64
}

// ApprovalPolicy controls the default execution policy for a role.
type ApprovalPolicy string

const (
	// ApprovalPolicyNormal requires approval for commands that are not clearly
	// safe under the built-in execute policy.
	ApprovalPolicyNormal ApprovalPolicy = "normal"
	// ApprovalPolicyReadOnly disables write-oriented file behavior and applies a
	// read-only shell policy.
	ApprovalPolicyReadOnly ApprovalPolicy = "readonly"
	// ApprovalPolicyPermissive allows project-local command execution more
	// freely. Use this only for trusted roles/environments.
	ApprovalPolicyPermissive ApprovalPolicy = "permissive"
)

// PromptConfig describes one prompt source. Text and File are both optional;
// when both are set, Text is applied before File.
type PromptConfig struct {
	Text string
	File string
}

// RolePreset describes a static role template, such as "main", "explorer", or
// "worker". It is startup configuration, not a runtime object, and must not
// hold uid/session/message-specific state.
type RolePreset struct {
	// Prompt is appended after the global turn prompt for this role.
	Prompt PromptConfig
	// Model selects the default model and optional allow-list for this role.
	Model ModelPolicy
	// ApprovalPolicy controls file and shell permissions for this role.
	ApprovalPolicy ApprovalPolicy
	// Middlewares are static role defaults folded into ResolvedTurnProfile.Capabilities.
	Middlewares []middleware.Middleware
}

// ModelPolicy describes how a role selects from TurnConfig.Models.
type ModelPolicy struct {
	Default string
	Allowed []string
}

// CompactionConfig controls thread-level context compaction.
type CompactionConfig struct {
	// AutoCompactLimitTokens is the token threshold used by automatic context
	// compaction. Zero chooses a percentage of the model context window.
	AutoCompactLimitTokens int
	// CompactKeptUserTokens controls how much recent user context compaction
	// tries to preserve. Zero uses the SDK default.
	CompactKeptUserTokens int
	// PromptAppend is optional host guidance appended to the SDK default
	// compaction prompt. It should stay business-agnostic at the SDK boundary.
	PromptAppend string
}

// CollaborationConfig controls thread-level multi-agent collaboration defaults.
type CollaborationConfig struct {
	// SpawnMetadataDescription is shown to the model for task-thread metadata.
	SpawnMetadataDescription string
	// TaskRolesDescription explains available sub-agent roles to the task tool.
	// When empty, the SDK provides a default explorer/worker description.
	TaskRolesDescription string
}

// ThreadConfig describes how a claimed Coordinator thread exists over its
// lifetime: workspace resources, backend, compaction, and collaboration
// defaults.
type ThreadConfig struct {
	// WorkDir is the root directory used when Agent Coordinator ThreadProfile.Cwd
	// is empty. The default layout is WorkDir/u{thread.user_id}/{thread.session_id}.
	// It is also the local backend root when Backend.Type is empty or local.
	WorkDir string
	// Backend selects the workspace computer used by agent filesystem and shell
	// tools. Supported types are local and ai_infra.
	Backend cloudbackend.Config
	// Compaction configures durable thread context compaction.
	Compaction CompactionConfig
	// Collaboration configures task/sub-agent tool defaults.
	Collaboration CollaborationConfig
	// ResolveProfile optionally overrides the resolved thread profile when a
	// Coordinator thread is claimed.
	ResolveProfile ThreadProfileResolver
}

// TurnConfig is static startup config for turn execution. It provides
// registries and defaults; per-request dynamic state should be resolved into
// ResolvedTurnProfile by ResolveProfile.
type TurnConfig struct {
	// Prompt is the global prompt source shared by all roles.
	Prompt PromptConfig
	// Roles registers available role presets. At least DefaultRoleID should be
	// present for normal user threads.
	Roles map[string]RolePreset
	// Models registers named model profiles referenced by role presets.
	Models map[string]ModelProfile
	// Defaults are static default capabilities and execution limits for turns.
	Defaults TurnDefaults
	// ResolveProfile optionally overrides the resolved turn profile when a new
	// turn starts.
	ResolveProfile TurnProfileResolver
}

// TurnDefaults contains static defaults for every turn. Dynamic tools or
// middlewares should be added by ResolveProfile, not stored here.
type TurnDefaults struct {
	Capabilities TurnCapabilities
	Budget       TurnBudget
	Policy       TurnPolicy
}

// TurnCapabilities describes the execution surface visible to one turn.
type TurnCapabilities struct {
	// Tools are extra Eino tools exposed to turns.
	Tools []tool.BaseTool
	// Middlewares are extra DeepAgent middlewares attached to turns.
	Middlewares []middleware.Middleware
	// Callbacks are Eino callback handlers for observability.
	Callbacks []callbacks.Handler
	// Skills controls Anthropic-style skill loading for this turn.
	Skills SkillConfig
}

// SkillConfig describes skill loading for a turn. Loader wins over Sources
// when both are set.
type SkillConfig struct {
	// Sources are filesystem-like skill sources resolved against the turn's
	// thread backend, except local backend sources are read from the host.
	Sources []string
	Loader  skillmw.Loader
}

// TurnBudget limits one logical ReAct turn.
type TurnBudget struct {
	// MaxSteps controls the maximum graph run steps. Leave <= 0 for the default.
	MaxSteps int
	// MaxModelCalls limits ChatModel invocations. Leave <= 0 to disable.
	MaxModelCalls int
}

// TurnPolicy controls built-in turn behavior.
type TurnPolicy struct {
	// ApprovalPolicy controls role-level approval behavior for built-in tools.
	ApprovalPolicy ApprovalPolicy
	// DisableApplyPatch disables the filesystem apply_patch tool.
	DisableApplyPatch bool
	// EnableFollowUpTool exposes the built-in FollowUpTool.
	EnableFollowUpTool bool
}

// MemoryConfig enables the optional user-level long-term memory write loop.
// When disabled, the worker does not touch memory state.
type MemoryConfig struct {
	Enabled                  bool
	ScanInterval             time.Duration
	WakeupDebounce           time.Duration
	Stage1IdleWindow         time.Duration
	Stage1LeaseTTL           time.Duration
	Stage1MaxClaimedPerScan  int
	Stage1HistoryInput       memory.Stage1HistoryInputConfig
	Stage2LeaseTTL           time.Duration
	Stage2SuccessCooldown    time.Duration
	Stage2ScanInterval       time.Duration
	Stage2MaxUsersPerScan    int
	Stage2OutputLimitPerUser int
	WorkspaceRoot            string
}

// EventLogOption overrides EventLog persistence for one CloudAgent protocol
// event type.
type EventLogOption = workerthread.EventLogOption

// OutputConfig controls worker output delivery hints. EventLogOptions keys are
// CloudAgent protocol event type strings, for example "TOOL_CALL_STARTED".
// Missing or unmatched keys keep the SDK default policy.
type OutputConfig = workerthread.OutputConfig

// Config is the complete configuration for the default CloudAgent worker.
type Config struct {
	// Host configures the Agent Coordinator worker host loop.
	Host HostConfig
	// Thread configures Coordinator thread lifetime resources and defaults.
	Thread ThreadConfig
	// Turn configures per-turn DeepAgent behavior.
	Turn TurnConfig
	// Memory configures optional long-term memory integration.
	Memory MemoryConfig
	// Output configures how CloudAgent runtime events are delivered to host
	// storage/fanout surfaces.
	Output OutputConfig
}

// ResolvedThreadProfile is the fully resolved thread-lifetime profile for one claimed
// Coordinator thread.
type ResolvedThreadProfile struct {
	RoleID        string
	WorkDir       string
	Project       string
	Backend       cloudbackend.Config
	Compaction    CompactionConfig
	Collaboration CollaborationConfig
}

// ThreadProfileRequest describes the thread whose lifetime profile is being
// resolved.
type ThreadProfileRequest struct {
	ThreadInfo *ThreadInfo
	Base       ResolvedThreadProfile
}

// ThreadOutputObservation is a read-only snapshot of one output item emitted by
// the CloudAgent thread runtime.
type ThreadOutputObservation = workerthread.ThreadOutputObservation

// ThreadOutputObserver observes output items emitted by the CloudAgent thread
// runtime after they have been offered to the worker host.
type ThreadOutputObserver = workerthread.ThreadOutputObserver

// InterruptResumeDecoder converts generic interrupt resume payloads into the
// typed data expected by custom Eino interrupt handlers.
type InterruptResumeDecoder = workerthread.InterruptResumeDecoder

// ThreadProfileResolver optionally supplies per-thread lifetime config.
type ThreadProfileResolver func(ctx context.Context, req ThreadProfileRequest) (ResolvedThreadProfile, error)

// ResolvedTurnProfile is the fully resolved execution profile for one DeepAgent turn.
type ResolvedTurnProfile struct {
	RoleID       string
	ModelID      string
	Model        ModelProfile
	Prompt       TurnPromptProfile
	Capabilities TurnCapabilities
	Budget       TurnBudget
	Policy       TurnPolicy
}

// TurnPromptProfile is the final ordered prompt input for one turn.
type TurnPromptProfile struct {
	Sources []PromptSource
}

// PromptSource is named so prompt assembly is debuggable.
type PromptSource struct {
	Name string
	Text string
	File string
}

// TurnProfileRequest describes one turn whose execution profile is being
// resolved.
type TurnProfileRequest struct {
	ThreadInfo    *ThreadInfo
	ThreadProfile ResolvedThreadProfile
	// TurnID is the turn being configured. It is empty for thread-initial
	// profile resolution before any turn exists.
	TurnID string
	// Trigger is the message/control action that causes profile resolution.
	// It is not the full list of messages consumed by this turn.
	Trigger TurnTrigger
	Base    ResolvedTurnProfile
}

// TurnProfileResolver optionally supplies per-turn execution config.
type TurnProfileResolver func(ctx context.Context, req TurnProfileRequest) (ResolvedTurnProfile, error)

// TurnTrigger is the input or control action that causes a turn profile to be
// resolved. Later queued inputs are attributed in events, but do not mutate the
// active turn profile.
type TurnTrigger struct {
	Kind    TurnTriggerKind
	Mode    protoinput.UserMessageMode
	Message *agentworker.Message
}

type TurnTriggerKind string

const (
	TurnTriggerInitial   TurnTriggerKind = "initial"
	TurnTriggerUserInput TurnTriggerKind = "user_input"
	TurnTriggerResume    TurnTriggerKind = "resume"
)

// HistoryStoreProvider returns the durable rollout/history store for one
// claimed Coordinator thread. It is called when the worker constructs that
// thread runtime, not before every turn. The threadID argument is the
// Coordinator thread id formatted as decimal string.
type HistoryStoreProvider func(ctx context.Context, threadID string) (agentthread.HistoryRolloutStore, error)

// CheckpointStoreProvider returns the Eino checkpoint store for one claimed
// Coordinator thread. It is called when the worker constructs that thread
// runtime. The store implementation should tolerate the normal Eino access
// pattern for a single running thread.
type CheckpointStoreProvider func(ctx context.Context, threadID string) (compose.CheckPointStore, error)

// WorkDirResolver optionally overrides the default thread workdir layout.
type WorkDirResolver func(threadInfo *ThreadInfo, profile tasktool.ThreadProfile) string

// ThreadRefStore stores short, user-facing references for threads in a session.
// It is used by collaboration tools so the model can say "wait alice" instead
// of carrying numeric thread ids.
type ThreadRefStore interface {
	Register(ctx context.Context, userID int64, sessionID string, ref string, threadID int64) error
	Allocate(ctx context.Context, userID int64, sessionID string, threadID int64) (string, error)
	Resolve(ctx context.Context, userID int64, sessionID string, ref string) (int64, bool, error)
	RefForThread(ctx context.Context, userID int64, sessionID string, threadID int64) (string, bool, error)
}

// ApprovalStore remembers approved tool calls. It is optional; without it every
// approval is evaluated independently.
type ApprovalStore interface {
	IsAllowed(ctx context.Context, threadInfo *ThreadInfo, toolName string, argumentsJSON string) bool
	Allow(ctx context.Context, threadInfo *ThreadInfo, toolName string, argumentsJSON string)
}

// Deps contains systems supplied by the hosting service. HistoryStore and
// CheckpointStore are required. CoordinatorClient is optional when
// Config.Host.Coordinator is configured; provide it only when the service
// wants to own client construction. The remaining deps add collaboration,
// approval reuse, custom workdir layout, or deterministic event ids.
type Deps struct {
	// CoordinatorClient is the Agent Coordinator RPC client. If nil, New
	// creates one from Config.Host.Coordinator.
	CoordinatorClient *CoordinatorClient
	// HistoryStore creates a durable history store per thread.
	HistoryStore HistoryStoreProvider
	// CheckpointStore creates an Eino checkpoint store per thread.
	CheckpointStore CheckpointStoreProvider
	// MemoryStore stores user-level memory source candidates and jobs. It is
	// only used when Config.Memory.Enabled is true.
	MemoryStore memory.Store
	// MemoryWorkspace is the base workspace for user-level memory. The memory
	// package derives an isolated per-user root under it before Stage 2 syncs
	// inputs or exposes filesystem tools to the consolidation thread.
	MemoryWorkspace *memory.Workspace

	// ThreadRefs enables named sub-agent references for collaboration tools.
	ThreadRefs ThreadRefStore
	// Approvals enables "allow this command in this session" reuse.
	Approvals ApprovalStore
	// MessageWaitObserver lets task tools inspect session events while waiting
	// for another thread.
	MessageWaitObserver tasktool.MessageWaitObserver
	// TaskResultModifier can mutate spawn_task and send_message results before they
	// are returned to the model.
	TaskResultModifier tasktool.TaskResultModifier
	// TaskToolInputValidator validates collaboration task tool inputs before
	// task tools perform host side effects. Validator errors are returned to
	// the model as task tool error results.
	TaskToolInputValidator tasktool.TaskToolInputValidator
	// ThreadOutputObserver observes ordered thread output items emitted by the
	// CloudAgent thread runtime. It is a read-only side channel and must not
	// block normal worker processing.
	ThreadOutputObserver ThreadOutputObserver
	// InterruptResume decodes generic interrupt resume payloads for host-defined
	// Eino interrupt tools. Built-in interrupt tools are decoded by the SDK.
	InterruptResume InterruptResumeDecoder
	// WorkDirResolver overrides the default workdir layout.
	WorkDirResolver WorkDirResolver

	// EventIDProvider optionally supplies event ids for thread events.
	EventIDProvider func(ctx context.Context, threadID, turnID string) string
}

// NewApprovalStore returns the SDK default in-memory approval reuse store.
func NewApprovalStore() ApprovalStore {
	return threadApprovalStore{store: policy.NewSessionApprovalStore()}
}

// TaskMessageWaitObserver is the SDK default observer used by collaboration
// tools to detect task responses and terminal task events.
func TaskMessageWaitObserver(events []*tasktool.Event, messageID string) tasktool.MessageWaitResult {
	return workerthread.TaskMessageWaitObserver(events, messageID)
}

// threadBuilder is the private assembly pipeline behind New. It keeps the
// public package surface focused on Config and Deps.
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

func validateConfig(cfg Config) error {
	if len(cfg.Turn.Roles) == 0 {
		return fmt.Errorf("cloudagent: at least one role is required")
	}
	if len(cfg.Turn.Models) == 0 {
		return fmt.Errorf("cloudagent: at least one model is required")
	}
	for id, profile := range cfg.Turn.Models {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("cloudagent: model id is required")
		}
		if profile.ChatModel == nil {
			return fmt.Errorf("cloudagent: model %q chat model is required", id)
		}
	}
	for id, role := range cfg.Turn.Roles {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("cloudagent: role id is required")
		}
		if strings.TrimSpace(role.Model.Default) == "" {
			return fmt.Errorf("cloudagent: role %q default model is required", id)
		}
		if _, ok := cfg.Turn.Models[role.Model.Default]; !ok {
			return fmt.Errorf("cloudagent: role %q default model %q is not configured", id, role.Model.Default)
		}
		if !roleAllowsModel(role, role.Model.Default) {
			return fmt.Errorf("cloudagent: role %q default model %q is not allowed", id, role.Model.Default)
		}
	}
	if cfg.Turn.Defaults.Budget.MaxSteps < 0 {
		return fmt.Errorf("cloudagent: turn max steps must be >= 0")
	}
	if cfg.Turn.Defaults.Budget.MaxModelCalls < 0 {
		return fmt.Errorf("cloudagent: turn max model calls must be >= 0")
	}
	if strings.TrimSpace(cfg.Host.Namespace) == "" {
		return fmt.Errorf("cloudagent: host namespace is required")
	}
	return nil
}

func (b *threadBuilder) validateDeps() error {
	if b.deps.CoordinatorClient == nil {
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

func (b *threadBuilder) baseThreadProfile(threadInfo *ac.Thread) ResolvedThreadProfile {
	wireProfile := cloud.ThreadProfileFromWire(threadInfo)
	roleID := strings.TrimSpace(wireProfile.Role)
	if roleID == "" {
		roleID = DefaultRoleID
	}
	return ResolvedThreadProfile{
		RoleID:        roleID,
		WorkDir:       b.threadWorkDir(threadInfo, wireProfile),
		Project:       threadProjectName(threadInfo, wireProfile),
		Backend:       b.cfg.Thread.Backend,
		Compaction:    b.cfg.Thread.Compaction,
		Collaboration: b.cfg.Thread.Collaboration,
	}
}

func (b *threadBuilder) resolveThreadProfile(ctx context.Context, threadInfo *ac.Thread) (ResolvedThreadProfile, error) {
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

func (b *threadBuilder) resolveTurnProfile(ctx context.Context, spec threadSpec, threadProfile ResolvedThreadProfile, turnID string, trigger TurnTrigger) (ResolvedTurnProfile, error) {
	base, err := b.baseTurnProfile(threadProfile)
	if err != nil {
		if b.cfg.Turn.ResolveProfile == nil {
			return ResolvedTurnProfile{}, err
		}
		base = b.fallbackTurnProfile(threadProfile)
	}
	if b.cfg.Turn.ResolveProfile == nil {
		return b.validateTurnProfile(base)
	}
	profile, err := b.cfg.Turn.ResolveProfile(ctx, TurnProfileRequest{
		ThreadInfo:    threadInfoFromCoordinator(spec.Info),
		ThreadProfile: threadProfile,
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

func validateTurnProfile(profile ResolvedTurnProfile) (ResolvedTurnProfile, error) {
	if strings.TrimSpace(profile.RoleID) == "" {
		return ResolvedTurnProfile{}, fmt.Errorf("cloudagent: turn profile role id is required")
	}
	if strings.TrimSpace(profile.ModelID) == "" {
		return ResolvedTurnProfile{}, fmt.Errorf("cloudagent: turn profile model id is required")
	}
	if profile.Model.ChatModel == nil {
		return ResolvedTurnProfile{}, fmt.Errorf("cloudagent: turn profile model %q chat model is required", profile.ModelID)
	}
	if profile.Budget.MaxSteps < 0 {
		return ResolvedTurnProfile{}, fmt.Errorf("cloudagent: turn profile max steps must be >= 0")
	}
	if profile.Budget.MaxModelCalls < 0 {
		return ResolvedTurnProfile{}, fmt.Errorf("cloudagent: turn profile max model calls must be >= 0")
	}
	return profile, nil
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

// New builds the full Agent Coordinator-backed cloud agent worker.
func New(cfg Config, deps Deps) (*Worker, error) {
	cfg, deps, err := normalizeConfigAndResolveDeps(cfg, deps)
	if err != nil {
		return nil, err
	}
	return newResolvedWorker(cfg, deps)
}

func normalizeConfigAndResolveDeps(cfg Config, deps Deps) (Config, Deps, error) {
	cfg = normalizeConfig(cfg)
	if err := validateConfig(cfg); err != nil {
		return Config{}, Deps{}, err
	}
	var err error
	deps, err = resolveDeps(cfg, deps)
	if err != nil {
		return Config{}, Deps{}, err
	}
	return cfg, deps, nil
}

func newResolvedWorker(cfg Config, deps Deps) (*Worker, error) {
	builder, err := newThreadBuilder(cfg, deps)
	if err != nil {
		return nil, err
	}
	return newWorker(cfg.Host, deps.CoordinatorClient, builder.newAgentThread)
}

func resolveDeps(cfg Config, deps Deps) (Deps, error) {
	if deps.CoordinatorClient == nil {
		client, err := NewCoordinatorClient(cfg.Host.Coordinator)
		if err != nil {
			return deps, err
		}
		deps.CoordinatorClient = client
	}
	deps = resolveMemoryDeps(cfg.Memory, deps)
	return deps, nil
}

// Run builds and runs the worker until ctx is canceled or the worker returns an
// error.
func Run(ctx context.Context, cfg Config, deps Deps) error {
	normalized, deps, err := normalizeConfigAndResolveDeps(cfg, deps)
	if err != nil {
		return err
	}
	w, err := newResolvedWorker(normalized, deps)
	if err != nil {
		return err
	}
	if loop, err := newMemoryJobLoopFromConfig(ctx, normalized, deps); err != nil {
		return err
	} else if loop != nil {
		go func() { _ = loop.Run(ctx) }()
	}
	return w.Run(ctx)
}

func normalizeConfig(cfg Config) Config {
	if strings.TrimSpace(cfg.Thread.WorkDir) == "" {
		cfg.Thread.WorkDir = DefaultWorkDirRoot
	}
	if strings.TrimSpace(string(cfg.Thread.Backend.Type)) == "" {
		cfg.Thread.Backend.Type = cloudbackend.TypeLocal
	}
	if cfg.Thread.Backend.Type == cloudbackend.TypeLocal && strings.TrimSpace(cfg.Thread.Backend.Local.Root) == "" {
		cfg.Thread.Backend.Local.Root = cfg.Thread.WorkDir
	}
	cfg.Thread.Backend = cloudbackend.Normalize(cfg.Thread.Backend)
	if cfg.Thread.Compaction.CompactKeptUserTokens <= 0 {
		cfg.Thread.Compaction.CompactKeptUserTokens = 4000
	}
	if cfg.Thread.Collaboration.TaskRolesDescription == "" {
		cfg.Thread.Collaboration.TaskRolesDescription = defaultTaskRolesDescription
	}
	return cfg
}

func roleAllowsModel(role RolePreset, modelID string) bool {
	if len(role.Model.Allowed) == 0 {
		return true
	}
	for _, allowed := range role.Model.Allowed {
		if strings.TrimSpace(allowed) == modelID {
			return true
		}
	}
	return false
}

func turnPromptProfile(roleID string, global PromptConfig, role PromptConfig) TurnPromptProfile {
	var sources []PromptSource
	if !emptyPrompt(global) {
		sources = append(sources, PromptSource{Name: "global", Text: global.Text, File: global.File})
	}
	if !emptyPrompt(role) {
		sources = append(sources, PromptSource{Name: "role:" + roleID, Text: role.Text, File: role.File})
	}
	return TurnPromptProfile{Sources: sources}
}

func emptyPrompt(prompt PromptConfig) bool {
	return strings.TrimSpace(prompt.Text) == "" && strings.TrimSpace(prompt.File) == ""
}

func cloneTurnCapabilities(in TurnCapabilities) TurnCapabilities {
	return TurnCapabilities{
		Tools:       append([]tool.BaseTool(nil), in.Tools...),
		Middlewares: append([]middleware.Middleware(nil), in.Middlewares...),
		Callbacks:   append([]callbacks.Handler(nil), in.Callbacks...),
		Skills: SkillConfig{
			Sources: append([]string(nil), in.Skills.Sources...),
			Loader:  in.Skills.Loader,
		},
	}
}
