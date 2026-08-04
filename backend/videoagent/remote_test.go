package videoagent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func TestRemoteClientsSendConfiguredRequestAndDecodeJob(t *testing.T) {
	var gotPath string
	var gotInput ImageRequest
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotPath = request.URL.Path
		if err := json.NewDecoder(request.Body).Decode(&gotInput); err != nil {
			return nil, err
		}
		body, err := json.Marshal(SubmittedJob{Provider: "image", JobID: "job-1"})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})

	remote := &remoteTransport{baseURL: "http://video-agent.test", endpoints: map[string]string{"image_submit": "/image/submit"}, client: &http.Client{Transport: transport}}
	job, err := remote.SubmitImage(context.Background(), ImageRequest{Prompt: "shoe", SubmitKey: "run:node:item"})
	if err != nil {
		t.Fatalf("SubmitImage() error = %v", err)
	}
	if gotPath != "/image/submit" || gotInput.SubmitKey != "run:node:item" || job.JobID != "job-1" {
		t.Fatalf("request/result = path %q input %#v job %#v", gotPath, gotInput, job)
	}
}

func TestRemoteClientAcceptsEmptySuccessResponse(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: http.NoBody, Header: make(http.Header)}, nil
	})
	remote := &remoteTransport{baseURL: "http://video-agent.test", endpoints: map[string]string{"video_cancel": "/video/cancel"}, client: &http.Client{Transport: transport}}

	if err := remote.CancelVideo(context.Background(), "job-1"); err != nil {
		t.Fatalf("CancelVideo() error = %v", err)
	}
}

func TestRemoteClientAcceptsAbsoluteEndpointWithoutBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/audit" {
			t.Fatalf("request path = %q", request.URL.Path)
		}
		_, _ = response.Write([]byte(`{"audit_result":{"status":"MARK"}}`))
	}))
	defer server.Close()

	remote := &remoteTransport{endpoints: map[string]string{"image_audit": server.URL + "/audit"}, client: server.Client()}
	if err := remote.CheckImage(context.Background(), "tos://bucket/image.png"); err != nil {
		t.Fatalf("CheckImage() error = %v", err)
	}
}

func TestRemoteModerationRejectsBlockedAndUnknownResponses(t *testing.T) {
	responses := []string{`{"decision":"BLOCK"}`, `{}`}
	for _, payload := range responses {
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
		})
		remote := &remoteTransport{baseURL: "http://video-agent.test", endpoints: map[string]string{"prompt_shield": "/shield"}, client: &http.Client{Transport: transport}}
		if err := remote.CheckPrompt(context.Background(), "unsafe prompt"); err == nil {
			t.Fatalf("CheckPrompt() accepted response %s", payload)
		}
	}
}

func TestRemotePromptModerationAcceptsExplicitPass(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"pass":true}`)), Header: make(http.Header)}, nil
	})
	remote := &remoteTransport{baseURL: "http://video-agent.test", endpoints: map[string]string{"prompt_shield": "/shield"}, client: &http.Client{Transport: transport}}
	if err := remote.CheckPrompt(context.Background(), "safe prompt"); err != nil {
		t.Fatalf("CheckPrompt() error = %v", err)
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
