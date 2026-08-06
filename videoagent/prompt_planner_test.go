package videoagent

import (
	"context"
	"fmt"
	"testing"
)

type fakePromptExecutor struct {
	responses map[string]string
	requests  []PromptRequest
}

func (executor *fakePromptExecutor) Execute(_ context.Context, request PromptRequest) (string, error) {
	executor.requests = append(executor.requests, request)
	response, ok := executor.responses[request.Key]
	if !ok {
		return "", fmt.Errorf("unexpected prompt: %s", request.Key)
	}
	return response, nil
}

func TestPromptPlannerUsesManagedPromptsAndBuildsResourcePlans(t *testing.T) {
	executor := &fakePromptExecutor{responses: map[string]string{
		requirementPromptKey: "<format_json># 需求分析\n\n目标人群：通勤女性</format_json>",
		clipScriptPromptKey: `<answer>
1. 创意方案如下
{"方案总览":{"创意标题":"轻盈通勤"}}
2. 分镜脚本如下
{"适用人物形象":[{"Id":"woman-1","Age":"25-35","Gender":"女","Description":"都市通勤女性"}],"语义分段":[{"语义分段asr":"轻盈舒适","画面描述":["鞋底回弹特写"],"画面类型":["产品特写"],"出现的人物形象":["woman-1"],"画面描述asr":["轻盈舒适"],"tts配音":["woman-1"],"表现手法":"快节奏"}]}
</answer>`,
		competitionPromptKey: `{"infringing_items_dict":[{"infringing_items_name":"跑道","infringing_items_description":"红色运动跑道，真实摄影"}],"script":[{"segment_index":1,"shots":[{"shot_index":1,"shot_description":"鞋子落在[跑道]上"}]}]}`,
		ttsPromptKey:         `[{"character_id":"woman-1","caption":"自然有活力的年轻女声","cpm":[260],"voice_id":"voice-1"}]`,
		characterPromptKey:   `{"character_prompt":[{"character_id":"woman-1","character_prompt_rewrite":"自然光下的都市女性，真实皮肤质感"}]}`,
	}}
	planner, err := NewPromptPlanner(executor, PromptPlannerConfig{})
	if err != nil {
		t.Fatalf("NewPromptPlanner() error = %v", err)
	}
	input := RunInput{ProductName: "厚底女鞋", ProductImageURLs: []string{"https://example.com/shoe.png"}, Brief: "制作通勤短视频"}
	requirement, err := planner.AnalyzeRequirement(context.Background(), input)
	if err != nil {
		t.Fatalf("AnalyzeRequirement() error = %v", err)
	}
	script, err := planner.CreateClipScript(context.Background(), requirement, input)
	if err != nil {
		t.Fatalf("CreateClipScript() error = %v", err)
	}
	competition, err := planner.PlanCompetition(context.Background(), script, input)
	if err != nil {
		t.Fatalf("PlanCompetition() error = %v", err)
	}
	voices, err := planner.PlanTTS(context.Background(), script)
	if err != nil {
		t.Fatalf("PlanTTS() error = %v", err)
	}
	characters, err := planner.PlanCharacterReferences(context.Background(), script, input)
	if err != nil {
		t.Fatalf("PlanCharacterReferences() error = %v", err)
	}

	if requirement.Markdown == "" || len(script.Scenes) != 1 {
		t.Fatalf("requirement = %#v, script = %#v", requirement, script)
	}
	if len(competition) != 1 || competition[0].SceneID != "scene-1-1" || competition[0].Model != "ad.genai.seedream_4_5" {
		t.Fatalf("competition plans = %#v", competition)
	}
	if len(voices) != 1 || voices[0].Text != "轻盈舒适" || voices[0].CPM != 260 || voices[0].Speaker != "voice-1" {
		t.Fatalf("tts plans = %#v", voices)
	}
	if len(characters) != 1 || characters[0].ID != "woman-1" || characters[0].Model != "gemini-3-pro-image-preview" {
		t.Fatalf("character plans = %#v", characters)
	}
	if len(executor.requests) != 5 {
		t.Fatalf("prompt requests = %#v", executor.requests)
	}
	if got := executor.requests[0].ImageVariables["product_img"]; len(got) != 1 || got[0] != input.ProductImageURLs[0] {
		t.Fatalf("requirement image variable = %#v", got)
	}
	if executor.requests[3].Text == "" {
		t.Fatal("tts prompt must carry clip_script_v2 as user text")
	}
	if executor.requests[4].Variables["clip_inputs"] == "" || executor.requests[4].Variables["human_caption"] == "" {
		t.Fatalf("character variables = %#v", executor.requests[4].Variables)
	}
}

func TestPromptPlannerRejectsUnknownBindingSource(t *testing.T) {
	planner, err := NewPromptPlanner(&fakePromptExecutor{}, PromptPlannerConfig{
		Requirement: PromptStageConfig{Bindings: map[string]string{"query": "missing"}},
	})
	if err != nil {
		t.Fatalf("NewPromptPlanner() error = %v", err)
	}
	_, err = planner.AnalyzeRequirement(context.Background(), RunInput{})
	if err == nil {
		t.Fatal("AnalyzeRequirement() error = nil")
	}
}

var _ PromptExecutor = (*fakePromptExecutor)(nil)
