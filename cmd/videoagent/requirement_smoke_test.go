//go:build fornax

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"eino-cli/backend/videoagent"
	modelcomponent "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestRequirementModelSmoke(t *testing.T) {
	if os.Getenv("VIDEO_AGENT_REQUIREMENT_SMOKE") != "1" {
		t.Skip("set VIDEO_AGENT_REQUIREMENT_SMOKE=1 to call the real requirement prompt")
	}
	credentialsPath := os.Getenv("VIDEO_AGENT_CREDENTIALS_CONFIG")
	if credentialsPath == "" {
		t.Fatal("VIDEO_AGENT_CREDENTIALS_CONFIG is required")
	}
	credentials, err := readJSON[videoagent.CredentialsConfig](credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	requirementModel, err := loadCredentialModel(ctx, credentials, "aic.aic_tool.user_req_analysis")
	if err != nil {
		t.Fatal(err)
	}
	clipScriptModel, err := loadCredentialModel(ctx, credentials, "jichuang.creative.dr_script_e2e")
	if err != nil {
		t.Fatal(err)
	}
	capture := &capturingModel{BaseChatModel: clipScriptModel}
	resourceModel, err := loadCredentialModel(ctx, credentials, "aic.aic_agent.main_agent")
	if err != nil {
		t.Fatal(err)
	}
	planner, err := videoagent.NewStageModelPlanner(requirementModel, capture, resourceModel)
	if err != nil {
		t.Fatal(err)
	}
	input := videoagent.RunInput{ProductName: "软底厚底休闲鞋", Brief: "请分析商品核心卖点、目标人群和适用场景"}
	requirement, err := planner.AnalyzeRequirement(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if requirement.Markdown == "" {
		t.Fatal("requirement model returned empty content")
	}
	clipScript, err := planner.CreateClipScript(ctx, requirement, input)
	if err != nil {
		t.Fatalf("%v; clipscript response=%q", err, capture.content)
	}
	if len(clipScript.Scenes) == 0 {
		t.Fatal("clipscript model returned no scenes")
	}
	t.Logf("planning models succeeded: requirement_bytes=%d scenes=%d", len(requirement.Markdown), len(clipScript.Scenes))
}

type capturingModel struct {
	modelcomponent.BaseChatModel
	content string
}

func (model *capturingModel) Generate(ctx context.Context, input []*schema.Message, options ...modelcomponent.Option) (*schema.Message, error) {
	message, err := model.BaseChatModel.Generate(ctx, input, options...)
	if message != nil {
		model.content = message.Content
	}
	return message, err
}
