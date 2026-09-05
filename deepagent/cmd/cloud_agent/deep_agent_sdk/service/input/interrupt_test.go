package input

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	httpapi "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/deep_agent_sdk"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/deep_agent_sdk_common"
)

func TestResumeInputUsesInterruptFromContext(t *testing.T) {
	req := &httpapi.SubmitInputHTTPRequest{
		ResumeRef: &httpcommon.ResumeRef{
			TurnID:       "turn-1",
			CheckpointID: "checkpoint-1",
			InterruptID:  "interrupt-1",
		},
	}
	ctx := ContextWithResumeInputExtensions(context.Background(), &ResumeInputExtensions{
		Interrupt: &protoinput.InterruptResumePayload{
			Kind: "follow_up",
			Data: json.RawMessage(`{"user_answer":"yes"}`),
		},
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
	ctx := ContextWithResumeInputExtensions(context.Background(), &ResumeInputExtensions{
		Interrupt: &protoinput.InterruptResumePayload{Kind: "follow_up"},
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

func TestResumeInputPreservesRawStructuredAnswersAndConsumedMessageIDs(t *testing.T) {
	req := &httpapi.SubmitInputHTTPRequest{ResumeRef: &httpcommon.ResumeRef{
		TurnID: "turn-1", CheckpointID: "checkpoint-1", InterruptID: "interrupt-1",
	}}
	ctx := ContextWithResumeInputExtensions(context.Background(), &ResumeInputExtensions{
		RequestUserInput: &protoinput.RequestUserInputResponse{Answers: map[string]protoinput.RequestUserInputAnswer{
			"environment": {Answers: []string{"staging"}},
			"regions":     {Answers: []string{"eu-west", "ap-south"}},
		}},
		ConsumedMessageIDs: []string{"message-1", "message-2"},
	})

	got, err := resumeInput(ctx, req)
	if err != nil {
		t.Fatalf("resumeInput() error = %v", err)
	}
	if got.RequestUserInput == nil || len(got.RequestUserInput.Answers) != 2 {
		t.Fatalf("RequestUserInput = %+v", got.RequestUserInput)
	}
	if answers := got.RequestUserInput.Answers["regions"].Answers; len(answers) != 2 || answers[0] != "eu-west" || answers[1] != "ap-south" {
		t.Fatalf("regions answers = %v", answers)
	}
	if len(got.ConsumedMessageIDs) != 2 || got.ConsumedMessageIDs[1] != "message-2" {
		t.Fatalf("ConsumedMessageIDs = %v", got.ConsumedMessageIDs)
	}
}

func TestResumeInputPreservesRawApprovalCancellation(t *testing.T) {
	approved := false
	req := &httpapi.SubmitInputHTTPRequest{
		ResumeRef: &httpcommon.ResumeRef{TurnID: "turn-1", CheckpointID: "checkpoint-1", InterruptID: "interrupt-1"},
		Approval:  &httpcommon.ApprovalInput{Approved: approved},
	}
	ctx := ContextWithResumeInputExtensions(context.Background(), &ResumeInputExtensions{
		ApprovalCancelTurn: true,
		ConsumedMessageIDs: []string{"message-1"},
	})

	got, err := resumeInput(ctx, req)
	if err != nil {
		t.Fatalf("resumeInput() error = %v", err)
	}
	if got.Approval == nil || got.Approval.Approved || !got.Approval.CancelTurn {
		t.Fatalf("Approval = %+v", got.Approval)
	}
	if len(got.ConsumedMessageIDs) != 1 || got.ConsumedMessageIDs[0] != "message-1" {
		t.Fatalf("ConsumedMessageIDs = %v", got.ConsumedMessageIDs)
	}
}

func TestResumeInputRejectsMixedRawResumeBodies(t *testing.T) {
	content := "fallback"
	req := &httpapi.SubmitInputHTTPRequest{
		Content:   &content,
		ResumeRef: &httpcommon.ResumeRef{TurnID: "turn-1", CheckpointID: "checkpoint-1", InterruptID: "interrupt-1"},
	}
	ctx := ContextWithResumeInputExtensions(context.Background(), &ResumeInputExtensions{
		RequestUserInput: &protoinput.RequestUserInputResponse{Answers: map[string]protoinput.RequestUserInputAnswer{
			"scope": {Answers: []string{"SDK only"}},
		}},
	})

	if _, err := resumeInput(ctx, req); err == nil {
		t.Fatal("resumeInput() error = nil, want mixed resume body rejection")
	}
}

func TestResumeInputRejectsApprovalCombinedWithInterrupt(t *testing.T) {
	req := &httpapi.SubmitInputHTTPRequest{
		ResumeRef: &httpcommon.ResumeRef{TurnID: "turn-1", CheckpointID: "checkpoint-1", InterruptID: "interrupt-1"},
		Approval:  &httpcommon.ApprovalInput{Approved: false},
	}
	ctx := ContextWithResumeInputExtensions(context.Background(), &ResumeInputExtensions{
		Interrupt: &protoinput.InterruptResumePayload{Kind: "follow_up", Data: json.RawMessage(`{"user_answer":"yes"}`)},
	})
	if _, err := resumeInput(ctx, req); err == nil {
		t.Fatal("mixed approval and interrupt accepted")
	}
}

func TestSubmitRejectsResumeBodyBeforeLoadingSession(t *testing.T) {
	content := "hello"
	req := &httpapi.SubmitInputHTTPRequest{SessionID: 1, Content: &content, Approval: &httpcommon.ApprovalInput{Approved: false}}
	if _, err := Submit(context.Background(), 1, req); err == nil || !strings.Contains(err.Error(), "resume_ref") {
		t.Fatalf("Submit() error = %v, want missing resume_ref", err)
	}
}
