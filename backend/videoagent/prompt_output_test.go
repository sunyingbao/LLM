package videoagent

import "testing"

func TestParseRequirementKeepsMarkdown(t *testing.T) {
	result, err := parseRequirement("<format_json># 需求分析\n\n**目标人群**：通勤女性</format_json>", RunInput{Brief: "制作鞋子广告"})
	if err != nil {
		t.Fatalf("parseRequirement() error = %v", err)
	}
	if result.Objective != "制作鞋子广告" || result.Markdown != "# 需求分析\n\n**目标人群**：通勤女性" {
		t.Fatalf("requirement = %#v", result)
	}
}

func TestParseRequirementRejectsEmptyResponse(t *testing.T) {
	if _, err := parseRequirement("<format_json> </format_json>", RunInput{Brief: "制作鞋子广告"}); err == nil {
		t.Fatal("parseRequirement() error = nil")
	}
}

func TestParseDrScriptE2EOutput(t *testing.T) {
	content := `<answer>
1. 创意方案如下
{"方案总览":{"创意标题":"轻盈通勤"}}
2. 分镜脚本如下
{
  "适用人物形象":[{"Id":"woman-1","Age":"25-35","Gender":"女","Description":"都市通勤女性"}],
  "语义分段":[{
    "语义分段asr":"轻盈舒适，通勤也能走得从容。",
    "画面描述":["鞋底回弹特写","女性穿鞋走过街角"],
    "画面类型":["产品特写","生活方式"],
    "出现的人物形象":["","woman-1"],
    "画面描述asr":["轻盈舒适","通勤也能走得从容"],
    "tts配音":["narration","woman-1"],
    "表现手法":"快节奏商品口播"
  }]
}
</answer>`
	result, err := parseClipScript(content)
	if err != nil {
		t.Fatalf("parseClipScript() error = %v", err)
	}
	if len(result.Characters) != 1 || result.Characters[0].ID != "woman-1" {
		t.Fatalf("characters = %#v", result.Characters)
	}
	if len(result.Scenes) != 2 {
		t.Fatalf("scenes = %#v", result.Scenes)
	}
	if result.Scenes[0].Voiceover != "轻盈舒适" || result.Scenes[0].SpeakerID != "narration" {
		t.Fatalf("first scene = %#v", result.Scenes[0])
	}
	if len(result.Scenes[1].CharacterIDs) != 1 || result.Scenes[1].CharacterIDs[0] != "woman-1" {
		t.Fatalf("second scene = %#v", result.Scenes[1])
	}
}
