package tasktool

import (
	"strings"
	"time"
)

// MessageObservationKind describes one backend-independent state transition
// observed while a task message is being processed.
type MessageObservationKind string

const (
	MessageObservationResponse         MessageObservationKind = "response"
	MessageObservationApprovalRequired MessageObservationKind = "approval_required"
	MessageObservationFollowupRequired MessageObservationKind = "followup_required"
	MessageObservationInterrupted      MessageObservationKind = "interrupted"
	MessageObservationCompleted        MessageObservationKind = "completed"
	MessageObservationFailed           MessageObservationKind = "failed"
)

// MessageObservation is the backend-independent input to message completion
// evaluation. Adapters may emit an observation with an empty Kind solely to
// associate MessageIDs with a TurnID.
type MessageObservation struct {
	MessageIDs []string
	TurnID     string
	Kind       MessageObservationKind
	Result     string
	SysError   string
	ObservedAt time.Time
}

// ObserveMessageCompletion reduces backend observations into the state exposed
// by wait_message. Correlation and reduction are intentionally separate passes
// so callers may provide events in chronological or reverse order.
func ObserveMessageCompletion(observations []MessageObservation, messageID string) MessageWaitResult {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return MessageWaitResult{Done: true, State: WaitMessageStateFailed, SysError: "invalid message_id"}
	}

	targetTurns := make(map[string]struct{})
	for _, observation := range observations {
		if observation.TurnID != "" && containsMessageID(observation.MessageIDs, messageID) {
			targetTurns[observation.TurnID] = struct{}{}
		}
	}

	responses := make(map[string]observedMessageResponse)
	var (
		terminal     MessageWaitResult
		terminalTurn string
		terminalRank int
		blocked      MessageWaitResult
		blockedRank  int
	)
	for _, observation := range observations {
		if !matchesMessageObservation(observation, messageID, targetTurns) {
			continue
		}
		switch observation.Kind {
		case MessageObservationResponse:
			if observation.Result != "" && isNewerMessageResponse(responses[observation.TurnID], observation) {
				responses[observation.TurnID] = observedMessageResponse{
					result:     observation.Result,
					observedAt: observation.ObservedAt,
				}
			}
		case MessageObservationFailed:
			if terminalRank < 2 {
				terminal = MessageWaitResult{
					Result:   observation.Result,
					Done:     true,
					State:    WaitMessageStateFailed,
					SysError: observation.SysError,
				}
				terminalTurn = observation.TurnID
				terminalRank = 2
			}
		case MessageObservationCompleted:
			if terminalRank < 1 {
				terminal = MessageWaitResult{Done: true, State: WaitMessageStateCompleted, Result: observation.Result}
				terminalTurn = observation.TurnID
				terminalRank = 1
			}
		case MessageObservationInterrupted:
			blocked, blockedRank = strongerBlockedResult(
				blocked,
				blockedRank,
				MessageWaitResult{Result: observation.Result, State: WaitMessageStateInterrupted},
				3,
			)
		case MessageObservationFollowupRequired:
			blocked, blockedRank = strongerBlockedResult(
				blocked,
				blockedRank,
				MessageWaitResult{Result: observation.Result, State: WaitMessageStateFollowupRequired},
				2,
			)
		case MessageObservationApprovalRequired:
			blocked, blockedRank = strongerBlockedResult(
				blocked,
				blockedRank,
				MessageWaitResult{Result: observation.Result, State: WaitMessageStateApprovalRequired},
				1,
			)
		}
	}
	if terminal.Done {
		if terminal.Result == "" {
			terminal.Result = responses[terminalTurn].result
		}
		return terminal
	}
	return blocked
}

type observedMessageResponse struct {
	result     string
	observedAt time.Time
}

func isNewerMessageResponse(current observedMessageResponse, candidate MessageObservation) bool {
	if current.result == "" {
		return true
	}
	return !candidate.ObservedAt.IsZero() && candidate.ObservedAt.After(current.observedAt)
}

func strongerBlockedResult(current MessageWaitResult, currentRank int, candidate MessageWaitResult, candidateRank int) (MessageWaitResult, int) {
	if candidateRank > currentRank {
		return candidate, candidateRank
	}
	return current, currentRank
}

func matchesMessageObservation(observation MessageObservation, messageID string, targetTurns map[string]struct{}) bool {
	if containsMessageID(observation.MessageIDs, messageID) {
		return true
	}
	_, ok := targetTurns[observation.TurnID]
	return observation.TurnID != "" && ok
}

func containsMessageID(messageIDs []string, target string) bool {
	for _, messageID := range messageIDs {
		if messageID == target {
			return true
		}
	}
	return false
}
