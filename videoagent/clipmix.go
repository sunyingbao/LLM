package videoagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultClipMixPlanningModel      = "ad.genai.jichuang_agent_video_gen"
	clipMixPlanModeGenerateNewSchema = 3
	clipMixVideoItemAIGC             = 1
	clipMixVideoItemMaterial         = 2
	clipMixRefImageProduct           = 2
	defaultPreviewDurationSeconds    = 5
)

type PreviewStrategy string

const (
	PreviewStrategyT2V      PreviewStrategy = "t2v"
	PreviewStrategyI2V      PreviewStrategy = "i2v"
	PreviewStrategyR2V      PreviewStrategy = "r2v"
	PreviewStrategyMaterial PreviewStrategy = "material"
)

// ClipMixPlan is the subset of AIGCPlanningV2 output needed to launch preview generation.
type ClipMixPlan struct {
	Version          string             `json:"version"`
	PlanMode         int                `json:"plan_mode"`
	GlobalReferences ClipMixReferences  `json:"global_references,omitempty"`
	Candidates       []ClipMixCandidate `json:"video_set,omitempty"`
	Cuts             []ClipMixCut       `json:"cuts,omitempty"`
}

// ClipMixCandidate describes one generated preview candidate and its model references.
type ClipMixCandidate struct {
	ID                   string               `json:"item_id"`
	Type                 int                  `json:"item_type"`
	SourceType           int                  `json:"source_type,omitempty"`
	OriginPictureClipIDs []int64              `json:"origin_picture_clip_ids,omitempty"`
	MaterialInfo         *ClipMixMaterialInfo `json:"material_info,omitempty"`
	Prompt               string               `json:"prompt,omitempty"`
	RefImageIDs          []string             `json:"ref_image_ids,omitempty"`
	RefAudioIDs          []string             `json:"ref_audio_ids,omitempty"`
	RefVideoIDs          []string             `json:"ref_video_ids,omitempty"`
	VideoDuration        int32                `json:"video_duration,omitempty"`
}

type ClipMixMaterialInfo struct {
	VID         string `json:"vid,omitempty"`
	StartTimeMS int    `json:"start_time_ms,omitempty"`
	EndTimeMS   int    `json:"end_time_ms,omitempty"`
	Caption     string `json:"caption,omitempty"`
}

type ClipMixReferences struct {
	Images []ClipMixImageReference `json:"ref_images,omitempty"`
	Audios []ClipMixAudioReference `json:"ref_audios,omitempty"`
	Videos []ClipMixVideoReference `json:"ref_videos,omitempty"`
}

type ClipMixImageReference struct {
	RefImageID string `json:"ref_image_id"`
	Type       int    `json:"ref_type,omitempty"`
	URI        string `json:"uri,omitempty"`
	URL        string `json:"url,omitempty"`
}

type ClipMixAudioReference struct {
	VoiceID string `json:"voice_id"`
	URI     string `json:"uri,omitempty"`
	URL     string `json:"url,omitempty"`
}

type ClipMixVideoReference struct {
	VideoID string `json:"video_id"`
	URI     string `json:"uri,omitempty"`
	URL     string `json:"url,omitempty"`
}

type ClipMixCut struct {
	Type  int              `json:"cut_type"`
	No    int32            `json:"cut_no"`
	Items []ClipMixCutItem `json:"cut_item_list"`
}

type ClipMixCutItem struct {
	CandidateID            string   `json:"video_item_id"`
	AdditionalCandidateIDs []string `json:"additional_video_item_ids,omitempty"`
	OriginPictureClipIDs   []int64  `json:"origin_picture_clip_ids,omitempty"`
}

type ClipMixPlanner struct {
	gateway ModelGateway
	model   string
}

type ClipMixConfig struct {
	Model string `json:"model"`
}

func NewClipMixPlanner(gateway ModelGateway, model string) (*ClipMixPlanner, error) {
	if gateway == nil {
		return nil, fmt.Errorf("model gateway is nil")
	}
	if model = strings.TrimSpace(model); model == "" {
		model = defaultClipMixPlanningModel
	}
	return &ClipMixPlanner{gateway: gateway, model: model}, nil
}

func (planner *ClipMixPlanner) Plan(ctx context.Context, script ClipScript, input RunInput) ([]ResourcePlan, error) {
	return planner.PlanPreview(ctx, script, input, nil)
}

func (planner *ClipMixPlanner) PlanPreview(ctx context.Context, script ClipScript, input RunInput, artifacts []Artifact) ([]ResourcePlan, error) {
	request, err := buildClipMixPlanningRequest(script, input, artifacts)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	output, err := planner.gateway.Generate(ctx, ModelTaskRequest{Input: payload, Model: planner.model})
	if err != nil {
		return nil, err
	}

	var plan ClipMixPlan
	if err = json.Unmarshal(output, &plan); err != nil {
		return nil, err
	}
	return plan.resourcePlans(request.GlobalReferences)
}

