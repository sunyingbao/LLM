package event

import (
	"encoding/json"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
)

type EventType string

const (
	EventTypeAssistantMessage    EventType = "ASSISTANT_MESSAGE"
	EventTypeAssistantDelta      EventType = "ASSISTANT_DELTA"
	EventTypeToolCallStarted     EventType = "TOOL_CALL_STARTED"
	EventTypeToolCallOutputDelta EventType = "TOOL_CALL_OUTPUT_DELTA"
	EventTypeToolCallFinished    EventType = "TOOL_CALL_FINISHED"
	EventTypeApprovalRequired    EventType = "APPROVAL_REQUIRED"
	EventTypeInterruptRequired   EventType = "INTERRUPT_REQUIRED"
	EventTypePlanUpdated         EventType = "PLAN_UPDATED"
	EventTypePlanInputRequired   EventType = "PLAN_INPUT_REQUIRED"
	EventTypeCompactStarted      EventType = "COMPACT_STARTED"
	EventTypeContextCompacted    EventType = "CONTEXT_COMPACTED"
	EventTypeTurnStarted         EventType = "TURN_STARTED"
	EventTypeTurnFinished        EventType = "TURN_FINISHED"
	EventTypeTurnInterrupted     EventType = "TURN_INTERRUPTED"
	EventTypeError               EventType = "ERROR"
	EventTypeCompactInterrupted  EventType = "COMPACT_INTERRUPTED"
)

func (t EventType) String() string {
	return string(t)
}

type ToolCallStatus int64

const (
	ToolCallStatusStarted  ToolCallStatus = 1
	ToolCallStatusFinished ToolCallStatus = 2
	ToolCallStatusFailed   ToolCallStatus = 3
)

type SenderType int64

const (
	SenderTypeUser   SenderType = 1
	SenderTypeSystem SenderType = 2
	SenderTypeAgent  SenderType = 3
)

type InputMode int64

const (
	InputModeImplPlan       InputMode = 1
	InputModeCompactContext InputMode = 2
)

type MessagePart = protoinput.MessagePart
type MessagePartType = protoinput.MessagePartType

const (
	MessagePartTypeText  = protoinput.MessagePartTypeText
	MessagePartTypeImage = protoinput.MessagePartTypeImage
	MessagePartTypeAudio = protoinput.MessagePartTypeAudio
	MessagePartTypeVideo = protoinput.MessagePartTypeVideo
	MessagePartTypeFile  = protoinput.MessagePartTypeFile
)

type MessageEventPayload struct {
	Parts              []MessagePart       `json:"parts,omitempty"`
	ThinkingContent    string              `json:"thinking_content,omitempty"`
	LLMResponseID      string              `json:"llm_response_id,omitempty"`
	Sender             *Sender             `json:"sender,omitempty"`
	MessageID          *string             `json:"message_id,omitempty"`
	Mode               *InputMode          `json:"mode,omitempty"`
	ConsumedMessageIDs []string            `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta,omitempty"`
	ContextUsage       *ContextUsage       `json:"context_usage,omitempty"`
}

type Sender struct {
	SenderType SenderType `json:"sender_type"`
	SenderID   string     `json:"sender_id"`
}

type AssistantDeltaEventPayload struct {
	Delta                string              `json:"delta"`
	ThinkingContentDelta string              `json:"thinking_content_delta,omitempty"`
	LLMResponseID        string              `json:"llm_response_id,omitempty"`
	MessageID            *string             `json:"message_id,omitempty"`
	ConsumedMessageIDs   []string            `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta   []map[string]string `json:"consumed_inputs_meta,omitempty"`
}

type ToolCallEventPayload struct {
	ToolCallID         string              `json:"tool_call_id"`
	ToolName           string              `json:"tool_name"`
	ArgumentsJSON      *string             `json:"arguments_json,omitempty"`
	ResultJSON         *string             `json:"result_json,omitempty"`
	Status             ToolCallStatus      `json:"status"`
	ErrorMessage       *string             `json:"error_message,omitempty"`
	ElapsedMs          *int64              `json:"elapsed_ms,omitempty"`
	OutputDelta        *string             `json:"output_delta,omitempty"`
	ConsumedMessageIDs []string            `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta,omitempty"`
	ContextUsage       *ContextUsage       `json:"context_usage,omitempty"`
}

