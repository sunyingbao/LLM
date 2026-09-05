package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"eino-cli/deepagent/cloud/protocol/timeline"
)

const (
	httpEventBufferSize  = 128
	httpSSEMaxLineBytes  = 1 << 20
	httpSSEMaxEventBytes = 1 << 20
	httpReadAttempts     = 3
)

type httpSubscription struct {
	runtime   *HTTPRuntime
	sessionID string
	ctx       context.Context
	cancel    context.CancelFunc

	rawEvents chan timeline.Event
	events    chan timeline.Event
	config    chan httpSubscriptionConfig
	ready     chan struct{}
	done      chan struct{}

	mu            sync.Mutex
	err           error
	readyErr      error
	queueID       string
	threadID      string
	needsBackfill bool
	readyOnce     sync.Once
	closeOnce     sync.Once
	configureOnce sync.Once
}

type httpSubscriptionConfig struct {
	ThreadID string
	TurnID   string
	Replay   []timeline.Event
	Known    []timeline.Event
}

type httpStreamError struct {
	err       error
	retryable bool
}

func (streamErr *httpStreamError) Error() (message string) {
	if streamErr == nil || streamErr.err == nil {
		return "HTTP stream failed"
	}
	return streamErr.err.Error()
}

func (subscription *httpSubscription) Events() (events <-chan timeline.Event) {
	if subscription == nil {
		return nil
	}
	return subscription.events
}

func (subscription *httpSubscription) Err() (err error) {
	if subscription == nil {
		return nil
	}
	subscription.mu.Lock()
	err = subscription.err
	subscription.mu.Unlock()
	return err
}

func (subscription *httpSubscription) Close() (err error) {
	if subscription == nil {
		return nil
	}
	subscription.closeOnce.Do(func() { subscription.cancel() })
	<-subscription.done
	return nil
}

func (subscription *httpSubscription) waitReady(ctx context.Context) (err error) {
	if subscription == nil {
		return fmt.Errorf("HTTP subscription is required")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-subscription.ready:
		subscription.mu.Lock()
		err = subscription.readyErr
		subscription.mu.Unlock()
		return err
	}
}

func (subscription *httpSubscription) setThread(threadID string) {
	if subscription == nil {
		return
	}
	subscription.mu.Lock()
	subscription.threadID = strings.TrimSpace(threadID)
	subscription.mu.Unlock()
}

func (subscription *httpSubscription) configure(threadID string, turnID string, replay []timeline.Event, known []timeline.Event) {
	if subscription == nil {
		return
	}
	subscription.setThread(threadID)
	configuration := httpSubscriptionConfig{
		ThreadID: strings.TrimSpace(threadID), TurnID: strings.TrimSpace(turnID),
		Replay: append([]timeline.Event(nil), replay...), Known: append([]timeline.Event(nil), known...),
	}
	subscription.configureOnce.Do(func() {
		select {
		case <-subscription.ctx.Done():
		case subscription.config <- configuration:
		}
	})
}