type clipMixPlanningRequest struct {
	GlobalReferences clipMixPlanningReferences `json:"global_references,omitempty"`
	ClipScript       clipMixPlanningClipScript `json:"clip_script"`
	ProductInfo      clipMixProductInfo        `json:"product_info,omitempty"`
	ClipMixPlanMode  int                       `json:"clip_mix_plan_mode"`
}

type clipMixPlanningReferences struct {
	RefImages []ClipMixImageReference `json:"ref_images,omitempty"`
	RefAudios []ClipMixAudioReference `json:"ref_audios,omitempty"`
	RefVideos []ClipMixVideoReference `json:"ref_videos,omitempty"`
}

type clipMixProductInfo struct {
	Name          string   `json:"name,omitempty"`
	ImageURL      string   `json:"image_url,omitempty"`
	SellingPoints []string `json:"selling_points,omitempty"`
}

type clipMixPlanningClipScript struct {
	SemanticClips []clipMixPlanningSemanticClip `json:"semantic_clips"`
}

type clipMixPlanningSemanticClip struct {
	StartTime      float64                      `json:"start_time"`
	EndTime        float64                      `json:"end_time"`
	ASR            string                       `json:"asr,omitempty"`
	PictureClips   []clipMixPlanningPictureClip `json:"picture_clips"`
	SemanticClipID int64                        `json:"semantic_clip_id"`
}

type clipMixPlanningPictureClip struct {
	StartTime     float64  `json:"start_time"`
	EndTime       float64  `json:"end_time"`
	ASR           string   `json:"asr,omitempty"`
	Description   string   `json:"desc,omitempty"`
	PictureClipID int64    `json:"picture_clip_id"`
	CharacterIDs  []string `json:"character_ids,omitempty"`
}

func buildClipMixPlanningRequest(script ClipScript, input RunInput, artifacts []Artifact) (clipMixPlanningRequest, error) {
	if len(script.Scenes) == 0 {
		return clipMixPlanningRequest{}, fmt.Errorf("clip script has no scenes")
	}

	request := clipMixPlanningRequest{
		ClipScript:      buildPlanningClipScript(script),
		ClipMixPlanMode: clipMixPlanModeGenerateNewSchema,
		ProductInfo: clipMixProductInfo{
			Name: input.ProductName,
		},
	}
	if input.Brief != "" {
		request.ProductInfo.SellingPoints = []string{input.Brief}
	}
	if len(input.ProductImageURLs) > 0 {
		request.GlobalReferences.RefImages = make([]ClipMixImageReference, 0, len(input.ProductImageURLs))
		for index, imageURL := range input.ProductImageURLs {
			if imageURL == "" {
				continue
			}
			if request.ProductInfo.ImageURL == "" {
				request.ProductInfo.ImageURL = imageURL
			}
			request.GlobalReferences.RefImages = append(request.GlobalReferences.RefImages, ClipMixImageReference{
				RefImageID: fmt.Sprintf("product_%d", index+1),
				Type:       clipMixRefImageProduct,
				URL:        imageURL,
			})
		}
	}
	appendPlanningReferences(&request.GlobalReferences, artifacts)
	return request, nil
}

func appendPlanningReferences(references *clipMixPlanningReferences, artifacts []Artifact) {
	for _, artifact := range artifacts {
		if artifact.Status != string(Succeeded) {
			continue
		}
		uri, url := artifactMediaValues(artifact)
		if uri == "" && url == "" {
			continue
		}
		switch artifact.Kind {
		case "competition_reference_image", "character_reference_image":
			references.RefImages = append(references.RefImages, ClipMixImageReference{RefImageID: artifact.ID, URI: uri, URL: url})
		case "voice_preview":
			references.RefAudios = append(references.RefAudios, ClipMixAudioReference{VoiceID: artifact.ID, URI: uri, URL: url})
		case "preview_video":
			references.RefVideos = append(references.RefVideos, ClipMixVideoReference{VideoID: artifact.ID, URI: uri, URL: url})
		}
	}
}

func artifactMediaValues(artifact Artifact) (string, string) {
	uri := firstArtifactValue(artifact, "uri", "preview_audio_uri", "audio_uri")
	url := firstArtifactValue(artifact, "url", "preview_audio_url", "audio_url")
	return uri, url
}

