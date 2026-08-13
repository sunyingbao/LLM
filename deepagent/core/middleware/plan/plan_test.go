package plan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"eino-cli/deepagent/core/constant"
)

func TestPlanMiddlewareUpdatePlanInvokesCallback(t *testing.T) {
	ctx := context.Background()
	var got PlanUpdate
	called := 0
	m := New(&PlanMiddlewareConfig{
		OnPlanUpdate: func(_ context.Context, update PlanUpdate) error {
			called++
			got = update
			return nil
		},
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	updateTool := findTool(t, tools, constant.ToolUpdatePlan)

	out := invokeTool(t, updateTool, `{"explanation":" initial plan ","plan":[{"step":" Inspect repo ","status":"completed"},{"step":"Implement v2","status":"in_progress"}]}`)

	assert.Equal(t, "Plan updated", out)
	assert.Equal(t, 1, called)
	assert.Equal(t, "initial plan", got.Explanation)
	require.Len(t, got.Plan, 2)
	assert.Equal(t, "Inspect repo", got.Plan[0].Step)
	assert.Equal(t, PlanStepStatusCompleted, got.Plan[0].Status)
	assert.Equal(t, "Implement v2", got.Plan[1].Step)
	assert.Equal(t, PlanStepStatusInProgress, got.Plan[1].Status)
}

func TestPlanMiddlewareRejectsInvalidChecklist(t *testing.T) {
	ctx := context.Background()
	called := false
	m := New(&PlanMiddlewareConfig{
		OnPlanUpdate: func(context.Context, PlanUpdate) error {
			called = true
			return nil
		},
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	updateTool := findTool(t, tools, constant.ToolUpdatePlan)
	out := invokeTool(t, updateTool, `{"plan":[{"step":"A","status":"in_progress"},{"step":"B","status":"in_progress"}]}`)

	assert.Contains(t, out, "[Error] update_plan failed")
	assert.Contains(t, out, "at most one step can be in_progress")
	assert.False(t, called)
}

func TestPlanMiddlewareRequiresPlanField(t *testing.T) {
	ctx := context.Background()
	m := New(nil)

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	updateTool := findTool(t, tools, constant.ToolUpdatePlan)
	out := invokeTool(t, updateTool, `{"explanation":"missing plan"}`)

	assert.Contains(t, out, "[Error] update_plan failed")
	assert.Contains(t, out, "plan is required")
}

func TestPlanMiddlewareAllowsEmptyPlanSnapshot(t *testing.T) {
	ctx := context.Background()
	called := false
	m := New(&PlanMiddlewareConfig{
		OnPlanUpdate: func(_ context.Context, update PlanUpdate) error {
			called = true
			assert.Empty(t, update.Plan)
			return nil
		},
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	updateTool := findTool(t, tools, constant.ToolUpdatePlan)
	out := invokeTool(t, updateTool, `{"plan":[]}`)

	assert.Equal(t, "Plan updated", out)
	assert.True(t, called)
}

func TestPlanMiddlewareReportsCallbackError(t *testing.T) {
	ctx := context.Background()
	m := New(&PlanMiddlewareConfig{
		OnPlanUpdate: func(context.Context, PlanUpdate) error {
			return errors.New("event bus unavailable")
		},
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	updateTool := findTool(t, tools, constant.ToolUpdatePlan)
	out := invokeTool(t, updateTool, `{"plan":[{"step":"A","status":"pending"}]}`)

	assert.Contains(t, out, "[Error] update_plan failed")
	assert.Contains(t, out, "event bus unavailable")
}

func TestPlanMiddlewareToolMaskFiltersToolAndPrompt(t *testing.T) {
	ctx := context.Background()
	m := New(&PlanMiddlewareConfig{
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != constant.ToolUpdatePlan
		},
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	assert.Empty(t, tools)

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	assert.Empty(t, msgs)
}

func TestPlanMiddlewarePromptMentionsOnlyUpdatePlan(t *testing.T) {
	ctx := context.Background()
	m := New(nil)

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)

	prompt := msgs[0].Content
	assert.Contains(t, prompt, "update_plan")
	assert.Contains(t, prompt, "pending, in_progress, or completed")
	assert.False(t, strings.Contains(prompt, "write_todos"))
	assert.False(t, strings.Contains(prompt, "read_todos"))
	assert.False(t, strings.Contains(prompt, "update_todo"))
}

func TestPlanMiddlewareConfigOverridesPromptAndToolDesc(t *testing.T) {
	ctx := context.Background()
	m := New(&PlanMiddlewareConfig{
		ToolUpdatePlanDesc: "Custom update_plan description for business agent.",
		PlanSystemPrompt:   "## Custom Planning\nUse the business-specific progress policy.",
	})

	tools, err := m.Tools(ctx)
	require.NoError(t, err)
	updateTool := findTool(t, tools, constant.ToolUpdatePlan)
	assert.Equal(t, "Custom update_plan description for business agent.", toolInfo(t, updateTool).Desc)

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "## Custom Planning\nUse the business-specific progress policy.", msgs[0].Content)
}
