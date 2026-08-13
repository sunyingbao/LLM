package inprocess

import "eino-cli/deepagent/worker"

// ThreadEventSubscription is one live event subscription for a thread.
//
// Replay remains the responsibility of ListEvents; subscriptions only stream
// new live events published after subscription.
type ThreadEventSubscription struct {
	Events <-chan *agentworker.Event
	cancel func()
}

func (s *ThreadEventSubscription) Close() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
}

// SessionEventSubscription is one live event subscription for a session.
//
// Replay remains the caller's responsibility; subscriptions only stream new
// live events published after subscription.
type SessionEventSubscription struct {
	Events <-chan *agentworker.Event
	cancel func()
}

func (s *SessionEventSubscription) Close() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
}

// ListEventsOptions configures one thread event listing request.
type ListEventsOptions struct {
	Limit   int
	Cursor  string
	Reverse bool
}
