package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPSubscriptionParsesSplitMultilineCRLFAndRecoversWithoutDuplicates(t *testing.T) {
	var subscriptions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ad/deep_agent_sdk/subscribe_timeline":
			attempt := subscriptions.Add(1)
			writer.Header().Set("Content-Type", "text/event-stream")
			flusher := writer.(http.Flusher)
			fmt.Fprint(writer, "event: queue\r\ndata: {\"queue_id\":\"queue-1\",\r\ndata: \"BaseResp\":{\"StatusCode\":0}}\r\n\r\n")
			flusher.Flush()
			if attempt == 1 {
				fmt.Fprint(writer, "event: event\r\ndata: {\"event\":{\"event_id\":\"1\",\"event_type\":\"ASSISTANT_DELTA\",\"session_id\":\""+httpTestSessionID+"\",\"thread_id\":\""+httpTestThreadID+"\",\"turn_id\":\"turn-1\",\"payload\":{\"delta\":\"a\"}},\"BaseResp\":{\"StatusCode\":0}}\r\n\r\n")
				flusher.Flush()
				return
			}
			fmt.Fprint(writer, "event: event\r\ndata: {\"event\":{\"event_id\":\"2\",\"event_type\":\"TURN_FINISHED\",\"session_id\":\""+httpTestSessionID+"\",\"thread_id\":\""+httpTestThreadID+"\",\"turn_id\":\"turn-1\",\"payload\":{}},\"BaseResp\":{\"StatusCode\":0}}\r\n\r\n")
			flusher.Flush()
			<-request.Context().Done()
		case "/ad/deep_agent_sdk/list_timeline":
			if subscriptions.Load() < 2 {
				t.Error("timeline backfill started before the replacement subscription was established")
			}
			writeHTTPJSON(t, writer, map[string]any{
				"events": []any{
					map[string]any{"event_id": "1", "event_type": "ASSISTANT_DELTA", "session_id": httpTestSessionID, "thread_id": httpTestThreadID, "turn_id": "turn-1", "payload": map[string]any{"delta": "a"}},
				},
				"page_info": map[string]any{"has_more": false}, "BaseResp": map[string]any{"StatusCode": 0},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	runtime, err := NewHTTPRuntime(server.URL, "cli-project", "token")
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := runtime.subscribe(context.Background(), httpTestSessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if err = subscription.waitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	subscription.configure(httpTestThreadID, "turn-1", nil, nil)
	first := receiveHTTPEvent(t, subscription.Events())
	second := receiveHTTPEvent(t, subscription.Events())
	if first.EventID != "1" || second.EventID != "2" {
		t.Fatalf("event IDs = %q, %q", first.EventID, second.EventID)
	}
	if subscriptions.Load() < 2 {
		t.Fatalf("subscription attempts = %d", subscriptions.Load())
	}
}

func TestHTTPSubscriptionStopsOnProtocolFailure(t *testing.T) {
	var subscriptions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		subscriptions.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "event: error\ndata: {\"BaseResp\":{\"StatusCode\":403,\"StatusMessage\":\"denied\"}}\n\n")
	}))
	t.Cleanup(server.Close)
	runtime, err := NewHTTPRuntime(server.URL, "cli-project", "token")
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := runtime.subscribe(context.Background(), httpTestSessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = subscription.waitReady(ctx)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("waitReady() error = %v", err)
	}
	if subscriptions.Load() != 1 {
		t.Fatalf("protocol failure retried %d times", subscriptions.Load())
	}
}

func TestHTTPSubscriptionRejectsOversizedMultilineEventWithoutRetry(t *testing.T) {
	var input strings.Builder
	input.WriteString("event: queue\n")
	input.WriteString("data: {\"queue_id\":\"queue-large\",\"padding\":[\n")
	for index := 0; index < 600; index++ {
		input.WriteString("data: \"")
		input.WriteString(strings.Repeat("a", 2048))
		input.WriteString("\",\n")
	}
	input.WriteString("data: \"end\"],\"BaseResp\":{\"StatusCode\":0}}\n\n")
	subscription := &httpSubscription{ready: make(chan struct{})}
	err := subscription.parseSSE(strings.NewReader(input.String()))
	streamErr, ok := err.(*httpStreamError)
	if !ok || streamErr.retryable || !strings.Contains(streamErr.Error(), "event exceeds") {
		t.Fatalf("oversized event error = %#v", err)
	}
}
