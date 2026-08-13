//go:build !windows

package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	cloudbackend "eino-cli/deepagent/cloud/backend"
	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/repairjson"
	"eino-cli/deepagent/mock/mock_model"
	"eino-cli/deepagent/worker/tasktool"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/mock/gomock"
)

func TestNewValidatesConfig(t *testing.T) {
	_, err := New(Config{}, Deps{})
	if err == nil || err.Error() != "cloudagent: at least one role is required" {
		t.Fatalf("New() error=%v", err)
	}

	_, err = New(Config{Turn: TurnConfig{
		Roles: map[string]RolePreset{DefaultRoleID: {Model: ModelPolicy{Default: "default"}}},
		Models: map[string]ModelProfile{
			"default": {},
		},
	}}, Deps{})
	if err == nil || err.Error() != `cloudagent: model "default" chat model is required` {
		t.Fatalf("New() error=%v", err)
	}
}

func TestNewRequiresNamespace(t *testing.T) {
	ctrl := gomock.NewController(t)
	_, err := New(Config{Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl))}, Deps{})
	if err == nil || err.Error() != "cloudagent: host namespace is required" {
		t.Fatalf("New() error=%v", err)
	}
}

func TestNewRequiresCoordinatorPSMWhenClientNotProvided(t *testing.T) {
	ctrl := gomock.NewController(t)
	_, err := New(Config{
		Host: HostConfig{Namespace: "ns"},
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl)),
	}, Deps{})
	if err == nil || err.Error() != "cloudagent: coordinator psm is required" {
		t.Fatalf("New() error=%v", err)
	}
}

func TestNormalizeConfig(t *testing.T) {
	cfg := normalizeConfig(Config{})
	if cfg.Thread.WorkDir != DefaultWorkDirRoot {
		t.Fatalf("WorkDir=%q, want %q", cfg.Thread.WorkDir, DefaultWorkDirRoot)
	}
	if cfg.Thread.Compaction.CompactKeptUserTokens != 4000 {
		t.Fatalf("CompactKeptUserTokens=%d, want 4000", cfg.Thread.Compaction.CompactKeptUserTokens)
	}
	if strings.TrimSpace(cfg.Thread.Collaboration.TaskRolesDescription) == "" {
		t.Fatal("TaskRolesDescription is empty")
	}
	if cfg.Turn.Defaults.Policy.EnableFollowUpTool {
		t.Fatal("EnableFollowUpTool = true, want SDK default false")
	}
}

