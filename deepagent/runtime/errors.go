package runtime

import "fmt"

type ErrorCode string

const (
	ErrorCodeInvalidArgument       ErrorCode = "invalid_argument"
	ErrorCodeNotFound              ErrorCode = "not_found"
	ErrorCodeConflict              ErrorCode = "conflict"
	ErrorCodeCapabilityUnavailable ErrorCode = "capability_unavailable"
	ErrorCodeUnavailable           ErrorCode = "unavailable"
	ErrorCodeCanceled              ErrorCode = "canceled"
	ErrorCodeInternal              ErrorCode = "internal"
)

var (
	ErrInvalidArgument       = &Error{Code: ErrorCodeInvalidArgument}
	ErrNotFound              = &Error{Code: ErrorCodeNotFound}
	ErrConflict              = &Error{Code: ErrorCodeConflict}
	ErrCapabilityUnavailable = &Error{Code: ErrorCodeCapabilityUnavailable}
	ErrUnavailable           = &Error{Code: ErrorCodeUnavailable}
	ErrCanceled              = &Error{Code: ErrorCodeCanceled}
	ErrInternal              = &Error{Code: ErrorCodeInternal}
)

type Error struct {
	Code    ErrorCode
	Op      string
	Runtime RuntimeKind
	Message string
	Cause   error
}

func (e *Error) Error() (message string) {
	if e == nil {
		return ""
	}
	message = string(e.Code)
	if e.Op != "" {
		message = e.Op + ": " + message
	}
	if e.Runtime != "" {
		message += fmt.Sprintf(" (%s)", e.Runtime)
	}
	if e.Message != "" {
		message += ": " + e.Message
	} else if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *Error) Unwrap() (cause error) {
	if e == nil {
		return nil
	}
	cause = e.Cause
	return cause
}

func (e *Error) Is(target error) (matches bool) {
	if e == nil {
		return false
	}
	targetError, ok := target.(*Error)
	if !ok || targetError == nil {
		return false
	}
	matches = targetError.Code == "" || e.Code == targetError.Code
	return matches
}
