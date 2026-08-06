package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRunnerReportsEveryMissingClient(t *testing.T) {
	_, err := NewRunner(NewStore(t.TempDir()+"/workflow.json"), Clients{})
	if err == nil {
		t.Fatal("NewRunner() error = nil")
	}
	for _, dependency := range []string{"planner", "image", "tts", "video", "image_audit", "prompt_shield"} {
		if !strings.Contains(err.Error(), dependency) {
			t.Fatalf("NewRunner() error = %q, missing %q", err, dependency)
		}
	}
}

func TestRemoteClientsDoNotRequireGenericGateway(t *testing.T) {
	clients, err := NewRemoteClients(RemoteConfig{}, nil)
	if err != nil {
		t.Fatalf("NewRemoteClients() error = %v", err)
	}
	if clients != (Clients{}) {
		t.Fatalf("NewRemoteClients() = %#v, want empty direct clients", clients)
	}
}

func TestRemoteClientsRejectPlaceholderConfiguration(t *testing.T) {
	_, err := NewRemoteClients(RemoteConfig{Endpoints: map[string]string{"prompt_shield": "https://capability.example.com/shield"}}, nil)
	if err == nil {
		t.Fatal("NewRemoteClients() error = nil")
	}
}

func TestRemoteClientsRequireStorageBetweenDirectPreviewAndFinalVideo(t *testing.T) {
	_, err := NewRemoteClients(RemoteConfig{
		BaseURL:    "http://video-agent.test",
		Seedance:   &SeedanceConfig{BaseURL: "http://seedance.test", APIKey: "key", Model: "model"},
		FinalVideo: &MetaFinalVideoConfig{BizID: 1},
	}, nil)
	if err == nil {
		t.Fatal("NewRemoteClients() error = nil")
	}
}

func TestValidateCanvasRemoteConfigReportsEveryMissingCapability(t *testing.T) {
	err := ValidateCanvasRemoteConfig(RemoteConfig{})
	if err == nil {
		t.Fatal("ValidateCanvasRemoteConfig() error = nil")
	}
	for _, capability := range []string{"image", "tts", "preview", "finalvideo", "image_audit", "prompt_shield"} {
		if !strings.Contains(err.Error(), capability) {
			t.Fatalf("ValidateCanvasRemoteConfig() error = %q, missing %q", err, capability)
		}
	}
}

func TestValidateCanvasRemoteConfigAcceptsDirectMediaClients(t *testing.T) {
	err := ValidateCanvasRemoteConfig(RemoteConfig{
		CallbackSecret: "secret",
		Seedance:       &SeedanceConfig{BaseURL: "http://seedance.test", APIKey: "key", Model: "model"},
		PromptTTS:      &PromptTTSConfig{},
		ImageArk:       &ArkImageConfig{BaseURL: "http://ark.test", APIKey: "key", Model: "model"},
		FinalVideo:     &MetaFinalVideoConfig{BizID: 1},
		VideoStorage:   &VideoStorageConfig{TopAccountID: "account"},
		Endpoints: map[string]string{
			"image_audit":   "http://capability.test/audit",
			"prompt_shield": "http://capability.test/shield",
		},
	})
	if err != nil {
		t.Fatalf("ValidateCanvasRemoteConfig() error = %v", err)
	}
}

func TestConfirmedWorkflowOperationPublishesNewVersion(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	payload, _ := json.Marshal(CanvasOperation{Type: OperationUpdateWorkflow, Payload: mustEncode(t, VideoWorkflow())})
	request := httptest.NewRequest(http.MethodPost, "/projects/demo/operations", bytes.NewReader(payload))
	response := httptest.NewRecorder()
	handler := NewHTTPHandler(application)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST operation status = %d, want %d", response.Code, http.StatusCreated)
	}
	var operation CanvasOperation
	if err := json.NewDecoder(response.Body).Decode(&operation); err != nil {
		t.Fatalf("decode operation: %v", err)
	}
	confirm := httptest.NewRecorder()
	handler.ServeHTTP(confirm, httptest.NewRequest(http.MethodPost, "/operations/"+operation.ID+"/confirm", nil))
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm operation status = %d, want %d", confirm.Code, http.StatusOK)
	}
	project, err := application.Store.GetProject(context.Background(), "demo")
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if len(project.WorkflowVersions) != 2 {
		t.Fatalf("workflow versions = %d, want 2", len(project.WorkflowVersions))
	}
}

func mustEncode(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	return payload
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}
