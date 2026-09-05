//go:build !windows

package thread

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/middleware/planmode"
	"eino-cli/deepagent/worker"
	"github.com/cloudwego/eino/schema"
)

func agentEventPayloadForOutput(ev agentthread.Event, usage *agentthread.ContextUsageSnapshot) (eventType protoevent.EventType, payload any, err error) {
	defer func() {
		if err == nil && payload != nil {
			err = attachConsumedInputs(payload, ev.ConsumedInputs, ev.ConsumedInputsMeta)
		}
	}()
	contextUsage := convertContextUsagePayload(usage)
	switch ev.Type {
	case agentthread.EventTurnStart:
		payload, err := agentEventPayload[agentthread.TurnStartPayload](ev)
		if err != nil {
			return "", nil, err
		}
		out := messageEventPayloadFromTurnStart(payload, ev.ConsumedInputs)
		if out != nil {
			out.ContextUsage = contextUsage
		}
		return protoevent.EventTypeTurnStarted, out, nil
	case agentthread.EventLLMRequesting:
		return "", nil, nil
	case agentthread.EventLLMToken:
		payload, err := agentEventPayload[agentthread.LLMTokenChunk](ev)
		if err != nil {
			return "", nil, err
		}
		return protoevent.EventTypeAssistantDelta, &protoevent.AssistantDeltaEventPayload{
			Delta:                payload.Text,
			ThinkingContentDelta: payload.ReasoningText,
			LLMResponseID:        payload.LLMResponseID,
		}, nil
	case agentthread.EventLLMEnd:
		payload, err := agentEventPayload[agentthread.LLMEnd](ev)
		if err != nil {
			return "", nil, err
		}
		out := &protoevent.MessageEventPayload{
			LLMResponseID: payload.LLMResponseID,
			ContextUsage:  contextUsage,
		}
		if payload.Message != nil {
			out.Parts = schemaAssistantMessageToCloudParts(payload.Message)
			out.ThinkingContent = payload.Message.ReasoningContent
		}
		return protoevent.EventTypeAssistantMessage, out, nil
	case agentthread.EventToolStart:
		payload, err := agentEventPayload[agentthread.ToolStartPayload](ev)
		if err != nil {
			return "", nil, err
		}
		return protoevent.EventTypeToolCallStarted, &protoevent.ToolCallEventPayload{
			ToolCallID:    payload.CallID,
			ToolName:      payload.Name,
			ArgumentsJSON: stringPtrIfNotEmpty(payload.Args),
			Status:        protoevent.ToolCallStatusStarted,
			ContextUsage:  contextUsage,
		}, nil
	case agentthread.EventToolCallOutputChunk:
		payload, err := agentEventPayload[agentthread.ToolCallOutputChunkPayload](ev)
		if err != nil {
			return "", nil, err
		}
		return protoevent.EventTypeToolCallOutputDelta, &protoevent.ToolCallEventPayload{
			ToolCallID:  payload.CallID,
			ToolName:    payload.Name,
			Status:      protoevent.ToolCallStatusStarted,
			OutputDelta: stringPtrIfNotEmpty(payload.Chunk),
		}, nil
	case agentthread.EventToolEnd:
		payload, err := agentEventPayload[agentthread.ToolEndPayload](ev)
		if err != nil {
			return "", nil, err
		}
		out := &protoevent.ToolCallEventPayload{
			ToolCallID:    payload.CallID,
			ToolName:      payload.Name,
			ArgumentsJSON: stringPtrIfNotEmpty(payload.ArgumentsInJSON),
			ResultJSON:    stringPtrIfNotEmpty(payload.Result),
			Status:        protoevent.ToolCallStatusFinished,
			ContextUsage:  contextUsage,
		}
		if !payload.ToolStartTime.IsZero() && !ev.TS.IsZero() && ev.TS.After(payload.ToolStartTime) {
			elapsed := ev.TS.Sub(payload.ToolStartTime).Milliseconds()
			out.ElapsedMs = &elapsed
		}
		return protoevent.EventTypeToolCallFinished, out, nil
	case agentthread.EventPlanUpdated:
		payload, err := agentEventPayload[agentthread.PlanUpdatedPayload](ev)
		if err != nil {
			return "", nil, err
		}
		out := planUpdatedPayload(payload)
		out.ContextUsage = contextUsage
		return protoevent.EventTypePlanUpdated, out, nil
	case agentthread.EventContextCompactStarted:
		payload, err := agentEventPayload[agentthread.ContextCompactStartedPayload](ev)
		if err != nil {
			return "", nil, err
		}
		compactUsage := convertContextUsagePayload(&payload.ContextUsage)
		if payload.ContextUsage == (agentthread.ContextUsageSnapshot{}) {
			compactUsage = contextUsage
		}
		return protoevent.EventTypeCompactStarted, &protoevent.CompactStartedEventPayload{ContextUsage: compactUsage}, nil
	case agentthread.EventContextCompacted:
		payload, err := agentEventPayload[agentthread.ContextCompactedPayload](ev)
		if err != nil {
			return "", nil, err
		}
		return protoevent.EventTypeContextCompacted, &protoevent.ContextCompactedEventPayload{ContextUsage: convertContextUsagePayload(&payload.After)}, nil
	case agentEventContextCompactInterrupted:
		payload, err := agentEventPayload[contextCompactInterruptedPayload](ev)
		if err != nil {
			return "", nil, err
		}
		return protoevent.EventTypeCompactInterrupted, &protoevent.CompactInterruptedEventPayload{
			Kind:             payload.Kind,
			Reason:           payload.Reason,
			ControlMessageID: payload.ControlMessageID,
			CutoffMessageID:  payload.CutoffMessageID,
		}, nil
	case agentthread.EventApproveRequested:
		payload, err := agentEventPayload[agentthread.ApprovalRequiredPayload](ev)
		if err != nil {
			return "", nil, err
		}
		return protoevent.EventTypeApprovalRequired, convertApprovalRequiredPayload(payload), nil
	case agentthread.EventFollowUpRequested:
		payload, err := agentEventPayload[agentthread.FollowUpRequestedPayload](ev)
		if err != nil {
			return "", nil, err
		}
		return protoevent.EventTypeInterruptRequired, followUpRequiredPayload(payload), nil
	case agentthread.EventInterrupted:
		payload, err := agentEventPayload[agentthread.InterruptedPayload](ev)
		if err != nil {
			return "", nil, err
		}
		if isExternalInterrupt(payload) {
			return protoevent.EventTypeTurnInterrupted, &protoevent.ErrorEventPayload{Message: interruptedMessage(payload), ContextUsage: contextUsage}, nil
		}
		if info, ok := payload.Info.(*planmode.RequestUserInputInfo); ok {
			return protoevent.EventTypePlanInputRequired, planInputRequiredPayload(payload, info), nil
		}
		if isRecoverableRuntimeInterrupt(payload) {
			out, err := interruptRequiredPayload(payload)
			if err != nil {
				return "", nil, err
			}
			return protoevent.EventTypeInterruptRequired, out, nil
		}
		return protoevent.EventTypeTurnInterrupted, &protoevent.ErrorEventPayload{Message: interruptedMessage(payload), ContextUsage: contextUsage}, nil
	case agentthread.EventTurnEnd:
		if _, err := agentEventPayload[agentthread.TurnEndPayload](ev); err != nil {
			return "", nil, err
		}
		return protoevent.EventTypeTurnFinished, &protoevent.TurnFinishedEventPayload{ContextUsage: contextUsage}, nil
	case agentthread.EventError:
		out := convertErrorPayload(ev.Payload)
		out.ContextUsage = contextUsage
		return protoevent.EventTypeError, out, nil
	default:
		return "", nil, nil
	}
}

