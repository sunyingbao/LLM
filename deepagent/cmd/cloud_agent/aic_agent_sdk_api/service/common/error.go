package common

import (
	"fmt"
	"net/http"

	httpbase "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/base"
)

const (
	CodeOK                int32 = 0
	CodeInvalidArgument   int32 = 400
	CodeUnauthenticated   int32 = 401
	CodePermissionDenied  int32 = 403
	CodeNotFound          int32 = 404
	CodeDownstreamFailure int32 = 502
	CodeDownstreamTimeout int32 = 504
	CodeInternal          int32 = 500
	CodeNotImplemented    int32 = 501
	CodeStreamInterrupted int32 = 520
)

type Error struct {
	HTTPStatus int
	Code       int32
	Message    string
	Err        error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

func NewError(httpStatus int, code int32, message string, err error) *Error {
	return &Error{HTTPStatus: httpStatus, Code: code, Message: message, Err: err}
}

func InvalidArgument(message string) *Error {
	return NewError(http.StatusBadRequest, CodeInvalidArgument, message, nil)
}

func Unauthenticated(err error) *Error {
	return NewError(http.StatusUnauthorized, CodeUnauthenticated, "unauthenticated", err)
}

func Downstream(method string, err error) *Error {
	return NewError(http.StatusBadGateway, CodeDownstreamFailure, method+" failed", err)
}

func Internal(message string, err error) *Error {
	return NewError(http.StatusInternalServerError, CodeInternal, message, err)
}

func NotImplemented(message string) *Error {
	return NewError(http.StatusNotImplemented, CodeNotImplemented, message, nil)
}

func BaseRespOK() *httpbase.BaseResp {
	return &httpbase.BaseResp{StatusCode: CodeOK, StatusMessage: "OK"}
}

func BaseRespFromError(err error) (*httpbase.BaseResp, int) {
	if err == nil {
		return BaseRespOK(), http.StatusOK
	}
	if e, ok := err.(*Error); ok {
		return &httpbase.BaseResp{StatusCode: e.Code, StatusMessage: e.Error()}, e.HTTPStatus
	}
	return &httpbase.BaseResp{StatusCode: CodeInternal, StatusMessage: err.Error()}, http.StatusInternalServerError
}
