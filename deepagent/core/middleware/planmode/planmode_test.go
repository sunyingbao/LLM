package planmode

import (
	"context"
	"encoding/json"
	"testing"

	"eino-cli/deepagent/core"
	"eino-cli/deepagent/core/checkpointer"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"eino-cli/deepagent/core/middleware/contextmanager"
	"eino-cli/deepagent/mock/mock_model"
)

func TestMiddlewareInjectsPlanModePrompt(t *testing.T) {
	ctx := context.Background()
	m := New(nil)

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, schema.System, msgs[0].Role)
	assert.Contains(t, msgs[0].Content, "Plan Mode")
	assert.Contains(t, msgs[0].Content, "Phase 1: ground in the environment")
	assert.Contains(t, msgs[0].Content, "Explore first and ask second")
	assert.Contains(t, msgs[0].Content, "request_user_input")
	assert.Contains(t, msgs[0].Content, "<proposed_plan>")
}

func TestMiddlewareCanDisableAskUserTool(t *testing.T) {
	ctx := context.Background()
	m := New(&Config{})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	assert.Empty(t, tools)

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.NotContains(t, msgs[0].Content, "request_user_input")
	assert.Contains(t, msgs[0].Content, "<proposed_plan>")
}

func TestRequestUserInputToolInfo(t *testing.T) {
	ctx := context.Background()
	m := New(nil)

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	require.Len(t, tools, 1)

	info, err := tools[0].Info(ctx)
	require.NoError(t, err)
	assert.Equal(t, ToolRequestUserInput, info.Name)
	assert.Contains(t, info.Desc, "structured user input")
}

func TestRequestUserInputInterruptsWithStructuredQuestions(t *testing.T) {
	ctx := context.Background()
	askTool := requestUserInputTool(t)

	_, err := askTool.InvokableRun(ctx, `{"questions":[{"id":"scope","header":"Scope","question":"Which scope should the plan target?","options":[{"label":"SDK only (Recommended)","description":"Limit the change to reusable SDK middleware."},{"label":"SDK + cmd","description":"Also wire the reference CLI."}]}]}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interrupt signal")
	assert.Contains(t, err.Error(), "Which scope should the plan target?")
}

func TestRequestUserInputResumeReturnsStructuredAnswer(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	var callID = "call_request_user_input"
	var resumedAnswer RequestUserInputResponse
	cm.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			for _, message := range messages {
				if message != nil && message.Role == schema.Tool {
					require.NoError(t, json.Unmarshal([]byte(message.Content), &resumedAnswer))
					return schema.AssistantMessage("answer received", nil), nil
				}
			}
			return schema.AssistantMessage("ask",
				[]schema.ToolCall{{
					ID: callID,
					Function: schema.FunctionCall{
						Name:      ToolRequestUserInput,
						Arguments: `{"questions":[{"id":"scope","header":"Scope","question":"Which scope should the plan target?","options":[{"label":"SDK only (Recommended)","description":"Limit the change to reusable SDK middleware."}]}]}`,
					},
				}}), nil
		}).Times(2)

	agent, err := deepagents.New(ctx,
		deepagents.WithModel(cm),
		deepagents.WithMiddleware(New(nil)),
		deepagents.WithContextManager(contextmanager.New()),
		deepagents.WithCheckpointStore(checkpointer.NewInMemoryStore()),
	)
	require.NoError(t, err)

	const checkpointID = "planmode-request-user-input"
	_, err = agent.Run(ctx, []*schema.Message{schema.UserMessage("plan")}, deepagents.WithCheckpointID(checkpointID))
	require.Error(t, err)
	info, ok := compose.ExtractInterruptInfo(err)
	require.True(t, ok)
	require.Len(t, info.InterruptContexts, 1)
	request, ok := info.InterruptContexts[0].Info.(*RequestUserInputInfo)
	require.Truef(t, ok, "interrupt info type = %T", info.InterruptContexts[0].Info)
	require.Len(t, request.Questions, 1)
	assert.Equal(t, "scope", request.Questions[0].ID)
	assert.Equal(t, "SDK only (Recommended)", request.Questions[0].Options[0].Label)

	response := &RequestUserInputResponse{
		Answers: map[string]RequestUserInputAnswer{
			"scope": {Answers: []string{"SDK only (Recommended)", "user_note: keep cmd out of v1"}},
		},
	}
	got, err := agent.Run(ctx, []*schema.Message{schema.UserMessage("resume")},
		deepagents.WithCheckpointID(checkpointID),
		deepagents.WithResumeData(map[string]any{info.InterruptContexts[0].ID: response}),
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, schema.Assistant, got.Role)
	assert.Equal(t, "answer received", got.Content)
	assert.Equal(t, response.Answers["scope"].Answers, resumedAnswer.Answers["scope"].Answers)
}

func TestPlanModeDoesNotExposeUpdatePlan(t *testing.T) {
	ctx := context.Background()
	m := New(nil)

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	info, err := tools[0].Info(ctx)
	require.NoError(t, err)
	assert.Equal(t, ToolRequestUserInput, info.Name)
	assert.NotEqual(t, "update_plan", info.Name)
}

func requestUserInputTool(t *testing.T) tool.InvokableTool {
	t.Helper()
	m := New(nil)
	tools, err := m.Tools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1)
	askTool, ok := tools[0].(tool.InvokableTool)
	require.True(t, ok)
	return askTool
}
