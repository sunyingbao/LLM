package bizerror

import (
	"errors"
	"fmt"
	"reflect"

	"code.byted.org/overpass/common/rpc_error"
)

const (
	ErrCodeSuccess int32 = 0

	ErrCodeMinInclude int32 = 543 * 1e6
	ErrCodeMaxExclude int32 = 544 * 1e6

	ErrCodeBizFileWatchBuildBizMetaError int32 = 543400101

	ErrCodeInternalError                int32 = 543500001
	ErrCodeInternalFileWatchCreateError int32 = 543500101
	ErrCodeInternalFileWatchPollError   int32 = 543500102
)

var (
	_ BizError = &bizError{}
)

// BizError is the minimal error contract we need in this repo.
type BizError interface {
	error
	Code() int32
	Message() string
	CodeProducedBy() string
	Origin() error
}

type errorParser interface {
	error
	Code() int32
	Message() string
}

type bizError struct {
	code           int32
	msg            string
	codeProducedBy string
	origin         error
}

func (b *bizError) Error() string {
	return fmt.Sprintf("<bizError code=[%d] msg=[%s]>", b.code, b.msg)
}

func (b *bizError) Code() int32 {
	return b.code
}

func (b *bizError) Message() string {
	return b.msg
}

func (b *bizError) CodeProducedBy() string {
	return b.codeProducedBy
}

func (b *bizError) Origin() error {
	return b.origin
}

func ParseError(err error) error {
	if err == nil {
		return nil
	}

	errValue := reflect.ValueOf(err)
	switch errValue.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Map, reflect.Func, reflect.Interface:
		if errValue.IsNil() {
			return nil
		}
	}

	var bizErr BizError
	if errors.As(err, &bizErr) {
		return bizErr
	}

	var rpcErr *rpc_error.RPCError
	if errors.As(err, &rpcErr) {
		if rpcErr.Is(rpc_error.RPC_STATUS_CODE_NOT_ZERO) {
			if rpcErr.GetBizStatusCode() < ErrCodeMinInclude || rpcErr.GetBizStatusCode() >= ErrCodeMaxExclude {
				return &bizError{
					code:           rpcErr.GetBizStatusCode(),
					msg:            rpcErr.GetBizStatusMessage(),
					codeProducedBy: rpcErr.PSM,
					origin:         err,
				}
			}
		}
	}

	var errParser errorParser
	if errors.As(err, &errParser) {
		code := errParser.Code()
		msg := errParser.Message()
		if code < ErrCodeMinInclude || code >= ErrCodeMaxExclude {
			return &bizError{
				code:           code,
				msg:            msg,
				codeProducedBy: "",
				origin:         err,
			}
		}
	}

	return &bizError{
		code:   ErrCodeInternalError,
		msg:    err.Error(),
		origin: err,
	}
}

func ParseErrorCode(err error) int32 {
	e := ParseError(err)
	if e == nil {
		return ErrCodeSuccess
	}
	if be, ok := e.(BizError); ok {
		return be.Code()
	}
	return ErrCodeInternalError
}

// ParseOrNewError converts a generic error into a BizError-like error when needed.
func ParseOrNewError(err error, defaultCode int32, defaultMessage string) error {
	e := ParseError(err)
	if e == nil {
		return nil
	}
	if ee, ok := e.(BizError); ok && ee.Code() == ErrCodeInternalError {
		return &bizError{
			code:           defaultCode,
			msg:            defaultMessage,
			codeProducedBy: "aic_agent_sdk",
			origin:         err,
		}
	}
	return e
}
