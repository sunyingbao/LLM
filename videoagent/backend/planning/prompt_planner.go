package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	requirementPromptKey = "aic.aic_tool.user_req_analysis"
	clipScriptPromptKey  = "jichuang.creative.dr_script_e2e"
	competitionPromptKey = "aic.jichuang_agent.infringing_items_detection_new_schema"
	ttsPromptKey         = "aic.aic_agent.aigc_prompttts_raw2"
	characterPromptKey   = "aic.aic_agent.aigc_planning"
)

type PromptStageConfig struct {
	Key           string            `json:"key"`
	Bindings      map[string]string `json:"bindings,omitempty"`
	Model         string            `json:"model,omitempty"`
	FallbackModel string            `json:"fallback_model,omitempty"`
	Width         int               `json:"width,omitempty"`
	Height        int               `json:"height,omitempty"`
}

type PromptPlannerConfig struct {
	Requirement PromptStageConfig `json:"requirement"`
	ClipScript  PromptStageConfig `json:"clipscript"`
	Competition PromptStageConfig `json:"competition"`
	TTS         PromptStageConfig `json:"tts"`
	Character   PromptStageConfig `json:"character"`
}

type PromptRuntimeConfig struct {
	Fornax  FornaxConfig        `json:"fornax"`
	Planner PromptPlannerConfig `json:"planner"`
}

type PromptPlanner struct {
	executor PromptExecutor
	config   PromptPlannerConfig
}

func NewPromptPlanner(executor PromptExecutor, config PromptPlannerConfig) (*PromptPlanner, error) {
	if executor == nil {
		return nil, fmt.Errorf("prompt executor is nil")
	}
	defaults := DefaultPromptPlannerConfig()
	config.Requirement = fillPromptStage(config.Requirement, defaults.Requirement)
	config.ClipScript = fillPromptStage(config.ClipScript, defaults.ClipScript)
	config.Competition = fillPromptStage(config.Competition, defaults.Competition)
	config.TTS = fillPromptStage(config.TTS, defaults.TTS)
	config.Character = fillPromptStage(config.Character, defaults.Character)
	return &PromptPlanner{executor: executor, config: config}, nil
}

func DefaultPromptPlannerConfig() PromptPlannerConfig {
	return PromptPlannerConfig{
		Requirement: PromptStageConfig{
			Key: requirementPromptKey,
			Bindings: map[string]string{
				"query":       "brief",
				"prod_name":   "product_name",
				"product_img": "product_images",
			},
		},
		ClipScript: PromptStageConfig{
			Key: clipScriptPromptKey,
			Bindings: map[string]string{
				"query":          "brief",
				"prod_name":      "product_name",
				"product_img":    "product_images",
				"requirement":    "requirement",
				"creative_brief": "requirement_markdown",
			},
		},
		Competition: PromptStageConfig{Key: competitionPromptKey, Model: "ad.genai.seedream_4_5", Width: 1440, Height: 2560},
		TTS:         PromptStageConfig{Key: ttsPromptKey},
		Character:   PromptStageConfig{Key: characterPromptKey, Model: "gemini-3-pro-image-preview", Width: 1024, Height: 1536},
	}
}

func (planner *PromptPlanner) AnalyzeRequirement(ctx context.Context, input RunInput) (Requirement, error) {
	request, err := boundPromptRequest(planner.config.Requirement, promptSources(input, Requirement{}, ClipScript{}))
	if err != nil {
		return Requirement{}, err
	}
	content, err := planner.executor.Execute(ctx, request)
	if err != nil {
		return Requirement{}, err
	}
	return parseRequirement(content, input)
}

func (planner *PromptPlanner) CreateClipScript(ctx context.Context, requirement Requirement, input RunInput) (ClipScript, error) {
	request, err := boundPromptRequest(planner.config.ClipScript, promptSources(input, requirement, ClipScript{}))
	if err != nil {
		return ClipScript{}, err
	}
	content, err := planner.executor.Execute(ctx, request)
	if err != nil {
		return ClipScript{}, err
	}
	return parseClipScript(content)
}

func (planner *PromptPlanner) PlanCompetition(ctx context.Context, script ClipScript, input RunInput) ([]ResourcePlan, error) {
	request := PromptRequest{
		Key: planner.config.Competition.Key,
		Variables: map[string]any{
			"prod_name": input.ProductName,
			"script":    competitionPromptInput(script, input),
		},
	}
	if len(input.ProductImageURLs) > 0 {
		request.ImageVariables = map[string][]string{"product_img": input.ProductImageURLs}
	}
	content, err := planner.executor.Execute(ctx, request)
	if err != nil {
		return nil, err
	}
	return parseCompetitionPlans(content, script, planner.config.Competition)
}

