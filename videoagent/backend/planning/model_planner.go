package planning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
	content, err := planner.generateContent(ctx, planner.clipScriptModel, `根据需求生成广告分镜脚本。输入中的 requirement 是参考资料，不能再次输出需求分析。
只返回一个 JSON 对象，不要 Markdown、解释或代码块：
{"title":"创意标题","scenes":[{"id":"scene-1","semantic_id":"semantic-1","duration_ms":3000,"visual":"画面描述","voiceover":"口播文案"}]}
至少生成三个 scenes；每个 scene 都必须包含 visual 和 voiceover。`, struct {
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
	return planner.generatePlans(ctx, "为分镜脚本规划竞品参考图任务，返回 JSON 数组；每项包含 id、scene_id、prompt、model、width、height，scene_id 必须来自输入分镜。不要输出 Markdown。", struct {
		Script ClipScript `json:"clipscript"`
		Input  RunInput   `json:"input"`
	}{script, input}, script)
}

// PlanTTS creates one or more voice jobs from scene narration.
func (planner *ModelPlanner) PlanTTS(ctx context.Context, script ClipScript) ([]ResourcePlan, error) {
	return planner.generatePlans(ctx, "为每个分镜规划配音任务，返回 JSON 数组；每项包含 id、scene_id、prompt、speaker、text、cpm，scene_id 必须来自输入分镜，prompt 描述音色和说话风格。不要输出 Markdown。", script, script)
}

// PlanCharacterReferences creates character-reference image jobs.
func (planner *ModelPlanner) PlanCharacterReferences(ctx context.Context, script ClipScript, input RunInput) ([]ResourcePlan, error) {
	return planner.generatePlans(ctx, "为分镜脚本规划人物参考图任务，返回 JSON 数组；每项包含 id、scene_id、prompt、model、fallback_model、width、height，scene_id 必须来自输入分镜。不要输出 Markdown。", struct {
		Script ClipScript `json:"clipscript"`
		Input  RunInput   `json:"input"`
	}{script, input}, script)
}

func (planner *ModelPlanner) generatePlans(ctx context.Context, instruction string, input any, script ClipScript) ([]ResourcePlan, error) {
	content, err := planner.generateContent(ctx, planner.resourceModel, instruction, input)
	if err != nil {
		return nil, err
	}
	return decodeResourcePlans(content, script)
}

func decodeResourcePlans(content string, script ClipScript) ([]ResourcePlan, error) {
	var rawPlans []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(extractJSON(content)), &rawPlans); err != nil {
		return nil, fmt.Errorf("decode planner response: %w", err)
	}
	plans := make([]ResourcePlan, 0, len(rawPlans))
	for _, rawPlan := range rawPlans {
		for _, key := range []string{"id", "scene_id"} {
			value, err := resourcePlanString(rawPlan[key])
			if err != nil {
				return nil, fmt.Errorf("decode planner response %s: %w", key, err)
			}
			if value != "" {
				rawPlan[key], _ = json.Marshal(value)
			}
		}
		data, err := json.Marshal(rawPlan)
		if err != nil {
			return nil, err
		}
		plan := ResourcePlan{}
		if err := json.Unmarshal(data, &plan); err != nil {
			return nil, fmt.Errorf("decode planner response: %w", err)
		}
		plan.SceneID = resolveSceneID(plan.SceneID, script)
		plans = append(plans, plan)
	}
	return plans, nil
}

func resourcePlanString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	value := ""
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	number := json.Number("")
	if err := decoder.Decode(&number); err != nil {
		return "", err
	}
	return number.String(), nil
}

func resolveSceneID(sceneID string, script ClipScript) string {
	index, err := strconv.Atoi(sceneID)
	if err != nil || index < 1 || index > len(script.Scenes) {
		return sceneID
	}
	return script.Scenes[index-1].ID
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
