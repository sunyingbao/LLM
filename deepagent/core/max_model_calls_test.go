package deepagents

import (
	"errors"
	"testing"
)

func TestMaxModelCallsStatePersistsConsumedCalls(t *testing.T) {
	state := newMaxModelCallsState(3)
	if err := state.consume(); err != nil {
		t.Fatalf("first consume error = %v", err)
	}
	if err := state.consume(); err != nil {
		t.Fatalf("second consume error = %v", err)
	}

	resumed := newMaxModelCallsState(3)
	if err := resumed.UnmarshalRuntimeState(state.MarshalRuntimeState()); err != nil {
		t.Fatalf("UnmarshalRuntimeState() error = %v", err)
	}
	if err := resumed.consume(); err != nil {
		t.Fatalf("third consume after resume error = %v", err)
	}
	if err := resumed.consume(); !errors.Is(err, ErrExceedMaxModelCalls) {
		t.Fatalf("fourth consume error = %v, want ErrExceedMaxModelCalls", err)
	}
}
