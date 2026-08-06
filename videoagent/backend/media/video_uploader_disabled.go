//go:build !bytedance

package media

import "fmt"

type VideoStorageConfig struct {
	Space        string `json:"space"`
	TopAccountID string `json:"top_account_id"`
	AccessKey    string `json:"video_access_key"`
	SecretKey    string `json:"video_secret_key"`
	MaxBytes     int64  `json:"max_bytes,omitempty"`
}

func NewBytedanceVideoUploader(VideoStorageConfig) (VideoUploader, error) {
	return nil, fmt.Errorf("video storage requires the bytedance build tag")
}
