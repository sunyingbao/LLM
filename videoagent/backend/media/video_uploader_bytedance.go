//go:build bytedance

package media

import (
	"context"
	"fmt"
	"io"

	"code.byted.org/overpass/ad_site_creative_common_server/kitex_gen/common_server"
	"code.byted.org/overpass/ad_site_creative_common_server/rpc/ad_site_creative_common_server"
	uploader "code.byted.org/videoarch/uploader_v5"
)

type VideoStorageConfig struct {
	Space        string `json:"space"`
	TopAccountID string `json:"top_account_id"`
	AccessKey    string `json:"video_access_key"`
	SecretKey    string `json:"video_secret_key"`
	MaxBytes     int64  `json:"max_bytes,omitempty"`
}

type bytedanceVideoUploader struct {
	config VideoStorageConfig
	client *uploader.Client
}

func NewBytedanceVideoUploader(config VideoStorageConfig) (uploaderClient VideoUploader, err error) {
	if config.Space == "" {
		config.Space = "jichuang"
	}
	if config.TopAccountID == "" {
		return nil, fmt.Errorf("video storage top_account_id is required")
	}
	if (config.AccessKey == "") != (config.SecretKey == "") {
		return nil, fmt.Errorf("video storage requires both video_access_key and video_secret_key")
	}

	options := make([]uploader.Option, 0, 1)
	if config.AccessKey != "" {
		options = append(options, uploader.WithPubVisBkt(uploader.BktPubVisOption{
			Env: uploader.CNENV,
			Ak:  config.AccessKey,
			Sk:  config.SecretKey,
		}))
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			uploaderClient = nil
			err = fmt.Errorf("initialize video uploader: %v", recovered)
		}
	}()
	return &bytedanceVideoUploader{config: config, client: uploader.NewClientV2(options...)}, nil
}

func (client *bytedanceVideoUploader) UploadVideo(ctx context.Context, reader io.Reader, size int64) (string, error) {
	response, err := client.client.PutFile(ctx, reader, size, uploader.VIDEO, client.config.Space, client.config.TopAccountID)
	if err != nil {
		return "", err
	}
	if response == nil || len(response.Results) == 0 || response.Results[0].VideoMeta == nil || response.Results[0].Vid == "" {
		return "", fmt.Errorf("video upload returned no video")
	}
	return response.Results[0].Vid, nil
}

func (*bytedanceVideoUploader) SetVideoVisible(ctx context.Context, vid string) error {
	_, err := ad_site_creative_common_server.RawCall.SetVideoStatus(ctx, &common_server.SetVideoStatusRequest{
		Vid: vid, Status: common_server.VideoStatus_Visibility_L0,
	})
	return err
}

var _ VideoUploader = (*bytedanceVideoUploader)(nil)
