package videoagent

import (
	"context"
	"testing"
)

func TestCombinedVideoCancellationUsesTheOwningClient(t *testing.T) {
	preview := &cancelRecordingVideoClient{}
	finalVideo := &cancelRecordingVideoClient{}
	client := combinedVideoClient{PreviewClient: preview, FinalVideoClient: finalVideo}
	handler := nodeHandler{clients: Clients{Video: client}}

	err := handler.Cancel(context.Background(), Run{NodeRuns: []NodeRun{
		{NodeID: "preview", Kind: PreviewNode, InstanceKey: "scene-1", State: Waiting, JobID: "preview-job"},
		{NodeID: "finalvideo", Kind: FinalVideoNode, InstanceKey: "cut-1", State: Waiting, JobID: "final-job"},
	}})
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if len(preview.canceled) != 1 || preview.canceled[0] != "preview-job" {
		t.Fatalf("preview canceled jobs = %#v, want preview-job", preview.canceled)
	}
	if len(finalVideo.canceled) != 1 || finalVideo.canceled[0] != "final-job" {
		t.Fatalf("finalvideo canceled jobs = %#v, want final-job", finalVideo.canceled)
	}
}

type cancelRecordingVideoClient struct {
	canceled []string
}

func (*cancelRecordingVideoClient) SubmitPreview(context.Context, VideoRequest) (SubmittedJob, error) {
	return SubmittedJob{}, nil
}

func (*cancelRecordingVideoClient) GetPreview(context.Context, string) (JobStatus, error) {
	return JobStatus{}, nil
}

func (*cancelRecordingVideoClient) FindPreviewBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, nil
}

func (*cancelRecordingVideoClient) SubmitFinalVideo(context.Context, VideoRequest) (SubmittedJob, error) {
	return SubmittedJob{}, nil
}

func (*cancelRecordingVideoClient) GetFinalVideo(context.Context, string) (JobStatus, error) {
	return JobStatus{}, nil
}

func (*cancelRecordingVideoClient) FindFinalVideoBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, nil
}

func (client *cancelRecordingVideoClient) CancelVideo(_ context.Context, jobID string) error {
	client.canceled = append(client.canceled, jobID)
	return nil
}
