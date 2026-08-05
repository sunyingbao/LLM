package videoagent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	creativePlanTitle = "1. 创意方案如下"
	clipScriptTitle   = "2. 分镜脚本如下"
)

var markdownSceneRange = regexp.MustCompile(`[（(【]\s*(\d+(?:\.\d+)?)\s*[-~至]\s*(\d+(?:\.\d+)?)\s*秒[^）)】]*[）)】]`)
var markdownDurationRange = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*[-~至]\s*(\d+(?:\.\d+)?)\s*秒`)

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
	if markdown, ok := parseMarkdownClipScript(content); ok {
		return markdown, nil
	}

	creativePlan, err := jsonAfterTitle(content, creativePlanTitle)
	if err != nil {
		return ClipScript{}, err
	}
	clipScriptJSON, err := jsonAfterTitle(content, clipScriptTitle)
	if err != nil {
		return ClipScript{}, err
	}
	result := ClipScript{Title: creativePlanTitle, CreativePlan: json.RawMessage(creativePlan)}
	if parseNestedClipScript(clipScriptJSON, &result) {
		return result, nil
	}

	var raw drClipScript
	if err := json.Unmarshal([]byte(clipScriptJSON), &raw); err != nil {
		return ClipScript{}, fmt.Errorf("decode dr clipscript: %w", err)
	}
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

type nestedClipScript struct {
	Scenes []struct {
		Shots []struct {
			Visual    string `json:"shot"`
			Duration  string `json:"duration"`
			Voiceover string `json:"dialogue"`
		} `json:"shots"`
	} `json:"scenes"`
}

func parseNestedClipScript(content string, result *ClipScript) bool {
	var raw nestedClipScript
	if json.Unmarshal([]byte(content), &raw) != nil || len(raw.Scenes) == 0 {
		return false
	}
	for sceneIndex, scene := range raw.Scenes {
		for shotIndex, shot := range scene.Shots {
			result.Scenes = append(result.Scenes, Scene{
				ID:         fmt.Sprintf("scene-%d-%d", sceneIndex+1, shotIndex+1),
				SemanticID: fmt.Sprintf("semantic-%d", sceneIndex+1),
				Voiceover:  strings.TrimSpace(shot.Voiceover),
				Visual:     strings.TrimSpace(shot.Visual),
				DurationMS: parseDurationMS(shot.Duration),
			})
		}
	}
	return len(result.Scenes) > 0
}

func parseDurationMS(value string) int {
	seconds := strings.TrimSpace(value)
	seconds = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(seconds, "秒"), "s"))
	parsed, err := strconv.ParseFloat(seconds, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return int(parsed * 1000)
}

func parseMarkdownClipScript(content string) (ClipScript, bool) {
	result := ClipScript{Title: "分镜脚本"}
	current := Scene{}
	visualOpen := false
	appendScene := func() {
		if current.ID == "" || (current.Visual == "" && current.Voiceover == "") {
			return
		}
		result.Scenes = append(result.Scenes, current)
	}

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if match := markdownSceneRange.FindStringSubmatch(line); len(match) == 3 {
			appendScene()
			start, _ := strconv.ParseFloat(match[1], 64)
			end, _ := strconv.ParseFloat(match[2], 64)
			sceneIndex := len(result.Scenes) + 1
			current = Scene{
				ID:         fmt.Sprintf("scene-%d", sceneIndex),
				SemanticID: fmt.Sprintf("semantic-%d", sceneIndex),
				DurationMS: int((end - start) * 1000),
			}
			visualOpen = false
			if rangeIndex := markdownSceneRange.FindStringIndex(line); rangeIndex != nil {
				current.Visual = markdownInlineValue(line[rangeIndex[1]:])
				visualOpen = current.Visual != ""
			}
			continue
		}
		if current.ID == "" {
			continue
		}

		if strings.Contains(line, "字幕") || strings.Contains(line, "旁白") {
			current.Voiceover = appendSceneText(current.Voiceover, markdownFieldValue(line))
			visualOpen = false
			continue
		}
		if strings.Contains(line, "音效") || strings.Contains(line, "音乐") {
			visualOpen = false
			continue
		}
		if strings.Contains(line, "画面") || strings.Contains(line, "镜头") {
			value := markdownFieldValue(line)
			if !markdownDirection(value) {
				current.Visual = appendSceneText(current.Visual, value)
			}
			visualOpen = true
			continue
		}
		if visualOpen && strings.HasPrefix(line, "*") {
			current.Visual = appendSceneText(current.Visual, markdownBulletValue(line))
		}
	}
	appendScene()
	if len(result.Scenes) > 0 {
		return result, true
	}
	return parseMarkdownTableClipScript(content)
}

func parseMarkdownTableClipScript(content string) (ClipScript, bool) {
	result := ClipScript{Title: "分镜脚本"}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 6 {
			continue
		}
		duration := strings.TrimSpace(cells[2])
		match := markdownDurationRange.FindStringSubmatch(duration)
		if len(match) != 3 {
			continue
		}
		start, _ := strconv.ParseFloat(match[1], 64)
		end, _ := strconv.ParseFloat(match[2], 64)
		sceneIndex := len(result.Scenes) + 1
		result.Scenes = append(result.Scenes, Scene{
			ID:         fmt.Sprintf("scene-%d", sceneIndex),
			SemanticID: fmt.Sprintf("semantic-%d", sceneIndex),
			DurationMS: int((end - start) * 1000),
			Visual:     markdownTableText(cells[3]),
			Voiceover:  markdownTableText(cells[4]),
		})
	}
	return result, len(result.Scenes) > 0
}

func markdownFieldValue(line string) string {
	line = markdownBulletValue(line)
	_, value, found := strings.Cut(line, "：")
	if found {
		line = value
	} else if _, value, found = strings.Cut(line, ":"); found {
		line = value
	}
	line = strings.TrimSpace(strings.ReplaceAll(line, "**", ""))
	line = strings.Trim(line, "\"“”")
	return strings.TrimSpace(line)
}

func markdownBulletValue(line string) string {
	line = strings.TrimSpace(strings.TrimLeft(line, "*- "))
	line = strings.TrimSpace(strings.ReplaceAll(line, "**", ""))
	return strings.Trim(line, "\"“”")
}

func markdownInlineValue(line string) string {
	line = strings.TrimSpace(strings.ReplaceAll(line, "**", ""))
	if !strings.HasPrefix(line, "：") && !strings.HasPrefix(line, ":") {
		return ""
	}
	return strings.TrimSpace(strings.Trim(line, "：: -"))
}

func markdownTableText(value string) string {
	value = strings.ReplaceAll(value, "<br/>", "；")
	value = strings.ReplaceAll(value, "<br>", "；")
	value = strings.ReplaceAll(value, "**", "")
	value = strings.ReplaceAll(value, "`", "")
	return strings.TrimSpace(value)
}

func markdownDirection(value string) bool {
	return strings.HasPrefix(value, "（") && strings.HasSuffix(value, "）") ||
		strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")")
}

func appendSceneText(current, next string) string {
	if next == "" {
		return current
	}
	if current == "" {
		return next
	}
	return current + "；" + next
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
