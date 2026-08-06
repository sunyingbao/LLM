package planning

import (
	"context"
	"testing"

	modelcomponent "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestStageModelPlannerRoutesEachStageToItsModel(t *testing.T) {
	requirementModel := &plannerResponseModel{content: "# 需求分析\n目标人群：通勤女性"}
	clipScriptModel := &plannerResponseModel{content: `{"title":"方案","scenes":[{"id":"scene-1","voiceover":"旁白","visual":"画面"}]}`}
	resourceModel := &plannerResponseModel{content: `[{"id":"voice-1","scene_id":"scene-1","text":"旁白"}]`}
	planner, err := NewStageModelPlanner(requirementModel, clipScriptModel, resourceModel)
	if err != nil {
		t.Fatal(err)
	}

	requirement, err := planner.AnalyzeRequirement(context.Background(), RunInput{ProductName: "鞋"})
	if err != nil {
		t.Fatal(err)
	}
	clipScript, err := planner.CreateClipScript(context.Background(), requirement, RunInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.PlanTTS(context.Background(), clipScript); err != nil {
		t.Fatal(err)
	}
	if requirementModel.calls != 1 || clipScriptModel.calls != 1 || resourceModel.calls != 1 {
		t.Fatalf("model calls = requirement:%d clipscript:%d resource:%d", requirementModel.calls, clipScriptModel.calls, resourceModel.calls)
	}
}

func TestStageModelPlannerAcceptsNumericResourceIDs(t *testing.T) {
	planner, err := NewStageModelPlanner(
		&plannerResponseModel{content: "requirement"},
		&plannerResponseModel{content: `{"scenes":[{"id":"scene-1","visual":"画面","voiceover":"旁白"}]}`},
		&plannerResponseModel{content: `[{"id":1,"scene_id":1,"text":"旁白"}]`},
	)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := planner.PlanTTS(context.Background(), ClipScript{Scenes: []Scene{{ID: "scene-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].ID != "1" || plans[0].SceneID != "scene-1" {
		t.Fatalf("plans = %#v", plans)
	}
}

type plannerResponseModel struct {
	content string
	calls   int
}

func (model *plannerResponseModel) Generate(context.Context, []*schema.Message, ...modelcomponent.Option) (*schema.Message, error) {
	model.calls++
	return schema.AssistantMessage(model.content, nil), nil
}

func (model *plannerResponseModel) Stream(ctx context.Context, input []*schema.Message, options ...modelcomponent.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := model.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}
