package deepagents

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/contextmanager"
	"eino-cli/deepagent/core/middleware/filesystem"
	skillmw "eino-cli/deepagent/core/middleware/skill"
	"eino-cli/deepagent/core/middleware/subagent"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/mock/mock_model"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	cbutils "github.com/cloudwego/eino/utils/callbacks"
	"go.uber.org/mock/gomock"
)

type testSubAgentContextInjector struct{}

func (i *testSubAgentContextInjector) LoadContext(ctx context.Context, agentName string) ([]*schema.Message, error) {
	return []*schema.Message{schema.UserMessage("forked-context")}, nil
}

type testBuilderMiddleware struct {
	middleware.BaseMiddleware
}

func (m *testBuilderMiddleware) Name() string {
	return "test_builder_middleware"
}

type testSkillLoader struct{}

func (l *testSkillLoader) ListSkills(ctx context.Context) ([]*skillmw.SkillMetadata, error) {
	return []*skillmw.SkillMetadata{{
		Name:        "code_search",
		Description: "search codebase",
		Path:        "/skills/code_search/SKILL.md",
	}}, nil
}

func findSkillMiddleware(t *testing.T, middlewares []middleware.Middleware) *skillmw.Middleware {
	t.Helper()

	for _, mw := range middlewares {
		if skillMiddleware, ok := mw.(*skillmw.Middleware); ok {
			return skillMiddleware
		}
	}
	t.Fatalf("expected skill middleware to be present")
	return nil
}

func newTestBackend(t *testing.T) *backends.FilesystemBackend {
	t.Helper()
	return backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
}

type testApplyPatchBackend struct {
	*backends.FilesystemBackend
}

func newTestApplyPatchBackend(t *testing.T) *testApplyPatchBackend {
	t.Helper()
	return &testApplyPatchBackend{FilesystemBackend: newTestBackend(t)}
}

func (b *testApplyPatchBackend) SupportsApplyPatch() bool {
	return true
}

func (b *testApplyPatchBackend) ApplyPatch(context.Context, string) (string, error) {
	return "patched", nil
}

func collectToolNames(t *testing.T, ctx context.Context, toolList []tool.BaseTool) []string {
	t.Helper()

	names := make([]string, 0, len(toolList))
	for _, tl := range toolList {
		info, err := tl.Info(ctx)
		if err != nil {
			t.Fatalf("tool.Info() error = %v", err)
		}
		names = append(names, info.Name)
	}
	return names
}

func TestNewFromSpec_MinimalSpec(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()
			sw.Send(schema.AssistantMessage("spec-done", nil), nil)
			return sr, nil
		},
	).AnyTimes()

	agent, err := NewFromSpec(ctx, &DeepAgentSpec{
		Model:           cm,
		Middlewares:     []middleware.Middleware{contextmanager.New()},
		CheckpointStore: checkpointer.NewInMemoryStore(),
		Depth:           2,
	})
	if err != nil {
		t.Fatalf("NewFromSpec() error = %v", err)
	}
	if agent.Name() != constant.GraphName {
		t.Fatalf("unexpected agent name: %s", agent.Name())
	}
	if agent.Depth() != 2 {
		t.Fatalf("unexpected agent Depth: %d", agent.Depth())
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	msg, err := out.Recv()
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	if msg.Content != "spec-done" {
		t.Fatalf("unexpected content: %s", msg.Content)
	}
}

func TestNew_UsesModelNodeKeyForCallback(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()
			sw.Send(schema.AssistantMessage("done", nil), nil)
			return sr, nil
		},
	).AnyTimes()

	var gotName string
	handler := cbutils.NewHandlerHelper().
		ChatModel(&cbutils.ModelCallbackHandler{
			OnStart: func(ctx context.Context, info *callbacks.RunInfo, input *model.CallbackInput) context.Context {
				gotName = info.Name
				return ctx
			},
		}).
		Handler()

	agent, err := New(ctx,
		WithModel(cm),
		WithContextManager(contextmanager.New()),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("hi")}, WithCallbacks(handler))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	msg, err := out.Recv()
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("unexpected content: %s", msg.Content)
	}
	if gotName != constant.NodeKeyModel {
		t.Fatalf("callback node name = %q, want %q", gotName, constant.NodeKeyModel)
	}
}

