//go:build !bytedance

package videoagent

import "fmt"

func NewBytedanceMatxClient() (MatxClient, error) {
	return nil, fmt.Errorf("PromptTTS requires a build with -tags bytedance")
}
