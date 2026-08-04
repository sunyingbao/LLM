//go:build bytedance

package videoagent

import (
	"context"
	"fmt"
	"strings"

	"code.byted.org/videoarch/imagex-sdk-golang/base"
	"code.byted.org/videoarch/imagex-sdk-golang/service/imagex"
)

type bytedanceImageUploader struct {
	client    *imagex.ImageXClient
	serviceID string
}

func NewBytedanceImageUploader(config ImageXConfig) (ImageBinaryUploader, error) {
	if strings.TrimSpace(config.AccessKey) == "" || strings.TrimSpace(config.SecretKey) == "" {
		return nil, fmt.Errorf("imagex access key and secret key are required")
	}
	if config.ServiceID == "" {
		config.ServiceID = "r7j0lgfnz6"
	}
	client := imagex.NewInstance()
	client.SetCredential(base.Credentials{AccessKeyID: config.AccessKey, SecretAccessKey: config.SecretKey})
	return &bytedanceImageUploader{client: client, serviceID: config.ServiceID}, nil
}

func (uploader *bytedanceImageUploader) UploadImage(ctx context.Context, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("image data is empty")
	}
	response, err := uploader.client.UploadImage(ctx, &imagex.UploadImageParam{ServiceId: uploader.serviceID}, data)
	if err != nil {
		return "", err
	}
	if len(response.Results) == 0 || len(response.ImageInfos) == 0 || response.Results[0].UriStatus != imagex.UploadSuccessCode {
		return "", fmt.Errorf("imagex upload failed")
	}
	return response.ImageInfos[0].ImageUri, nil
}
