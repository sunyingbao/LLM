package timeline

import (
	"bytes"
	"encoding/json"
)

// Event is the timeline wire shape exposed to API, TUI, and WebUI callers.
// The envelope fields come from Agent Coordinator; Payload is the opaque
// CloudAgent event payload written by the worker.
type Event struct {
	EventID     string          `json:"event_id,omitempty"`
	EventType   string          `json:"event_type,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
	ThreadID    string          `json:"thread_id,omitempty"`
	TurnID      string          `json:"turn_id,omitempty"`
	CreatedAtMs int64           `json:"created_at_ms,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

func NormalizePayload(payload []byte) json.RawMessage {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return json.RawMessage("null")
	}
	if json.Valid(payload) {
		return append(json.RawMessage(nil), payload...)
	}
	encoded, _ := json.Marshal(string(payload))
	return json.RawMessage(encoded)
}
