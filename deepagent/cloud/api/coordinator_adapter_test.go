package api

import (
	"testing"
	"time"

	"eino-cli/deepagent/coordinator"
)

func TestCoordinatorDirection(t *testing.T) {
	if got := coordinatorDirection(false); got != coordinator.ListDirectionForward {
		t.Fatalf("forward direction = %q", got)
	}
	if got := coordinatorDirection(true); got != coordinator.ListDirectionBackward {
		t.Fatalf("backward direction = %q", got)
	}
}

func TestTimelineEventFromCoordinator(t *testing.T) {
	createdAt := time.UnixMilli(1234)
	got := timelineEvent(&coordinator.Event{
		EventID:   10,
		ThreadID:  20,
		TurnID:    "run-1",
		EventType: "message",
		Payload:   []byte(`{"text":"hello"}`),
		CreatedAt: createdAt,
	})
	if got.EventID != "10" || got.ThreadID != "20" || got.TurnID != "run-1" || got.CreatedAtMs != 1234 {
		t.Fatalf("timeline event = %+v", got)
	}
}

func TestParseRequiredInt64(t *testing.T) {
	got, err := parseRequiredInt64("42", "thread_id")
	if err != nil || got != 42 {
		t.Fatalf("parse result = %d, %v", got, err)
	}
	if _, err = parseRequiredInt64("", "thread_id"); err == nil {
		t.Fatal("empty required id should fail")
	}
}
