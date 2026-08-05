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

func TestParseNestedClipScriptOutput(t *testing.T) {
	content := `<answer>
1. 创意方案如下
{"方案总览":{"创意标题":"轻盈通勤"}}
2. 分镜脚本如下{
  "scenes":[{
    "scene":"场景一：通勤出发",
    "shots":[
      {"shot":"鞋底回弹特写","duration":"3s","dialogue":"轻盈舒适"},
      {"shot":"女性穿鞋走过街角","duration":"2.5秒","dialogue":"通勤也能走得从容"}
    ],
    "background_music":"轻快节奏"
  }]
}
</answer>`
	result, err := parseClipScript(content)
	if err != nil {
		t.Fatalf("parseClipScript() error = %v", err)
	}
	if len(result.Scenes) != 2 {
		t.Fatalf("scenes = %#v", result.Scenes)
	}
	if result.Scenes[0].SemanticID != "semantic-1" || result.Scenes[0].Visual != "鞋底回弹特写" || result.Scenes[0].Voiceover != "轻盈舒适" || result.Scenes[0].DurationMS != 3000 {
		t.Fatalf("first scene = %#v", result.Scenes[0])
	}
	if result.Scenes[1].DurationMS != 2500 {
		t.Fatalf("second scene = %#v", result.Scenes[1])
	}
}

func TestParseMarkdownClipScriptOutput(t *testing.T) {
	content := `**【0-3秒：开场】**
* **画面：** 一位职场女性穿着高跟鞋，在地铁口揉着脚踝。
* **字幕/旁白：** “通勤脚累？想增高又怕磨脚？”

**(3-7秒) 舒适展示**
* **画面：** （快速切换）
  * 特写：手指按压鞋子软弹的鞋底。
  * 女主角换上黑色休闲鞋，轻松行走。
* **音效：** 轻快音乐。
* **字幕/旁白：** “软底厚底，久走不累！”`
	result, err := parseClipScript(content)
	if err != nil {
		t.Fatalf("parseClipScript() error = %v", err)
	}
	if len(result.Scenes) != 2 {
		t.Fatalf("scenes = %#v", result.Scenes)
	}
	if result.Scenes[0].DurationMS != 3000 || result.Scenes[0].Voiceover != "通勤脚累？想增高又怕磨脚？" {
		t.Fatalf("first scene = %#v", result.Scenes[0])
	}
	if result.Scenes[1].DurationMS != 4000 || result.Scenes[1].Visual != "特写：手指按压鞋子软弹的鞋底。；女主角换上黑色休闲鞋，轻松行走。" {
		t.Fatalf("second scene = %#v", result.Scenes[1])
	}
}

func TestParseMarkdownTableClipScriptOutput(t *testing.T) {
	content := `| 镜头序号 | 时长 | 画面内容 | 配音/字幕 | 音乐 |
| :-- | :-- | :-- | :-- | :-- |
| 1 | 0-3秒 | **【场景】** 地铁站，模特轻松行走。 | **字幕：** 舒适增高！<br>**配音：** 通勤路上，轻松一点？ | 轻快音乐 |
| 2 | 3-8秒 | **【特写】** 鞋底回弹。 | **配音：** 软底缓震，久走不累。 | 节奏加强 |`
	result, err := parseClipScript(content)
	if err != nil {
		t.Fatalf("parseClipScript() error = %v", err)
	}
	if len(result.Scenes) != 2 {
		t.Fatalf("scenes = %#v", result.Scenes)
	}
	if result.Scenes[0].DurationMS != 3000 || result.Scenes[0].Visual != "【场景】 地铁站，模特轻松行走。" || result.Scenes[0].Voiceover != "字幕： 舒适增高！；配音： 通勤路上，轻松一点？" {
		t.Fatalf("first scene = %#v", result.Scenes[0])
	}
	if result.Scenes[1].DurationMS != 5000 {
		t.Fatalf("second scene = %#v", result.Scenes[1])
	}
}

func TestParseMarkdownClipScriptKeepsInlineShotVisual(t *testing.T) {
	content := `**镜头1 (0-3秒)：** 鞋底回弹特写，展示厚底缓震。
* **字幕/旁白：** 软底厚底，久走不累。

**镜头2 (3-6秒)**
* **镜头：** 通勤女性轻松穿过街角。`
	result, err := parseClipScript(content)
	if err != nil {
		t.Fatalf("parseClipScript() error = %v", err)
	}
	if len(result.Scenes) != 2 {
		t.Fatalf("scenes = %#v", result.Scenes)
	}
	if result.Scenes[0].Visual != "鞋底回弹特写，展示厚底缓震。" || result.Scenes[0].Voiceover != "软底厚底，久走不累。" {
		t.Fatalf("first scene = %#v", result.Scenes[0])
	}
	if result.Scenes[1].Visual != "通勤女性轻松穿过街角。" {
		t.Fatalf("second scene = %#v", result.Scenes[1])
	}
}
