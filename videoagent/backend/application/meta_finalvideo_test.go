package application

import (
	"context"
	"encoding/json"
	"testing"

	"eino-cli/videoagent/backend/media"
)

type recordingVideoRenderer struct {
	plan RenderPlan
}

func (renderer *recordingVideoRenderer) StartRender(_ context.Context, plan RenderPlan) (string, error) {
	renderer.plan = plan
	return "render-1", nil
}

func (*recordingVideoRenderer) GetRender(context.Context, string) (JobStatus, error) {
	return JobStatus{State: JobSucceeded, URI: "vid://final", URL: "https://example/final.mp4"}, nil
}

func TestMetaFinalVideoClientBuildsOrderedRenderPlan(t *testing.T) {
	renderer := &recordingVideoRenderer{}
	client, err := media.NewMetaFinalVideoClient(media.MetaFinalVideoConfig{Width: 720, Height: 1280, BizID: 109}, renderer)
	if err != nil {
		t.Fatalf("NewMetaFinalVideoClient() error = %v", err)
	}
	previewOne, _ := json.Marshal(map[string]string{"scene_id": "scene-1", "uri": "vid://preview-1"})
	previewTwo, _ := json.Marshal(map[string]string{"scene_id": "scene-2", "uri": "vid://preview-2"})
	voiceOne, _ := json.Marshal(map[string]any{"scene_id": "scene-1", "audio_url": "https://example/voice-1.wav", "duration_ms": 3500})
	voiceTwo, _ := json.Marshal(map[string]string{"scene_id": "scene-2", "audio_url": "https://example/voice-2.wav"})
	job, err := client.SubmitFinalVideo(context.Background(), VideoRequest{
		ClipScript: &ClipScript{Scenes: []Scene{
			{ID: "scene-1", DurationMS: 3000},
			{ID: "scene-2", DurationMS: 4200},
		}},
		Inputs: []Artifact{
			{ID: "preview:scene-2", Kind: "preview_video", Data: previewTwo},
			{ID: "preview:scene-1", Kind: "preview_video", Data: previewOne},
			{ID: "tts:scene-2", Kind: "voice_preview", Data: voiceTwo},
			{ID: "tts:scene-1", Kind: "voice_preview", Data: voiceOne},
		},
	})
	if err != nil {
		t.Fatalf("SubmitFinalVideo() error = %v", err)
	}
	if job.JobID != "render-1" || len(renderer.plan.Scenes) != 2 {
		t.Fatalf("job = %#v, plan = %#v", job, renderer.plan)
	}
	if renderer.plan.Scenes[0].Source != "vid://preview-1" || renderer.plan.Scenes[1].Source != "vid://preview-2" {
		t.Fatalf("scenes = %#v", renderer.plan.Scenes)
	}
	if len(renderer.plan.Audios) != 2 || renderer.plan.Audios[0].StartMS != 0 || renderer.plan.Audios[0].DurationMS != 3500 || renderer.plan.Audios[1].StartMS != 3500 || renderer.plan.Audios[1].DurationMS != 4200 {
		t.Fatalf("audios = %#v", renderer.plan.Audios)
	}
	if renderer.plan.Scenes[0].DurationMS != 3500 {
		t.Fatalf("scene duration = %d, want narration-aligned 3500", renderer.plan.Scenes[0].DurationMS)
	}
}

func TestMetaFinalVideoClientRejectsExternalPreviewURL(t *testing.T) {
	client, err := media.NewMetaFinalVideoClient(media.MetaFinalVideoConfig{BizID: 109}, &recordingVideoRenderer{})
	if err != nil {
		t.Fatalf("NewMetaFinalVideoClient() error = %v", err)
	}
	preview, _ := json.Marshal(map[string]string{"scene_id": "scene-1", "url": "https://example/preview.mp4"})
	_, err = client.SubmitFinalVideo(context.Background(), VideoRequest{
		ClipScript: &ClipScript{Scenes: []Scene{{ID: "scene-1", DurationMS: 3000}}},
		Inputs:     []Artifact{{ID: "preview:scene-1", Kind: "preview_video", Data: preview}},
	})
	if err == nil {
		t.Fatal("SubmitFinalVideo() error = nil")
	}
}

