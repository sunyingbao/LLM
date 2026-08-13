package deepagents

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/contextmanager"
	"eino-cli/deepagent/core/middleware/subagent"
	"eino-cli/deepagent/core/types"
	"eino-cli/deepagent/mock/mock_model"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/mock/gomock"
)

type subAgentFactoryMarkerMiddleware struct {
	middleware.BaseMiddleware
	content string
}

func (m *subAgentFactoryMarkerMiddleware) Name() string {
	return "subagent_factory_marker"
}

func (m *subAgentFactoryMarkerMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	return []*schema.Message{schema.SystemMessage(m.content)}, nil
}

type subAgentNamedTool struct {
	name string
}

func (t *subAgentNamedTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: t.name + " tool"}, nil
}

func (t *subAgentNamedTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return t.name, nil
}

type subAgentSharedState struct {
	Value int
}

func (s *subAgentSharedState) MarshalRuntimeState() string {
	return fmt.Sprintf("%d", s.Value)
}

func (s *subAgentSharedState) UnmarshalRuntimeState(data string) error {
	_, err := fmt.Sscanf(data, "%d", &s.Value)
	return err
}

func TestCreateSubAgentFactory_AppendsDefaultMiddleware(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	parentConfig := buildCreateConfig(
		WithModel(cm),
		WithContextManager(contextmanager.New()),
		WithBackend(newTestBackend(t)),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
	)

	factory := createSubAgentFactory(parentConfig)
	marker := &subAgentFactoryMarkerMiddleware{content: "factory-marker"}

	runner, err := factory(ctx, cm, &subagent.SubAgent{
		Name:     "child",
		MaxSteps: 2,
	}, nil, []middleware.Middleware{marker})
	if err != nil {
		t.Fatalf("factory() error = %v", err)
	}
	defer runner.Close(ctx)

	adapter, ok := runner.(*subAgentRunnerAdapter)
	if !ok {
		t.Fatalf("unexpected runner type: %T", runner)
	}

	initialContext, err := adapter.agent.middlewareChain.BuildPrompts(ctx)
	if err != nil {
		t.Fatalf("BuildPrompts() error = %v", err)
	}

	var found bool
	for _, msg := range initialContext {
		if msg != nil && msg.Role == schema.System && strings.Contains(msg.Content, "factory-marker") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected default middleware initial context to be present")
	}
}

func TestCreateSubAgentFactory_InheritsFilesystemConfigWorkDir(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	parentWorkDir := t.TempDir()
	parentConfig := buildCreateConfig(
		WithModel(cm),
		WithContextManager(contextmanager.New()),
		WithBackend(newTestApplyPatchBackend(t)),
		WithFilesystemConfig(&FilesystemConfig{WorkDir: parentWorkDir, DisableApplyPatch: true}),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
	)

	factory := createSubAgentFactory(parentConfig)
	runner, err := factory(ctx, cm, &subagent.SubAgent{
		Name:             "child",
		EnableFilesystem: true,
		MaxSteps:         2,
	}, nil, nil)
	if err != nil {
		t.Fatalf("factory() error = %v", err)
	}
	defer runner.Close(ctx)

	adapter, ok := runner.(*subAgentRunnerAdapter)
	if !ok {
		t.Fatalf("unexpected runner type: %T", runner)
	}
	initialContext, err := adapter.agent.middlewareChain.BuildPrompts(ctx)
	if err != nil {
		t.Fatalf("BuildPrompts() error = %v", err)
	}
	if len(initialContext) == 0 {
		t.Fatalf("expected child filesystem initial context")
	}
	var combined strings.Builder
	for _, msg := range initialContext {
		if msg != nil {
			combined.WriteString(msg.Content)
		}
	}
	if !strings.Contains(combined.String(), parentWorkDir) {
		t.Fatalf("child filesystem context does not contain parent workdir %q:\n%s", parentWorkDir, combined.String())
	}
	if strings.Contains(combined.String(), constant.ToolApplyPatch) {
		t.Fatalf("child filesystem context exposed disabled apply_patch:\n%s", combined.String())
	}
	if !strings.Contains(combined.String(), constant.ToolEditFile) {
		t.Fatalf("child filesystem context missing edit_file fallback:\n%s", combined.String())
	}
}