func TestNewFromSpec_RequiresModel(t *testing.T) {
	_, err := NewFromSpec(context.Background(), &DeepAgentSpec{
		Middlewares: []middleware.Middleware{contextmanager.New()},
	})
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("expected model required error, got %v", err)
	}
}

func TestNew_RequiresConfiguredSharedCustomState(t *testing.T) {
	_, err := New(context.Background(),
		WithModel(mock_model.NewMockToolCallingChatModel(gomock.NewController(t))),
		WithContextManager(contextmanager.New()),
		WithSubAgentSharedCustomState("missing"),
	)
	if err == nil || !strings.Contains(err.Error(), `sub-agent shared custom state "missing" is not configured`) {
		t.Fatalf("expected missing shared custom state error, got %v", err)
	}
}

func TestBuildSpecFromConfig_MapsBuilderFields(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	mask := func(_ context.Context, info *schema.ToolInfo) bool {
		return info.Name != "counter"
	}
	preHandler := func(ctx context.Context, input *schema.Message) (*schema.Message, error) {
		return input, nil
	}
	postHandler := func(ctx context.Context, output []*schema.Message) ([]*schema.Message, error) {
		return output, nil
	}

	stateBackend := newTestBackend(t)
	cfg := buildCreateConfig(
		WithModel(cm),
		WithContextManager(contextmanager.New()),
		WithTools(&fakeToolCounter{}),
		WithToolMask(mask),
		WithMiddleware(&testBuilderMiddleware{}),
		WithStreamToolCall(),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithBackend(stateBackend),
		WithToolNodePreHandler(preHandler),
		WithToolNodePostHandler(postHandler),
	)
	cfg.Depth = 7

	spec, err := buildSpecFromConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("buildSpecFromConfig() error = %v", err)
	}

	if spec.Model != cm {
		t.Fatalf("expected model to be preserved")
	}
	if !spec.EnableStreamToolCall {
		t.Fatalf("expected EnableStreamToolCall=true")
	}
	if spec.Depth != 7 {
		t.Fatalf("unexpected Depth: %d", spec.Depth)
	}
	if spec.Backend != stateBackend {
		t.Fatalf("expected backend to be preserved")
	}
	if len(spec.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(spec.Tools))
	}
	if spec.ToolMask == nil {
		t.Fatalf("expected ToolMask to be preserved")
	}
	if spec.ToolMask(ctx, &schema.ToolInfo{Name: "counter"}) {
		t.Fatalf("expected ToolMask to hide counter")
	}
	if spec.ToolNodePreHandler == nil || reflect.ValueOf(spec.ToolNodePreHandler).Pointer() != reflect.ValueOf(preHandler).Pointer() {
		t.Fatalf("expected ToolNodePreHandler to be preserved")
	}
	if spec.ToolNodePostHandler == nil || reflect.ValueOf(spec.ToolNodePostHandler).Pointer() != reflect.ValueOf(postHandler).Pointer() {
		t.Fatalf("expected ToolNodePostHandler to be preserved")
	}

	names := make(map[string]bool)
	for _, mw := range spec.Middlewares {
		names[mw.Name()] = true
	}
	if !names["SimpleContextManager"] {
		t.Fatalf("expected context manager middleware to be present: %+v", names)
	}
	if names["repair_json"] {
		t.Fatalf("repair_json should be opt-in middleware, got: %+v", names)
	}
	if !names["test_builder_middleware"] {
		t.Fatalf("expected custom middleware to be present: %+v", names)
	}
}

func TestBuildSpecFromConfig_DefaultBackendIsNil(t *testing.T) {
	cfg := buildCreateConfig(
		WithModel(mock_model.NewMockToolCallingChatModel(gomock.NewController(t))),
		WithContextManager(contextmanager.New()),
	)

	spec, err := buildSpecFromConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("buildSpecFromConfig() error = %v", err)
	}
	if spec.Backend != nil {
		t.Fatalf("expected nil default backend, got %T", spec.Backend)
	}
}

