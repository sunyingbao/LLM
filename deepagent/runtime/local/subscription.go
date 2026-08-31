package local

import (
	"context"
	"sync"

	"eino-cli/deepagent/cloud/protocol/timeline"
	"eino-cli/deepagent/worker"
)

func workerEventsAfter(events []*agentworker.Event, eventID string) (filtered []*agentworker.Event) {
	if eventID == "" {
		return events
	}
	for i, event := range events {
		if event != nil && event.ID == eventID {
			return events[i+1:]
		}
	}
	return events
}

type subscriptionStream struct {
	events chan timeline.Event
	cancel func()
	once   sync.Once
	mu     sync.Mutex
	err    error
}

func (stream *subscriptionStream) Events() (events <-chan timeline.Event) { return stream.events }
func (stream *subscriptionStream) Err() (err error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.err
}
func (stream *subscriptionStream) Close() (err error) {
	stream.once.Do(stream.cancel)
	return nil
}

func (stream *subscriptionStream) forward(ctx context.Context, replay []*agentworker.Event, live <-chan *agentworker.Event) {
	defer close(stream.events)
	seen := make(map[string]struct{}, len(replay))
	for _, event := range replay {
		seen[event.ID] = struct{}{}
		if !stream.send(ctx, timelineEventFromWorker(event)) {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			stream.mu.Lock()
			stream.err = ctx.Err()
			stream.mu.Unlock()
			return
		case event, ok := <-live:
			if !ok {
				return
			}
			if _, duplicate := seen[event.ID]; duplicate {
				continue
			}
			seen[event.ID] = struct{}{}
			if !stream.send(ctx, timelineEventFromWorker(event)) {
				return
			}
		}
	}
}

func (stream *subscriptionStream) send(ctx context.Context, event timeline.Event) (sent bool) {
	select {
	case <-ctx.Done():
		return false
	case stream.events <- event:
		return true
	}
}
