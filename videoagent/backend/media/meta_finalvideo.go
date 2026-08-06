package media

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type MetaFinalVideoConfig struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	BizID  int `json:"biz_id"`
}

type MetaFinalVideoClient struct {
	config   MetaFinalVideoConfig
	renderer VideoRenderer
}

func NewMetaFinalVideoClient(config MetaFinalVideoConfig, renderer VideoRenderer) (*MetaFinalVideoClient, error) {
	if renderer == nil {
		return nil, fmt.Errorf("video renderer is nil")
	}
	if config.BizID <= 0 {
		return nil, fmt.Errorf("meta biz_id must be positive")
	}
	if config.Width <= 0 {
		config.Width = 720
	}
	if config.Height <= 0 {
		config.Height = 1280
	}
	return &MetaFinalVideoClient{config: config, renderer: renderer}, nil
}

func (client *MetaFinalVideoClient) SubmitFinalVideo(ctx context.Context, request VideoRequest) (SubmittedJob, error) {
	plan, err := buildRenderPlan(client.config, request)
	if err != nil {
		return SubmittedJob{}, err
	}
	jobID, err := client.renderer.StartRender(ctx, plan)
	if err != nil {
		return SubmittedJob{}, err
	}
	if jobID == "" {
		return SubmittedJob{}, fmt.Errorf("meta renderer returned an empty task id")
	}
	return SubmittedJob{Provider: "meta", JobID: jobID}, nil
}

func (client *MetaFinalVideoClient) GetFinalVideo(ctx context.Context, jobID string) (JobStatus, error) {
	return client.renderer.GetRender(ctx, jobID)
}

func (*MetaFinalVideoClient) FindFinalVideoBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, ErrSubmitReconciliationUnsupported
}

func buildRenderPlan(config MetaFinalVideoConfig, request VideoRequest) (RenderPlan, error) {
	if request.ClipScript == nil || len(request.ClipScript.Scenes) == 0 {
		return RenderPlan{}, fmt.Errorf("finalvideo requires clipscript scenes")
	}
	voices := artifactsByScene(request.Inputs, "voice_preview")
	previewsByScene := artifactsByScene(request.Inputs, "preview_video")
	previewsByPicture := make(map[int64][]Artifact)
	for _, artifact := range request.Inputs {
		if artifact.Kind != "preview_video" {
			continue
		}
		for _, pictureID := range artifactInt64s(artifact, "origin_picture_clip_ids") {
			previewsByPicture[pictureID] = append(previewsByPicture[pictureID], artifact)
		}
	}

	sceneDurations := make(map[string]int, len(request.ClipScript.Scenes))
	pictureDurations := make(map[int64]int, len(request.ClipScript.Scenes))
	for index, scene := range request.ClipScript.Scenes {
		sceneID := scene.ID
		if sceneID == "" {
			sceneID = fmt.Sprintf("scene-%d", index+1)
		}
		durationMS := normalizedDurationMS(scene.DurationMS)
		if voice, ok := voices[sceneID]; ok {
			if audioDuration := firstArtifactInt(voice, "duration_ms"); audioDuration > durationMS {
				durationMS = audioDuration
			}
		}
		sceneDurations[sceneID] = durationMS
		pictureDurations[parsePositiveID(scene.ID, int64(index+1))] = durationMS
	}

	plan := RenderPlan{Width: config.Width, Height: config.Height}
	cutPreviews, hasCutOrder := orderedCutPreviews(request.Inputs, request.CutNumbers)
	if hasCutOrder {
		scenes, err := buildCutRenderScenes(cutPreviews, pictureDurations)
		if err != nil {
			return RenderPlan{}, err
		}
		plan.Scenes = scenes
	} else {
		scenes, err := buildSceneOrderedRenderScenes(request.ClipScript.Scenes, previewsByScene, previewsByPicture, sceneDurations, pictureDurations)
		if err != nil {
			return RenderPlan{}, err
		}
		plan.Scenes = scenes
	}

	startMS := 0
	for index, scene := range request.ClipScript.Scenes {
		sceneID := scene.ID
		if sceneID == "" {
			sceneID = fmt.Sprintf("scene-%d", index+1)
		}
		durationMS := sceneDurations[sceneID]
		if voice, ok := voices[sceneID]; ok {
			source := firstArtifactValue(voice, "audio_uri", "audio_url")
			if source != "" {
				audioDuration := firstArtifactInt(voice, "duration_ms")
				if audioDuration <= 0 || audioDuration > durationMS {
					audioDuration = durationMS
				}
				plan.Audios = append(plan.Audios, RenderAudio{Source: source, StartMS: startMS, DurationMS: audioDuration})
			}
		}
		startMS += durationMS
	}
	if len(plan.Audios) == 0 {
		for _, artifact := range request.Inputs {
			if artifact.Kind != "voice_preview" {
				continue
			}
			source := firstArtifactValue(artifact, "audio_uri", "audio_url")
			if source != "" {
				plan.Audios = append(plan.Audios, RenderAudio{Source: source, DurationMS: startMS})
				break
			}
		}
	}
	return plan, nil
}

