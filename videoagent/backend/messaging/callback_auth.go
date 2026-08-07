package messaging

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AllowAllCallbackVerifier is used only by the deterministic local application.
type AllowAllCallbackVerifier struct{}

func (AllowAllCallbackVerifier) Verify(context.Context, string, []byte, http.Header) error {
	return nil
}

// HMACCallbackVerifier authenticates the raw callback body with SHA-256 HMAC.
type HMACCallbackVerifier struct {
	Secret string
}

func (verifier HMACCallbackVerifier) Verify(_ context.Context, provider string, body []byte, header http.Header) error {
	if strings.TrimSpace(verifier.Secret) == "" {
		return fmt.Errorf("callback secret is empty for provider %s", provider)
	}
	want := hmac.New(sha256.New, []byte(verifier.Secret))
	_, _ = want.Write(body)
	actual := strings.TrimSpace(header.Get("X-Callback-Signature"))
	actual = strings.TrimPrefix(actual, "sha256=")
	wantHex := hex.EncodeToString(want.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(actual)), []byte(wantHex)) {
		return fmt.Errorf("invalid callback signature for provider %s", provider)
	}
	return nil
}

func ParseCallbackMessageWithEventID(provider string, body []byte, headerEventID string) (CallbackMessage, error) {
	var payload struct {
		Provider  string          `json:"provider"`
		EventID   json.RawMessage `json:"event_id"`
		JobID     json.RawMessage `json:"job_id"`
		TaskID    json.RawMessage `json:"task_id"`
		SubmitKey json.RawMessage `json:"submit_key"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return CallbackMessage{}, err
	}
	jobID := jsonID(payload.JobID)
	if jobID == "" {
		jobID = jsonID(payload.TaskID)
	}
	submitKey := jsonID(payload.SubmitKey)
	if jobID == "" && submitKey == "" {
		return CallbackMessage{}, fmt.Errorf("callback job_id, task_id or submit_key is required")
	}
	eventID := jsonID(payload.EventID)
	if eventID == "" {
		eventID = strings.TrimSpace(headerEventID)
	}
	if eventID == "" {
		digest := sha256.Sum256(body)
		eventID = hex.EncodeToString(digest[:])
	}
	if strings.TrimSpace(payload.Provider) != "" {
		provider = strings.TrimSpace(payload.Provider)
	}
	if strings.TrimSpace(provider) == "" {
		return CallbackMessage{}, fmt.Errorf("callback provider is required")
	}
	return CallbackMessage{Provider: provider, EventID: eventID, JobID: jobID, SubmitKey: submitKey}, nil
}

func jsonID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(string(raw))
}
