//go:build !bytedance

package videoagent

import "fmt"

func NewBytedanceImageUploader(ImageXConfig) (ImageBinaryUploader, error) {
	return nil, fmt.Errorf("imagex uploader requires the bytedance build tag")
}
