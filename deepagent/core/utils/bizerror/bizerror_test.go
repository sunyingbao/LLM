package bizerror

import (
	"errors"
	"testing"
)

type nilPtrErr struct{}

func (*nilPtrErr) Error() string { return "nilPtrErr" }

type parserErr struct {
	code int32
	msg  string
}

func (e parserErr) Error() string   { return "parserErr" }
func (e parserErr) Code() int32     { return e.code }
func (e parserErr) Message() string { return e.msg }

type customBizErr struct {
	code   int32
	msg    string
	by     string
	origin error
}

func (e customBizErr) Error() string          { return "customBizErr" }
func (e customBizErr) Code() int32            { return e.code }
func (e customBizErr) Message() string        { return e.msg }
func (e customBizErr) CodeProducedBy() string { return e.by }
func (e customBizErr) Origin() error          { return e.origin }

func TestParseError_Nil(t *testing.T) {
	if got := ParseError(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestParseError_TypedNilPointer(t *testing.T) {
	var e *nilPtrErr
	var err error = e
	if err == nil {
		t.Fatalf("expected err interface to be non-nil")
	}
	if got := ParseError(err); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestParseError_PreserveBizError(t *testing.T) {
	be := &bizError{code: 123, msg: "hello", codeProducedBy: "x", origin: errors.New("o")}
	got := ParseError(be)
	if got != be {
		t.Fatalf("expected to return the same BizError instance")
	}
	if ParseErrorCode(be) != 123 {
		t.Fatalf("expected code 123")
	}
}

func TestParseError_FromErrorParser_OutOfRange(t *testing.T) {
	err := parserErr{code: 1, msg: "msg"}
	got := ParseError(err)
	be, ok := got.(*bizError)
	if !ok {
		t.Fatalf("expected *bizError, got %T", got)
	}
	if be.code != 1 {
		t.Fatalf("expected code=1, got %d", be.code)
	}
	if be.msg != "msg" {
		t.Fatalf("expected msg=msg, got %q", be.msg)
	}
	if be.origin == nil {
		t.Fatalf("expected origin to be set")
	}
}

func TestParseError_FromErrorParser_InRangeFallbackToInternal(t *testing.T) {
	err := parserErr{code: ErrCodeMinInclude, msg: "msg"}
	got := ParseError(err)
	be, ok := got.(*bizError)
	if !ok {
		t.Fatalf("expected *bizError, got %T", got)
	}
	if be.code != ErrCodeInternalError {
		t.Fatalf("expected internal code=%d, got %d", ErrCodeInternalError, be.code)
	}
	if be.msg != err.Error() {
		t.Fatalf("expected msg=%q, got %q", err.Error(), be.msg)
	}
}

func TestParseError_GenericFallbackToInternal(t *testing.T) {
	err := errors.New("boom")
	got := ParseError(err)
	be, ok := got.(*bizError)
	if !ok {
		t.Fatalf("expected *bizError, got %T", got)
	}
	if be.code != ErrCodeInternalError {
		t.Fatalf("expected internal code=%d, got %d", ErrCodeInternalError, be.code)
	}
	if be.msg != "boom" {
		t.Fatalf("expected msg=boom, got %q", be.msg)
	}
}

func TestParseErrorCode_CustomBizError_NoPanic(t *testing.T) {
	err := customBizErr{code: 42, msg: "m", by: "y", origin: errors.New("o")}
	if got := ParseErrorCode(err); got != 42 {
		t.Fatalf("expected code=42, got %d", got)
	}
}

func TestParseOrNewError_OverrideInternalFallback(t *testing.T) {
	origin := errors.New("boom")
	got := ParseOrNewError(origin, 999, "default")
	be, ok := got.(*bizError)
	if !ok {
		t.Fatalf("expected *bizError, got %T", got)
	}
	if be.code != 999 {
		t.Fatalf("expected code=999, got %d", be.code)
	}
	if be.msg != "default" {
		t.Fatalf("expected msg=default, got %q", be.msg)
	}
	if be.codeProducedBy != "aic_agent_sdk" {
		t.Fatalf("expected codeProducedBy=aic_agent_sdk, got %q", be.codeProducedBy)
	}
	if be.origin != origin {
		t.Fatalf("expected origin to be preserved")
	}
}

func TestParseOrNewError_DoNotOverrideNonInternalCode(t *testing.T) {
	err := parserErr{code: 1, msg: "msg"}
	got := ParseOrNewError(err, 999, "default")
	be, ok := got.(*bizError)
	if !ok {
		t.Fatalf("expected *bizError, got %T", got)
	}
	if be.code != 1 {
		t.Fatalf("expected code=1, got %d", be.code)
	}
}