func (subscription *httpSubscription) read() {
	defer close(subscription.rawEvents)
	queueWasReady := false
	for attempt := 0; attempt < httpReadAttempts; attempt++ {
		if err := subscription.ctx.Err(); err != nil {
			return
		}
		subscription.mu.Lock()
		subscription.needsBackfill = attempt > 0 && queueWasReady
		subscription.mu.Unlock()
		err := subscription.readOnce()
		if err == nil {
			err = &httpStreamError{err: io.EOF, retryable: true}
		}
		if subscription.ctx.Err() != nil {
			return
		}
		streamErr, ok := err.(*httpStreamError)
		if !ok || !streamErr.retryable {
			subscription.fail(err)
			return
		}
		subscription.mu.Lock()
		queueWasReady = subscription.queueID != ""
		subscription.mu.Unlock()
		if attempt+1 == httpReadAttempts {
			subscription.fail(err)
			return
		}
		delay := time.Duration(attempt+1) * 25 * time.Millisecond
		select {
		case <-subscription.ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (subscription *httpSubscription) readOnce() (err error) {
	subscription.mu.Lock()
	queueID := subscription.queueID
	subscription.mu.Unlock()
	body := map[string]any{"session_id": subscription.sessionID}
	if queueID != "" {
		body["recover_queue_id"] = queueID
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return &httpStreamError{err: err, retryable: false}
	}
	request, err := http.NewRequestWithContext(subscription.ctx, http.MethodPost, subscription.runtime.apiBaseURL+"/subscribe_timeline", bytes.NewReader(encoded))
	if err != nil {
		return &httpStreamError{err: err, retryable: false}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if subscription.runtime.token != "" {
		request.Header.Set(httpUserTokenHeader, subscription.runtime.token)
	}
	response, err := subscription.runtime.client.Do(request)
	if err != nil {
		return &httpStreamError{err: fmt.Errorf("subscribe_timeline request failed: %w", err), retryable: true}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, readErr := io.ReadAll(io.LimitReader(response.Body, httpResponseMaxBytes))
		if readErr != nil {
			data = nil
		}
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = response.Status
		}
		retryable := response.StatusCode >= 500
		return &httpStreamError{err: fmt.Errorf("subscribe_timeline failed: HTTP %d: %s", response.StatusCode, message), retryable: retryable}
	}
	err = subscription.parseSSE(response.Body)
	return err
}

func (subscription *httpSubscription) parseSSE(reader io.Reader) (err error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), httpSSEMaxLineBytes)
	eventName := "message"
	dataLines := make([]string, 0, 2)
	eventBytes := 0
	dispatch := func() (dispatchErr error) {
		if len(dataLines) == 0 {
			eventName = "message"
			eventBytes = 0
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		eventBytes = 0
		dispatchErr = subscription.acceptFrame(eventName, []byte(data))
		eventName = "message"
		return dispatchErr
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err = dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			field = line
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			additionalBytes := len(value)
			if len(dataLines) > 0 {
				additionalBytes++
			}
			if eventBytes > httpSSEMaxEventBytes-additionalBytes {
				return &httpStreamError{err: fmt.Errorf("subscribe_timeline SSE event exceeds %d bytes", httpSSEMaxEventBytes), retryable: false}
			}
			eventBytes += additionalBytes
			dataLines = append(dataLines, value)
		}
	}
	if err = scanner.Err(); err != nil {
		return &httpStreamError{err: fmt.Errorf("read subscribe_timeline stream: %w", err), retryable: true}
	}
	if err = dispatch(); err != nil {
		return err
	}
	return &httpStreamError{err: io.EOF, retryable: true}
}

func (subscription *httpSubscription) acceptFrame(eventName string, data []byte) (err error) {
	var response struct {
		QueueID string          `json:"queue_id"`
		Event   *timeline.Event `json:"event"`
		httpResponseBase
	}
	if err = json.Unmarshal(data, &response); err != nil {
		return &httpStreamError{err: fmt.Errorf("decode subscribe_timeline SSE frame: %w", err), retryable: false}
	}
	statusCode, statusMessage := responseStatus(response.httpResponseBase)
	if statusCode != 0 {
		if statusMessage == "" {
			statusMessage = eventName
		}
		return &httpStreamError{err: fmt.Errorf("subscribe_timeline failed with code %d: %s", statusCode, statusMessage), retryable: false}
	}
	if response.QueueID != "" {
		subscription.mu.Lock()
		subscription.queueID = response.QueueID
		needsBackfill := subscription.needsBackfill
		subscription.needsBackfill = false
		subscription.mu.Unlock()
		subscription.readyOnce.Do(func() { close(subscription.ready) })
		if needsBackfill {
			if err = subscription.backfill(); err != nil {
				return &httpStreamError{err: fmt.Errorf("backfill timeline after reconnect: %w", err), retryable: false}
			}
		}
		return nil
	}
	if response.Event == nil {
		return nil
	}
	select {
	case <-subscription.ctx.Done():
		return subscription.ctx.Err()
	case subscription.rawEvents <- *response.Event:
		return nil
	}
}

func (subscription *httpSubscription) backfill() (err error) {
	subscription.mu.Lock()
	threadID := subscription.threadID
	subscription.mu.Unlock()
	events, err := subscription.runtime.listTimelineAll(subscription.ctx, subscription.sessionID, threadID)
	if err != nil {
		return err
	}
	for _, event := range events {
		select {
		case <-subscription.ctx.Done():
			return subscription.ctx.Err()
		case subscription.rawEvents <- event:
		}
	}
	return nil
}

func (subscription *httpSubscription) forward() {
	defer close(subscription.events)
	defer close(subscription.done)
	var configuration httpSubscriptionConfig
	select {
	case <-subscription.ctx.Done():
		return
	case configuration = <-subscription.config:
	}
	seen := make(map[string]bool)
	for _, event := range configuration.Known {
		if event.EventID != "" {
			seen[event.EventID] = true
		}
	}
	for _, event := range configuration.Replay {
		if event.EventID != "" {
			seen[event.EventID] = true
		}
		if !subscription.emit(event) {
			return
		}
	}
	for event := range subscription.rawEvents {
		if event.EventID != "" && seen[event.EventID] {
			continue
		}
		if event.EventID != "" {
			seen[event.EventID] = true
		}
		if configuration.ThreadID != "" && event.ThreadID != configuration.ThreadID {
			continue
		}
		if configuration.TurnID != "" && event.TurnID != configuration.TurnID {
			continue
		}
		if !subscription.emit(event) {
			return
		}
	}
}

func (subscription *httpSubscription) emit(event timeline.Event) (sent bool) {
	select {
	case <-subscription.ctx.Done():
		return false
	case subscription.events <- event:
		return true
	}
}

func (subscription *httpSubscription) fail(err error) {
	if err == nil {
		return
	}
	if streamErr, ok := err.(*httpStreamError); ok {
		err = streamErr.err
	}
	subscription.mu.Lock()
	subscription.err = err
	subscription.readyErr = err
	subscription.mu.Unlock()
	subscription.readyOnce.Do(func() { close(subscription.ready) })
}
