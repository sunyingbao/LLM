package planning

import (
	"context"
	"encoding/json"
	"testing"
)

func TestClipMixPlannerBuildsPlanningRequestAndPreviewPlans(t *testing.T) {
	gateway := &clipMixGateway{output: []byte(`{
		"version":"v1",
		"plan_mode":3,
		"global_references":{
			"ref_images":[{"ref_image_id":"product_1","ref_type":2,"url":"https://example.com/product.png"}],
			"ref_audios":[{"voice_id":"voice_1","url":"https://example.com/voice.mp3"}],
			"ref_videos":[{"video_id":"video_1","url":"https://example.com/reference.mp4"}]
		},
		"video_set":[
			{"item_id":"t2v","item_type":1,"origin_picture_clip_ids":[11],"prompt":"t2v prompt","video_duration":5},
			{"item_id":"i2v","item_type":1,"origin_picture_clip_ids":[12],"prompt":"i2v prompt","ref_image_ids":["product_1"],"video_duration":6},
			{"item_id":"r2v","item_type":1,"origin_picture_clip_ids":[13],"prompt":"r2v prompt","ref_image_ids":["product_1"],"ref_audio_ids":["voice_1"],"ref_video_ids":["video_1"],"video_duration":7}
		],
		"cuts":[{"cut_type":1,"cut_no":1,"cut_item_list":[
			{"video_item_id":"t2v","origin_picture_clip_ids":[11]},
			{"video_item_id":"i2v","origin_picture_clip_ids":[12]},
			{"video_item_id":"r2v","origin_picture_clip_ids":[13]}
		]}]
	}`)}
	planner, err := NewClipMixPlanner(gateway, "planning-model")
	if err != nil {
		t.Fatalf("NewClipMixPlanner() error = %v", err)
	}

	plans, err := planner.Plan(context.Background(), ClipScript{Scenes: []Scene{
		{ID: "11", SemanticID: "1", Voiceover: "first voice", Visual: "first visual", DurationMS: 5000},
		{ID: "12", SemanticID: "2", Voiceover: "second voice", Visual: "second visual", DurationMS: 6000},
		{ID: "13", SemanticID: "3", Voiceover: "third voice", Visual: "third visual", DurationMS: 7000},
	}}, RunInput{ProductName: "shoe", ProductImageURLs: []string{"https://example.com/product.png"}, Brief: "soft sole"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if gateway.request.Model != "planning-model" {
		t.Fatalf("model = %q", gateway.request.Model)
	}
	var request clipMixPlanningRequest
	if err := json.Unmarshal(gateway.request.Input, &request); err != nil {
		t.Fatalf("unmarshal planning request: %v", err)
	}
	if request.ClipMixPlanMode != clipMixPlanModeGenerateNewSchema {
		t.Fatalf("clip_mix_plan_mode = %d", request.ClipMixPlanMode)
	}
	if request.ProductInfo.Name != "shoe" || request.ProductInfo.ImageURL != "https://example.com/product.png" {
		t.Fatalf("product_info = %#v", request.ProductInfo)
	}
	if len(request.ClipScript.SemanticClips) != 3 || request.ClipScript.SemanticClips[1].PictureClips[0].PictureClipID != 12 {
		t.Fatalf("clip_script = %#v", request.ClipScript)
	}
	if len(request.GlobalReferences.RefImages) != 1 || request.GlobalReferences.RefImages[0].RefImageID != "product_1" {
		t.Fatalf("global_references = %#v", request.GlobalReferences)
	}

	if len(plans) != 3 {
		t.Fatalf("plan count = %d", len(plans))
	}
	if plans[0].Strategy != PreviewStrategyT2V || plans[0].Prompt != "t2v prompt" || plans[0].Duration != 5 {
		t.Fatalf("t2v plan = %#v", plans[0])
	}
	if plans[1].Strategy != PreviewStrategyI2V || len(plans[1].ImageURLs) != 1 {
		t.Fatalf("i2v plan = %#v", plans[1])
	}
	if plans[2].Strategy != PreviewStrategyR2V || len(plans[2].ImageURLs) != 1 || len(plans[2].AudioURLs) != 1 || len(plans[2].VideoURLs) != 1 {
		t.Fatalf("r2v plan = %#v", plans[2])
	}
	for index, plan := range plans {
		if len(plan.CutPlacements) != 1 || plan.CutPlacements[0].CutNumber != 1 || plan.CutPlacements[0].ItemIndex != index {
			t.Fatalf("plan %d placements = %#v", index, plan.CutPlacements)
		}
	}
}

func TestClipMixPlannerKeepsCandidatePositionForEveryCut(t *testing.T) {
	gateway := &clipMixGateway{output: []byte(`{
		"version":"v1",
		"plan_mode":3,
		"video_set":[
			{"item_id":"a","item_type":1,"origin_picture_clip_ids":[1],"prompt":"a","video_duration":4},
			{"item_id":"b","item_type":1,"origin_picture_clip_ids":[2],"prompt":"b","video_duration":4}
		],
		"cuts":[
			{"cut_no":1,"cut_item_list":[{"video_item_id":"a"},{"video_item_id":"b"}]},
			{"cut_no":2,"cut_item_list":[{"video_item_id":"b"},{"video_item_id":"a"}]}
		]
	}`)}
	planner, err := NewClipMixPlanner(gateway, "planning-model")
	if err != nil {
		t.Fatalf("NewClipMixPlanner() error = %v", err)
	}
	plans, err := planner.Plan(context.Background(), ClipScript{Scenes: []Scene{{ID: "1"}, {ID: "2"}}}, RunInput{})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plans) != 2 || len(plans[0].CutPlacements) != 2 || len(plans[1].CutPlacements) != 2 {
		t.Fatalf("plans = %#v", plans)
	}
	if plans[0].CutPlacements[1].CutNumber != 2 || plans[0].CutPlacements[1].ItemIndex != 1 {
		t.Fatalf("candidate a placements = %#v", plans[0].CutPlacements)
	}
	if plans[1].CutPlacements[1].CutNumber != 2 || plans[1].CutPlacements[1].ItemIndex != 0 {
		t.Fatalf("candidate b placements = %#v", plans[1].CutPlacements)
	}
}

func TestClipMixPlannerReusesMaterialCandidate(t *testing.T) {
	gateway := &clipMixGateway{output: []byte(`{
		"version":"v1",
		"plan_mode":3,
		"video_set":[{"item_id":"material_1","item_type":2,"origin_picture_clip_ids":[1],"material_info":{"vid":"material-vid","start_time_ms":1200,"end_time_ms":6300}}],
		"cuts":[{"cut_type":2,"cut_no":1,"cut_item_list":[{"video_item_id":"material_1","origin_picture_clip_ids":[1]}]}]
	}`)}
	planner, err := NewClipMixPlanner(gateway, "planning-model")
	if err != nil {
		t.Fatalf("NewClipMixPlanner() error = %v", err)
	}

	plans, err := planner.Plan(context.Background(), ClipScript{Scenes: []Scene{{ID: "1", Visual: "scene"}}}, RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plans) != 1 || plans[0].Strategy != PreviewStrategyMaterial || plans[0].ExistingVideoURI != "vid://material-vid" {
		t.Fatalf("material plan = %#v", plans)
	}
	if plans[0].ClipStartMS != 1200 || plans[0].ClipEndMS != 6300 || plans[0].Duration != 6 {
		t.Fatalf("material timing = %#v", plans[0])
	}
}

func TestBuildPlanningClipScriptKeepsStringSemanticGroups(t *testing.T) {
	result := buildPlanningClipScript(ClipScript{Scenes: []Scene{
		{ID: "scene-1-1", SemanticID: "semantic-1", Visual: "first"},
		{ID: "scene-1-2", SemanticID: "semantic-1", Visual: "second"},
		{ID: "scene-2-1", SemanticID: "semantic-2", Visual: "third"},
	}})
	if len(result.SemanticClips) != 2 {
		t.Fatalf("semantic clip count = %d, want 2", len(result.SemanticClips))
	}
	if len(result.SemanticClips[0].PictureClips) != 2 {
		t.Fatalf("first semantic pictures = %d, want 2", len(result.SemanticClips[0].PictureClips))
	}
	if result.SemanticClips[0].SemanticClipID == result.SemanticClips[1].SemanticClipID {
		t.Fatal("different semantic groups share one id")
	}
}

type clipMixGateway struct {
	request ModelTaskRequest
	output  []byte
}

type fakeModelGateway struct {
	request ModelTaskRequest
	status  ModelTaskStatus
}

func (gateway *clipMixGateway) Generate(_ context.Context, request ModelTaskRequest) ([]byte, error) {
	gateway.request = request
	return gateway.output, nil
}

func (*clipMixGateway) CreateTask(context.Context, ModelTaskRequest) (string, error) {
	return "", nil
}

func (*clipMixGateway) GetTask(context.Context, string) (ModelTaskStatus, error) {
	return ModelTaskStatus{}, nil
}

func (gateway *fakeModelGateway) Generate(_ context.Context, request ModelTaskRequest) ([]byte, error) {
	gateway.request = request
	return gateway.status.Result, nil
}