func TestMetaFinalVideoClientUsesMergedPreviewOnce(t *testing.T) {
	renderer := &recordingVideoRenderer{}
	client, err := media.NewMetaFinalVideoClient(media.MetaFinalVideoConfig{BizID: 109}, renderer)
	if err != nil {
		t.Fatalf("NewMetaFinalVideoClient() error = %v", err)
	}
	preview, _ := json.Marshal(map[string]any{
		"candidate_id":            "candidate-1",
		"origin_picture_clip_ids": []int64{1, 2},
		"uri":                     "vid://merged-preview",
	})
	voiceOne, _ := json.Marshal(map[string]any{"scene_id": "1", "audio_uri": "tos://voice-1", "duration_ms": 3000})
	voiceTwo, _ := json.Marshal(map[string]any{"scene_id": "2", "audio_uri": "tos://voice-2", "duration_ms": 5000})

	_, err = client.SubmitFinalVideo(context.Background(), VideoRequest{
		ClipScript: &ClipScript{Scenes: []Scene{
			{ID: "1", DurationMS: 3000},
			{ID: "2", DurationMS: 5000},
		}},
		Inputs: []Artifact{
			{ID: "preview:candidate-1", Kind: "preview_video", Data: preview},
			{ID: "voice:1", Kind: "voice_preview", Data: voiceOne},
			{ID: "voice:2", Kind: "voice_preview", Data: voiceTwo},
		},
	})
	if err != nil {
		t.Fatalf("SubmitFinalVideo() error = %v", err)
	}
	if len(renderer.plan.Scenes) != 1 || renderer.plan.Scenes[0].ID != "candidate-1" || renderer.plan.Scenes[0].DurationMS != 8000 {
		t.Fatalf("merged preview scenes = %#v", renderer.plan.Scenes)
	}
	if len(renderer.plan.Audios) != 2 || renderer.plan.Audios[1].StartMS != 3000 {
		t.Fatalf("audio timeline = %#v", renderer.plan.Audios)
	}
}

func TestPlanFinalVideoCreatesOneRenderJobPerCut(t *testing.T) {
	clipScript, _ := json.Marshal(ClipScript{Scenes: []Scene{{ID: "1"}, {ID: "2"}}})
	previewOne, _ := json.Marshal(map[string]any{
		"uri": "vid://one", "cut_numbers": []int32{1},
		"cut_placements": []CutPlacement{{CutNumber: 1, ItemIndex: 0}},
	})
	previewTwo, _ := json.Marshal(map[string]any{
		"uri": "vid://two", "cut_numbers": []int32{2},
		"cut_placements": []CutPlacement{{CutNumber: 2, ItemIndex: 0}},
	})
	result, err := planFinalVideo(Command{
		RunID: "run-1", NodeRun: NodeRun{NodeID: "finalvideo", Kind: FinalVideoNode},
		Inputs: []Artifact{
			{ID: "clipscript", Kind: "clipscript", Status: string(Succeeded), Data: clipScript},
			{ID: "preview-one", Kind: "preview_video", Status: string(Succeeded), Data: previewOne},
			{ID: "preview-two", Kind: "preview_video", Status: string(Succeeded), Data: previewTwo},
		},
	}, NodeConfig{})
	if err != nil {
		t.Fatalf("planFinalVideo() error = %v", err)
	}
	if len(result.Children) != 2 || result.Children[0].InstanceKey != "cut-1" || result.Children[1].InstanceKey != "cut-2" {
		t.Fatalf("children = %#v", result.Children)
	}
	for index, child := range result.Children {
		plan, err := decode[ResourcePlan](child.Output)
		if err != nil {
			t.Fatalf("decode child %d: %v", index, err)
		}
		if len(plan.CutNumbers) != 1 || plan.CutNumbers[0] != int32(index+1) || len(plan.ArtifactIDs) != 2 {
			t.Fatalf("child %d plan = %#v", index, plan)
		}
	}
}

