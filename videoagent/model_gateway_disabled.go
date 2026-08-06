//go:build !bytedance

package videoagent

import "fmt"

func NewBytedanceModelGateway() (ModelGateway, error) {
	return nil, fmt.Errorf("model gateway requires a build with -tags bytedance")
}