func (planner *PromptPlanner) PlanTTS(ctx context.Context, script ClipScript) ([]ResourcePlan, error) {
	payload, err := json.Marshal(map[string]any{"clip_script_v2": script})
	if err != nil {
		return nil, err
	}
	content, err := planner.executor.Execute(ctx, PromptRequest{Key: planner.config.TTS.Key, Text: string(payload)})
	if err != nil {
		return nil, err
	}
	return parseTTSPlans(content, script)
}

func (planner *PromptPlanner) PlanCharacterReferences(ctx context.Context, script ClipScript, _ RunInput) ([]ResourcePlan, error) {
	content, err := planner.executor.Execute(ctx, PromptRequest{
		Key: planner.config.Character.Key,
		Variables: map[string]any{
			"clip_inputs":   characterClipInputs(script),
			"human_caption": characterDescriptions(script),
		},
	})
	if err != nil {
		return nil, err
	}
	return parseCharacterPlans(content, planner.config.Character)
}

func fillPromptStage(value, fallback PromptStageConfig) PromptStageConfig {
	if value.Key == "" {
		value.Key = fallback.Key
	}
	if value.Bindings == nil {
		value.Bindings = fallback.Bindings
	}
	if value.Model == "" {
		value.Model = fallback.Model
	}
	if value.FallbackModel == "" {
		value.FallbackModel = fallback.FallbackModel
	}
	if value.Width == 0 {
		value.Width = fallback.Width
	}
	if value.Height == 0 {
		value.Height = fallback.Height
	}
	return value
}

func promptSources(input RunInput, requirement Requirement, script ClipScript) map[string]any {
	return map[string]any{
		"brief":                input.Brief,
		"product_name":         input.ProductName,
		"product_images":       input.ProductImageURLs,
		"requirement":          requirement,
		"requirement_markdown": requirement.Markdown,
		"clipscript":           script,
	}
}

func boundPromptRequest(stage PromptStageConfig, sources map[string]any) (PromptRequest, error) {
	request := PromptRequest{Key: stage.Key, Variables: make(map[string]any)}
	for variable, source := range stage.Bindings {
		value, ok := sources[source]
		if !ok {
			return PromptRequest{}, fmt.Errorf("prompt binding source is unknown: %s", source)
		}
		if images, ok := value.([]string); ok {
			if len(images) > 0 {
				if request.ImageVariables == nil {
					request.ImageVariables = make(map[string][]string)
				}
				request.ImageVariables[variable] = append([]string(nil), images...)
			}
			continue
		}
		request.Variables[variable] = value
	}
	return request, nil
}

func competitionPromptInput(script ClipScript, input RunInput) map[string]any {
	segments := make([]map[string]any, 0)
	segmentIndex := make(map[string]int)
	for _, scene := range script.Scenes {
		key := scene.SemanticID
		if key == "" {
			key = scene.ID
		}
		index, ok := segmentIndex[key]
		if !ok {
			index = len(segments)
			segmentIndex[key] = index
			segments = append(segments, map[string]any{
				"semantic_clip_id": index + 1,
				"picture_clips":    []map[string]any{},
			})
		}
		clips := segments[index]["picture_clips"].([]map[string]any)
		segments[index]["picture_clips"] = append(clips, map[string]any{
			"picture_clip_id": len(clips) + 1,
			"desc":            scene.Visual,
		})
	}
	return map[string]any{
		"clip_script": map[string]any{"semantic_clips": segments},
		"characters":  script.Characters,
		"product_info": map[string]any{
			"name": input.ProductName,
		},
	}
}

func characterClipInputs(script ClipScript) string {
	lines := make([]string, 0, len(script.Scenes))
	for index, scene := range script.Scenes {
		lines = append(lines, fmt.Sprintf("<分镜%d>\n- 画面描述: %s - 画面对应的口播文案：%s - 画面预估时长：%.1f秒", index+1, scene.Visual, scene.Voiceover, float64(normalizedDurationMS(scene.DurationMS))/1000))
	}
	return strings.Join(lines, "\n")
}

func characterDescriptions(script ClipScript) string {
	lines := make([]string, 0, len(script.Characters))
	for _, character := range script.Characters {
		lines = append(lines, fmt.Sprintf("<%s>：年龄：%s, 性别：%s, 形象描述：%s", character.ID, character.Age, character.Gender, character.Description))
	}
	return strings.Join(lines, "\n")
}

var _ Planner = (*PromptPlanner)(nil)
