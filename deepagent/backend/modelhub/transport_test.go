package modelhub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"code.byted.org/kite/kitutil"
)

type roundTripFunc func(req *http.Request) (resp *http.Response, err error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	return fn(req)
}

func TestTransportUsesCrawlEndpointAndAKQuery(t *testing.T) {
	endpoint, err := url.Parse("https://aidp.bytedance.net/api/modelhub/online/v2/crawl")
	if err != nil {
		t.Fatal(err)
	}
	transport := &Transport{
		endpoint: endpoint,
		apiKey:   "test-ak",
		base: roundTripFunc(func(req *http.Request) (resp *http.Response, err error) {
			if req.URL.Path != "/api/modelhub/online/v2/crawl" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			if req.URL.Query().Get("ak") != "test-ak" {
				t.Fatalf("unexpected ak query")
			}
			if req.Header.Get("Authorization") != "" {
				t.Fatalf("authorization header was not removed")
			}
			if req.Header.Get("X-TT-LOGID") != "modelhub-test" {
				t.Fatalf("unexpected log id: %s", req.Header.Get("X-TT-LOGID"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("{}")),
				Header:     make(http.Header),
			}, nil
		}),
	}
	ctx := context.WithValue(context.Background(), kitutil.LOGIDKEY, "modelhub-test")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://placeholder.invalid/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-ak")

	if _, err = transport.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
}

func TestTransportDoesNotLeakAKOnRequestError(t *testing.T) {
	endpoint, err := url.Parse("https://aidp.bytedance.net/api/modelhub/online/v2/crawl")
	if err != nil {
		t.Fatal(err)
	}
	transport := &Transport{
		endpoint: endpoint,
		apiKey:   "sensitive-ak",
		base: roundTripFunc(func(req *http.Request) (resp *http.Response, err error) {
			return nil, fmt.Errorf("request failed: %s", req.URL.String())
		}),
	}
	req, err := http.NewRequest(http.MethodPost, "https://placeholder.invalid/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = transport.RoundTrip(req)
	if !errors.Is(err, ErrRequest) || strings.Contains(err.Error(), "sensitive-ak") {
		t.Fatalf("unexpected error: %v", err)
	}
}