func TestBuildSpecFromConfig_FilesystemRequiresBackendOrWorkDir(t *testing.T) {
	cfg := buildCreateConfig(
		WithModel(mock_model.NewMockToolCallingChatModel(gomock.NewController(t))),
		WithContextManager(contextmanager.New()),
		WithFilesystem(),
	)

	_, err := buildSpecFromConfig(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "filesystem requires backend or workdir") {
		t.Fatalf("expected filesystem backend error, got %v", err)
	}
}

func TestCollectAllTools_AppliesToolMaskToFilesystemAndFollowUp(t *testing.T) {
	ctx := context.Background()
	config := buildCreateConfig(
		WithModel(mock_model.NewMockToolCallingChatModel(gomock.NewController(t))),
		WithContextManager(contextmanager.New()),
		WithBackend(newTestBackend(t)),
		WithFilesystem(),
		WithHITLConfig(&HITLConfig{NeedFollowUpTool: true}),
		WithToolMask(func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != constant.ToolReadFile && info.Name != "FollowUpTool"
		}),
	)

	middlewares, err := buildCreateMiddlewares(config, config.Backend)
	if err != nil {
		t.Fatalf("buildCreateMiddlewares() error = %v", err)
	}

	allTools, err := collectAllTools(ctx, middleware.NewMiddlewareChain(middlewares...), config)
	if err != nil {
		t.Fatalf("collectAllTools() error = %v", err)
	}

	names := collectToolNames(t, ctx, allTools)
	if containsString(names, constant.ToolReadFile) {
		t.Fatalf("expected read_file to be masked, got %v", names)
	}
	if containsString(names, "FollowUpTool") {
		t.Fatalf("expected FollowUpTool to be masked, got %v", names)
	}
	if !containsString(names, constant.ToolLs) {
		t.Fatalf("expected ls to remain available, got %v", names)
	}
}

func TestCollectAllTools_ToolMaskRunsBeforeHITLWrapping(t *testing.T) {
	ctx := context.Background()
	config := &Config{
		Tools: []tool.BaseTool{&fakeToolCounter{}},
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != "counter"
		},
		HITLConfig: &HITLConfig{
			NeedApproveTools: map[string]deeptools.NeedApproval{
				"counter": func(context.Context, *deeptools.ApprovalInfo) bool { return true },
			},
		},
	}

	allTools, err := collectAllTools(ctx, middleware.NewMiddlewareChain(), config)
	if err != nil {
		t.Fatalf("collectAllTools() error = %v", err)
	}
	if len(allTools) != 0 {
		t.Fatalf("expected masked tool to be removed before HITL wrapping, got %v", collectToolNames(t, ctx, allTools))
	}
}

