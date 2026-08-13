package input

import (
	"context"
	"encoding/json"
	"testing"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	httpapi "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_api"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_common"
)

func TestResumeInputUsesInterruptFromContext(t *testing.T) {
	req := &httpapi.SubmitInputHTTPRequest{
		ResumeRef: &httpcommon.ResumeRef{
			TurnID:       "turn-1",
			CheckpointID: "checkpoint-1",
			InterruptID:  "interrupt-1",
		},
	}
	ctx := ContextWithResumeInterrupt(context.Background(), &ResumeInterruptInput{
		Kind: "follow_up",
		Data: json.RawMessage(`{"user_answer":"yes"}`),
	})

	got, err := resumeInput(ctx, req)
	if err != nil {
		t.Fatalf("resumeInput() error = %v", err)
	}
	if got.Interrupt == nil || got.Interrupt.Kind != "follow_up" {
		t.Fatalf("Interrupt = %+v", got.Interrupt)
	}
	if string(got.Interrupt.Data) != `{"user_answer":"yes"}` {
		t.Fatalf("Interrupt data = %s", got.Interrupt.Data)
	}
	if got.RequestUserInput != nil || got.Approval != nil {
		t.Fatalf("unexpected fallback payload: request_user_input=%+v approval=%+v", got.RequestUserInput, got.Approval)
	}
}

func TestResumeInputRejectsInvalidInterruptFromContext(t *testing.T) {
	req := &httpapi.SubmitInputHTTPRequest{
		ResumeRef: &httpcommon.ResumeRef{
			TurnID:       "turn-1",
			CheckpointID: "checkpoint-1",
			InterruptID:  "interrupt-1",
		},
	}
	ctx := ContextWithResumeInterrupt(context.Background(), &ResumeInterruptInput{
		Kind: "follow_up",
	})

	if _, err := resumeInput(ctx, req); err == nil {
		t.Fatal("resumeInput() error = nil, want invalid interrupt error")
	}
}

func TestResumeInputContentFallbackStillUsesPlanInputSchema(t *testing.T) {
	content := "answer"
	req := &httpapi.SubmitInputHTTPRequest{
		Content: &content,
		ResumeRef: &httpcommon.ResumeRef{
			TurnID:       "turn-1",
			CheckpointID: "checkpoint-1",
			InterruptID:  "interrupt-1",
		},
	}

	got, err := resumeInput(context.Background(), req)
	if err != nil {
		t.Fatalf("resumeInput() error = %v", err)
	}
	if got.RequestUserInput == nil {
		t.Fatalf("RequestUserInput is nil")
	}
	answers := got.RequestUserInput.Answers[protoinput.DefaultTextAnswerID].Answers
	if len(answers) != 1 || answers[0] != "answer" {
		t.Fatalf("answers = %+v", answers)
	}
}
