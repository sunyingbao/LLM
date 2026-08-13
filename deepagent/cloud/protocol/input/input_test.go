package input

import (
	"encoding/json"
	"testing"
)

func TestUserMessageValidatesMultimodalParts(t *testing.T) {
	msg := UserMessage{
		Mode: UserMessageModeImplPlan,
		Parts: []MessagePart{
			{Type: MessagePartTypeText, Text: "describe this image"},
			{Type: MessagePartTypeImage, Base64Data: "base64-image", MIMEType: "image/png", Detail: "auto"},
			{Type: MessagePartTypeFile, URL: "https://example.com/spec.pdf", Name: "spec.pdf"},
		},
	}
	if err := msg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestUserMessageExtraJSONRoundTrip(t *testing.T) {
	raw := []byte(`{
		"mode":"impl_plan",
		"parts":[
			{
				"type":"image",
				"url":"https://example.com/a.png",
				"extra":{"adapter":{"resource_id":"res_1","width":1024}}
			}
		],
		"extra":{"adapter":{"message_id":"msg_1"}}
	}`)
	var msg UserMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got := string(msg.Extra["adapter"]); got != `{"message_id":"msg_1"}` {
		t.Fatalf("message extra = %s", got)
	}
	if got := string(msg.Parts[0].Extra["adapter"]); got != `{"resource_id":"res_1","width":1024}` {
		t.Fatalf("part extra = %s", got)
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var roundTrip UserMessage
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("Unmarshal(roundtrip) error = %v", err)
	}
	if got := string(roundTrip.Extra["adapter"]); got != `{"message_id":"msg_1"}` {
		t.Fatalf("roundtrip message extra = %s", got)
	}
	if got := string(roundTrip.Parts[0].Extra["adapter"]); got != `{"resource_id":"res_1","width":1024}` {
		t.Fatalf("roundtrip part extra = %s", got)
	}
}

func TestUserMessageExtraDoesNotBypassMediaValidation(t *testing.T) {
	msg := UserMessage{
		Parts: []MessagePart{
			{
				Type: MessagePartTypeImage,
				Extra: map[string]json.RawMessage{
					"adapter": json.RawMessage(`{"resource_id":"res_1"}`),
				},
			},
		},
	}
	if err := msg.Validate(); err == nil {
		t.Fatal("Validate() succeeded, want error")
	}
}

func TestUserMessageRejectsInvalidParts(t *testing.T) {
	tests := []UserMessage{
		{},
		{Mode: "unknown", Parts: []MessagePart{{Type: MessagePartTypeText, Text: "hello"}}},
		{Parts: []MessagePart{{Type: MessagePartTypeText}}},
		{Parts: []MessagePart{{Type: MessagePartTypeImage}}},
		{Parts: []MessagePart{{Type: "unknown"}}},
	}
	for _, msg := range tests {
		if err := msg.Validate(); err == nil {
			t.Fatalf("Validate(%+v) succeeded, want error", msg)
		}
	}
}

func TestResumeTurnPayloadValidate(t *testing.T) {
	payload := ResumeTurnPayload{
		TurnID:       "turn-1",
		CheckpointID: "checkpoint-1",
		InterruptID:  "interrupt-1",
		RequestUserInput: &RequestUserInputResponse{
			Answers: map[string]RequestUserInputAnswer{
				DefaultTextAnswerID: {Answers: []string{"yes"}},
			},
		},
	}
	if err := payload.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestResumeTurnPayloadValidateInterrupt(t *testing.T) {
	payload := ResumeTurnPayload{
		TurnID:       "turn-1",
		CheckpointID: "checkpoint-1",
		InterruptID:  "interrupt-1",
		Interrupt: &InterruptResumePayload{
			Kind: "follow_up",
			Data: json.RawMessage(`{"user_answer":"yes"}`),
		},
	}
	if err := payload.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestResumeTurnPayloadRejectsInvalidPayload(t *testing.T) {
	for _, payload := range []ResumeTurnPayload{
		{},
		{TurnID: "t1", CheckpointID: "c1", InterruptID: "i1"},
		{
			TurnID:           "t1",
			CheckpointID:     "c1",
			InterruptID:      "i1",
			Approval:         &ApprovalDecision{Approved: true},
			RequestUserInput: &RequestUserInputResponse{Answers: map[string]RequestUserInputAnswer{DefaultTextAnswerID: {Answers: []string{"yes"}}}},
		},
		{
			TurnID:           "t1",
			CheckpointID:     "c1",
			InterruptID:      "i1",
			RequestUserInput: &RequestUserInputResponse{},
		},
		{
			TurnID:           "t1",
			CheckpointID:     "c1",
			InterruptID:      "i1",
			RequestUserInput: &RequestUserInputResponse{Answers: map[string]RequestUserInputAnswer{DefaultTextAnswerID: {Answers: []string{"yes"}}}},
			Interrupt:        &InterruptResumePayload{Kind: "follow_up", Data: json.RawMessage(`{"user_answer":"yes"}`)},
		},
		{
			TurnID:       "t1",
			CheckpointID: "c1",
			InterruptID:  "i1",
			Interrupt:    &InterruptResumePayload{Kind: "follow_up"},
		},
	} {
		if err := payload.Validate(); err == nil {
			t.Fatalf("Validate(%+v) succeeded, want error", payload)
		}
	}
}
