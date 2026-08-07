package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"eino-cli/videoagent/backend/contract"
	"eino-cli/videoagent/backend/media"
)

type recordingVideoUploader struct {
	payload []byte
	visible string
	uploads int
}

func (uploader *recordingVideoUploader) UploadVideo(_ context.Context, reader io.Reader, size int64) (string, error) {
	uploader.uploads++
	uploader.payload, _ = io.ReadAll(reader)
	if int64(len(uploader.payload)) != size {
		return "", io.ErrUnexpectedEOF
	}
	return "vid-1", nil
}

func TestHTTPVideoImporterReusesPersistedJobResult(t *testing.T) {
	payload := []byte("video-data")
	downloads := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		downloads++
		_, _ = io.Copy(writer, bytes.NewReader(payload))
	}))
	defer server.Close()

	store := NewStore(t.TempDir() + "/workflow.json")
	uploader := &recordingVideoUploader{}
	first, err := media.NewHTTPVideoImporter(uploader, server.Client(), 1024, store)
	if err != nil {
		t.Fatalf("NewHTTPVideoImporter() error = %v", err)
	}
	firstResult, err := first.ImportVideo(context.Background(), "task-1", server.URL)
	if err != nil {
		t.Fatalf("first ImportVideo() error = %v", err)
	}

	second, err := media.NewHTTPVideoImporter(uploader, server.Client(), 1024, store)
	if err != nil {
		t.Fatalf("NewHTTPVideoImporter() error = %v", err)
	}
	secondResult, err := second.ImportVideo(context.Background(), "task-1", "https://unused.invalid/video.mp4")
	if err != nil {
		t.Fatalf("second ImportVideo() error = %v", err)
	}
	if firstResult != secondResult {
		t.Fatalf("results differ: first=%#v second=%#v", firstResult, secondResult)
	}
	if downloads != 1 || uploader.uploads != 1 {
		t.Fatalf("downloads = %d, uploads = %d", downloads, uploader.uploads)
	}
}

func (uploader *recordingVideoUploader) SetVideoVisible(_ context.Context, vid string) error {
	uploader.visible = vid
	return nil
}

func TestHTTPVideoImporterDownloadsUploadsAndPublishesVideo(t *testing.T) {
	payload := []byte("video-data")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.Copy(writer, bytes.NewReader(payload))
	}))
	defer server.Close()

	uploader := &recordingVideoUploader{}
	importer, err := media.NewHTTPVideoImporter(uploader, server.Client(), 1024, nil)
	if err != nil {
		t.Fatalf("NewHTTPVideoImporter() error = %v", err)
	}
	video, err := importer.ImportVideo(context.Background(), "task-1", server.URL)
	if err != nil {
		t.Fatalf("ImportVideo() error = %v", err)
	}
	if !bytes.Equal(uploader.payload, payload) || uploader.visible != "vid-1" {
		t.Fatalf("upload = %q, visible = %q", uploader.payload, uploader.visible)
	}
	if video.URI != "vid://vid-1" || video.URL != server.URL {
		t.Fatalf("video = %#v", video)
	}
}

var _ contract.VideoUploader = (*recordingVideoUploader)(nil)
