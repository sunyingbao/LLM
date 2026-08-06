package videoagent

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCallbackRequiresVerification(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()
	application.SetCallbackVerifier(nil)

	request := httptest.NewRequest(http.MethodPost, "/callbacks/image", nil)
	response := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("callback status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestHMACCallbackVerifier(t *testing.T) {
	secret := "secret"
	body := []byte(`{"event_id":"event-1","job_id":"job-1"}`)
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write(body)
	header := http.Header{"X-Callback-Signature": []string{hex.EncodeToString(digest.Sum(nil))}}
	if err := (HMACCallbackVerifier{Secret: secret}).Verify(context.Background(), "image", body, header); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	header.Set("X-Callback-Signature", "invalid")
	if err := (HMACCallbackVerifier{Secret: secret}).Verify(context.Background(), "image", body, header); err == nil {
		t.Fatal("Verify() accepted an invalid signature")
	}
}

func TestCallbackPublishesMessageInsteadOfRunningInline(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()
	var published CallbackMessage
	application.SetMessagePublisher(MessagePublisherFunc(func(_ context.Context, message CallbackMessage) error {
		published = message
		return nil
	}))

	body, err := json.Marshal(map[string]string{"event_id": "event-1", "job_id": "job-1"})
	if err != nil {
		t.Fatalf("marshal callback: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/callbacks/image", bytes.NewReader(body))
	response := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("callback status = %d, want %d", response.Code, http.StatusAccepted)
	}
	if published != (CallbackMessage{Provider: "image", EventID: "event-1", JobID: "job-1"}) {
		t.Fatalf("published callback = %#v", published)
	}
}

func TestCallbackAcceptsProviderTaskIDWithoutEventID(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()
	var published CallbackMessage
	application.SetMessagePublisher(MessagePublisherFunc(func(_ context.Context, message CallbackMessage) error {
		published = message
		return nil
	}))

	request := httptest.NewRequest(http.MethodPost, "/callbacks/capability", bytes.NewBufferString(`{"task_id":123,"task_status":30}`))
	response := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("callback status = %d, want %d", response.Code, http.StatusAccepted)
	}
	if published.Provider != "capability" || published.JobID != "123" || published.EventID == "" {
		t.Fatalf("published callback = %#v", published)
	}
}

func TestCallbackUsesTransportEventID(t *testing.T) {
	message, err := parseCallbackMessageWithEventID("capability", []byte(`{"task_id":123}`), "event-from-transport")
	if err != nil {
		t.Fatalf("parseCallbackMessageWithEventID() error = %v", err)
	}
	if message.EventID != "event-from-transport" {
		t.Fatalf("event id = %q, want transport event id", message.EventID)
	}
}

func TestCallbackPayloadProviderOverridesConsumerFallback(t *testing.T) {
	message, err := parseCallbackMessageWithEventID("capability", []byte(`{"provider":"tts","event_id":"event-1","job_id":"job-1"}`), "")
	if err != nil {
		t.Fatalf("parseCallbackMessageWithEventID() error = %v", err)
	}
	if message.Provider != "tts" {
		t.Fatalf("provider = %q, want payload provider", message.Provider)
	}
}

func TestCallbackAcceptsSubmitKeyWithoutJobID(t *testing.T) {
	message, err := parseCallbackMessageWithEventID("seedance", []byte(`{"event_id":"event-1","submit_key":"run:preview:scene-1"}`), "")
	if err != nil {
		t.Fatalf("parseCallbackMessageWithEventID() error = %v", err)
	}
	if message.JobID != "" || message.SubmitKey != "run:preview:scene-1" {
		t.Fatalf("callback message = %#v", message)
	}
}

func TestClaimCallbackUsesStableSubmitKeyAcrossProviderAliases(t *testing.T) {
	store := NewStore(t.TempDir() + "/workflow.json")
	run := Run{ID: "run-1", NodeRuns: []NodeRun{{
		NodeID: "preview", Kind: PreviewNode, InstanceKey: "scene-1", State: Waiting,
		Provider: "video", SubmitKey: "run:preview:scene-1", SubmitStarted: true,
	}}}
	if err := store.Create(context.Background(), run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	command, claimed, refresh, duplicate, err := store.ClaimCallback(CallbackMessage{
		Provider: "seedance", EventID: "event-1", SubmitKey: "run:preview:scene-1",
	})
	if err != nil {
		t.Fatalf("claimCallback() error = %v", err)
	}
	if !claimed || !refresh || duplicate || command.RunID != run.ID {
		t.Fatalf("claimCallback() = command %#v, claimed %v, refresh %v, duplicate %v", command, claimed, refresh, duplicate)
	}
}
