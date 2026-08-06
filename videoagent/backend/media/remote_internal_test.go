package media

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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}
