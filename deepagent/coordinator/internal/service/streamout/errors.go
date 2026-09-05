package streamout

import "errors"

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrQueueNotFound    = errors.New("queue not found")
	ErrRecoverExpired   = errors.New("recover expired")
	ErrConsumerMismatch = errors.New("consumer mismatch")
)