func TestThreadOutputObserverDepsPlumbedToRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventBus := make(chan agentthread.Event, 2)
	observed := make(chan ThreadOutputObservation, 1)
	b := &threadBuilder{
		cfg: Config{Turn: testTurnConfig(nil)},
		deps: Deps{
			ThreadOutputObserver: func(_ context.Context, obs ThreadOutputObservation) {
				observed <- obs
			},
		},
	}
	deepThread := agentthread.New(
		"42",
		&agentthread.TurnRunnerConfig{CheckpointStore: checkpointer.NewInMemoryStore()},
		agentthread.NewMemoryContextManager("42", nil, nil, nil),
		eventBus,
	)
	agentThread, err := b.adaptAgentThreadToWorker(ctx, threadSpec{
		Info:     &ac.Thread{ThreadId: 42, SessionId: stringPtr("session-1")},
		ThreadID: "42",
		RoleID:   DefaultRoleID,
		Profile:  ResolvedThreadProfile{RoleID: DefaultRoleID},
	}, threadResources{EventBus: eventBus}, ResolvedThreadProfile{RoleID: DefaultRoleID}, deepThread)
	if err != nil {
		t.Fatalf("adaptAgentThreadToWorker() error=%v", err)
	}
	output, err := agentThread.Init(ctx)
	if err != nil {
		t.Fatalf("Init() error=%v", err)
	}

	eventBus <- agentthread.Event{
		ID:      "event-1",
		TurnID:  "turn-1",
		Type:    agentthread.EventTurnEnd,
		Payload: agentthread.TurnEndPayload{},
	}

	select {
	case item := <-output.Items:
		if item.Event == nil || item.Event.TurnID != "turn-1" {
			t.Fatalf("output item = %+v", item)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime output")
	}
	select {
	case obs := <-observed:
		if obs.SessionID != "session-1" || obs.ThreadID != "42" {
			t.Fatalf("observation = %+v", obs)
		}
		if obs.Item.Event == nil || obs.Item.Event.TurnID != "turn-1" {
			t.Fatalf("observation item = %+v", obs.Item)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for thread output observer")
	}
}

func TestOutputConfigPlumbedToRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventBus := make(chan agentthread.Event, 2)
	b := &threadBuilder{
		cfg: Config{
			Turn: testTurnConfig(nil),
			Output: OutputConfig{
				EventLogOptions: map[string]EventLogOption{
					protoevent.EventTypeToolCallStarted.String(): {Persist: true},
				},
			},
		},
	}
	deepThread := agentthread.New(
		"42",
		&agentthread.TurnRunnerConfig{CheckpointStore: checkpointer.NewInMemoryStore()},
		agentthread.NewMemoryContextManager("42", nil, nil, nil),
		eventBus,
	)
	agentThread, err := b.adaptAgentThreadToWorker(ctx, threadSpec{
		Info:     &ac.Thread{ThreadId: 42, SessionId: stringPtr("session-1")},
		ThreadID: "42",
		RoleID:   DefaultRoleID,
		Profile:  ResolvedThreadProfile{RoleID: DefaultRoleID},
	}, threadResources{EventBus: eventBus}, ResolvedThreadProfile{RoleID: DefaultRoleID}, deepThread)
	if err != nil {
		t.Fatalf("adaptAgentThreadToWorker() error=%v", err)
	}
	output, err := agentThread.Init(ctx)
	if err != nil {
		t.Fatalf("Init() error=%v", err)
	}

	eventBus <- agentthread.Event{
		ID:      "event-tool-start",
		TurnID:  "turn-1",
		Type:    agentthread.EventToolStart,
		Payload: agentthread.ToolStartPayload{Name: "bash", CallID: "call-1", Args: "{}"},
	}

	select {
	case item := <-output.Items:
		if item.Event == nil || item.Event.Type != "TOOL_CALL_STARTED" {
			t.Fatalf("output item = %+v", item)
		}
		if item.Event.PersistToEventLog == nil || !*item.Event.PersistToEventLog {
			t.Fatalf("persist_to_event_log = %+v, want true", item.Event.PersistToEventLog)
		}
		if item.Event.FanoutToSession == nil || !*item.Event.FanoutToSession {
			t.Fatalf("fanout_to_session = %+v, want true", item.Event.FanoutToSession)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime output")
	}
}

func TestSessionWorkDir(t *testing.T) {
	got := sessionWorkDir("/tmp/agent", 123, "hello/world")
	if !strings.HasSuffix(got, "/tmp/agent/u123/hello_world") {
		t.Fatalf("sessionWorkDir()=%q", got)
	}
}

func TestBuildTurnRunnerConfigLoadsSkillsFromThreadBackend(t *testing.T) {
	ctrl := gomock.NewController(t)
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "demo-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo-skill\ndescription: demo skill from sandbox backend\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}

	b := &threadBuilder{cfg: Config{
		Thread: ThreadConfig{Backend: cloudbackend.Config{Type: cloudbackend.TypeAIInfra}},
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl), func(t *TurnConfig) {
			t.Defaults.Capabilities.Skills.Sources = []string{"/skills"}
		}),
	}}
	cfg, err := b.buildTurnRunnerConfig(context.Background(), testSpec("/workspace/project"), threadResources{
		Backend: backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
			RootDir:     root,
			VirtualMode: true,
		}),
	}, ResolvedThreadProfile{RoleID: DefaultRoleID, Backend: cloudbackend.Config{Type: cloudbackend.TypeAIInfra}}, mustBaseTurnProfile(t, b))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SkillLoader == nil {
		t.Fatal("SkillLoader is nil")
	}
	skills, err := cfg.SkillLoader.ListSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "demo-skill" {
		t.Fatalf("skills = %+v, want demo-skill from thread backend", skills)
	}
}

func TestBuildTurnRunnerConfigLoadsLocalSkillsFromConfiguredDirectory(t *testing.T) {
	ctrl := gomock.NewController(t)
	root := t.TempDir()
	skillDir := filepath.Join(root, "demo-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo-skill\ndescription: demo skill from configured local directory\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}

	b := &threadBuilder{cfg: Config{
		Thread: ThreadConfig{Backend: cloudbackend.Config{Type: cloudbackend.TypeLocal}},
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl), func(t *TurnConfig) {
			t.Defaults.Capabilities.Skills.Sources = []string{root}
		}),
	}}
	cfg, err := b.buildTurnRunnerConfig(context.Background(), testSpec(t.TempDir()), threadResources{
		Backend: backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
			RootDir:     t.TempDir(),
			VirtualMode: true,
		}),
	}, ResolvedThreadProfile{RoleID: DefaultRoleID, Backend: cloudbackend.Config{Type: cloudbackend.TypeLocal}}, mustBaseTurnProfile(t, b))
	if err != nil {
		t.Fatal(err)
	}
	skills, err := cfg.SkillLoader.ListSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "demo-skill" {
		t.Fatalf("skills = %+v, want demo-skill from configured local directory", skills)
	}
}

