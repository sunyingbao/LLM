//go:build !windows

package thread

import (
	"context"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/core/middleware/planmode"
	deeptools "eino-cli/deepagent/core/tools"
	agentworker "eino-cli/deepagent/worker"
	"encoding/json"
	"fmt"
	"strings"
)

func parseResumePayload(message *agentworker.Message) (protoinput.ResumeTurnPayload, error) {
	var payload protoinput.ResumeTurnPayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return protoinput.ResumeTurnPayload{}, fmt.Errorf("unmarshal resume payload: %w", err)
	}
	if err := payload.Validate(); err != nil {
		return protoinput.ResumeTurnPayload{}, err
	}
	return payload, nil
}

func resumeData(ctx context.Context, payload protoinput.ResumeTurnPayload, interruptResume InterruptResumeDecoder) (map[string]any, error) {
	out := map[string]any{}
	switch {
	case payload.Approval != nil:
		out[payload.InterruptID] = approvalResult(payload.Approval)
	case payload.RequestUserInput != nil:
		out[payload.InterruptID] = planInputResponse(payload.RequestUserInput)
	case payload.Interrupt != nil:
		data, err := interruptResumeData(ctx, payload, interruptResume)
		if err != nil {
			return nil, err
		}
		out[payload.InterruptID] = data
	default:
		return nil, fmt.Errorf("resume payload requires approval, request_user_input or interrupt")
	}
	return out, nil
}

func approvalResult(decision *protoinput.ApprovalDecision) *deeptools.ApprovalResult {
	if decision == nil {
		return &deeptools.ApprovalResult{}
	}
	out := &deeptools.ApprovalResult{Approved: decision.Approved}
	if decision.Reason != "" {
		out.DisapproveReason = &decision.Reason
	}
	return out
}

func planInputResponse(response *protoinput.RequestUserInputResponse) *planmode.RequestUserInputResponse {
	if response == nil {
		return nil
	}
	answers := make(map[string]planmode.RequestUserInputAnswer, len(response.Answers))
	for key, answer := range response.Answers {
		answers[key] = planmode.RequestUserInputAnswer{Answers: append([]string(nil), answer.Answers...)}
	}
	return &planmode.RequestUserInputResponse{Answers: answers}
}

func interruptResumeData(ctx context.Context, payload protoinput.ResumeTurnPayload, decoder InterruptResumeDecoder) (any, error) {
	if payload.Interrupt == nil {
		return nil, fmt.Errorf("interrupt resume payload is required")
	}
	switch strings.TrimSpace(payload.Interrupt.Kind) {
	case "follow_up":
		var body struct {
			UserAnswer string `json:"user_answer"`
		}
		if err := json.Unmarshal(payload.Interrupt.Data, &body); err != nil {
			return nil, fmt.Errorf("decode follow_up resume data: %w", err)
		}
		answer := strings.TrimSpace(body.UserAnswer)
		if answer == "" {
			return nil, fmt.Errorf("follow_up.user_answer is required")
		}
		return &deeptools.FollowUpInfo{UserAnswer: answer}, nil
	default:
		if decoder == nil {
			return nil, fmt.Errorf("unsupported interrupt resume kind=%q info_type=%q", payload.Interrupt.Kind, payload.Interrupt.InfoType)
		}
		data, err := decoder(ctx, payload)
		if err != nil {
			return nil, fmt.Errorf("decode interrupt resume kind=%q info_type=%q: %w", payload.Interrupt.Kind, payload.Interrupt.InfoType, err)
		}
		return data, nil
	}
}
