package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"eino-cli/deepagent/cloud/protocol/timeline"
	clientruntime "eino-cli/deepagent/host/runtime"
)

type oneShotRuntime struct {
	clientruntime.InteractiveRuntime
	events []timeline.Event
}

func (runtime *oneShotRuntime) StartTurn(_ context.Context, _ string) (stream *clientruntime.TurnStream, err error) {
	events := make(chan timeline.Event, len(runtime.events))
	for _, event := range runtime.events {
		event.TurnID = "turn"
		events <- event
	}
	close(events)
	return &clientruntime.TurnStream{TurnID: "turn", Events: events}, nil
}

func TestRunOneShotReconcilesRecoveredMessages(t *testing.T) {
	for _, test := range []struct {
		name   string
		events []timeline.Event
		want   string
	}{
		{
			name: "partial delta and full recovered snapshot",
			events: []timeline.Event{
				{EventType: "ASSISTANT_DELTA", Payload: json.RawMessage(`{"delta":"hello","llm_response_id":"response-1"}`)},
				{EventType: "ASSISTANT_MESSAGE", Payload: json.RawMessage(`{"parts":[{"type":"text","text":"hello world"}],"llm_response_id":"response-1"}`)},
				{EventType: "ASSISTANT_MESSAGE", Payload: json.RawMessage(`{"parts":[{"type":"text","text":" next"}],"llm_response_id":"response-2"}`)},
			},
			want: "hello world next\n",
		},
		{
			name: "completed deltas are not repeated",
			events: []timeline.Event{
				{EventType: "ASSISTANT_DELTA", Payload: json.RawMessage(`{"delta":"hello","message_id":"message-1"}`)},
				{EventType: "ASSISTANT_MESSAGE", Payload: json.RawMessage(`{"parts":[{"type":"text","text":"hello"}],"message_id":"message-1"}`)},
			},
			want: "hello\n",
		},
		{
			name: "non-prefix recovery stays visible",
			events: []timeline.Event{
				{EventType: "ASSISTANT_DELTA", Payload: json.RawMessage(`{"delta":"AC","llm_response_id":"response-1"}`)},
				{EventType: "ASSISTANT_MESSAGE", Payload: json.RawMessage(`{"parts":[{"type":"text","text":"ABC"}],"llm_response_id":"response-1"}`)},
			},
			want: "AC\n[recovered message]\nABC\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := &oneShotRuntime{events: append(test.events, timeline.Event{EventType: "TURN_FINISHED"})}
			var output strings.Builder
			if err := runOneShot(context.Background(), runtime, "hello", &output); err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("output = %q, want %q", output.String(), test.want)
			}
		})
	}
}
