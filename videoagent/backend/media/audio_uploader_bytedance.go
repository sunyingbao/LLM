//go:build bytedance

package media

import (
	"bytes"
	"context"
	"fmt"

	"code.byted.org/gopkg/tos"
)

type AudioStorageConfig struct {
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	MaxBytes  int64  `json:"max_bytes,omitempty"`
}

type bytedanceAudioUploader struct {
	bucket string
	client *tos.Tos
}

func NewBytedanceAudioUploader(config AudioStorageConfig) (AudioUploader, error) {
	if config.Bucket == "" || config.AccessKey == "" {
		return nil, fmt.Errorf("audio storage bucket and access_key are required")
	}
	client, err := tos.NewTos(
		tos.WithBucket(config.Bucket),
		tos.WithCredentials(&tos.BucketAccessKeyCredentials{BucketName: config.Bucket, AccessKey: config.AccessKey}),
	)
	if err != nil {
		return nil, err
	}
	return &bytedanceAudioUploader{bucket: config.Bucket, client: client}, nil
}

func (uploader *bytedanceAudioUploader) UploadAudio(ctx context.Context, key string, payload []byte) (string, error) {
	if err := uploader.client.PutObject(ctx, key, int64(len(payload)), bytes.NewReader(payload), tos.ContentType("audio/wav")); err != nil {
		return "", err
	}
	return fmt.Sprintf("tos://%s/%s", uploader.bucket, key), nil
}

var _ AudioUploader = (*bytedanceAudioUploader)(nil)