func TestCollectAllTools_ToolPolicyGateDeniesWithoutRunningTool(t *testing.T) {
	ctx := context.Background()
	counter := &fakeToolCounter{}
	config := &Config{
		Tools: []tool.BaseTool{counter},
		HITLConfig: &HITLConfig{
			ToolPolicyGates: map[string]deeptools.ToolPolicyGate{
				"counter": {
					Policy: func(context.Context, *deeptools.ApprovalInfo) (deeptools.ToolCallDecision, error) {
						return deeptools.ToolCallDecision{Action: deeptools.ToolCallDeny, Reason: "blocked"}, nil
					},
					DenyFormatter: func(ctx context.Context, info *deeptools.ApprovalInfo, decision deeptools.ToolCallDecision) (string, error) {
						return `{"denied":true,"reason":"` + decision.Reason + `"}`, nil
					},
				},
			},
		},
	}

	allTools, err := collectAllTools(ctx, middleware.NewMiddlewareChain(), config)
	if err != nil {
		t.Fatalf("collectAllTools() error = %v", err)
	}
	if len(allTools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(allTools))
	}
	invokable, ok := allTools[0].(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool is not invokable")
	}
	got, err := invokable.InvokableRun(ctx, `{"delta":3}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if got != `{"denied":true,"reason":"blocked"}` {
		t.Fatalf("output = %q", got)
	}
	if counter.total != 0 {
		t.Fatalf("counter total = %d, want 0", counter.total)
	}
}

func TestCollectAllTools_ToolPolicyGateConflictsWithNeedApprove(t *testing.T) {
	ctx := context.Background()
	config := &Config{
		Tools: []tool.BaseTool{&fakeToolCounter{}},
		HITLConfig: &HITLConfig{
			NeedApproveTools: map[string]deeptools.NeedApproval{
				"counter": func(context.Context, *deeptools.ApprovalInfo) bool { return true },
			},
			ToolPolicyGates: map[string]deeptools.ToolPolicyGate{
				"counter": {
					Policy: func(context.Context, *deeptools.ApprovalInfo) (deeptools.ToolCallDecision, error) {
						return deeptools.ToolCallDecision{Action: deeptools.ToolCallAllow}, nil
					},
					DenyFormatter: func(ctx context.Context, info *deeptools.ApprovalInfo, decision deeptools.ToolCallDecision) (string, error) {
						return "denied", nil
					},
				},
			},
		},
	}

	_, err := collectAllTools(ctx, middleware.NewMiddlewareChain(), config)
	if err == nil || !strings.Contains(err.Error(), "both NeedApproveTools and ToolPolicyGates") {
		t.Fatalf("collectAllTools() error = %v, want conflict", err)
	}
}

func TestCollectAllTools_ToolPolicyGateRequiresFormatter(t *testing.T) {
	ctx := context.Background()
	config := &Config{
		Tools: []tool.BaseTool{&fakeToolCounter{}},
		HITLConfig: &HITLConfig{
			ToolPolicyGates: map[string]deeptools.ToolPolicyGate{
				"counter": {
					Policy: func(context.Context, *deeptools.ApprovalInfo) (deeptools.ToolCallDecision, error) {
						return deeptools.ToolCallDecision{Action: deeptools.ToolCallAllow}, nil
					},
				},
			},
		},
	}

	_, err := collectAllTools(ctx, middleware.NewMiddlewareChain(), config)
	if err == nil || !strings.Contains(err.Error(), "requires DenyFormatter") {
		t.Fatalf("collectAllTools() error = %v, want missing formatter", err)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestBuildCreateMiddlewares_WithFilesystemConfig(t *testing.T) {
	config := buildCreateConfig(
		WithModel(mock_model.NewMockToolCallingChatModel(gomock.NewController(t))),
		WithContextManager(contextmanager.New()),
		WithFilesystem(),
		WithFilesystemConfig(&FilesystemConfig{
			WorkDir:               "/sandbox",
			DisableUploadDownload: false,
			DisableApplyPatch:     true,
			CommandTimeout:        9 * time.Second,
		}),
		WithWorkDir("/explicit"),
		WithDisableUploadDownload(),
		WithBackend(newTestApplyPatchBackend(t)),
	)

	middlewares, err := buildCreateMiddlewares(config, config.Backend)
	if err != nil {
		t.Fatalf("buildCreateMiddlewares() error = %v", err)
	}

	var filesystemMW *filesystem.FilesystemMiddleware
	for _, mw := range middlewares {
		if fs, ok := mw.(*filesystem.FilesystemMiddleware); ok {
			filesystemMW = fs
			break
		}
	}
	if filesystemMW == nil {
		t.Fatalf("expected filesystem middleware to be present")
	}
	if filesystemMW.WorkDir() != "/explicit" {
		t.Fatalf("unexpected filesystem workdir: %s", filesystemMW.WorkDir())
	}
	if filesystemMW.CommandTimeout() != 9*time.Second {
		t.Fatalf("unexpected command timeout: %s", filesystemMW.CommandTimeout())
	}

	tools, err := filesystemMW.Tools(context.Background())
	if err != nil {
		t.Fatalf("filesystem.Tools() error = %v", err)
	}
	for _, tl := range tools {
		info, _ := tl.Info(context.Background())
		if info.Name == "upload_files" || info.Name == "download_files" {
			t.Fatalf("expected upload/download tools to be disabled")
		}
		if info.Name == constant.ToolApplyPatch {
			t.Fatalf("expected apply_patch tool to be disabled")
		}
	}
	if !containsString(collectToolNames(t, context.Background(), tools), constant.ToolEditFile) {
		t.Fatalf("expected edit_file fallback when apply_patch is disabled")
	}
}

func TestBuildCreateMiddlewares_WiresSubAgentContextInjector(t *testing.T) {
	config := buildCreateConfig(
		WithModel(mock_model.NewMockToolCallingChatModel(gomock.NewController(t))),
		WithContextManager(contextmanager.New()),
		WithBackend(newTestBackend(t)),
		WithSubAgents(&subagent.SubAgent{Name: "test_sub"}),
		WithSubAgentContextInjector(&testSubAgentContextInjector{}),
	)

	middlewares, err := buildCreateMiddlewares(config, config.Backend)
	if err != nil {
		t.Fatalf("buildCreateMiddlewares() error = %v", err)
	}

	var subAgentMW *subagent.SubAgentMiddleware
	for _, mw := range middlewares {
		if sub, ok := mw.(*subagent.SubAgentMiddleware); ok {
			subAgentMW = sub
			break
		}
	}
	if subAgentMW == nil {
		t.Fatalf("expected subagent middleware to be present")
	}
	if !subAgentMW.HasAgent("test_sub") {
		t.Fatalf("expected configured subagent to be registered")
	}
	if subAgentMW.HasAgent(constant.ExecutorName) {
		t.Fatalf("expected builtin executor subagent not to be auto-registered")
	}
	if subAgentMWContext := reflect.ValueOf(subAgentMW).Elem().FieldByName("contextInjector"); subAgentMWContext.IsNil() {
		t.Fatalf("expected context injector to be wired")
	}
}

func TestBuildCreateMiddlewares_WiresSubAgentTaskStreaming(t *testing.T) {
	ctx := context.Background()
	config := buildCreateConfig(
		WithModel(mock_model.NewMockToolCallingChatModel(gomock.NewController(t))),
		WithContextManager(contextmanager.New()),
		WithBackend(newTestBackend(t)),
		WithSubAgents(&subagent.SubAgent{Name: "test_sub"}),
		WithSubAgentTaskStreaming(),
	)

	middlewares, err := buildCreateMiddlewares(config, config.Backend)
	if err != nil {
		t.Fatalf("buildCreateMiddlewares() error = %v", err)
	}

	var subAgentMW *subagent.SubAgentMiddleware
	for _, mw := range middlewares {
		if sub, ok := mw.(*subagent.SubAgentMiddleware); ok {
			subAgentMW = sub
			break
		}
	}
	if subAgentMW == nil {
		t.Fatalf("expected subagent middleware to be present")
	}

	tools, err := subAgentMW.Tools(ctx)
	if err != nil {
		t.Fatalf("SubAgentMiddleware.Tools() error = %v", err)
	}
	for _, tl := range tools {
		info, err := tl.Info(ctx)
		if err != nil {
			t.Fatalf("tool.Info() error = %v", err)
		}
		if info.Name == constant.ToolTask {
			if _, ok := tl.(tool.StreamableTool); !ok {
				t.Fatalf("expected task tool to be streamable")
			}
			return
		}
	}
	t.Fatalf("expected task tool to be present")
}

func TestBuildCreateMiddlewares_WiresSubAgentSkillFactory(t *testing.T) {
	config := buildCreateConfig(
		WithModel(mock_model.NewMockToolCallingChatModel(gomock.NewController(t))),
		WithContextManager(contextmanager.New()),
		WithBackend(newTestBackend(t)),
		WithSkillLoader(&testSkillLoader{}),
		WithSubAgents(&subagent.SubAgent{Name: "skill_sub", EnableSkill: true}),
	)

	middlewares, err := buildCreateMiddlewares(config, config.Backend)
	if err != nil {
		t.Fatalf("buildCreateMiddlewares() error = %v", err)
	}

	var subAgentMW *subagent.SubAgentMiddleware
	for _, mw := range middlewares {
		if sub, ok := mw.(*subagent.SubAgentMiddleware); ok {
			subAgentMW = sub
			break
		}
	}
	if subAgentMW == nil {
		t.Fatalf("expected subagent middleware to be present")
	}

	if skillFactory := reflect.ValueOf(subAgentMW).Elem().FieldByName("subAgentSkillMiddlewareFactory"); skillFactory.IsNil() {
		t.Fatalf("expected skill middleware factory to be wired")
	}
}

func TestBuildCreateMiddlewares_AppliesSkillMaskToSkillsDirs(t *testing.T) {
	ctx := context.Background()
	backend := newTestBackend(t)
	_, _ = backend.Write(ctx, "/skills/code-search/SKILL.md", `---
name: code-search
description: search codebase
---
# Code Search
`)
	_, _ = backend.Write(ctx, "/skills/internal-debug/SKILL.md", `---
name: internal-debug
description: internal only
---
# Internal
`)

	config := buildCreateConfig(
		WithModel(mock_model.NewMockToolCallingChatModel(gomock.NewController(t))),
		WithContextManager(contextmanager.New()),
		WithBackend(backend),
		WithSkillsDir("/skills"),
		WithSkillMask(func(ctx context.Context, metadata *skillmw.SkillMetadata) bool {
			return metadata.Name != "internal-debug"
		}),
	)

	middlewares, err := buildCreateMiddlewares(config, config.Backend)
	if err != nil {
		t.Fatalf("buildCreateMiddlewares() error = %v", err)
	}

	skillMiddleware := findSkillMiddleware(t, middlewares)
	if err := skillMiddleware.BeforeAgent(ctx); err != nil {
		t.Fatalf("BeforeAgent() error = %v", err)
	}

	initialContext, err := skillMiddleware.BuildInitialContext(ctx)
	if err != nil {
		t.Fatalf("BuildInitialContext() error = %v", err)
	}
	if len(initialContext) != 1 {
		t.Fatalf("len(initialContext) = %d, want 1", len(initialContext))
	}
	content := initialContext[0].Content
	if !strings.Contains(content, "code-search") {
		t.Fatalf("expected filtered skills prompt to contain code-search, got %q", content)
	}
	if strings.Contains(content, "internal-debug") {
		t.Fatalf("expected filtered skills prompt to hide internal-debug, got %q", content)
	}
}

func TestBuildCreateMiddlewares_DoesNotApplySkillMaskToCustomLoader(t *testing.T) {
	ctx := context.Background()
	config := buildCreateConfig(
		WithModel(mock_model.NewMockToolCallingChatModel(gomock.NewController(t))),
		WithContextManager(contextmanager.New()),
		WithBackend(newTestBackend(t)),
		WithSkillLoader(&testSkillLoader{}),
		WithSkillMask(func(ctx context.Context, metadata *skillmw.SkillMetadata) bool {
			return false
		}),
	)

	middlewares, err := buildCreateMiddlewares(config, config.Backend)
	if err != nil {
		t.Fatalf("buildCreateMiddlewares() error = %v", err)
	}

	skillMiddleware := findSkillMiddleware(t, middlewares)
	if err := skillMiddleware.BeforeAgent(ctx); err != nil {
		t.Fatalf("BeforeAgent() error = %v", err)
	}

	initialContext, err := skillMiddleware.BuildInitialContext(ctx)
	if err != nil {
		t.Fatalf("BuildInitialContext() error = %v", err)
	}
	if len(initialContext) != 1 {
		t.Fatalf("len(initialContext) = %d, want 1", len(initialContext))
	}
	if !strings.Contains(initialContext[0].Content, "code_search") {
		t.Fatalf("expected custom loader skills to remain visible, got %q", initialContext[0].Content)
	}
}