func TestResolveThreadProfileUsesResolverEveryTime(t *testing.T) {
	ctrl := gomock.NewController(t)
	calls := 0
	b := &threadBuilder{cfg: Config{
		Thread: ThreadConfig{
			Backend: cloudbackend.Config{Type: cloudbackend.TypeAIInfra},
			ResolveProfile: func(ctx context.Context, req ThreadProfileRequest) (ResolvedThreadProfile, error) {
				calls++
				profile := req.Base
				profile.Project = "resolved-project"
				return profile, nil
			},
		},
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl)),
	}}

	first, err := b.resolveThreadProfile(context.Background(), &ac.Thread{ThreadId: 1, SessionId: stringPtr("s")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.resolveThreadProfile(context.Background(), &ac.Thread{ThreadId: 2, SessionId: stringPtr("s")})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("resolver calls=%d, want 2", calls)
	}
	if first.Project != "resolved-project" || second.Project != "resolved-project" {
		t.Fatalf("thread profiles = %+v %+v", first, second)
	}
}

func TestResolveTurnProfileUsesResolverEveryTime(t *testing.T) {
	ctrl := gomock.NewController(t)
	baseModel := mock_model.NewMockToolCallingChatModel(ctrl)
	runtimeModel := mock_model.NewMockToolCallingChatModel(ctrl)
	handler := callbacks.NewHandlerBuilder().Build()
	calls := 0
	var triggers []TurnTrigger
	var turnIDs []string
	b := &threadBuilder{cfg: Config{
		Turn: testTurnConfig(baseModel, func(turn *TurnConfig) {
			turn.ResolveProfile = func(ctx context.Context, req TurnProfileRequest) (ResolvedTurnProfile, error) {
				calls++
				triggers = append(triggers, req.Trigger)
				turnIDs = append(turnIDs, req.TurnID)
				profile := req.Base
				profile.ModelID = "runtime"
				profile.Model = ModelProfile{ChatModel: runtimeModel, ModelName: "runtime-model"}
				profile.Capabilities.Callbacks = []callbacks.Handler{handler}
				profile.Budget.MaxSteps = 42
				profile.Budget.MaxModelCalls = 7
				return profile, nil
			}
		}),
	}}

	threadProfile := ResolvedThreadProfile{RoleID: DefaultRoleID}
	first, err := b.resolveTurnProfile(context.Background(), testSpec("/workspace"), threadProfile, "", TurnTrigger{Kind: TurnTriggerInitial})
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.resolveTurnProfile(context.Background(), testSpec("/workspace"), threadProfile, "turn-2", TurnTrigger{Kind: TurnTriggerUserInput, Mode: protoinput.UserMessageModeImplPlan})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("resolver calls=%d, want 2", calls)
	}
	if len(triggers) != 2 || triggers[0].Kind != TurnTriggerInitial || triggers[1].Kind != TurnTriggerUserInput || triggers[1].Mode != protoinput.UserMessageModeImplPlan {
		t.Fatalf("triggers=%+v", triggers)
	}
	if len(turnIDs) != 2 || turnIDs[0] != "" || turnIDs[1] != "turn-2" {
		t.Fatalf("turn ids=%+v, want empty initial and turn-2", turnIDs)
	}
	if first.Model.ChatModel != runtimeModel || second.Model.ModelName != "runtime-model" {
		t.Fatalf("turn profiles were not resolved: first=%+v second=%+v", first, second)
	}
	if len(first.Capabilities.Callbacks) != 1 || first.Budget.MaxSteps != 42 || first.Budget.MaxModelCalls != 7 {
		t.Fatalf("turn profile = %+v", first)
	}
}

