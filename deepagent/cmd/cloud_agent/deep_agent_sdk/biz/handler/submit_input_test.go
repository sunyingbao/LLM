package handler

import (
	"strings"
	"testing"
)

func TestSubmitInputExtensionsParseInterrupt(t *testing.T) {
	got, err := submitInputExtensions([]byte(`{"resume_ref":{"turn_id":"t"},"interrupt":{"kind":"follow_up","data":{"user_answer":"yes"}}}`))
	if err != nil || got == nil || got.Interrupt == nil {
		t.Fatalf("submitInputExtensions() = %+v, %v", got, err)
	}
	if got.Interrupt.Kind != "follow_up" {
		t.Fatalf("Kind = %q", got.Interrupt.Kind)
	}
	if string(got.Interrupt.Data) != `{"user_answer":"yes"}` {
		t.Fatalf("Data = %s", got.Interrupt.Data)
	}
}

func TestSubmitInputExtensionsSkipOrdinaryInput(t *testing.T) {
	for _, body := range [][]byte{
		nil,
		[]byte(`{"content":"hello"}`),
	} {
		if got, err := submitInputExtensions(body); got != nil || err != nil {
			t.Fatalf("submitInputExtensions(%s)=%+v, %v; want nil, nil", body, got, err)
		}
	}
}

func TestSubmitInputExtensionsParseStructuredResumeBody(t *testing.T) {
	got, err := submitInputExtensions([]byte(`{
		"resume_ref":{"turn_id":"turn-1","checkpoint_id":"checkpoint-1","interrupt_id":"interrupt-1"},
		"request_user_input":{"answers":{"scope":{"answers":["SDK only"]}}},
		"consumed_message_ids":["message-1","message-2"]
	}`))
	if err != nil {
		t.Fatalf("submitInputExtensions() error = %v", err)
	}
	if got == nil || got.RequestUserInput == nil {
		t.Fatalf("extensions = %+v", got)
	}
	if answers := got.RequestUserInput.Answers["scope"].Answers; len(answers) != 1 || answers[0] != "SDK only" {
		t.Fatalf("scope answers = %v", answers)
	}
	if len(got.ConsumedMessageIDs) != 2 || got.ConsumedMessageIDs[1] != "message-2" {
		t.Fatalf("consumed IDs = %v", got.ConsumedMessageIDs)
	}
}

func TestSubmitInputExtensionsRejectsMalformedAndUnboundExtensions(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
		want string
	}{
		{name: "wrong answer type", body: `{"resume_ref":{"turn_id":"t"},"request_user_input":{"answers":{"scope":{"answers":"SDK"}}}}`, want: "request_user_input"},
		{name: "wrong consumed type", body: `{"resume_ref":{"turn_id":"t"},"consumed_message_ids":"message-1"}`, want: "consumed_message_ids"},
		{name: "extension without resume", body: `{"session_id":"1","request_user_input":{"answers":{"scope":{"answers":["SDK"]}}}}`, want: "resume_ref"},
		{name: "null resume", body: `{"resume_ref":null,"approval":{"approved":false}}`, want: "resume_ref"},
		{name: "malformed JSON", body: `{bad json`, want: "invalid character"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := submitInputExtensions([]byte(testCase.body))
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("submitInputExtensions() error = %v, want containing %q", err, testCase.want)
			}
		})
	}
}

func TestSubmitInputExtensionsPreserveApprovalCancellation(t *testing.T) {
	got, err := submitInputExtensions([]byte(`{"resume_ref":{"turn_id":"t"},"approval":{"approved":false,"cancel_turn":true}}`))
	if err != nil || got == nil || !got.ApprovalCancelTurn {
		t.Fatalf("submitInputExtensions() = %+v, %v", got, err)
	}
}
