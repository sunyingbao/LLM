//go:build !bytedance

package videoagent

import "fmt"

type MediaURLConfig struct {
	PSM           string `json:"psm"`
	Domain        string `json:"domain"`
	AccessKey     string `json:"access_key"`
	SecretKey     string `json:"secret_key"`
	Template      string `json:"template"`
	ExpireSeconds int    `json:"expire_seconds"`
}

func NewBytedanceMediaURLResolver(MediaURLConfig) (MediaURLResolver, error) {
	return nil, fmt.Errorf("media url resolver requires the bytedance build tag")
}