type placedPreview struct {
	artifact  Artifact
	placement CutPlacement
}

func orderedCutPreviews(artifacts []Artifact, cuts []int32) ([]placedPreview, bool) {
	if len(cuts) == 0 {
		return nil, false
	}
	cut := cuts[0]
	previews := make([]placedPreview, 0)
	for _, artifact := range artifacts {
		if artifact.Kind != "preview_video" || artifact.Status != string(Succeeded) {
			continue
		}
		for _, placement := range artifactCutPlacements(artifact) {
			if placement.CutNumber == cut {
				previews = append(previews, placedPreview{artifact: artifact, placement: placement})
				break
			}
		}
	}
	if len(previews) == 0 {
		return nil, false
	}
	sort.SliceStable(previews, func(i, j int) bool {
		if previews[i].placement.ItemIndex != previews[j].placement.ItemIndex {
			return previews[i].placement.ItemIndex < previews[j].placement.ItemIndex
		}
		return previews[i].placement.CandidateIndex < previews[j].placement.CandidateIndex
	})
	return previews, true
}

func buildCutRenderScenes(previews []placedPreview, pictureDurations map[int64]int) ([]RenderScene, error) {
	scenes := make([]RenderScene, 0, len(previews))
	for start := 0; start < len(previews); {
		end := start + 1
		for end < len(previews) && previews[end].placement.ItemIndex == previews[start].placement.ItemIndex {
			end++
		}
		targetDuration := pictureDuration(previews[start].artifact, pictureDurations)
		groupScenes, err := fitPreviewGroup(previews[start:end], targetDuration)
		if err != nil {
			return nil, err
		}
		scenes = append(scenes, groupScenes...)
		start = end
	}
	if len(scenes) == 0 {
		return nil, fmt.Errorf("selected cut has no preview video")
	}
	return scenes, nil
}

func fitPreviewGroup(previews []placedPreview, targetDuration int) ([]RenderScene, error) {
	if targetDuration <= 0 {
		for _, preview := range previews {
			targetDuration += firstArtifactInt(preview.artifact, "duration_seconds") * 1000
		}
	}
	if targetDuration <= 0 {
		targetDuration = 5000
	}

	remaining := targetDuration
	scenes := make([]RenderScene, 0, len(previews))
	for index, preview := range previews {
		if remaining <= 0 {
			break
		}
		scene, rawDuration, err := renderSceneFromArtifact(preview.artifact, remaining)
		if err != nil {
			return nil, err
		}
		if index < len(previews)-1 && rawDuration > 0 && float64(rawDuration)/float64(remaining) < 0.7 {
			scene.Speed = 0.7
			scene.DurationMS = int(float64(rawDuration) / scene.Speed)
		} else if rawDuration > 0 {
			scene.Speed = float64(rawDuration) / float64(remaining)
			if scene.Speed > 1.3 {
				scene.Speed = 1.3
				scene.ClipEndMS = scene.ClipStartMS + int(float64(remaining)*scene.Speed)
			}
			scene.DurationMS = remaining
		}
		scenes = append(scenes, scene)
		remaining -= scene.DurationMS
	}
	if remaining > 0 && len(scenes) > 0 {
		last := &scenes[len(scenes)-1]
		last.DurationMS += remaining
		rawDuration := last.ClipEndMS - last.ClipStartMS
		if rawDuration > 0 {
			last.Speed = float64(rawDuration) / float64(last.DurationMS)
		}
	}
	return scenes, nil
}

func renderSceneFromArtifact(preview Artifact, fallbackDuration int) (RenderScene, int, error) {
	source := firstArtifactValue(preview, "uri", "url")
	if !strings.HasPrefix(source, "vid://") {
		return RenderScene{}, 0, fmt.Errorf("preview %s is not an internal video", preview.ID)
	}
	clipStart := firstArtifactInt(preview, "clip_start_ms")
	clipEnd := firstArtifactInt(preview, "clip_end_ms")
	rawDuration := clipEnd - clipStart
	if rawDuration <= 0 {
		rawDuration = firstArtifactInt(preview, "duration_seconds") * 1000
		if rawDuration <= 0 {
			rawDuration = fallbackDuration
		}
		clipEnd = clipStart + rawDuration
	}
	previewID := firstArtifactValue(preview, "candidate_id")
	if previewID == "" {
		previewID = preview.ID
	}
	return RenderScene{
		ID: previewID, Source: source, ClipStartMS: clipStart, ClipEndMS: clipEnd,
		DurationMS: fallbackDuration, Speed: 1,
	}, rawDuration, nil
}

