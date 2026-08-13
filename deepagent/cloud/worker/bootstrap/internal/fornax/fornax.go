package fornax

import (
	"context"
	"fmt"
	"net/http"
	"time"

	einofornax "code.byted.org/flow/eino-byted-ext/callbacks/fornax"
	"code.byted.org/flowdevops/fornax_sdk"
	"code.byted.org/flowdevops/fornax_sdk/domain"
	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/cloud/worker/bootstrap/config"
	"github.com/cloudwego/eino/callbacks"
)

// Runtime owns the Fornax SDK lifecycle for one worker process.
type Runtime struct {
	enabled  bool
	handlers []callbacks.Handler
}

func Build(ctx context.Context, cfg config.FornaxConfig) (*Runtime, error) {
	if !cfg.Enabled {
		return &Runtime{}, nil
	}

	sdkConfig := &domain.Config{
		Identity: &domain.Identity{
			AK: cfg.AK,
			SK: cfg.SK,
		},
	}
	if cfg.Region != "" {
		region := cfg.Region
		sdkConfig.FornaxCustomRegion = &region
	}

	var opts []domain.Option
	opts = append(opts, domain.WithHTTPClient(newDirectHTTPClient(cfg.HTTPTimeoutMS)))
	if cfg.HTTPTimeoutMS > 0 {
		opts = append(opts, domain.WithHTTPTimeout(time.Duration(cfg.HTTPTimeoutMS)*time.Millisecond))
	}
	client, err := fornax_sdk.NewClient(sdkConfig, opts...)
	if err != nil {
		return nil, fmt.Errorf("init fornax client: %w", err)
	}
	handler := newCorrelationHandler(client, einofornax.NewDefaultCallbackHandler(client))
	logs.CtxInfo(ctx, "[cloud_agent worker] fornax trace ready: region=%s http_timeout_ms=%d", logRegion(sdkConfig.FornaxCustomRegion), cfg.HTTPTimeoutMS)
	return &Runtime{enabled: true, handlers: []callbacks.Handler{handler}}, nil
}

func (r *Runtime) Handlers() []callbacks.Handler {
	if r == nil || len(r.handlers) == 0 {
		return nil
	}
	return append([]callbacks.Handler(nil), r.handlers...)
}

func (r *Runtime) Close() {
	if r == nil || !r.enabled {
		return
	}
	einofornax.Close()
}

func logRegion(region *string) string {
	if region == nil || *region == "" {
		return "auto"
	}
	return *region
}

func newDirectHTTPClient(timeoutMS int) *http.Client {
	timeout := 10 * time.Minute
	if timeoutMS > 0 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Local development machines may export a corp HTTP proxy that rejects
	// CONNECT to Fornax hosts. Fornax endpoints are internal and should be
	// reached directly, matching fornax-cli's successful network path.
	transport.Proxy = nil
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
