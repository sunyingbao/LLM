package agentworker

import "errors"

var (
	// ErrThreadClosed tells a host that the runtime can no longer accept input.
	// Hosts must not ack the rejected message.
	ErrThreadClosed       = errors.New("agentworker: thread is closed")
	ErrThreadBackpressure = errors.New("agentworker: thread input queue is full")
)