func buildPlanningClipScript(script ClipScript) clipMixPlanningClipScript {
	semanticClips := make([]clipMixPlanningSemanticClip, 0, len(script.Scenes))
	currentTime := 0.0
	pictureIDs := newNumericIDRegistry()
	semanticIDs := newNumericIDRegistry()

	for _, scene := range script.Scenes {
		pictureID := pictureIDs.ID(scene.ID)
		semanticKey := scene.SemanticID
		if semanticKey == "" {
			semanticKey = scene.ID
		}
		semanticID := semanticIDs.ID(semanticKey)
		duration := float64(scene.DurationMS) / 1000
		if duration <= 0 {
			duration = defaultPreviewDurationSeconds
		}
		picture := clipMixPlanningPictureClip{
			StartTime: currentTime, EndTime: currentTime + duration,
			ASR: scene.Voiceover, Description: scene.Visual,
			PictureClipID: pictureID, CharacterIDs: append([]string(nil), scene.CharacterIDs...),
		}

		semanticIndex := len(semanticClips) - 1
		if semanticIndex < 0 || semanticClips[semanticIndex].SemanticClipID != semanticID {
			semanticClips = append(semanticClips, clipMixPlanningSemanticClip{
				StartTime: currentTime, EndTime: picture.EndTime,
				ASR: scene.Voiceover, SemanticClipID: semanticID,
			})
			semanticIndex++
		} else {
			semanticClips[semanticIndex].EndTime = picture.EndTime
			semanticClips[semanticIndex].ASR += scene.Voiceover
		}
		semanticClips[semanticIndex].PictureClips = append(semanticClips[semanticIndex].PictureClips, picture)
		currentTime = picture.EndTime
	}
	return clipMixPlanningClipScript{SemanticClips: semanticClips}
}

type numericIDRegistry struct {
	values map[string]int64
	used   map[int64]bool
	next   int64
}

func newNumericIDRegistry() *numericIDRegistry {
	return &numericIDRegistry{values: make(map[string]int64), used: make(map[int64]bool), next: 1}
}

func (registry *numericIDRegistry) ID(value string) int64 {
	value = strings.TrimSpace(value)
	if id := registry.values[value]; id > 0 {
		return id
	}
	if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 && !registry.used[id] {
		registry.values[value] = id
		registry.used[id] = true
		return id
	}
	for registry.used[registry.next] {
		registry.next++
	}
	id := registry.next
	registry.next++
	registry.values[value] = id
	registry.used[id] = true
	return id
}

func parsePositiveID(value string, fallback int64) int64 {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return fallback
	}
	return id
}

func (plan ClipMixPlan) resourcePlans(inputReferences clipMixPlanningReferences) ([]ResourcePlan, error) {
	if plan.PlanMode != clipMixPlanModeGenerateNewSchema {
		return nil, fmt.Errorf("unsupported clip mix plan mode: %d", plan.PlanMode)
	}
	if len(plan.Cuts) == 0 {
		return nil, fmt.Errorf("clip mix plan has no cuts")
	}

	candidates := make(map[string]ClipMixCandidate, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		if candidate.ID != "" {
			candidates[candidate.ID] = candidate
		}
	}
	references := newClipMixReferenceIndex(plan.GlobalReferences, inputReferences)
	plans := make([]ResourcePlan, 0, len(plan.Candidates))
	planIndexes := make(map[string]int, len(plan.Candidates))

	for _, cut := range plan.Cuts {
		for itemIndex, item := range cut.Items {
			candidateIDs := append([]string{item.CandidateID}, item.AdditionalCandidateIDs...)
			for candidateIndex, candidateID := range candidateIDs {
				candidate, exists := candidates[candidateID]
				if !exists {
					return nil, fmt.Errorf("clip mix candidate not found: %s", candidateID)
				}
				if candidate.Type != clipMixVideoItemAIGC && candidate.Type != clipMixVideoItemMaterial {
					return nil, fmt.Errorf("unsupported preview candidate type %d: %s", candidate.Type, candidate.ID)
				}

				if planIndex, exists := planIndexes[candidate.ID]; exists {
					plans[planIndex].CutNumbers = appendUniqueInt32(plans[planIndex].CutNumbers, cut.No)
					plans[planIndex].CutPlacements = append(plans[planIndex].CutPlacements, CutPlacement{
						CutNumber: cut.No, ItemIndex: itemIndex, CandidateIndex: candidateIndex,
					})
					continue
				}
				resourcePlan, err := candidate.resourcePlan(item.OriginPictureClipIDs, cut.No, itemIndex, candidateIndex, references)
				if err != nil {
					return nil, err
				}
				planIndexes[candidate.ID] = len(plans)
				plans = append(plans, resourcePlan)
			}
		}
	}
	return plans, nil
}

type clipMixReferenceIndex struct {
	images map[string]string
	audios map[string]string
	videos map[string]string
}

