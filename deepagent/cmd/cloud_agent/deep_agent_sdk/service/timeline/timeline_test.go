package timeline

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	prototimeline "eino-cli/deepagent/cloud/protocol/timeline"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/deep_agent_sdk_common"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/common"
)

func TestListResponseWireShapeUsesACEnvelopeAndPayload(t *testing.T) {
	resp := &ListResponse{
		Events: []*TimelineEvent{
			{
				EventID:     "123",
				EventType:   "ASSISTANT_MESSAGE",
				SessionID:   "42",
				ThreadID:    "456",
				TurnID:      "turn-1",
				CreatedAtMs: 789,
				Payload:     prototimeline.NormalizePayload([]byte(`{"parts":[{"type":"text","text":"hello"}]}`)),
			},
		},
		PageInfo: &httpcommon.PageInfo{},
		BaseResp: common.BaseRespOK(),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "event_json") {
		t.Fatalf("response still contains event_json: %s", data)
	}
	var decoded struct {
		Events []struct {
			EventID   string         `json:"event_id"`
			EventType string         `json:"event_type"`
			Payload   map[string]any `json:"payload"`
		} `json:"events"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(decoded.Events) != 1 || decoded.Events[0].EventID != "123" || decoded.Events[0].EventType != "ASSISTANT_MESSAGE" {
		t.Fatalf("unexpected response shape: %s", data)
	}
	if decoded.Events[0].Payload["event_type"] != nil {
		t.Fatalf("payload should not contain event header fields: %s", data)
	}
	if _, ok := decoded.Events[0].Payload["parts"]; !ok {
		t.Fatalf("payload was not emitted as JSON object: %s", data)
	}
}

func TestWriteSSEEventWireShapeUsesPayload(t *testing.T) {
	var out bytes.Buffer
	err := writeSSE(&out, "event", &SubscribeResponse{
		Event: &TimelineEvent{
			EventID:   "123",
			EventType: "ASSISTANT_DELTA",
			Payload:   prototimeline.NormalizePayload([]byte(`{"delta":"hi"}`)),
		},
		BaseResp: common.BaseRespOK(),
	})
	if err != nil {
		t.Fatalf("writeSSE() error = %v", err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "event: event\n") || strings.Contains(got, "event_json") || !strings.Contains(got, `"event_type":"ASSISTANT_DELTA"`) || !strings.Contains(got, `"payload":{"delta":"hi"}`) {
		t.Fatalf("unexpected SSE frame: %s", got)
	}
}

func TestSubscribeTreatsEOFAsExpectedStreamClose(t *testing.T) {
	if !isExpectedStreamClose(context.Background(), io.EOF) {
		t.Fatal("io.EOF should be classified as an expected stream close")
	}
}
