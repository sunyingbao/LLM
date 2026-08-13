package main

import (
	"encoding/json"
	"strconv"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/middleware/planmode"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/worker/tasktool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

const (
	messageExtraMessageID       = "message_id"
	messageExtraIncomingMessage = "incoming_message"

	localMessageTypeResume = "deepagent.resume"
	localTurnModeKey       = "turn_mode"
	localTurnModePlan      = "plan"
)

type localIncomingMessage struct {
	MessageID string            `json:"message_id"`
	Content   string            `json:"content"`
	Sender    *localSender      `json:"sender,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type localSender struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type localEventPayload struct {
	Text               string                         `json:"text,omitempty"`
	ReasoningText      string                         `json:"reasoning_text,omitempty"`
	LLMResponseID      string                         `json:"llm_response_id,omitempty"`
	Message            string                         `json:"message,omitempty"`
	Name               string                         `json:"name,omitempty"`
	CallID             string                         `json:"call_id,omitempty"`
	Args               string                         `json:"args,omitempty"`
	Result             string                         `json:"result,omitempty"`
	Usage              float64                        `json:"usage,omitempty"`
	PromptTokens       int                            `json:"prompt_tokens,omitempty"`
	CompletionTokens   int                            `json:"completion_tokens,omitempty"`
	TotalTokens        int                            `json:"total_tokens,omitempty"`
	ConsumedMessageIDs []string                       `json:"consumed_message_ids,omitempty"`
	InterruptID        string                         `json:"interrupt_id,omitempty"`
	CheckpointID       string                         `json:"checkpoint_id,omitempty"`
	ToolName           string                         `json:"tool_name,omitempty"`
	ArgumentsInJSON    string                         `json:"arguments_in_json,omitempty"`
	ApprovalInfo       *deeptools.ApprovalInfo        `json:"approval_info,omitempty"`
	RequestUserInput   *planmode.RequestUserInputInfo `json:"request_user_input,omitempty"`
	Raw                any                            `json:"raw,omitempty"`
}

type localResumePayload struct {
	TurnID           string                             `json:"turn_id"`
	CheckpointID     string                             `json:"checkpoint_id"`
	InterruptID      string                             `json:"interrupt_id"`
	ToolName         string                             `json:"tool_name,omitempty"`
	ArgumentsInJSON  string                             `json:"arguments_in_json,omitempty"`
	Approval         *localApprovalDecision             `json:"approval,omitempty"`
	RequestUserInput *planmode.RequestUserInputResponse `json:"request_user_input,omitempty"`
}

type localApprovalDecision struct {
	Approved       bool   `json:"approved"`
	AllowInSession bool   `json:"allow_in_session,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

func encodeAgentEventPayload(ev agentthread.Event) ([]byte, error) {
	payload := localEventPayload{ConsumedMessageIDs: consumedMessageIDsFromInputs(ev.ConsumedInputs)}
	switch ev.Type {
	case agentthread.EventLLMToken:
		if p, ok := ev.Payload.(agentthread.LLMTokenChunk); ok {
			payload.Text = p.Text
			payload.ReasoningText = p.ReasoningText
			payload.LLMResponseID = p.LLMResponseID
		}
	case agentthread.EventTurnStart:
		if p, ok := ev.Payload.(agentthread.TurnStartPayload); ok && p.Input != nil {
			payload.Message = p.Input.Content
		}
	case agentthread.EventLLMEnd:
		payload.Message = llmEndText(ev.Payload)
		payload.ReasoningText = llmEndReasoningText(ev.Payload)
		payload.Raw = ev.Payload
		if p, ok := ev.Payload.(agentthread.LLMEnd); ok {
			payload.LLMResponseID = p.LLMResponseID
		}
		if p, ok := ev.Payload.(agentthread.LLMEnd); ok && p.TokenUsage != nil {
			payload.PromptTokens = p.TokenUsage.PromptTokens
			payload.CompletionTokens = p.TokenUsage.CompletionTokens
			payload.TotalTokens = p.TokenUsage.TotalTokens
		}
	case agentthread.EventToolStart:
		if p, ok := ev.Payload.(agentthread.ToolStartPayload); ok {
			payload.Name = p.Name
			payload.CallID = p.CallID
			payload.Args = p.Args
		}
	case agentthread.EventToolCallOutputChunk:
		if p, ok := ev.Payload.(agentthread.ToolCallOutputChunkPayload); ok {
			payload.Name = p.Name
			payload.CallID = p.CallID
			payload.Text = p.Chunk
		}
	case agentthread.EventToolEnd:
		if p, ok := ev.Payload.(agentthread.ToolEndPayload); ok {
			payload.Name = p.Name
			payload.CallID = p.CallID
			payload.ArgumentsInJSON = p.ArgumentsInJSON
			payload.Result = p.Result
		}
	case agentthread.EventApproveRequested:
		if p, ok := ev.Payload.(agentthread.ApprovalRequiredPayload); ok {
			payload.InterruptID = p.InterruptID
			payload.CheckpointID = p.CheckpointID
			payload.ApprovalInfo = p.ApprovalInfo
			if p.ApprovalInfo != nil {
				payload.ToolName = p.ApprovalInfo.ToolName
				payload.ArgumentsInJSON = p.ApprovalInfo.ArgumentsInJSON
			}
		}
	case agentthread.EventInterrupted:
		if p, ok := ev.Payload.(agentthread.InterruptedPayload); ok {
			payload.InterruptID = p.InterruptID
			payload.CheckpointID = p.CheckpointID
			if info, ok := p.Info.(*planmode.RequestUserInputInfo); ok {
				payload.RequestUserInput = info
			}
			payload.Raw = ev.Payload
		}
	case agentthread.EventTurnEnd:
		if p, ok := ev.Payload.(agentthread.TurnEndPayload); ok {
			payload.Usage = p.Usage
		}
	case agentthread.EventError:
		if p, ok := ev.Payload.(agentthread.ErrorPayload); ok {
			payload.Message = p.Message
		}
	default:
		payload.Raw = ev.Payload
	}
	return json.Marshal(payload)
}

func decodeLocalEventPayload(data []byte) localEventPayload {
	var payload localEventPayload
	_ = json.Unmarshal(data, &payload)
	return payload
}

func attachIncomingMessage(msg *schema.Message, incoming localIncomingMessage) {
	if msg == nil {
		return
	}
	if msg.Extra == nil {
		msg.Extra = make(map[string]any, 2)
	}
	msg.Extra[messageExtraMessageID] = incoming.MessageID
	msg.Extra[messageExtraIncomingMessage] = incoming
}

func consumedMessageIDsFromInputs(inputs []*schema.Message) []string {
	ids := make([]string, 0, len(inputs))
	for _, input := range inputs {
		if input == nil || input.Extra == nil {
			continue
		}
		if id, ok := stringMessageID(input.Extra[messageExtraMessageID]); ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func stringMessageID(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, x != ""
	case int64:
		return strconv.FormatInt(x, 10), x != 0
	case int:
		return strconv.Itoa(x), x != 0
	default:
		return "", false
	}
}

func localMessageWaitObserver(events []*tasktool.Event, messageID string) tasktool.MessageWaitResult {
	observations := make([]tasktool.MessageObservation, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		payload := decodeLocalEventPayload(event.Payload)
		observation := tasktool.MessageObservation{
			MessageIDs: payload.ConsumedMessageIDs,
			TurnID:     event.TurnID,
			ObservedAt: event.TS,
		}
		recognized := true
		switch event.Type {
		case string(agentthread.EventTurnStart):
			// A correlation-only observation associates the input message with its turn.
		case string(agentthread.EventLLMEnd):
			observation.Kind = tasktool.MessageObservationResponse
			observation.Result = payload.Message
		case string(agentthread.EventApproveRequested):
			observation.Kind = tasktool.MessageObservationApprovalRequired
			observation.Result = "child task is waiting for approval"
		case string(agentthread.EventFollowUpRequested):
			observation.Kind = tasktool.MessageObservationFollowupRequired
			observation.Result = "child task is waiting for follow-up input"
		case string(agentthread.EventInterrupted):
			observation.Kind = tasktool.MessageObservationInterrupted
			observation.Result = "child task was interrupted"
		case string(agentthread.EventError):
			observation.Kind = tasktool.MessageObservationFailed
			observation.Result = payload.Message
		case string(agentthread.EventTurnEnd):
			observation.Kind = tasktool.MessageObservationCompleted
		default:
			recognized = false
		}
		if recognized {
			observations = append(observations, observation)
		}
	}
	return tasktool.ObserveMessageCompletion(observations, messageID)
}

func llmEndText(payload any) string {
	message := llmEndMessage(payload)
	if message == nil {
		return ""
	}
	return message.Content
}

func llmEndReasoningText(payload any) string {
	message := llmEndMessage(payload)
	if message == nil {
		return ""
	}
	return message.ReasoningContent
}

func llmEndMessage(payload any) *struct {
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
} {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var parsed struct {
		Message *struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.Message == nil {
		return nil
	}
	return parsed.Message
}

func newEventID() string {
	return "ev_" + uuid.NewString()
}