func attachConsumedInputs(payload any, inputs []*schema.Message, meta []any) (err error) {
	consumed := ConsumedMessageIDs(inputs)
	copied, err := consumedInputsMetadataForCloudAgent(meta)
	if len(consumed) == 0 && len(copied) == 0 {
		return err
	}
	switch p := payload.(type) {
	case *protoevent.MessageEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	case *protoevent.AssistantDeltaEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	case *protoevent.ToolCallEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	case *protoevent.ApprovalRequiredEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	case *protoevent.InterruptRequiredEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	case *protoevent.PlanUpdatedEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	case *protoevent.PlanInputRequiredEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	case *protoevent.CompactStartedEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	case *protoevent.ContextCompactedEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	case *protoevent.ErrorEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	case *protoevent.CompactInterruptedEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	case *protoevent.TurnFinishedEventPayload:
		p.ConsumedMessageIDs, p.ConsumedInputsMeta = consumed, copied
	}
	return err
}

func consumedInputsMetadataForCloudAgent(meta []any) ([]map[string]string, error) {
	if len(meta) == 0 {
		return nil, nil
	}
	out := make([]map[string]string, len(meta))
	hasValue := false
	for i, item := range meta {
		if item == nil {
			continue
		}
		typed, ok := item.(map[string]string)
		if !ok {
			return nil, fmt.Errorf("consumed input metadata at index %d has type %T, want map[string]string", i, item)
		}
		hasValue = true
		out[i] = maps.Clone(typed)
	}
	if !hasValue {
		return nil, nil
	}
	return out, nil
}

