package eventlog

import "errors"

var (
	ErrLeaseMismatch     = errors.New("event lease expired or no longer owned")
	ErrNamespaceNotFound = errors.New("namespace not found")
	ErrThreadNotFound    = errors.New("thread not found")
	ErrSessionIDRequired = errors.New("session_id is required")
	ErrTurnIDRequired    = errors.New("turn_id is required")
	ErrEventTypeRequired = errors.New("event_type is required")
	ErrInvalidCursor     = errors.New("invalid cursor")
)
