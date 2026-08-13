package local

import (
	"context"
	"sync"

	"eino-cli/deepagent/cloud/protocol/timeline"
	runtimeclient "eino-cli/deepagent/runtime"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/inprocess"
)

func (client *Client) ListTimeline(ctx context.Context, query runtimeclient.TimelineQuery) (result *runtimeclient.TimelineResult, err error) {
	if err = validateLocalRef(query.Ref, "list_timeline"); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 10000
	}
	events, err := client.worker.ListEvents(ctx, query.Ref.ThreadID, inprocess.ListEventsOptions{Limit: limit})
	if err != nil {
		return nil, wrapError("list_timeline", err)
	}
	events = workerEventsAfter(events, query.AfterEventID)
	result = &runtimeclient.TimelineResult{Events: make([]timeline.Event, 0, len(events))}
	for _, event := range events {
		result.Events = append(result.Events, timelineEventFromWorker(event))
	}
	return result, nil
}

func (client *Client) SubscribeTimeline(ctx context.Context, query runtimeclient.TimelineQuery) (subscription runtimeclient.TimelineSubscription, err error) {
	if err = validateLocalRef(query.Ref, "subscribe_timeline"); err != nil {
		return nil, err
	}
	live, err := client.worker.SubscribeThreadEvents(ctx, query.Ref.ThreadID, client.options.SubscriberBuffer)
	if err != nil {
		return nil, wrapError("subscribe_timeline", err)
	}
	replay, err := client.worker.ListEvents(ctx, query.Ref.ThreadID, inprocess.ListEventsOptions{Limit: 10000})
	if err != nil {
		live.Close()
		return nil, wrapError("subscribe_timeline", err)
	}
	replay = workerEventsAfter(replay, query.AfterEventID)
	stream := &subscriptionStream{events: make(chan timeline.Event, len(replay)+16), cancel: live.Close}
	go stream.forward(ctx, replay, live.Events)
	return stream, nil
}

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
