package plan

import (
	"context"
	"fmt"
	"strings"

	deeptools "eino-cli/deepagent/core/tools"

	"code.byted.org/lang/gg/choose"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
)

type PlanStepStatus string

const (
	PlanStepStatusPending    PlanStepStatus = "pending"
	PlanStepStatusInProgress PlanStepStatus = "in_progress"
	PlanStepStatusCompleted  PlanStepStatus = "completed"
)

type PlanStep struct {
	Step   string         `json:"step" jsonschema:"description=One-sentence progress step,required"`
	Status PlanStepStatus `json:"status" jsonschema:"description=One of: pending, in_progress, completed,required"`
}

type PlanUpdate struct {
	Explanation string     `json:"explanation,omitempty" jsonschema:"description=Optional rationale for creating or changing the plan"`
	Plan        []PlanStep `json:"plan" jsonschema:"description=The complete current progress checklist,required"`
}

type PlanUpdateCallback func(ctx context.Context, update PlanUpdate) error

type PlanMiddlewareConfig struct {
	// ToolMask controls visibility for update_plan and the matching prompt.
	ToolMask deeptools.Mask

	// OnPlanUpdate is called after update_plan receives a valid checklist snapshot.
	OnPlanUpdate PlanUpdateCallback

	// ToolUpdatePlanDesc overrides the default update_plan tool description.
	ToolUpdatePlanDesc string

	// PlanSystemPrompt overrides the default planning system prompt.
	PlanSystemPrompt string
}

type PlanMiddleware struct {
	middleware.BaseMiddleware
	toolMask           deeptools.Mask
	onPlanUpdate       PlanUpdateCallback
	toolUpdatePlanDesc string
	planSystemPrompt   string
}

func New(cfg *PlanMiddlewareConfig) *PlanMiddleware {
	if cfg == nil {
		cfg = &PlanMiddlewareConfig{}
	}
	return &PlanMiddleware{
		toolMask:           cfg.ToolMask,
		onPlanUpdate:       cfg.OnPlanUpdate,
		toolUpdatePlanDesc: choose.If(cfg.ToolUpdatePlanDesc == "", constant.ToolUpdatePlanDesc, cfg.ToolUpdatePlanDesc),
		planSystemPrompt:   choose.If(cfg.PlanSystemPrompt == "", constant.PlanSystemPrompt, cfg.PlanSystemPrompt),
	}
}

func (m *PlanMiddleware) Name() string {
	return constant.MiddlewarePlan
}

func (m *PlanMiddleware) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	tools := []tool.BaseTool{m.newUpdatePlanTool()}
	return deeptools.FilterToolsByMask(ctx, tools, m.toolMask, "PlanMiddleware::Tools"), nil
}

func (m *PlanMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	tools, err := m.Tools(ctx)
	if err != nil {
		return nil, err
	}
	if len(tools) == 0 {
		return nil, nil
	}
	return []*schema.Message{schema.SystemMessage(m.planSystemPrompt)}, nil
}

func (m *PlanMiddleware) newUpdatePlanTool() tool.BaseTool {
	t, _ := utils.InferTool(
		constant.ToolUpdatePlan,
		m.toolUpdatePlanDesc,
		func(ctx context.Context, input *PlanUpdate) (string, error) {
			if input == nil {
				return "[Error] update_plan failed: missing arguments", nil
			}
			update := PlanUpdate{
				Explanation: strings.TrimSpace(input.Explanation),
				Plan:        normalizePlanSteps(input.Plan),
			}
			if err := validatePlanUpdate(update); err != nil {
				return fmt.Sprintf("[Error] update_plan failed: %s", err), nil
			}
			if m.onPlanUpdate != nil {
				if err := m.onPlanUpdate(ctx, update); err != nil {
					return fmt.Sprintf("[Error] update_plan failed: callback error: %v", err), nil
				}
			}
			return "Plan updated", nil
		},
	)
	return t
}

func normalizePlanSteps(steps []PlanStep) []PlanStep {
	if steps == nil {
		return nil
	}
	normalized := make([]PlanStep, len(steps))
	for i, step := range steps {
		normalized[i] = PlanStep{
			Step:   strings.TrimSpace(step.Step),
			Status: step.Status,
		}
	}
	return normalized
}

func validatePlanUpdate(update PlanUpdate) error {
	if update.Plan == nil {
		return fmt.Errorf("plan is required")
	}
	inProgress := 0
	for i, step := range update.Plan {
		if step.Step == "" {
			return fmt.Errorf("plan[%d].step is empty", i)
		}
		switch step.Status {
		case PlanStepStatusPending, PlanStepStatusInProgress, PlanStepStatusCompleted:
		default:
			return fmt.Errorf("plan[%d].status must be pending, in_progress, or completed", i)
		}
		if step.Status == PlanStepStatusInProgress {
			inProgress++
		}
	}
	if inProgress > 1 {
		return fmt.Errorf("at most one step can be in_progress")
	}
	return nil
}
