package videoagent

import (
	"context"
	"fmt"
	"strings"
)

const (
	promptTTSModel        = "c.ai.swantale_tts"
	swantaleZeroShotModel = "c.ai.swantale_zeroshot"
	defaultPromptTTSCPM   = 280
)

type PromptTTSConfig struct {
	CPM         int                 `json:"cpm"`
	ExampleText string              `json:"example_text"`
	SpeechRate  float64             `json:"speech_rate"`
	Storage     *AudioStorageConfig `json:"storage,omitempty"`
}

type PromptTTSClient struct {
	config   PromptTTSConfig
	matx     MatxClient
	importer AudioImporter
}

func NewPromptTTSClient(config PromptTTSConfig, matx MatxClient) (*PromptTTSClient, error) {
	return NewPromptTTSClientWithImporter(config, matx, nil)
}

func NewPromptTTSClientWithImporter(config PromptTTSConfig, matx MatxClient, importer AudioImporter) (*PromptTTSClient, error) {
	if matx == nil {
		return nil, fmt.Errorf("matx client is nil")
	}
	if config.CPM <= 0 {
		config.CPM = defaultPromptTTSCPM
	}
	if config.ExampleText == "" {
		config.ExampleText = "这是一段用于试听音色效果的示例语音。"
	}
	if config.SpeechRate <= 0 {
		config.SpeechRate = 1.1
	}
	return &PromptTTSClient{config: config, matx: matx, importer: importer}, nil
}

func (client *PromptTTSClient) SubmitTTS(ctx context.Context, request TTSRequest) (SubmittedJob, error) {
	prompt := firstNonEmpty(request.Prompt, request.Speaker)
	if strings.TrimSpace(prompt) == "" {
		return SubmittedJob{}, fmt.Errorf("tts prompt is empty")
	}
	cpm := firstPositive(request.CPM, client.config.CPM)
	response, err := client.matx.Infer(ctx, MatxRequest{
		Model: promptTTSModel,
		Bytes: map[string][][]byte{"caption": {[]byte(prompt)}},
		Ints:  map[string][]int64{"cpm": {int64(cpm)}},
	})
	if err != nil {
		return SubmittedJob{}, err
	}
	audioURL, err := firstMatxOutput(response, "audio")
	if err != nil {
		return SubmittedJob{}, err
	}
	promptAudio, err := client.storeAudio(ctx, audioURL)
	if err != nil {
		return SubmittedJob{}, err
	}
	status := &JobStatus{State: JobSucceeded, URI: promptAudio.URI, URL: promptAudio.URL, DurationMS: promptAudio.DurationMS}
	if request.WithExample {
		exampleURL, generateErr := client.generateSpeech(ctx, client.config.ExampleText, audioURL)
		if generateErr != nil {
			return SubmittedJob{}, generateErr
		}
		example, storeErr := client.storeAudio(ctx, exampleURL)
		if storeErr != nil {
			return SubmittedJob{}, storeErr
		}
		status.ExampleURI, status.ExampleURL = example.URI, example.URL
	}
	if strings.TrimSpace(request.Text) != "" {
		narrationURL, generateErr := client.generateSpeech(ctx, request.Text, audioURL)
		if generateErr != nil {
			return SubmittedJob{}, generateErr
		}
		narration, storeErr := client.storeAudio(ctx, narrationURL)
		if storeErr != nil {
			return SubmittedJob{}, storeErr
		}
		status.URI, status.URL = narration.URI, narration.URL
		status.DurationMS = narration.DurationMS
	}
	return SubmittedJob{Provider: "prompt_tts", Status: status}, nil
}

func (client *PromptTTSClient) storeAudio(ctx context.Context, sourceURL string) (StoredAudio, error) {
	if client.importer == nil {
		return StoredAudio{URL: sourceURL}, nil
	}
	stored, err := client.importer.ImportAudio(ctx, sourceURL)
	if err != nil {
		return StoredAudio{}, err
	}
	if stored.URL == "" {
		stored.URL = sourceURL
	}
	if stored.URI == "" {
		return StoredAudio{}, fmt.Errorf("audio importer returned an empty uri")
	}
	return stored, nil
}

func (client *PromptTTSClient) generateSpeech(ctx context.Context, text, referenceAudioURL string) (string, error) {
	response, err := client.matx.Infer(ctx, MatxRequest{
		Model: swantaleZeroShotModel,
		Bytes: map[string][][]byte{
			"caption": {[]byte(swantaleCaption(text))},
			"wav_url": {[]byte(referenceAudioURL)},
		},
		Floats: map[string][]float64{"speech_rate": {client.config.SpeechRate}},
	})
	if err != nil {
		return "", err
	}
	return firstMatxOutput(response, "audio")
}

func (*PromptTTSClient) GetTTS(context.Context, string) (JobStatus, error) {
	return JobStatus{}, ErrSubmitReconciliationUnsupported
}

func (*PromptTTSClient) FindTTSBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, ErrSubmitReconciliationUnsupported
}

func firstMatxOutput(response MatxResponse, name string) (string, error) {
	values := response.Bytes[name]
	if len(values) == 0 || len(values[0]) == 0 {
		return "", fmt.Errorf("matx response has no %s output", name)
	}
	return strings.TrimSpace(string(values[0])), nil
}

func swantaleCaption(text string) string {
	return fmt.Sprintf("Content: { Speaker1激情地说：<S1><|sp|>%s<|sp|></S1> }.", text)
}

var _ TTSClient = (*PromptTTSClient)(nil)
