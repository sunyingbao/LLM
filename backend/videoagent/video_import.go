package videoagent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type HTTPVideoImporter struct {
	uploader VideoUploader
	client   *http.Client
	maxBytes int64
	cache    VideoImportCache
}

func NewHTTPVideoImporter(uploader VideoUploader, client *http.Client, maxBytes int64, cache VideoImportCache) (*HTTPVideoImporter, error) {
	if uploader == nil {
		return nil, fmt.Errorf("video uploader is nil")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Minute}
	}
	if maxBytes <= 0 {
		maxBytes = 1 << 30
	}
	return &HTTPVideoImporter{uploader: uploader, client: client, maxBytes: maxBytes, cache: cache}, nil
}

func (importer *HTTPVideoImporter) ImportVideo(ctx context.Context, jobID string, sourceURL string) (StoredVideo, error) {
	if importer.cache != nil {
		video, exists, err := importer.cache.GetImportedVideo(ctx, jobID)
		if err != nil {
			return StoredVideo{}, err
		}
		if exists {
			return video, nil
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return StoredVideo{}, err
	}
	response, err := importer.client.Do(request)
	if err != nil {
		return StoredVideo{}, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return StoredVideo{}, fmt.Errorf("download preview returned %s", response.Status)
	}
	if response.ContentLength > importer.maxBytes {
		return StoredVideo{}, fmt.Errorf("preview exceeds %d bytes", importer.maxBytes)
	}

	file, err := os.CreateTemp("", "video-agent-preview-*")
	if err != nil {
		return StoredVideo{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()

	size, err := io.Copy(file, io.LimitReader(response.Body, importer.maxBytes+1))
	if err != nil {
		return StoredVideo{}, err
	}
	if size > importer.maxBytes {
		return StoredVideo{}, fmt.Errorf("preview exceeds %d bytes", importer.maxBytes)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return StoredVideo{}, err
	}
	vid, err := importer.uploader.UploadVideo(ctx, file, size)
	if err != nil {
		return StoredVideo{}, err
	}
	if strings.TrimSpace(vid) == "" {
		return StoredVideo{}, fmt.Errorf("video uploader returned an empty vid")
	}
	if err := importer.uploader.SetVideoVisible(ctx, vid); err != nil {
		return StoredVideo{}, err
	}
	video := StoredVideo{URI: "vid://" + vid, URL: sourceURL}
	if importer.cache != nil {
		if err := importer.cache.SaveImportedVideo(ctx, jobID, video); err != nil {
			return StoredVideo{}, err
		}
	}
	return video, nil
}

var _ VideoImporter = (*HTTPVideoImporter)(nil)
