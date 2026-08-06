//go:build !bytedance

package media

import "fmt"

func NewBytedanceMatxClient() (MatxClient, error) {
	return nil, fmt.Errorf("PromptTTS requires a build with -tags bytedance")
}
