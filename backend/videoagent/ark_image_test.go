package videoagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestArkImageClientSubmitsImageAndReturnsURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/images/generations" {
			t.Fatalf("request path = %q", request.URL.Path)
		}
		var payload struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
			Size   string `json:"size"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.Model != "image-endpoint" || payload.Prompt != "shoe" || payload.Size != "640x960" {
			t.Fatalf("request payload = %+v", payload)
		}
		_, _ = io.WriteString(writer, `{"model":"image-endpoint","data":[{"url":"https://image.test/shoe.png"}]}`)
	}))
	defer server.Close()

	client, err := NewArkImageClient(ArkImageConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "image-endpoint",
		Width:   640,
		Height:  960,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := client.SubmitImage(context.Background(), ImageRequest{Prompt: "shoe"})
	if err != nil {
		t.Fatal(err)
	}
	if job.Provider != "ark" || job.Status == nil || job.Status.State != JobSucceeded || job.Status.URL != "https://image.test/shoe.png" {
		t.Fatalf("job = %+v", job)
	}
}
