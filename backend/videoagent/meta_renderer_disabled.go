//go:build !bytedance

package videoagent

import "fmt"

func NewBytedanceVideoRenderer(int) (VideoRenderer, error) {
	return nil, fmt.Errorf("meta renderer requires the bytedance build tag")
}
