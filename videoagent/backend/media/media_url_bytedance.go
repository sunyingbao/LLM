//go:build bytedance

package media

import (
	"context"
	"fmt"
	"strings"
	"time"

	"code.byted.org/videoarch/alpha-go-sdk/alpha"
)

type MediaURLConfig struct {
	PSM           string `json:"psm"`
	Domain        string `json:"domain"`
	AccessKey     string `json:"access_key"`
	SecretKey     string `json:"secret_key"`
	Template      string `json:"template"`
	ExpireSeconds int    `json:"expire_seconds"`
}

type bytedanceMediaURLResolver struct {
	client        *alpha.Client
	domain        string
	template      string
	expireSeconds int
}

func NewBytedanceMediaURLResolver(config MediaURLConfig) (MediaURLResolver, error) {
	if strings.TrimSpace(config.PSM) == "" || strings.TrimSpace(config.Domain) == "" || strings.TrimSpace(config.AccessKey) == "" || strings.TrimSpace(config.SecretKey) == "" {
		return nil, fmt.Errorf("media url psm, domain, access key and secret key are required")
	}
	if config.Template == "" {
		config.Template = "noop"
	}
	if config.ExpireSeconds <= 0 {
		config.ExpireSeconds = 3600
	}
	client, err := alpha.NewClient(config.PSM, config.Domain, "", alpha.WithAkSk(config.AccessKey, config.SecretKey))
	if err != nil {
		return nil, err
	}
	return &bytedanceMediaURLResolver{client: client, domain: config.Domain, template: config.Template, expireSeconds: config.ExpireSeconds}, nil
}

func (resolver *bytedanceMediaURLResolver) ResolveURL(_ context.Context, reference string) (string, error) {
	uri := strings.TrimPrefix(strings.TrimSpace(reference), "tos://")
	if uri == "" {
		return "", fmt.Errorf("media uri is empty")
	}
	result, err := resolver.client.GetUrlWithMaindomain(
		resolver.domain,
		uri,
		resolver.template,
		alpha.WithHttps(),
		alpha.WithFormat(alpha.FORMAT_ORIGINAL),
		alpha.WithExpires(time.Now().Add(time.Duration(resolver.expireSeconds)*time.Second)),
	)
	if err != nil {
		return "", err
	}
	if result == nil || result.MainUrl == "" {
		return "", fmt.Errorf("media url resolver returned an empty url")
	}
	return result.MainUrl, nil
}
