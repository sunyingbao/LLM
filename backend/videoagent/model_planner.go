package videoagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ModelPlanner uses one injected ChatModel for planning, without depending on Mega workflows.
type ModelPlanner struct {
	requirementModel model.BaseChatModel
	clipScriptModel  model.BaseChatModel
	resourceModel    model.BaseChatModel
}

// NewModelPlanner creates a Planner backed by an Eino chat model.
func NewModelPlanner(chatModel model.BaseChatModel) (*ModelPlanner, error) {
	return NewStageModelPlanner(chatModel, chatModel, chatModel)
}

// NewStageModelPlanner assigns the specialized requirement, clipscript, and resource-planning models.
func NewStageModelPlanner(requirementModel, clipScriptModel, resourceModel model.BaseChatModel) (*ModelPlanner, error) {
	if requirementModel == nil || clipScriptModel == nil || resourceModel == nil {
		return nil, fmt.Errorf("planner models are incomplete")
	}
	return &ModelPlanner{
		requirementModel: requirementModel,
		clipScriptModel:  clipScriptModel,
		resourceModel:    resourceModel,
	}, nil
}

// AnalyzeRequirement extracts a structured advertising brief from user input.
func (planner *ModelPlanner) AnalyzeRequirement(ctx context.Context, input RunInput) (Requirement, error) {
	content, err := planner.generateContent(ctx, planner.requirementModel, "分析商品与用户需求，输出可直接展示的 Markdown 需求分析。", input)
	if err != nil {
		return Requirement{}, err
	}
	return parseRequirement(content, input)
}

// CreateClipScript creates the ordered scenes used by later resource nodes.
func (planner *ModelPlanner) CreateClipScript(ctx context.Context, requirement Requirement, input RunInput) (ClipScript, error) {
	content, err := planner.generateContent(ctx, planner.clipScriptModel, "根据需求生成创意方案和分镜脚本。回答必须依次包含“1. 创意方案如下”和“2. 分镜脚本如下”两个 JSON 区段。", struct {
		Requirement Requirement `json:"requirement"`
		Input       RunInput    `json:"input"`
	}{requirement, input})
	if err != nil {
		return ClipScript{}, err
	}
	return parseClipScript(content)
}

// PlanCompetition creates reference-image jobs from the clipscript.
func (planner *ModelPlanner) PlanCompetition(ctx context.Context, script ClipScript, input RunInput) ([]ResourcePlan, error) {
	var output []ResourcePlan
	err := planner.generate(ctx, planner.resourceModel, "为分镜脚本规划竞品参考图任务，返回 JSON 数组；每项包含 id、scene_id、prompt、model、width、height，scene_id 必须来自输入分镜。不要输出 Markdown。", struct {
		Script ClipScript `json:"clipscript"`
		Input  RunInput   `json:"input"`
	}{script, input}, &output)
	return output, err
}

// PlanTTS creates one or more voice jobs from scene narration.
func (planner *ModelPlanner) PlanTTS(ctx context.Context, script ClipScript) ([]ResourcePlan, error) {
	var output []ResourcePlan
	err := planner.generate(ctx, planner.resourceModel, "为每个分镜规划配音任务，返回 JSON 数组；每项包含 id、scene_id、prompt、speaker、text、cpm，scene_id 必须来自输入分镜，prompt 描述音色和说话风格。不要输出 Markdown。", script, &output)
	return output, err
}

// PlanCharacterReferences creates character-reference image jobs.
func (planner *ModelPlanner) PlanCharacterReferences(ctx context.Context, script ClipScript, input RunInput) ([]ResourcePlan, error) {
	var output []ResourcePlan
	err := planner.generate(ctx, planner.resourceModel, "为分镜脚本规划人物参考图任务，返回 JSON 数组；每项包含 id、scene_id、prompt、model、fallback_model、width、height，scene_id 必须来自输入分镜。不要输出 Markdown。", struct {
		Script ClipScript `json:"clipscript"`
		Input  RunInput   `json:"input"`
	}{script, input}, &output)
	return output, err
}

func (planner *ModelPlanner) generate(ctx context.Context, chatModel model.BaseChatModel, instruction string, input any, output any) error {
	content, err := planner.generateContent(ctx, chatModel, instruction, input)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(extractJSON(content)), output); err != nil {
		return fmt.Errorf("decode planner response: %w", err)
	}
	return nil
}

func (planner *ModelPlanner) generateContent(ctx context.Context, chatModel model.BaseChatModel, instruction string, input any) (string, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	message, err := chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage(instruction),
		schema.UserMessage(string(payload)),
	})
	if err != nil {
		return "", err
	}
	if message == nil || strings.TrimSpace(message.Content) == "" {
		return "", fmt.Errorf("planner returned empty response")
	}
	return strings.TrimSpace(message.Content), nil
}

func extractJSON(content string) string {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	start := strings.IndexAny(content, "{[")
	if start < 0 {
		return content
	}
	end := strings.LastIndexAny(content, "}]")
	if end < start {
		return content
	}
	return content[start : end+1]
}

var _ Planner = (*ModelPlanner)(nil)
