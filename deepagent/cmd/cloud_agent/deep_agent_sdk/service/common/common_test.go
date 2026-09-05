package common

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrors(t *testing.T) {
	var nilError *Error
	require.Empty(t, nilError.Error())
	require.Equal(t, "message", NewError(418, 7, "message", nil).Error())
	require.Equal(t, "message: cause", NewError(418, 7, "message", errors.New("cause")).Error())

	tests := []struct {
		err        *Error
		httpStatus int
		code       int32
	}{
		{err: InvalidArgument("invalid"), httpStatus: http.StatusBadRequest, code: CodeInvalidArgument},
		{err: Unauthenticated(errors.New("cause")), httpStatus: http.StatusUnauthorized, code: CodeUnauthenticated},
		{err: Downstream("rpc", errors.New("cause")), httpStatus: http.StatusBadGateway, code: CodeDownstreamFailure},
		{err: Internal("internal", errors.New("cause")), httpStatus: http.StatusInternalServerError, code: CodeInternal},
		{err: NotImplemented("missing"), httpStatus: http.StatusNotImplemented, code: CodeNotImplemented},
	}
	for _, test := range tests {
		response, status := BaseRespFromError(test.err)
		require.Equal(t, test.httpStatus, status)
		require.Equal(t, test.code, response.StatusCode)
	}

	response, status := BaseRespFromError(nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, BaseRespOK(), response)
	response, status = BaseRespFromError(errors.New("ordinary"))
	require.Equal(t, http.StatusInternalServerError, status)
	require.Equal(t, CodeInternal, response.StatusCode)
	require.Equal(t, "ordinary", response.StatusMessage)
}
