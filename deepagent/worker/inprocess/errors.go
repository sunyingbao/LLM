package inprocess

import (
	"errors"

	"eino-cli/deepagent/worker"
)

var (
	ErrMissingThreadStateStore = errors.New("agentworker/inprocess: thread state store is required")
	ErrMissingEventStore       = errors.New("agentworker/inprocess: event store is required")
	ErrMissingThreadFactory    = errors.New("agentworker/inprocess: thread factory is required")
	ErrMissingThreadID         = errors.New("agentworker/inprocess: thread id is required")
	ErrMissingUserID           = errors.New("agentworker/inprocess: user id is required")
	ErrMissingSessionID        = errors.New("agentworker/inprocess: session id is required")
	ErrInvalidThreadState      = errors.New("agentworker/inprocess: invalid thread state")
	ErrInvalidMessage          = errors.New("agentworker/inprocess: invalid message")
	ErrInvalidResumeRequest    = errors.New("agentworker/inprocess: invalid resume request")
	ErrInvalidInterruptRequest = errors.New("agentworker/inprocess: invalid interrupt request")
	ErrThreadNotFound          = errors.New("agentworker/inprocess: thread not found")
	ErrThreadBlocked           = errors.New("agentworker/inprocess: thread is blocked")
	ErrThreadClosed            = agentworker.ErrThreadClosed
)
