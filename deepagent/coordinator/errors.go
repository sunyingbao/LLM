package coordinator

import (
	"eino-cli/deepagent/coordinator/internal/storage"
	"errors"
)

var (
	ErrNamespaceNotFound       = errors.New("namespace not found")
	ErrThreadNotFound          = errors.New("thread not found")
	ErrThreadNotRunnable       = errors.New("thread not runnable")
	ErrThreadNotBlocked        = errors.New("thread not blocked")
	ErrLeaseMismatch           = errors.New("lease mismatch")
	ErrInvalidStatusTransition = errors.New("invalid thread status transition")
	ErrSessionEnvMismatch      = errors.New("session env mismatch")
	ErrThreadClosed            = errors.New("thread closed")
	ErrRedisUnavailable        = errors.New("mailbox redis unavailable")
	ErrMessageNotFound         = storage.ErrMessageNotFound
	ErrInvalidCancel           = errors.New("invalid cancel input")
	ErrInvalidClose            = errors.New("invalid close thread")
	ErrThreadBlocked           = errors.New("thread blocked")
)