func agentEventPayload[T any](ev agentthread.Event) (T, error) {
	payload, ok := ev.Payload.(T)
	if ok {
		return payload, nil
	}
	var zero T
	return zero, fmt.Errorf("%s payload type mismatch: %T", ev.Type, ev.Payload)
}

func planUpdatedPayload(payload agentthread.PlanUpdatedPayload) *protoevent.PlanUpdatedEventPayload {
	items := make([]*protoevent.PlanItem, len(payload.Plan))
	for i, step := range payload.Plan {
		id := strconv.Itoa(i + 1)
		items[i] = &protoevent.PlanItem{
			ID:      id,
			Content: step.Step,
			Status:  string(step.Status),
		}
	}
	return &protoevent.PlanUpdatedEventPayload{
		Explanation: stringPtrIfNotEmpty(payload.Explanation),
		Items:       items,
	}
}

func planInputRequiredPayload(payload agentthread.InterruptedPayload, info *planmode.RequestUserInputInfo) *protoevent.PlanInputRequiredEventPayload {
	if info == nil {
		return nil
	}
	questions := make([]*protoevent.PlanInputQuestion, len(info.Questions))
	for i, question := range info.Questions {
		options := make([]*protoevent.PlanInputQuestionOption, len(question.Options))
		for j, option := range question.Options {
			options[j] = &protoevent.PlanInputQuestionOption{
				Label:       option.Label,
				Description: option.Description,
			}
		}
		questions[i] = &protoevent.PlanInputQuestion{
			ID:       question.ID,
			Header:   question.Header,
			Question: question.Question,
			Options:  options,
		}
	}
	return &protoevent.PlanInputRequiredEventPayload{
		InterruptID:  payload.InterruptID,
		CheckpointID: payload.CheckpointID,
		Questions:    questions,
	}
}

func convertContextUsagePayload(snapshot *agentthread.ContextUsageSnapshot) *protoevent.ContextUsage {
	if snapshot == nil {
		return nil
	}
	var ratio *float64
	if snapshot.ContextWindow > 0 && snapshot.CurrentTotal > 0 {
		value := float64(snapshot.CurrentTotal) / float64(snapshot.ContextWindow)
		ratio = &value
	}
	return &protoevent.ContextUsage{
		UsedTokens:       snapshot.CurrentTotal,
		MaxTokens:        int64PtrIfPositive(snapshot.ContextWindow),
		Ratio:            ratio,
		PromptTokens:     int64PtrIfPositive(snapshot.LastModelPromptTokens),
		CompletionTokens: int64PtrIfPositive(snapshot.LastModelCompletionTokens),
	}
}

