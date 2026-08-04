package videoagent

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	creativePlanTitle = "1. 创意方案如下"
	clipScriptTitle   = "2. 分镜脚本如下"
)

func parseRequirement(content string, input RunInput) (Requirement, error) {
	content = extractTaggedContent(content, "format_json")
	if strings.TrimSpace(content) == "" {
		return Requirement{}, fmt.Errorf("requirement response is empty")
	}
	result := Requirement{Objective: input.Brief, Markdown: strings.TrimSpace(content)}
	var structured Requirement
	if json.Unmarshal([]byte(extractJSON(content)), &structured) == nil {
		if structured.Objective != "" {
			result.Objective = structured.Objective
		}
		result.Audience = structured.Audience
		result.Selling = structured.Selling
	}
	return result, nil
}

func parseClipScript(content string) (ClipScript, error) {
	content = extractTaggedContent(content, "answer")
	var direct ClipScript
	if json.Unmarshal([]byte(extractJSON(content)), &direct) == nil && len(direct.Scenes) > 0 {
		normalizeScenes(direct.Scenes)
		return direct, nil
	}

	creativePlan, err := jsonAfterTitle(content, creativePlanTitle)
	if err != nil {
		return ClipScript{}, err
	}
	clipScriptJSON, err := jsonAfterTitle(content, clipScriptTitle)
	if err != nil {
		return ClipScript{}, err
	}
	var raw drClipScript
	if err := json.Unmarshal([]byte(clipScriptJSON), &raw); err != nil {
		return ClipScript{}, fmt.Errorf("decode dr clipscript: %w", err)
	}
	result := ClipScript{Title: creativePlanTitle, CreativePlan: json.RawMessage(creativePlan)}
	for _, character := range raw.Characters {
		result.Characters = append(result.Characters, Character{
			ID: character.ID, Age: character.Age, Gender: character.Gender, Description: character.Description,
		})
	}
	for semanticIndex, semantic := range raw.Segments {
		for shotIndex, visual := range semantic.Visuals {
			voiceover := valueAt(semantic.Voiceovers, shotIndex)
			if voiceover == "" && shotIndex == 0 {
				voiceover = semantic.Voiceover
			}
			result.Scenes = append(result.Scenes, Scene{
				ID:           fmt.Sprintf("scene-%d-%d", semanticIndex+1, shotIndex+1),
				SemanticID:   fmt.Sprintf("semantic-%d", semanticIndex+1),
				Voiceover:    voiceover,
				Visual:       visual,
				SpeakerID:    valueAt(semantic.Speakers, shotIndex),
				CharacterIDs: splitIDs(valueAt(semantic.AppearCharacters, shotIndex)),
				PictureType:  valueAt(semantic.PictureTypes, shotIndex),
				Presentation: semantic.Presentation,
			})
		}
	}
	if len(result.Scenes) == 0 {
		return ClipScript{}, fmt.Errorf("clipscript has no scenes")
	}
	return result, nil
}

type drClipScript struct {
	Characters []struct {
		ID          string `json:"Id"`
		Age         string `json:"Age"`
		Gender      string `json:"Gender"`
		Description string `json:"Description"`
	} `json:"适用人物形象"`
	Segments []struct {
		Voiceover        string   `json:"语义分段asr"`
		Visuals          []string `json:"画面描述"`
		PictureTypes     []string `json:"画面类型"`
		AppearCharacters []string `json:"出现的人物形象"`
		Voiceovers       []string `json:"画面描述asr"`
		Speakers         []string `json:"tts配音"`
		Presentation     string   `json:"表现手法"`
	} `json:"语义分段"`
}

func normalizeScenes(scenes []Scene) {
	for index := range scenes {
		if scenes[index].ID == "" {
			scenes[index].ID = fmt.Sprintf("scene-%d", index+1)
		}
	}
}

func extractTaggedContent(content, tag string) string {
	startTag := "<" + tag + ">"
	endTag := "</" + tag + ">"
	start := strings.Index(content, startTag)
	if start < 0 {
		return strings.TrimSpace(content)
	}
	start += len(startTag)
	end := strings.Index(content[start:], endTag)
	if end < 0 {
		return strings.TrimSpace(content[start:])
	}
	return strings.TrimSpace(content[start : start+end])
}

func jsonAfterTitle(content, title string) (string, error) {
	index := strings.Index(content, title)
	if index < 0 {
		return "", fmt.Errorf("clipscript response is missing %q", title)
	}
	return firstJSONObject(content[index+len(title):])
}

func firstJSONObject(content string) (string, error) {
	start := strings.Index(content, "{")
	if start < 0 {
		return "", fmt.Errorf("json object start not found")
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(content); index++ {
		character := content[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(content[start : index+1]), nil
			}
		}
	}
	return "", fmt.Errorf("json object end not found")
}

func valueAt(values []string, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	return strings.TrimSpace(values[index])
}

func splitIDs(value string) []string {
	return strings.FieldsFunc(value, func(character rune) bool {
		switch character {
		case '、', ',', '，', '/', ' ':
			return true
		default:
			return false
		}
	})
}