func TestResolveTurnProfileAllowsResolverToAddRole(t *testing.T) {
	ctrl := gomock.NewController(t)
	dynamicModel := mock_model.NewMockToolCallingChatModel(ctrl)
	b := &threadBuilder{cfg: Config{
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl), func(turn *TurnConfig) {
			turn.ResolveProfile = func(ctx context.Context, req TurnProfileRequest) (ResolvedTurnProfile, error) {
				if req.Base.RoleID != "dynamic" {
					t.Fatalf("base role id=%q, want dynamic", req.Base.RoleID)
				}
				profile := req.Base
				profile.ModelID = "dynamic-model"
				profile.Model = ModelProfile{ChatModel: dynamicModel}
				return profile, nil
			}
		}),
	}}

	got, err := b.resolveTurnProfile(context.Background(), testSpec("/workspace"), ResolvedThreadProfile{RoleID: "dynamic"}, "", TurnTrigger{Kind: TurnTriggerInitial})
	if err != nil {
		t.Fatal(err)
	}
	if got.RoleID != "dynamic" || got.Model.ChatModel != dynamicModel {
		t.Fatalf("turn profile=%+v", got)
	}
}

func TestResolveTurnProfileRejectsDisallowedResolvedModel(t *testing.T) {
	ctrl := gomock.NewController(t)
	defaultModel := mock_model.NewMockToolCallingChatModel(ctrl)
	otherModel := mock_model.NewMockToolCallingChatModel(ctrl)
	b := &threadBuilder{cfg: Config{
		Turn: testTurnConfig(defaultModel, func(turn *TurnConfig) {
			turn.Models["other"] = ModelProfile{ChatModel: otherModel}
			turn.Roles[DefaultRoleID] = RolePreset{
				Model: ModelPolicy{
					Default: "default",
					Allowed: []string{
						"default",
					},
				},
			}
			turn.ResolveProfile = func(ctx context.Context, req TurnProfileRequest) (ResolvedTurnProfile, error) {
				profile := req.Base
				profile.ModelID = "other"
				profile.Model = ModelProfile{ChatModel: otherModel}
				return profile, nil
			}
		}),
	}}

	_, err := b.resolveTurnProfile(context.Background(), testSpec("/workspace"), ResolvedThreadProfile{RoleID: DefaultRoleID}, "", TurnTrigger{Kind: TurnTriggerInitial})
	if err == nil || !strings.Contains(err.Error(), `model "other" is not allowed`) {
		t.Fatalf("resolveTurnProfile() error=%v, want disallowed model", err)
	}
}

func TestBaseTurnProfileUsesDefaultApprovalPolicyWhenRoleUnset(t *testing.T) {
	ctrl := gomock.NewController(t)
	b := &threadBuilder{cfg: Config{
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl), func(turn *TurnConfig) {
			turn.Defaults.Policy.ApprovalPolicy = ApprovalPolicyReadOnly
			turn.Roles[DefaultRoleID] = RolePreset{
				Model: ModelPolicy{Default: "default"},
			}
		}),
	}}

	got, err := b.baseTurnProfile(ResolvedThreadProfile{RoleID: DefaultRoleID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Policy.ApprovalPolicy != ApprovalPolicyReadOnly {
		t.Fatalf("approval policy=%q, want default %q", got.Policy.ApprovalPolicy, ApprovalPolicyReadOnly)
	}
}

func TestBaseTurnProfileRoleApprovalPolicyOverridesDefault(t *testing.T) {
	ctrl := gomock.NewController(t)
	b := &threadBuilder{cfg: Config{
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl), func(turn *TurnConfig) {
			turn.Defaults.Policy.ApprovalPolicy = ApprovalPolicyReadOnly
			turn.Roles[DefaultRoleID] = RolePreset{
				Model:          ModelPolicy{Default: "default"},
				ApprovalPolicy: ApprovalPolicyPermissive,
			}
		}),
	}}

	got, err := b.baseTurnProfile(ResolvedThreadProfile{RoleID: DefaultRoleID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Policy.ApprovalPolicy != ApprovalPolicyPermissive {
		t.Fatalf("approval policy=%q, want role override %q", got.Policy.ApprovalPolicy, ApprovalPolicyPermissive)
	}
}

func TestBuildTurnRunnerConfigIncludesExternalToolsAndCallbacks(t *testing.T) {
	ctrl := gomock.NewController(t)
	external := fakeTool{name: "mcp_tool"}
	handler := callbacks.NewHandlerBuilder().Build()
	b := &threadBuilder{cfg: Config{
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl), func(turn *TurnConfig) {
			turn.Defaults.Capabilities.Tools = []tool.BaseTool{external}
			turn.Defaults.Capabilities.Callbacks = []callbacks.Handler{handler}
		}),
	}}
	cfg, err := b.buildTurnRunnerConfig(context.Background(), testSpec(t.TempDir()), threadResources{}, ResolvedThreadProfile{RoleID: DefaultRoleID}, mustBaseTurnProfile(t, b))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tools) != 1 || len(cfg.Callbacks) != 1 {
		t.Fatalf("tools/callbacks len=%d/%d, want 1/1", len(cfg.Tools), len(cfg.Callbacks))
	}
	info, err := cfg.Tools[0].Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "mcp_tool" {
		t.Fatalf("tool name=%q", info.Name)
	}
}

