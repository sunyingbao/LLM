//go:build !bytedance

package videoagent

import "fmt"

type AudioStorageConfig struct {
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	MaxBytes  int64  `json:"max_bytes,omitempty"`
}

func NewBytedanceAudioUploader(AudioStorageConfig) (AudioUploader, error) {
	return nil, fmt.Errorf("audio storage requires the bytedance build tag")
}
