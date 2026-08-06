package media

import (
	"context"
	"testing"
)

func TestImageClientResolvesTOSURIForCanvas(t *testing.T) {
	client := withImageURL(imageURIClient{}, staticImageMediaResolver("https://cdn.example/image.png"))
	job, err := client.SubmitImage(context.Background(), ImageRequest{})
	if err != nil {
		t.Fatalf("SubmitImage() error = %v", err)
	}
	if job.Status == nil || job.Status.URI != "tos://bucket/image.png" || job.Status.URL != "https://cdn.example/image.png" {
		t.Fatalf("SubmitImage() status = %#v", job.Status)
	}
}

type imageURIClient struct{}

func (imageURIClient) SubmitImage(context.Context, ImageRequest) (SubmittedJob, error) {
	return SubmittedJob{Status: &JobStatus{State: JobSucceeded, URI: "tos://bucket/image.png"}}, nil
}

func (imageURIClient) GetImage(context.Context, string) (JobStatus, error) {
	return JobStatus{State: JobSucceeded, URI: "tos://bucket/image.png"}, nil
}

func (imageURIClient) FindImageBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, nil
}

type staticImageMediaResolver string

func (resolver staticImageMediaResolver) ResolveURL(context.Context, string) (string, error) {
	return string(resolver), nil
}
