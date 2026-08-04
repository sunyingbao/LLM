package videoagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

type recordingImageUploader struct {
	data []byte
	uri  string
}

func (uploader *recordingImageUploader) UploadImage(_ context.Context, data []byte) (string, error) {
	uploader.data = append([]byte(nil), data...)
	return uploader.uri, nil
}

func TestModelHubImageClientRetriesAPIKeysAndUploadsGeminiImage(t *testing.T) {
	wantedImage := []byte("generated-image")
	var mu sync.Mutex
	var keys []string
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		keys = append(keys, request.URL.Query().Get("key"))
		mu.Unlock()
		if request.URL.Query().Get("key") == "bad-key" {
			http.Error(writer, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"multimodal_contents": []any{map[string]any{
				"type": "inline_data", "inline_data": map[string]string{"data": base64.StdEncoding.EncodeToString(wantedImage)},
			}}},
		}}})
	}))
	defer server.Close()

	uploader := &recordingImageUploader{uri: "tos://image/generated"}
	client, err := NewModelHubImageClient(ModelHubImageConfig{
		GenURL: server.URL + "/generate?key={{api_key}}", APIKeys: []string{"bad-key", "good-key"},
		Model: "gemini-3-pro-image-preview", Attempts: 1,
	}, server.Client(), uploader)
	if err != nil {
		t.Fatal(err)
	}
	job, err := client.SubmitImage(context.Background(), ImageRequest{Prompt: "young woman", Width: 1024, Height: 1536})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(keys, []string{"bad-key", "good-key"}) {
		t.Fatalf("api keys = %v", keys)
	}
	if !reflect.DeepEqual(uploader.data, wantedImage) {
		t.Fatalf("uploaded data = %q", uploader.data)
	}
	if job.Provider != "model_hub" || job.Status == nil || job.Status.State != JobSucceeded || job.Status.URI != uploader.uri {
		t.Fatalf("job = %#v", job)
	}
	if requestBody["model"] != "gemini-3-pro-image-preview" {
		t.Fatalf("request model = %v", requestBody["model"])
	}
}

func TestModelHubImageClientUsesEditEndpointForReferenceImages(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path = request.URL.Path
		_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"multimodal_contents": []any{map[string]any{
				"type": "inline_data", "inline_data": map[string]string{"data": base64.StdEncoding.EncodeToString([]byte("image"))},
			}}},
		}}})
	}))
	defer server.Close()

	client, err := NewModelHubImageClient(ModelHubImageConfig{
		GenURL: server.URL + "/gen?key={{api_key}}", EditURL: server.URL + "/edit?key={{api_key}}",
		APIKeys: []string{"key"}, Model: "gemini",
	}, server.Client(), &recordingImageUploader{uri: "tos://image/edited"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SubmitImage(context.Background(), ImageRequest{Prompt: "edit", ImageURLs: []string{"https://example/image.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/edit" {
		t.Fatalf("path = %s", path)
	}
}

type immediateImageClient struct {
	requests []ImageRequest
	uri      string
}

func (client *immediateImageClient) SubmitImage(_ context.Context, request ImageRequest) (SubmittedJob, error) {
	client.requests = append(client.requests, request)
	return SubmittedJob{Provider: "immediate", Status: &JobStatus{State: JobSucceeded, URI: client.uri}}, nil
}

func (*immediateImageClient) GetImage(context.Context, string) (JobStatus, error) {
	return JobStatus{}, fmt.Errorf("unexpected GetImage")
}

func (*immediateImageClient) FindImageBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, ErrSubmitReconciliationUnsupported
}

type allowImageAudit struct{}

func (allowImageAudit) CheckImage(context.Context, string) error { return nil }

func TestNodeHandlerRoutesCharacterImagesToDedicatedClient(t *testing.T) {
	competition := &immediateImageClient{uri: "tos://competition"}
	character := &immediateImageClient{uri: "tos://character"}
	handler := nodeHandler{clients: Clients{Image: competition, CharacterImage: character, Audit: allowImageAudit{}}}
	command := Command{NodeRun: NodeRun{NodeID: "character", Kind: CharacterReferenceNode, InstanceKey: "person", SubmitKey: "submit"}}

	result, err := handler.submitImage(context.Background(), command, ResourcePlan{ID: "person", Prompt: "portrait"})
	if err != nil {
		t.Fatal(err)
	}
	if len(competition.requests) != 0 || len(character.requests) != 1 {
		t.Fatalf("competition requests = %d, character requests = %d", len(competition.requests), len(character.requests))
	}
	if result.State != Succeeded || len(result.Artifacts) != 1 {
		t.Fatalf("result = %#v", result)
	}
}
