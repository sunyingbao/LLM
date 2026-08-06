package media

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWAVDurationMS(t *testing.T) {
	payload := make([]byte, 44+16000)
	copy(payload[:4], "RIFF")
	binary.LittleEndian.PutUint32(payload[4:8], uint32(len(payload)-8))
	copy(payload[8:12], "WAVE")
	copy(payload[12:16], "fmt ")
	binary.LittleEndian.PutUint32(payload[16:20], 16)
	binary.LittleEndian.PutUint16(payload[20:22], 1)
	binary.LittleEndian.PutUint16(payload[22:24], 1)
	binary.LittleEndian.PutUint32(payload[24:28], 8000)
	binary.LittleEndian.PutUint32(payload[28:32], 16000)
	binary.LittleEndian.PutUint16(payload[32:34], 2)
	binary.LittleEndian.PutUint16(payload[34:36], 16)
	copy(payload[36:40], "data")
	binary.LittleEndian.PutUint32(payload[40:44], 16000)
	if duration := wavDurationMS(payload); duration != 1000 {
		t.Fatalf("wav duration = %d, want 1000", duration)
	}
}

type recordingAudioUploader struct {
	keys     []string
	payloads [][]byte
}

func (uploader *recordingAudioUploader) UploadAudio(_ context.Context, key string, payload []byte) (string, error) {
	uploader.keys = append(uploader.keys, key)
	uploader.payloads = append(uploader.payloads, append([]byte(nil), payload...))
	return "lab/" + key, nil
}

func TestHTTPAudioImporterUsesStableContentKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("audio-data"))
	}))
	defer server.Close()
	uploader := &recordingAudioUploader{}
	importer, err := NewHTTPAudioImporter(uploader, server.Client(), 1024)
	if err != nil {
		t.Fatalf("NewHTTPAudioImporter() error = %v", err)
	}
	first, err := importer.ImportAudio(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("ImportAudio() error = %v", err)
	}
	second, err := importer.ImportAudio(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("ImportAudio() second error = %v", err)
	}
	if len(uploader.keys) != 2 || uploader.keys[0] != uploader.keys[1] {
		t.Fatalf("upload keys = %#v, want stable keys", uploader.keys)
	}
	if first.URI != second.URI || first.URL != server.URL {
		t.Fatalf("stored audio = %#v, %#v", first, second)
	}
}

func (uploader *recordingAudioUploader) String() string {
	return fmt.Sprint(uploader.keys)
}

var _ AudioUploader = (*recordingAudioUploader)(nil)
