package runtime

import (
	"errors"
	"testing"
)

func TestGlobalThreadRefRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []GlobalThreadRef{
		{Runtime: RuntimeLocal, Namespace: "project/a", ThreadID: "thread:1"},
		{Runtime: RuntimeRemote, Namespace: "tenant b", ThreadID: "thread/2"},
	}
	for _, want := range tests {
		want := want
		t.Run(string(want.Runtime), func(t *testing.T) {
			t.Parallel()

			encoded, err := want.MarshalText()
			if err != nil {
				t.Fatalf("MarshalText() error = %v", err)
			}
			var got GlobalThreadRef
			if err = got.UnmarshalText(encoded); err != nil {
				t.Fatalf("UnmarshalText() error = %v", err)
			}
			if got != want {
				t.Fatalf("round trip = %+v, want %+v", got, want)
			}
		})
	}
}

func TestGlobalThreadRefRejectsIncompleteReferences(t *testing.T) {
	t.Parallel()

	for _, ref := range []GlobalThreadRef{
		{ThreadID: "thread-1"},
		{Runtime: RuntimeLocal},
		{Runtime: RuntimeKind("edge"), ThreadID: "thread-1"},
	} {
		if err := ref.Validate(); err == nil {
			t.Fatalf("Validate(%+v) error = nil", ref)
		}
	}
}

func TestRuntimeErrorSupportsIsAndAs(t *testing.T) {
	t.Parallel()

	cause := errors.New("transport closed")
	err := &Error{
		Code:    ErrorCodeUnavailable,
		Op:      "submit",
		Runtime: RuntimeRemote,
		Cause:   cause,
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("errors.Is(%v, ErrUnavailable) = false", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is(%v, cause) = false", err)
	}
	var runtimeErr *Error
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("errors.As(%v, *Error) = false", err)
	}
	if runtimeErr.Code != ErrorCodeUnavailable || runtimeErr.Runtime != RuntimeRemote {
		t.Fatalf("runtime error = %+v", runtimeErr)
	}
}