func newClipMixReferenceIndex(output ClipMixReferences, input clipMixPlanningReferences) clipMixReferenceIndex {
	index := clipMixReferenceIndex{
		images: make(map[string]string, len(output.Images)+len(input.RefImages)),
		audios: make(map[string]string, len(output.Audios)+len(input.RefAudios)),
		videos: make(map[string]string, len(output.Videos)+len(input.RefVideos)),
	}
	for _, reference := range input.RefImages {
		index.images[reference.RefImageID] = firstNonEmpty(reference.URL, reference.URI)
	}
	for _, reference := range input.RefAudios {
		index.audios[reference.VoiceID] = firstNonEmpty(reference.URL, reference.URI)
	}
	for _, reference := range input.RefVideos {
		index.videos[reference.VideoID] = firstNonEmpty(reference.URL, reference.URI)
	}
	for _, reference := range output.Images {
		index.images[reference.RefImageID] = firstNonEmpty(reference.URL, reference.URI)
	}
	for _, reference := range output.Audios {
		index.audios[reference.VoiceID] = firstNonEmpty(reference.URL, reference.URI)
	}
	for _, reference := range output.Videos {
		index.videos[reference.VideoID] = firstNonEmpty(reference.URL, reference.URI)
	}
	return index
}

func (candidate ClipMixCandidate) resourcePlan(
	cutPictureIDs []int64,
	cutNo int32,
	itemIndex int,
	candidateIndex int,
	references clipMixReferenceIndex,
) (ResourcePlan, error) {
	pictureIDs := append([]int64(nil), candidate.OriginPictureClipIDs...)
	if len(pictureIDs) == 0 {
		pictureIDs = append(pictureIDs, cutPictureIDs...)
	}
	resourcePlan := ResourcePlan{
		ID: candidate.ID, OriginPictureClipIDs: pictureIDs, CutNumbers: []int32{cutNo},
		CutPlacements: []CutPlacement{{CutNumber: cutNo, ItemIndex: itemIndex, CandidateIndex: candidateIndex}},
		Duration:      int(candidate.VideoDuration),
	}
	if len(pictureIDs) > 0 {
		resourcePlan.SceneID = strconv.FormatInt(pictureIDs[0], 10)
	}
	if candidate.Type == clipMixVideoItemMaterial {
		if candidate.MaterialInfo == nil || strings.TrimSpace(candidate.MaterialInfo.VID) == "" {
			return ResourcePlan{}, fmt.Errorf("material preview candidate has no vid: %s", candidate.ID)
		}
		resourcePlan.Strategy = PreviewStrategyMaterial
		resourcePlan.ExistingVideoURI = "vid://" + strings.TrimPrefix(candidate.MaterialInfo.VID, "vid://")
		resourcePlan.ClipStartMS = candidate.MaterialInfo.StartTimeMS
		resourcePlan.ClipEndMS = candidate.MaterialInfo.EndTimeMS
		if resourcePlan.Duration <= 0 && resourcePlan.ClipEndMS > resourcePlan.ClipStartMS {
			resourcePlan.Duration = (resourcePlan.ClipEndMS - resourcePlan.ClipStartMS + 999) / 1000
		}
		return resourcePlan, nil
	}
	if strings.TrimSpace(candidate.Prompt) == "" {
		return ResourcePlan{}, fmt.Errorf("preview candidate has no prompt: %s", candidate.ID)
	}
	imageURLs, err := resolveReferences(candidate.RefImageIDs, references.images, "image")
	if err != nil {
		return ResourcePlan{}, err
	}
	audioURLs, err := resolveReferences(candidate.RefAudioIDs, references.audios, "audio")
	if err != nil {
		return ResourcePlan{}, err
	}
	videoURLs, err := resolveReferences(candidate.RefVideoIDs, references.videos, "video")
	if err != nil {
		return ResourcePlan{}, err
	}

	resourcePlan.Prompt = candidate.Prompt
	resourcePlan.ImageURLs = imageURLs
	resourcePlan.AudioURLs = audioURLs
	resourcePlan.VideoURLs = videoURLs
	resourcePlan.Strategy = previewStrategy(imageURLs, audioURLs, videoURLs)
	return resourcePlan, nil
}

func previewStrategy(images, audios, videos []string) PreviewStrategy {
	if len(images) == 0 && len(audios) == 0 && len(videos) == 0 {
		return PreviewStrategyT2V
	}
	if len(images) == 1 && len(audios) == 0 && len(videos) == 0 {
		return PreviewStrategyI2V
	}
	return PreviewStrategyR2V
}

func resolveReferences(ids []string, references map[string]string, kind string) ([]string, error) {
	resolved := make([]string, 0, len(ids))
	for _, id := range ids {
		value, exists := references[id]
		if !exists || value == "" {
			return nil, fmt.Errorf("clip mix %s reference not found: %s", kind, id)
		}
		resolved = append(resolved, value)
	}
	return resolved, nil
}

func appendUniqueInt32(values []int32, value int32) []int32 {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}