type competitionPromptResponse struct {
	Items []struct {
		Name        string `json:"infringing_items_name"`
		Description string `json:"infringing_items_description"`
	} `json:"infringing_items_dict"`
	Script []struct {
		SegmentIndex int `json:"segment_index"`
		Shots        []struct {
			ShotIndex   int    `json:"shot_index"`
			Description string `json:"shot_description"`
		} `json:"shots"`
	} `json:"script"`
}

func parseCompetitionPlans(content string, script ClipScript, config PromptStageConfig) ([]ResourcePlan, error) {
	var response competitionPromptResponse
	if err := json.Unmarshal([]byte(extractJSON(content)), &response); err != nil {
		return nil, fmt.Errorf("decode competition plan: %w", err)
	}
	plans := make([]ResourcePlan, 0, len(response.Items))
	for index, item := range response.Items {
		if strings.TrimSpace(item.Description) == "" {
			continue
		}
		plans = append(plans, ResourcePlan{
			ID:            fmt.Sprintf("competition-%d", index+1),
			SceneID:       competitionItemScene(response, script, item.Name),
			Prompt:        strings.TrimSpace(item.Description),
			Model:         config.Model,
			FallbackModel: config.FallbackModel,
			Width:         config.Width,
			Height:        config.Height,
		})
	}
	return plans, nil
}

func competitionItemScene(response competitionPromptResponse, script ClipScript, itemName string) string {
	if itemName == "" {
		return ""
	}
	matched := make(map[string]struct{})
	marker := "[" + itemName + "]"
	for _, segment := range response.Script {
		for shotOffset, shot := range segment.Shots {
			if !strings.Contains(shot.Description, marker) {
				continue
			}
			shotIndex := shot.ShotIndex
			if shotIndex <= 0 {
				shotIndex = shotOffset + 1
			}
			if sceneID := sceneIDAt(script, segment.SegmentIndex, shotIndex); sceneID != "" {
				matched[sceneID] = struct{}{}
			}
		}
	}
	if len(matched) != 1 {
		return ""
	}
	for sceneID := range matched {
		return sceneID
	}
	return ""
}

func sceneIDAt(script ClipScript, segmentIndex, shotIndex int) string {
	if segmentIndex <= 0 || shotIndex <= 0 {
		return ""
	}
	groups := make([][]Scene, 0)
	groupIndex := make(map[string]int)
	for _, scene := range script.Scenes {
		key := scene.SemanticID
		if key == "" {
			key = scene.ID
		}
		index, ok := groupIndex[key]
		if !ok {
			index = len(groups)
			groupIndex[key] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], scene)
	}
	if segmentIndex > len(groups) || shotIndex > len(groups[segmentIndex-1]) {
		return ""
	}
	return groups[segmentIndex-1][shotIndex-1].ID
}

type promptTTSItem struct {
	CharacterID     string `json:"character_id"`
	Caption         string `json:"caption"`
	PreviewAudioURI string `json:"preview_audio_uri"`
	PreviewAudioURL string `json:"preview_audio_url"`
	ItemType        string `json:"item_type"`
	Name            string `json:"name"`
	CPM             []int  `json:"cpm"`
	VoiceID         string `json:"voice_id"`
}

func parseTTSPlans(content string, script ClipScript) ([]ResourcePlan, error) {
	var voices []promptTTSItem
	if err := json.Unmarshal([]byte(extractJSON(content)), &voices); err != nil {
		return nil, fmt.Errorf("decode tts plan: %w", err)
	}
	byCharacter := make(map[string]promptTTSItem, len(voices))
	for _, voice := range voices {
		if voice.CharacterID != "" {
			byCharacter[voice.CharacterID] = voice
		}
	}
	plans := make([]ResourcePlan, 0, len(script.Scenes))
	for index, scene := range script.Scenes {
		if strings.TrimSpace(scene.Voiceover) == "" {
			continue
		}
		voice, ok := byCharacter[scene.SpeakerID]
		if !ok && len(voices) == 1 {
			voice = voices[0]
		}
		cpm := defaultPromptTTSCPM
		if len(voice.CPM) > 0 && voice.CPM[0] > 0 {
			cpm = voice.CPM[0]
		}
		id := scene.ID
		if id == "" {
			id = fmt.Sprintf("scene-%d", index+1)
		}
		plans = append(plans, ResourcePlan{
			ID: id, SceneID: id, Prompt: voice.Caption,
			Speaker: firstNonEmpty(voice.VoiceID, scene.SpeakerID),
			Text:    scene.Voiceover, CPM: cpm,
		})
	}
	return plans, nil
}

func parseCharacterPlans(content string, config PromptStageConfig) ([]ResourcePlan, error) {
	var response struct {
		Characters []struct {
			ID     string `json:"character_id"`
			Prompt string `json:"character_prompt_rewrite"`
		} `json:"character_prompt"`
	}
	if err := json.Unmarshal([]byte(extractJSON(content)), &response); err != nil {
		return nil, fmt.Errorf("decode character plan: %w", err)
	}
	plans := make([]ResourcePlan, 0, len(response.Characters))
	for _, character := range response.Characters {
		if character.ID == "" || strings.TrimSpace(character.Prompt) == "" {
			continue
		}
		plans = append(plans, ResourcePlan{
			ID: character.ID, Prompt: strings.TrimSpace(character.Prompt),
			Model: config.Model, FallbackModel: config.FallbackModel,
			Width: config.Width, Height: config.Height,
		})
	}
	return plans, nil
}
