package tasktool

import (
	"testing"
	"time"
)

func TestObserveMessageCompletionIsIndependentOfEventOrder(t *testing.T) {
	chronological := []MessageObservation{
		{TurnID: "turn_1", MessageIDs: []string{"msg_1"}},
		{TurnID: "turn_1", Kind: MessageObservationResponse, Result: "child result"},
		{TurnID: "turn_1", Kind: MessageObservationCompleted},
	}
	reversed := []MessageObservation{chronological[2], chronological[1], chronological[0]}

	for name, observations := range map[string][]MessageObservation{
		"chronological": chronological,
		"reversed":      reversed,
	} {
		t.Run(name, func(t *testing.T) {
			got := ObserveMessageCompletion(observations, "msg_1")
			if !got.Done || got.State != WaitMessageStateCompleted || got.Result != "child result" {
				t.Fatalf("ObserveMessageCompletion() = %+v", got)
			}
		})
	}
}

func TestObserveMessageCompletionUsesLatestResponseInEitherEventOrder(t *testing.T) {
	startedAt := time.Unix(100, 0)
	chronological := []MessageObservation{
		{TurnID: "turn_1", MessageIDs: []string{"msg_1"}, ObservedAt: startedAt},
		{TurnID: "turn_1", Kind: MessageObservationResponse, Result: "draft", ObservedAt: startedAt.Add(time.Second)},
		{TurnID: "turn_1", Kind: MessageObservationResponse, Result: "final", ObservedAt: startedAt.Add(2 * time.Second)},
		{TurnID: "turn_1", Kind: MessageObservationCompleted, ObservedAt: startedAt.Add(3 * time.Second)},
	}
	reversed := []MessageObservation{chronological[3], chronological[2], chronological[1], chronological[0]}

	for name, observations := range map[string][]MessageObservation{
		"chronological": chronological,
		"reversed":      reversed,
	} {
		t.Run(name, func(t *testing.T) {
			got := ObserveMessageCompletion(observations, "msg_1")
			if !got.Done || got.State != WaitMessageStateCompleted || got.Result != "final" {
				t.Fatalf("ObserveMessageCompletion() = %+v", got)
			}
		})
	}
}

func TestObserveMessageCompletionTerminalStateOverridesBlockedState(t *testing.T) {
	observations := []MessageObservation{
		{
			TurnID:     "turn_1",
			MessageIDs: []string{"msg_1"},
			Kind:       MessageObservationApprovalRequired,
			Result:     "waiting for approval",
		},
		{
			TurnID: "turn_1",
			Kind:   MessageObservationFailed,
			Result: "resume failed",
		},
	}

	got := ObserveMessageCompletion(observations, "msg_1")
	if !got.Done || got.State != WaitMessageStateFailed || got.Result != "resume failed" {
		t.Fatalf("ObserveMessageCompletion() = %+v", got)
	}
}

func TestObserveMessageCompletionReportsStrongestBlockedStateAsNonTerminal(t *testing.T) {
	observations := []MessageObservation{
		{
			TurnID:     "turn_1",
			MessageIDs: []string{"msg_1"},
			Kind:       MessageObservationApprovalRequired,
			Result:     "waiting for approval",
		},
		{TurnID: "turn_1", Kind: MessageObservationFollowupRequired, Result: "waiting for input"},
		{TurnID: "turn_1", Kind: MessageObservationInterrupted, Result: "interrupted"},
	}

	got := ObserveMessageCompletion(observations, "msg_1")
	if got.Done || got.State != WaitMessageStateInterrupted || got.Result != "interrupted" {
		t.Fatalf("ObserveMessageCompletion() = %+v", got)
	}
}
