package videoagent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestModelGatewayImageClientSubmitsAndPolls(t *testing.T) {
	gateway := &fakeModelGateway{jobID: "image-task", status: ModelTaskStatus{
		Code:   0,
		Result: []byte(`{"image_urls":[{"url":"https://example/image.png"}]}`),
	}}
	client, err := NewModelGatewayImageClient(ModelGatewayImageConfig{Model: "seedream", TaskQueue: "test"}, gateway)
	if err != nil {
		t.Fatalf("NewModelGatewayImageClient() error = %v", err)
	}
	job, err := client.SubmitImage(context.Background(), ImageRequest{Prompt: "shoe", SubmitKey: "submit-1"})
	if err != nil {
		t.Fatalf("SubmitImage() error = %v", err)
	}
	if job.JobID != "image-task" || job.Provider != "model_gateway" {
		t.Fatalf("job = %#v", job)
	}
	var payload map[string]any
	if err := json.Unmarshal(gateway.request.Input, &payload); err != nil {
		t.Fatalf("decode model input: %v", err)
	}
	if payload["prompt"] != "shoe" || payload["size"] != "1024x1536" || gateway.request.Extra["submit_key"] != "submit-1" {
		t.Fatalf("request = %#v, payload = %#v", gateway.request, payload)
	}
	if _, exists := payload["image_urls"]; exists {
		t.Fatalf("text-to-image payload contains unsupported image_urls: %#v", payload)
	}
	status, err := client.GetImage(context.Background(), job.JobID)
	if err != nil {
		t.Fatalf("GetImage() error = %v", err)
	}
	if status.State != JobSucceeded || status.URL != "https://example/image.png" {
		t.Fatalf("status = %#v", status)
	}
}

func TestModelGatewayImageClientMapsPendingAndFailure(t *testing.T) {
	gateway := &fakeModelGateway{jobID: "image-task", status: ModelTaskStatus{Code: -1002}}
	client, err := NewModelGatewayImageClient(ModelGatewayImageConfig{Model: "seedream"}, gateway)
	if err != nil {
		t.Fatalf("NewModelGatewayImageClient() error = %v", err)
	}
	status, err := client.GetImage(context.Background(), "image-task")
	if err != nil || status.State != JobPending {
		t.Fatalf("pending status = %#v, err = %v", status, err)
	}
	gateway.status = ModelTaskStatus{Code: -1000, BizMessage: "rejected"}
	status, err = client.GetImage(context.Background(), "image-task")
	if err != nil || status.State != JobFailed || status.Message != "rejected" {
		t.Fatalf("failed status = %#v, err = %v", status, err)
	}
}

type fakeModelGateway struct {
	request ModelTaskRequest
	jobID   string
	status  ModelTaskStatus
}

func (gateway *fakeModelGateway) CreateTask(_ context.Context, request ModelTaskRequest) (string, error) {
	gateway.request = request
	return gateway.jobID, nil
}

func (gateway *fakeModelGateway) GetTask(context.Context, string) (ModelTaskStatus, error) {
	return gateway.status, nil
}
