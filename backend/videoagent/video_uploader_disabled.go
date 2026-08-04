//go:build !bytedance

package videoagent

import "fmt"

type VideoStorageConfig struct {
	Space        string `json:"space"`
	TopAccountID string `json:"top_account_id"`
	MaxBytes     int64  `json:"max_bytes,omitempty"`
}

func NewBytedanceVideoUploader(VideoStorageConfig) (VideoUploader, error) {
	return nil, fmt.Errorf("video storage requires the bytedance build tag")
}