func convertApprovalRequiredPayload(payload agentthread.ApprovalRequiredPayload) *protoevent.ApprovalRequiredEventPayload {
	out := &protoevent.ApprovalRequiredEventPayload{
		InterruptID:  payload.InterruptID,
		CheckpointID: payload.CheckpointID,
	}
	if payload.ApprovalInfo != nil {
		out.ToolName = payload.ApprovalInfo.ToolName
		out.ArgumentsJSON = stringPtrIfNotEmpty(payload.ApprovalInfo.ArgumentsInJSON)
		return out
	}
	if payload.ReviewEditInfo != nil {
		out.ToolName = payload.ReviewEditInfo.ToolName
		out.ArgumentsJSON = stringPtrIfNotEmpty(payload.ReviewEditInfo.ArgumentsInJSON)
		return out
	}
	return out
}

func followUpRequiredPayload(payload agentthread.FollowUpRequestedPayload) *protoevent.InterruptRequiredEventPayload {
	info := struct {
		Questions []string `json:"questions,omitempty"`
	}{}
	if payload.Info != nil {
		info.Questions = append([]string(nil), payload.Info.Questions...)
	}
	raw, _ := json.Marshal(info)
	return &protoevent.InterruptRequiredEventPayload{
		InterruptID:  payload.InterruptID,
		CheckpointID: payload.CheckpointID,
		Kind:         "follow_up",
		InfoType:     fmt.Sprintf("%T", payload.Info),
		Info:         raw,
	}
}

func interruptRequiredPayload(payload agentthread.InterruptedPayload) (*protoevent.InterruptRequiredEventPayload, error) {
	raw, err := json.Marshal(payload.Info)
	if err != nil {
		return nil, fmt.Errorf("marshal interrupt info: info_type=%s: %w", payload.InfoType, err)
	}
	return &protoevent.InterruptRequiredEventPayload{
		InterruptID:  payload.InterruptID,
		CheckpointID: payload.CheckpointID,
		Kind:         interruptKind(payload),
		InfoType:     payload.InfoType,
		Info:         raw,
	}, nil
}

func convertErrorPayload(payload any) *protoevent.ErrorEventPayload {
	switch p := payload.(type) {
	case agentthread.ErrorPayload:
		return &protoevent.ErrorEventPayload{Message: p.Message}
	case *agentthread.ErrorPayload:
		if p == nil {
			return &protoevent.ErrorEventPayload{}
		}
		return &protoevent.ErrorEventPayload{Message: p.Message}
	default:
		return &protoevent.ErrorEventPayload{Message: fmt.Sprint(payload)}
	}
}

func interruptedMessage(payload agentthread.InterruptedPayload) string {
	if payload.Source == "external" && payload.Metadata["kind"] == string(agentworker.ThreadInterruptKindWorkerShutdownTimeout) {
		if reason := strings.TrimSpace(payload.Metadata["reason"]); reason != "" {
			return reason
		}
		return "worker shutdown timeout"
	}
	if payload.Source != "" {
		return "interrupted by " + payload.Source
	}
	return "interrupted"
}

func isExternalInterrupt(payload agentthread.InterruptedPayload) bool {
	return payload.Source == "external"
}

func isRecoverableRuntimeInterrupt(payload agentthread.InterruptedPayload) bool {
	return payload.Source != "external" && payload.InterruptID != "" && payload.CheckpointID != ""
}

func interruptKind(payload agentthread.InterruptedPayload) string {
	if payload.InfoType != "" {
		return "custom"
	}
	if payload.Source != "" {
		return payload.Source
	}
	return "interrupt"
}

func stringPtrIfNotEmpty(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func int64PtrIfPositive(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}
