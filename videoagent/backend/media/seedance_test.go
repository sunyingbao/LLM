package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fixedMediaURLResolver struct{}

func (fixedMediaURLResolver) ResolveURL(_ context.Context, reference string) (string, error) {
	return "https://signed.example/" + reference, nil
}

type prefixedMediaURLResolver string

func (resolver prefixedMediaURLResolver) ResolveURL(_ context.Context, reference string) (string, error) {
	return "https://signed.example/" + string(resolver) + "/" + reference, nil
}

func TestSeedanceClientSubmitsAndPollsPreview(t *testing.T) {
	var submitted seedanceSubmitRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.Method + " " + request.URL.Path {
		case "POST /tasks":
			if err := json.NewDecoder(request.Body).Decode(&submitted); err != nil {
				t.Fatalf("decode submit request: %v", err)
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{"id": "task-1"})
		case "GET /tasks/task-1":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"status":  "succeeded",
				"content": map[string]string{"video_url": "https://example/preview.mp4"},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewSeedanceClient(SeedanceConfig{
		BaseURL: server.URL + "/tasks",
		APIKey:  "test-key",
		Model:   "seedance-endpoint",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewSeedanceClient() error = %v", err)
	}
	imageData, _ := json.Marshal(map[string]string{"url": "https://example/image.png"})
	audioData, _ := json.Marshal(map[string]string{"example_audio_url": "https://example/audio.wav", "preview_audio_url": "https://example/audio.wav"})
	job, err := client.SubmitPreview(context.Background(), VideoRequest{
		ClipScript: &ClipScript{Scenes: []Scene{{Visual: "shoe on a city street"}}},
		Inputs: []Artifact{
			{Kind: "competition_reference_image", Data: imageData},
			{Kind: "voice_preview", Data: audioData},
		},
	})
	if err != nil {
		t.Fatalf("SubmitPreview() error = %v", err)
	}
	if job.JobID != "task-1" || job.Provider != "seedance" {
		t.Fatalf("job = %#v", job)
	}
	if len(submitted.Content) != 3 || submitted.Content[0].Text != "shoe on a city street" {
		t.Fatalf("submitted content = %#v", submitted.Content)
	}
	if submitted.Content[1].Role != "reference_image" || submitted.Content[2].Role != "reference_audio" {
		t.Fatalf("submitted references = %#v", submitted.Content)
	}

	status, err := client.GetPreview(context.Background(), job.JobID)
	if err != nil {
		t.Fatalf("GetPreview() error = %v", err)
	}
	if status.State != JobSucceeded || status.URL != "https://example/preview.mp4" {
		t.Fatalf("status = %#v", status)
	}
}

func TestSeedanceClientRejectsUnknownStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{"status": "mystery"})
	}))
	defer server.Close()
	client, err := NewSeedanceClient(SeedanceConfig{BaseURL: server.URL, APIKey: "key", Model: "model"}, server.Client())
	if err != nil {
		t.Fatalf("NewSeedanceClient() error = %v", err)
	}
	if _, err := client.GetPreview(context.Background(), "task"); err == nil {
		t.Fatal("GetPreview() error = nil")
	}
}

func TestSeedanceClientResolvesInternalMediaURL(t *testing.T) {
	var submitted seedanceSubmitRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&submitted); err != nil {
			t.Fatalf("decode submit request: %v", err)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"id": "task-1"})
	}))
	defer server.Close()
	resolver := fixedMediaURLResolver{}
	client, err := NewSeedanceClientWithMediaResolvers(
		SeedanceConfig{BaseURL: server.URL, APIKey: "key", Model: "model"},
		server.Client(), nil, MediaURLResolvers{Image: resolver, Audio: resolver, Video: resolver},
	)
	if err != nil {
		t.Fatalf("NewSeedanceClientWithMediaResolvers() error = %v", err)
	}
	if _, err := client.SubmitPreview(context.Background(), VideoRequest{ImageURLs: []string{"tos://image/generated"}}); err != nil {
		t.Fatalf("SubmitPreview() error = %v", err)
	}
	if len(submitted.Content) != 2 || submitted.Content[1].ImageURL.URL != "https://signed.example/tos://image/generated" {
		t.Fatalf("submitted content = %#v", submitted.Content)
	}
}

func TestSeedanceClientRejectsInternalMediaWithoutResolver(t *testing.T) {
	client, err := NewSeedanceClient(SeedanceConfig{BaseURL: "https://example.com", APIKey: "key", Model: "model"}, nil)
	if err != nil {
		t.Fatalf("NewSeedanceClient() error = %v", err)
	}
	if _, err := client.SubmitPreview(context.Background(), VideoRequest{ImageURLs: []string{"tos://image/generated"}}); err == nil {
		t.Fatal("SubmitPreview() error = nil")
	}
}

func TestSeedanceClientUsesResolverForEachMediaType(t *testing.T) {
	var submitted seedanceSubmitRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&submitted); err != nil {
			t.Fatalf("decode submit request: %v", err)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"id": "task-1"})
	}))
	defer server.Close()
	client, err := NewSeedanceClientWithMediaResolvers(
		SeedanceConfig{BaseURL: server.URL, APIKey: "key", Model: "model"},
		server.Client(), nil,
		MediaURLResolvers{
			Image: prefixedMediaURLResolver("image"),
			Audio: prefixedMediaURLResolver("audio"),
			Video: prefixedMediaURLResolver("video"),
		},
	)
	if err != nil {
		t.Fatalf("NewSeedanceClientWithMediaResolvers() error = %v", err)
	}
	_, err = client.SubmitPreview(context.Background(), VideoRequest{
		ImageURLs: []string{"tos://image"}, AudioURLs: []string{"tos://audio"}, VideoURLs: []string{"tos://video"},
	})
	if err != nil {
		t.Fatalf("SubmitPreview() error = %v", err)
	}
	if len(submitted.Content) != 4 {
		t.Fatalf("submitted content = %#v", submitted.Content)
	}
	if submitted.Content[1].ImageURL.URL != "https://signed.example/image/tos://image" ||
		submitted.Content[2].AudioURL.URL != "https://signed.example/audio/tos://audio" ||
		submitted.Content[3].VideoURL.URL != "https://signed.example/video/tos://video" {
		t.Fatalf("submitted media = %#v", submitted.Content)
	}
}

type fixedVideoImporter struct{}

func (fixedVideoImporter) ImportVideo(context.Context, string, string) (StoredVideo, error) {
	return StoredVideo{URI: "vid://preview", URL: "https://example/preview.mp4"}, nil
}

func TestSeedanceClientImportsSucceededPreview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"status":  "succeeded",
			"content": map[string]string{"video_url": "https://ark/preview.mp4"},
		})
	}))
	defer server.Close()
	client, err := NewSeedanceClientWithMediaResolvers(
		SeedanceConfig{BaseURL: server.URL, APIKey: "key", Model: "model"},
		server.Client(),
		fixedVideoImporter{},
		MediaURLResolvers{},
	)
	if err != nil {
		t.Fatalf("NewSeedanceClientWithMediaResolvers() error = %v", err)
	}
	status, err := client.GetPreview(context.Background(), "task")
	if err != nil {
		t.Fatalf("GetPreview() error = %v", err)
	}
	if status.URI != "vid://preview" || status.URL != "https://example/preview.mp4" {
		t.Fatalf("status = %#v", status)
	}
}
