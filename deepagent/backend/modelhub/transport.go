package modelhub

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"code.byted.org/kite/kitutil"
)

var ErrRequest = errors.New("modelhub request failed")

func IsEndpoint(baseURL string) (isEndpoint bool) {
	return strings.HasSuffix(strings.TrimRight(strings.TrimSpace(baseURL), "/"), "/crawl")
}

func NewHTTPClient(baseURL string, apiKey string, timeout time.Duration) (client *http.Client, err error) {
	endpoint, err := url.ParseRequestURI(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, err
	}
	client = &http.Client{
		Transport: &Transport{
			endpoint: endpoint,
			apiKey:   strings.TrimSpace(apiKey),
			base:     http.DefaultTransport,
		},
		Timeout: timeout,
	}
	return client, nil
}

type Transport struct {
	endpoint *url.URL
	apiKey   string
	base     http.RoundTripper
}

func (transport *Transport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	endpoint := *transport.endpoint
	cloned := req.Clone(req.Context())
	cloned.URL = &endpoint
	query := cloned.URL.Query()
	query.Set("ak", transport.apiKey)
	cloned.URL.RawQuery = query.Encode()
	cloned.RequestURI = ""
	cloned.Host = ""
	cloned.Header = req.Header.Clone()
	cloned.Header.Del("Authorization")
	if logID, ok := kitutil.GetCtxLogID(req.Context()); ok && logID != "" {
		cloned.Header.Set("X-TT-LOGID", logID)
	}
	base := transport.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err = base.RoundTrip(cloned)
	if err != nil {
		return nil, ErrRequest
	}
	return resp, nil
}
