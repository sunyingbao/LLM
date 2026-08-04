package videoagent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestPromptTTSClientGeneratesAndReusesExampleAudio(t *testing.T) {
	matx := &fakeMatx{responses: map[string]string{
		promptTTSModel: "https://example/prompt.wav",
	}, zeroShotOutputs: []string{"https://example/example.wav", "https://example/narration.wav"}}
	client, err := NewPromptTTSClient(PromptTTSConfig{}, matx)
	if err != nil {
		t.Fatalf("NewPromptTTSClient() error = %v", err)
	}
	job, err := client.SubmitTTS(context.Background(), TTSRequest{
		Prompt:      "温暖自然的女声",
		Text:        "新品上市",
		WithExample: true,
	})
	if err != nil {
		t.Fatalf("SubmitTTS() error = %v", err)
	}
	if job.JobID != "" || job.Status == nil || job.Status.State != JobSucceeded {
		t.Fatalf("job = %#v", job)
	}
	if job.Status.URL != "https://example/narration.wav" || job.Status.ExampleURL != "https://example/example.wav" {
		t.Fatalf("status = %#v", job.Status)
	}
	if len(matx.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(matx.requests))
	}
	if got := string(matx.requests[1].Bytes["wav_url"][0]); got != "https://example/prompt.wav" {
		t.Fatalf("example wav_url = %q", got)
	}
	if got := string(matx.requests[2].Bytes["caption"][0]); got != swantaleCaption("新品上市") {
		t.Fatalf("narration caption = %q", got)
	}
	if got := matx.requests[0].Ints["cpm"][0]; got != defaultPromptTTSCPM {
		t.Fatalf("cpm = %d", got)
	}
}

func TestPromptTTSClientPersistsPromptExampleAndNarration(t *testing.T) {
	matx := &fakeMatx{
		responses:       map[string]string{promptTTSModel: "https://example/prompt.wav"},
		zeroShotOutputs: []string{"https://example/example.wav", "https://example/narration.wav"},
	}
	importer := &fixedAudioImporter{}
	client, err := NewPromptTTSClientWithImporter(PromptTTSConfig{}, matx, importer)
	if err != nil {
		t.Fatalf("NewPromptTTSClientWithImporter() error = %v", err)
	}
	job, err := client.SubmitTTS(context.Background(), TTSRequest{Prompt: "温暖女声", Text: "新品上市", WithExample: true})
	if err != nil {
		t.Fatalf("SubmitTTS() error = %v", err)
	}
	if len(importer.urls) != 3 {
		t.Fatalf("imported urls = %#v, want prompt, example and narration", importer.urls)
	}
	if job.Status.URI != "lab/audio/narration.wav" || job.Status.ExampleURI != "lab/audio/example.wav" {
		t.Fatalf("status = %#v", job.Status)
	}
}

func TestPromptTTSClientRequiresAudioOutput(t *testing.T) {
	client, err := NewPromptTTSClient(PromptTTSConfig{}, &fakeMatx{responses: map[string]string{}})
	if err != nil {
		t.Fatalf("NewPromptTTSClient() error = %v", err)
	}
	if _, err := client.SubmitTTS(context.Background(), TTSRequest{Prompt: "voice"}); err == nil {
		t.Fatal("SubmitTTS() error = nil")
	}
}

type fakeMatx struct {
	requests        []MatxRequest
	responses       map[string]string
	zeroShotOutputs []string
}

type fixedAudioImporter struct {
	urls []string
}

func (importer *fixedAudioImporter) ImportAudio(_ context.Context, sourceURL string) (StoredAudio, error) {
	importer.urls = append(importer.urls, sourceURL)
	return StoredAudio{URI: "lab/audio/" + sourceURL[strings.LastIndex(sourceURL, "/")+1:], URL: sourceURL}, nil
}

func (matx *fakeMatx) Infer(_ context.Context, request MatxRequest) (MatxResponse, error) {
	matx.requests = append(matx.requests, request)
	if request.Model == "error" {
		return MatxResponse{}, fmt.Errorf("inference failed")
	}
	output := matx.responses[request.Model]
	if request.Model == swantaleZeroShotModel && len(matx.zeroShotOutputs) > 0 {
		output = matx.zeroShotOutputs[0]
		matx.zeroShotOutputs = matx.zeroShotOutputs[1:]
	}
	if output == "" {
		return MatxResponse{}, nil
	}
	return MatxResponse{Bytes: map[string][][]byte{"audio": {[]byte(output)}}}, nil
}
