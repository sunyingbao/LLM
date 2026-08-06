package videoagent

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"time"
)

type StoredAudio struct {
	URI        string
	URL        string
	DurationMS int
}

type AudioImporter interface {
	ImportAudio(context.Context, string) (StoredAudio, error)
}

type AudioUploader interface {
	UploadAudio(context.Context, string, []byte) (string, error)
}

type HTTPAudioImporter struct {
	uploader AudioUploader
	client   *http.Client
	maxBytes int64
}

func NewHTTPAudioImporter(uploader AudioUploader, client *http.Client, maxBytes int64) (*HTTPAudioImporter, error) {
	if uploader == nil {
		return nil, fmt.Errorf("audio uploader is nil")
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	if maxBytes <= 0 {
		maxBytes = 32 << 20
	}
	return &HTTPAudioImporter{uploader: uploader, client: client, maxBytes: maxBytes}, nil
}

func (importer *HTTPAudioImporter) ImportAudio(ctx context.Context, sourceURL string) (StoredAudio, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return StoredAudio{}, err
	}
	response, err := importer.client.Do(request)
	if err != nil {
		return StoredAudio{}, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return StoredAudio{}, fmt.Errorf("download audio returned %s", response.Status)
	}
	if response.ContentLength > importer.maxBytes {
		return StoredAudio{}, fmt.Errorf("audio exceeds %d bytes", importer.maxBytes)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, importer.maxBytes+1))
	if err != nil {
		return StoredAudio{}, err
	}
	if int64(len(payload)) > importer.maxBytes {
		return StoredAudio{}, fmt.Errorf("audio exceeds %d bytes", importer.maxBytes)
	}
	key := fmt.Sprintf("audio/%x.wav", sha256.Sum256(payload))
	uri, err := importer.uploader.UploadAudio(ctx, key, payload)
	if err != nil {
		return StoredAudio{}, err
	}
	return StoredAudio{URI: uri, URL: sourceURL, DurationMS: wavDurationMS(payload)}, nil
}

func wavDurationMS(payload []byte) int {
	if len(payload) < 12 || string(payload[:4]) != "RIFF" || string(payload[8:12]) != "WAVE" {
		return 0
	}
	var byteRate, dataSize uint32
	for offset := 12; offset+8 <= len(payload); {
		name := string(payload[offset : offset+4])
		size := binary.LittleEndian.Uint32(payload[offset+4 : offset+8])
		start, end := offset+8, offset+8+int(size)
		if end > len(payload) {
			return 0
		}
		if name == "fmt " && size >= 12 {
			byteRate = binary.LittleEndian.Uint32(payload[start+8 : start+12])
		}
		if name == "data" {
			dataSize = size
		}
		offset = end + int(size%2)
	}
	if byteRate == 0 || dataSize == 0 {
		return 0
	}
	return int((uint64(dataSize)*1000 + uint64(byteRate) - 1) / uint64(byteRate))
}

var _ AudioImporter = (*HTTPAudioImporter)(nil)