func TestMetaFinalVideoClientUsesCutOrderAndSupplementCandidates(t *testing.T) {
	renderer := &recordingVideoRenderer{}
	client, err := media.NewMetaFinalVideoClient(media.MetaFinalVideoConfig{BizID: 109}, renderer)
	if err != nil {
		t.Fatalf("NewMetaFinalVideoClient() error = %v", err)
	}
	preview := func(id, uri string, origins []int64, placement CutPlacement, start, end int) Artifact {
		data, _ := json.Marshal(map[string]any{
			"candidate_id": id, "uri": uri, "origin_picture_clip_ids": origins,
			"cut_numbers": []int32{placement.CutNumber}, "cut_placements": []CutPlacement{placement},
			"clip_start_ms": start, "clip_end_ms": end,
		})
		return Artifact{ID: "preview:" + id, Kind: "preview_video", Status: string(Succeeded), Data: data}
	}
	_, err = client.SubmitFinalVideo(context.Background(), VideoRequest{
		ClipScript: &ClipScript{Scenes: []Scene{{ID: "1", DurationMS: 4000}, {ID: "2", DurationMS: 4000}}},
		CutNumbers: []int32{1},
		Inputs: []Artifact{
			preview("ignored", "vid://ignored", []int64{2}, CutPlacement{CutNumber: 2, ItemIndex: 0}, 0, 4000),
			preview("second", "vid://second", []int64{1}, CutPlacement{CutNumber: 1, ItemIndex: 0, CandidateIndex: 1}, 0, 2000),
			preview("third", "vid://third", []int64{2}, CutPlacement{CutNumber: 1, ItemIndex: 1}, 0, 4000),
			preview("first", "vid://first", []int64{1}, CutPlacement{CutNumber: 1, ItemIndex: 0, CandidateIndex: 0}, 0, 2000),
		},
	})
	if err != nil {
		t.Fatalf("SubmitFinalVideo() error = %v", err)
	}
	if len(renderer.plan.Scenes) != 3 {
		t.Fatalf("scenes = %#v", renderer.plan.Scenes)
	}
	if renderer.plan.Scenes[0].ID != "first" || renderer.plan.Scenes[1].ID != "second" || renderer.plan.Scenes[2].ID != "third" {
		t.Fatalf("scene order = %#v", renderer.plan.Scenes)
	}
	if renderer.plan.Scenes[0].Speed != 0.7 || renderer.plan.Scenes[1].Speed != 1.3 || renderer.plan.Scenes[2].Speed != 1 {
		t.Fatalf("scene speeds = %#v", renderer.plan.Scenes)
	}
}

func TestPreviewArtifactKeepsClipMixLineage(t *testing.T) {
	handler := nodeHandler{}
	result, err := handler.videoResult(
		Command{NodeRun: NodeRun{NodeID: "preview", Kind: PreviewNode}},
		ResourcePlan{
			ID:                   "candidate-1",
			SceneID:              "1",
			OriginPictureClipIDs: []int64{1, 2},
			CutNumbers:           []int32{3},
			Duration:             8,
			Strategy:             PreviewStrategyR2V,
		},
		SubmittedJob{Provider: "seedance", JobID: "job-1"},
		JobStatus{State: JobSucceeded, URI: "vid://preview"},
	)
	if err != nil {
		t.Fatalf("videoResult() error = %v", err)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts = %#v", result.Artifacts)
	}
	var data struct {
		CandidateID          string  `json:"candidate_id"`
		OriginPictureClipIDs []int64 `json:"origin_picture_clip_ids"`
		CutNumbers           []int32 `json:"cut_numbers"`
		DurationSeconds      int     `json:"duration_seconds"`
		Strategy             string  `json:"strategy"`
	}
	if err := json.Unmarshal(result.Artifacts[0].Data, &data); err != nil {
		t.Fatalf("decode preview artifact: %v", err)
	}
	if data.CandidateID != "candidate-1" || len(data.OriginPictureClipIDs) != 2 || data.CutNumbers[0] != 3 || data.DurationSeconds != 8 || data.Strategy != string(PreviewStrategyR2V) {
		t.Fatalf("preview artifact data = %#v", data)
	}
}

func TestMaterialPreviewCompletesWithoutVideoGeneration(t *testing.T) {
	handler := nodeHandler{}
	result, err := handler.submitVideo(context.Background(), Command{NodeRun: NodeRun{
		NodeID: "preview", Kind: PreviewNode, InstanceKey: "material-1",
	}}, ResourcePlan{
		ID: "material-1", SceneID: "1", Strategy: PreviewStrategyMaterial,
		ExistingVideoURI: "vid://source", ClipStartMS: 1000, ClipEndMS: 5000,
	})
	if err != nil {
		t.Fatalf("submitVideo() error = %v", err)
	}
	if result.State != Succeeded || result.Provider != "material" || len(result.Artifacts) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if firstArtifactValue(result.Artifacts[0], "uri") != "vid://source" || firstArtifactInt(result.Artifacts[0], "clip_start_ms") != 1000 {
		t.Fatalf("artifact = %#v", result.Artifacts[0])
	}
}

var _ VideoRenderer = (*recordingVideoRenderer)(nil)