func TestCreateSubAgentFactory_DoesNotInheritParentToolMask(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).DoAndReturn(
		func(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
			var foundCounter bool
			for _, info := range infos {
				if info != nil && info.Name == "counter" {
					foundCounter = true
				}
			}
			if !foundCounter {
				t.Fatalf("expected subagent tools to ignore parent ToolMask, got %+v", infos)
			}
			return cm, nil
		},
	).Times(1)

	parentConfig := buildCreateConfig(
		WithModel(cm),
		WithContextManager(contextmanager.New()),
		WithBackend(newTestBackend(t)),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithToolMask(func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != "counter"
		}),
	)

	factory := createSubAgentFactory(parentConfig)
	runner, err := factory(ctx, cm, &subagent.SubAgent{
		Name:     "child",
		Tools:    []tool.BaseTool{&fakeToolCounter{}},
		MaxSteps: 2,
	}, nil, nil)
	if err != nil {
		t.Fatalf("factory() error = %v", err)
	}
	defer runner.Close(ctx)
}

func TestCreateSubAgentFactory_AppliesSubAgentToolMaskOnlyToSubAgent(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).DoAndReturn(
		func(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
			var names []string
			for _, info := range infos {
				if info != nil {
					names = append(names, info.Name)
				}
			}
			if len(names) != 1 || names[0] != "allowed_tool" {
				t.Fatalf("expected only subagent-local allowed_tool to remain, got %+v", names)
			}
			return cm, nil
		},
	).Times(1)

	parentConfig := buildCreateConfig(
		WithModel(cm),
		WithContextManager(contextmanager.New()),
		WithBackend(newTestBackend(t)),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
	)

	factory := createSubAgentFactory(parentConfig)
	runner, err := factory(ctx, cm, &subagent.SubAgent{
		Name: "child",
		Tools: []tool.BaseTool{
			&fakeToolCounter{},
			&subAgentNamedTool{name: "allowed_tool"},
		},
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != "counter"
		},
		MaxSteps: 2,
	}, nil, nil)
	if err != nil {
		t.Fatalf("factory() error = %v", err)
	}
	defer runner.Close(ctx)
}

func TestCreateSubAgentFactory_RegistersSharedCustomStateAsRuntimeOnly(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	parentState := &subAgentSharedState{Value: 1}
	parentConfig := buildCreateConfig(
		WithModel(cm),
		WithContextManager(contextmanager.New()),
		WithBackend(newTestBackend(t)),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithCustomGraphState(map[string]types.RunTimeStateful{
			"shared": parentState,
		}),
		WithSubAgentSharedCustomState("shared"),
	)

	factory := createSubAgentFactory(parentConfig)
	runner, err := factory(ctx, cm, &subagent.SubAgent{
		Name:     "child",
		MaxSteps: 2,
	}, nil, nil)
	if err != nil {
		t.Fatalf("factory() error = %v", err)
	}
	defer runner.Close(ctx)

	adapter, ok := runner.(*subAgentRunnerAdapter)
	if !ok {
		t.Fatalf("unexpected runner type: %T", runner)
	}
	childState := adapter.agent.GraphState().GetStateful("shared")
	if childState != parentState {
		t.Fatalf("expected child to reference parent shared state, got %p want %p", childState, parentState)
	}

	parentState.Value = 7
	if err := adapter.agent.GraphState().Save(ctx, "child_cp"); err != nil {
		t.Fatalf("child GraphState.Save() error = %v", err)
	}
	parentState.Value = 99
	if err := adapter.agent.GraphState().Resume(ctx, "child_cp"); err != nil {
		t.Fatalf("child GraphState.Resume() error = %v", err)
	}
	if parentState.Value != 99 {
		t.Fatalf("runtime-only shared state should not be restored by child checkpoint, got %d", parentState.Value)
	}

	parentState.Value = 11
	parentGraphState, err := buildAgentState(ctx, middleware.NewMiddlewareChain(), parentConfig.CustomGraphState, parentConfig.CheckpointStore)
	if err != nil {
		t.Fatalf("buildAgentState() error = %v", err)
	}
	if err := parentGraphState.Save(ctx, "parent_cp"); err != nil {
		t.Fatalf("parent GraphState.Save() error = %v", err)
	}
	parentState.Value = 0
	if err := parentGraphState.Resume(ctx, "parent_cp"); err != nil {
		t.Fatalf("parent GraphState.Resume() error = %v", err)
	}
	if parentState.Value != 11 {
		t.Fatalf("parent checkpoint should restore shared state, got %d", parentState.Value)
	}
}