type ApprovalRequiredEventPayload struct {
	InterruptID        string              `json:"interrupt_id"`
	CheckpointID       string              `json:"checkpoint_id"`
	ToolName           string              `json:"tool_name"`
	ArgumentsJSON      *string             `json:"arguments_json,omitempty"`
	ConsumedMessageIDs []string            `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta,omitempty"`
}

type InterruptRequiredEventPayload struct {
	InterruptID        string              `json:"interrupt_id"`
	CheckpointID       string              `json:"checkpoint_id"`
	Kind               string              `json:"kind,omitempty"`
	InfoType           string              `json:"info_type,omitempty"`
	Info               json.RawMessage     `json:"info,omitempty"`
	ConsumedMessageIDs []string            `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta,omitempty"`
}

type PlanItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type PlanUpdatedEventPayload struct {
	PlanID             *string             `json:"plan_id,omitempty"`
	Items              []*PlanItem         `json:"items"`
	Explanation        *string             `json:"explanation,omitempty"`
	ConsumedMessageIDs []string            `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta,omitempty"`
	ContextUsage       *ContextUsage       `json:"context_usage,omitempty"`
}

type PlanInputQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type PlanInputQuestion struct {
	ID       string                     `json:"id"`
	Header   string                     `json:"header"`
	Question string                     `json:"question"`
	Options  []*PlanInputQuestionOption `json:"options"`
}

type PlanInputRequiredEventPayload struct {
	InterruptID        string               `json:"interrupt_id"`
	CheckpointID       string               `json:"checkpoint_id"`
	Questions          []*PlanInputQuestion `json:"questions"`
	ConsumedMessageIDs []string             `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta []map[string]string  `json:"consumed_inputs_meta,omitempty"`
}

type ContextUsage struct {
	UsedTokens       int64    `json:"used_tokens"`
	MaxTokens        *int64   `json:"max_tokens,omitempty"`
	Ratio            *float64 `json:"ratio,omitempty"`
	PromptTokens     *int64   `json:"prompt_tokens,omitempty"`
	CompletionTokens *int64   `json:"completion_tokens,omitempty"`
}

type ContextCompactedEventPayload struct {
	ConsumedMessageIDs []string            `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta,omitempty"`
	ContextUsage       *ContextUsage       `json:"context_usage,omitempty"`
}

type CompactStartedEventPayload struct {
	ConsumedMessageIDs []string            `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta,omitempty"`
	ContextUsage       *ContextUsage       `json:"context_usage,omitempty"`
}

type ErrorEventPayload struct {
	Message            string              `json:"message"`
	Code               *string             `json:"code,omitempty"`
	Detail             *string             `json:"detail,omitempty"`
	Retryable          *bool               `json:"retryable,omitempty"`
	ConsumedMessageIDs []string            `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta,omitempty"`
	ContextUsage       *ContextUsage       `json:"context_usage,omitempty"`
}

type CompactInterruptedEventPayload struct {
	Kind               string              `json:"kind,omitempty"`
	Reason             string              `json:"reason,omitempty"`
	ControlMessageID   string              `json:"control_message_id,omitempty"`
	CutoffMessageID    string              `json:"cutoff_message_id,omitempty"`
	ConsumedMessageIDs []string            `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta,omitempty"`
}

type TurnFinishedEventPayload struct {
	ConsumedMessageIDs []string            `json:"consumed_message_ids,omitempty"`
	ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta,omitempty"`
	ContextUsage       *ContextUsage       `json:"context_usage,omitempty"`
}
