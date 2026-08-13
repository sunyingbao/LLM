package input

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	MessageTypeInput   = "deepagent.input"
	MessageTypeResume  = "deepagent.resume"
	MessageTypeCompact = "deepagent.compact"
)

const (
	MetadataLogID    = "logid"
	MetadataTurnMode = "turn_mode"
	TurnModePlan     = "plan"

	DefaultTextAnswerID = "content"
)

type UserMessageMode string

const UserMessageModeImplPlan UserMessageMode = "impl_plan"

type UserMessage struct {
	Mode  UserMessageMode            `json:"mode,omitempty"`
	Parts []MessagePart              `json:"parts"`
	Extra map[string]json.RawMessage `json:"extra,omitempty"`
}

func (m UserMessage) Validate() error {
	switch m.Mode {
	case "", UserMessageModeImplPlan:
	default:
		return fmt.Errorf("unsupported user message mode: %s", m.Mode)
	}
	if len(m.Parts) == 0 {
		return fmt.Errorf("parts is required")
	}
	for i, part := range m.Parts {
		if err := part.Validate(); err != nil {
			return fmt.Errorf("parts[%d]: %w", i, err)
		}
	}
	return nil
}

type MessagePartType string

const (
	MessagePartTypeText  MessagePartType = "text"
	MessagePartTypeImage MessagePartType = "image"
	MessagePartTypeAudio MessagePartType = "audio"
	MessagePartTypeVideo MessagePartType = "video"
	MessagePartTypeFile  MessagePartType = "file"
)

type MessagePart struct {
	Type       MessagePartType            `json:"type"`
	Text       string                     `json:"text,omitempty"`
	URL        string                     `json:"url,omitempty"`
	Base64Data string                     `json:"base64_data,omitempty"`
	MIMEType   string                     `json:"mime_type,omitempty"`
	Name       string                     `json:"name,omitempty"`
	Detail     string                     `json:"detail,omitempty"`
	Extra      map[string]json.RawMessage `json:"extra,omitempty"`
}

func (p MessagePart) Validate() error {
	switch p.Type {
	case MessagePartTypeText:
		if strings.TrimSpace(p.Text) == "" {
			return fmt.Errorf("text is required")
		}
	case MessagePartTypeImage, MessagePartTypeAudio, MessagePartTypeVideo, MessagePartTypeFile:
		if strings.TrimSpace(p.URL) == "" && strings.TrimSpace(p.Base64Data) == "" {
			return fmt.Errorf("url or base64_data is required")
		}
	default:
		return fmt.Errorf("unsupported part type: %s", p.Type)
	}
	return nil
}

// ResumeTurnPayload is the JSON payload for MessageTypeResume.
type ResumeTurnPayload struct {
	TurnID             string                    `json:"turn_id"`
	CheckpointID       string                    `json:"checkpoint_id"`
	InterruptID        string                    `json:"interrupt_id"`
	ToolName           string                    `json:"tool_name,omitempty"`
	ArgumentsInJSON    string                    `json:"arguments_in_json,omitempty"`
	ConsumedMessageIDs []string                  `json:"consumed_message_ids,omitempty"`
	Approval           *ApprovalDecision         `json:"approval,omitempty"`
	RequestUserInput   *RequestUserInputResponse `json:"request_user_input,omitempty"`
	Interrupt          *InterruptResumePayload   `json:"interrupt,omitempty"`
}

func (payload ResumeTurnPayload) Validate() error {
	if strings.TrimSpace(payload.TurnID) == "" || strings.TrimSpace(payload.CheckpointID) == "" || strings.TrimSpace(payload.InterruptID) == "" {
		return fmt.Errorf("resume payload requires turn_id, checkpoint_id and interrupt_id")
	}
	bodyCount := 0
	if payload.Approval != nil {
		bodyCount++
	}
	if payload.RequestUserInput != nil {
		bodyCount++
	}
	if payload.Interrupt != nil {
		bodyCount++
	}
	if bodyCount != 1 {
		return fmt.Errorf("resume payload accepts exactly one of approval, request_user_input or interrupt")
	}
	switch {
	case payload.Approval != nil:
		return nil
	case payload.RequestUserInput != nil:
		if len(payload.RequestUserInput.Answers) == 0 {
			return fmt.Errorf("request_user_input.answers is required")
		}
		return nil
	case payload.Interrupt != nil:
		if err := payload.Interrupt.Validate(); err != nil {
			return fmt.Errorf("interrupt: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("resume payload requires approval, request_user_input or interrupt")
	}
}

// ApprovalDecision is the user decision for one HITL approval request.
type ApprovalDecision struct {
	Approved       bool   `json:"approved"`
	Reason         string `json:"reason,omitempty"`
	AllowInSession bool   `json:"allow_in_session,omitempty"`
	CancelTurn     bool   `json:"cancel_turn,omitempty"`
}

type RequestUserInputResponse struct {
	Answers map[string]RequestUserInputAnswer `json:"answers"`
}

type RequestUserInputAnswer struct {
	Answers []string `json:"answers"`
}

type InterruptResumePayload struct {
	Kind     string          `json:"kind,omitempty"`
	InfoType string          `json:"info_type,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

func (payload InterruptResumePayload) Validate() error {
	if strings.TrimSpace(payload.Kind) == "" && strings.TrimSpace(payload.InfoType) == "" {
		return fmt.Errorf("kind or info_type is required")
	}
	if len(payload.Data) == 0 || string(payload.Data) == "null" {
		return fmt.Errorf("data is required")
	}
	return nil
}