func TestBuildTurnRunnerConfigIncludesRoleMiddlewares(t *testing.T) {
	ctrl := gomock.NewController(t)
	roleMiddleware := repairjson.New()
	b := &threadBuilder{cfg: Config{
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl), func(turn *TurnConfig) {
			turn.Roles[DefaultRoleID] = RolePreset{
				Model:       ModelPolicy{Default: "default"},
				Middlewares: []middleware.Middleware{roleMiddleware},
			}
		}),
	}}
	cfg, err := b.buildTurnRunnerConfig(context.Background(), testSpec(t.TempDir()), threadResources{}, ResolvedThreadProfile{RoleID: DefaultRoleID}, mustBaseTurnProfile(t, b))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, mw := range cfg.Middlewares {
		if mw == roleMiddleware {
			found = true
		}
	}
	if !found {
		t.Fatalf("role middleware was not included: %+v", cfg.Middlewares)
	}
}

func TestBuildTurnRunnerConfigUsesTurnProfileRoleForToolPolicy(t *testing.T) {
	ctrl := gomock.NewController(t)
	b := &threadBuilder{cfg: Config{
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl), func(turn *TurnConfig) {
			turn.Roles["worker"] = RolePreset{Model: ModelPolicy{Default: "default"}}
		}),
	}}
	turnProfile := mustBaseTurnProfile(t, b)
	turnProfile.RoleID = "worker"

	cfg, err := b.buildTurnRunnerConfig(context.Background(), testSpec(t.TempDir()), threadResources{}, ResolvedThreadProfile{RoleID: DefaultRoleID}, turnProfile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ToolMask == nil {
		t.Fatal("ToolMask is nil, want worker role mask")
	}
	if cfg.ToolMask(context.Background(), &schema.ToolInfo{Name: tasktool.ToolSpawnTask}) {
		t.Fatal("spawn_task is visible, want hidden for worker turn profile")
	}
}

func TestBuildTurnRunnerConfigPassesTurnToolPolicy(t *testing.T) {
	ctrl := gomock.NewController(t)
	b := &threadBuilder{cfg: Config{
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl), func(turn *TurnConfig) {
			turn.Defaults.Policy.DisableApplyPatch = true
			turn.Defaults.Budget.MaxSteps = 55
			turn.Defaults.Budget.MaxModelCalls = 9
		}),
	}}

	cfg, err := b.buildTurnRunnerConfig(context.Background(), testSpec("/workspace"), threadResources{}, ResolvedThreadProfile{RoleID: DefaultRoleID}, mustBaseTurnProfile(t, b))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FilesystemConfig == nil || !cfg.FilesystemConfig.DisableApplyPatch {
		t.Fatalf("filesystem config = %+v, want DisableApplyPatch", cfg.FilesystemConfig)
	}
	if cfg.MaxSteps != 55 {
		t.Fatalf("MaxSteps=%d, want 55", cfg.MaxSteps)
	}
	if cfg.MaxModelCalls != 9 {
		t.Fatalf("MaxModelCalls=%d, want 9", cfg.MaxModelCalls)
	}
}

func testTurnConfig(chatModel model.ToolCallingChatModel, opts ...func(*TurnConfig)) TurnConfig {
	cfg := TurnConfig{
		Roles: map[string]RolePreset{
			DefaultRoleID: {Model: ModelPolicy{Default: "default"}},
		},
		Models: map[string]ModelProfile{
			"default": {ChatModel: chatModel},
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func testSpec(workDir string) threadSpec {
	return threadSpec{
		Info:     &ac.Thread{},
		ThreadID: "1",
		WorkDir:  workDir,
		RoleID:   DefaultRoleID,
		Profile:  ResolvedThreadProfile{RoleID: DefaultRoleID, WorkDir: workDir},
	}
}

func mustBaseTurnProfile(t *testing.T, b *threadBuilder) ResolvedTurnProfile {
	t.Helper()
	profile, err := b.baseTurnProfile(ResolvedThreadProfile{RoleID: DefaultRoleID})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

type fakeTool struct {
	name string
}

func (f fakeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: f.name}, nil
}

func stringPtr(v string) *string {
	return &v
}

func (f fakeTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "", nil
}