func pictureDuration(preview Artifact, durations map[int64]int) int {
	duration := 0
	for _, pictureID := range artifactInt64s(preview, "origin_picture_clip_ids") {
		duration += durations[pictureID]
	}
	return duration
}

func buildSceneOrderedRenderScenes(
	scriptScenes []Scene,
	previewsByScene map[string]Artifact,
	previewsByPicture map[int64][]Artifact,
	sceneDurations map[string]int,
	pictureDurations map[int64]int,
) ([]RenderScene, error) {
	result := make([]RenderScene, 0, len(scriptScenes))
	coveredPictures := make(map[int64]bool)
	usedPreviews := make(map[string]bool)
	for index, scene := range scriptScenes {
		sceneID := scene.ID
		if sceneID == "" {
			sceneID = fmt.Sprintf("scene-%d", index+1)
		}
		pictureID := parsePositiveID(scene.ID, int64(index+1))
		if coveredPictures[pictureID] {
			continue
		}
		preview, ok := firstUnusedArtifact(previewsByPicture[pictureID], usedPreviews)
		if !ok {
			preview, ok = previewsByScene[sceneID]
		}
		if !ok || usedPreviews[preview.ID] {
			return nil, fmt.Errorf("preview is missing for scene %s", sceneID)
		}
		originIDs := artifactInt64s(preview, "origin_picture_clip_ids")
		if len(originIDs) == 0 {
			originIDs = []int64{pictureID}
		}
		durationMS := 0
		for _, originID := range originIDs {
			coveredPictures[originID] = true
			durationMS += pictureDurations[originID]
		}
		if durationMS == 0 {
			durationMS = sceneDurations[sceneID]
		}
		renderScene, rawDuration, err := renderSceneFromArtifact(preview, durationMS)
		if err != nil {
			return nil, err
		}
		if durationMS > 0 && rawDuration > 0 {
			renderScene.Speed = float64(rawDuration) / float64(durationMS)
		}
		usedPreviews[preview.ID] = true
		result = append(result, renderScene)
	}
	return result, nil
}

func firstUnusedArtifact(artifacts []Artifact, used map[string]bool) (Artifact, bool) {
	for _, artifact := range artifacts {
		if !used[artifact.ID] {
			return artifact, true
		}
	}
	return Artifact{}, false
}

func artifactsByScene(artifacts []Artifact, kind string) map[string]Artifact {
	result := make(map[string]Artifact)
	for _, artifact := range artifacts {
		if artifact.Kind != kind {
			continue
		}
		sceneID := firstArtifactValue(artifact, "scene_id")
		if sceneID == "" {
			_, sceneID, _ = strings.Cut(artifact.ID, ":")
		}
		if sceneID != "" {
			result[sceneID] = artifact
		}
	}
	return result
}

func firstArtifactValue(artifact Artifact, keys ...string) string {
	var data map[string]json.RawMessage
	if json.Unmarshal(artifact.Data, &data) != nil {
		return ""
	}
	for _, key := range keys {
		var value string
		if json.Unmarshal(data[key], &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

func firstArtifactInt(artifact Artifact, keys ...string) int {
	var data map[string]json.RawMessage
	if json.Unmarshal(artifact.Data, &data) != nil {
		return 0
	}
	for _, key := range keys {
		var value int
		if json.Unmarshal(data[key], &value) == nil && value > 0 {
			return value
		}
	}
	return 0
}

func artifactInt64s(artifact Artifact, key string) []int64 {
	var data map[string]json.RawMessage
	if json.Unmarshal(artifact.Data, &data) != nil {
		return nil
	}
	var values []int64
	if json.Unmarshal(data[key], &values) != nil {
		return nil
	}
	return values
}

func artifactInt32s(artifact Artifact, key string) []int32 {
	var data map[string]json.RawMessage
	if json.Unmarshal(artifact.Data, &data) != nil {
		return nil
	}
	var values []int32
	if json.Unmarshal(data[key], &values) != nil {
		return nil
	}
	return values
}

func artifactCutPlacements(artifact Artifact) []CutPlacement {
	var data map[string]json.RawMessage
	if json.Unmarshal(artifact.Data, &data) != nil {
		return nil
	}
	var placements []CutPlacement
	if json.Unmarshal(data["cut_placements"], &placements) != nil {
		return nil
	}
	return placements
}

func normalizedDurationMS(duration int) int {
	if duration > 0 {
		return duration
	}
	return 5000
}

func finalVideoUpscaleSize(width, height int) (int, int) {
	if width > height {
		return 1920, 1080
	}
	return 1080, 1920
}

var _ FinalVideoClient = (*MetaFinalVideoClient)(nil)
