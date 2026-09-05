package redis

import (
	"testing"
	"time"

	"github.com/bytedance/sonic"
)

func TestMailboxKeys(t *testing.T) {
	if got := PendingInputKey("dreamina", 123); got != "ac:dreamina:thread:123:input" {
		t.Fatalf("pending input key = %s", got)
	}
	if got := MessageKey(456); got != "ac:message:456" {
		t.Fatalf("message key = %s", got)
	}
	if got := SessionQueueSetKey("dreamina", "sess-1"); got != "ac:dreamina:session:sess-1:queues" {
		t.Fatalf("session queue key = %s", got)
	}
	if got := StreamQueueMetaKey("9001"); got != "ac:queue:9001:meta" {
		t.Fatalf("stream queue meta key = %s", got)
	}
	if got := StreamQueuePendingKey("9001"); got != "ac:queue:9001:pending" {
		t.Fatalf("stream queue pending key = %s", got)
	}
	if got := StreamQueueSequenceKey("9001"); got != "ac:queue:9001:seq" {
		t.Fatalf("stream queue sequence key = %s", got)
	}
	if got := SessionLiveSequenceKey("dreamina", "sess-1"); got != "ac:dreamina:session:sess-1:live:seq" {
		t.Fatalf("session live sequence key = %s", got)
	}
	if got := SessionLiveEventKey("dreamina", "sess-1", 12); got != "ac:dreamina:session:sess-1:live:12" {
		t.Fatalf("session live event key = %s", got)
	}
}

func TestCachedMessageJSONRoundTrip(t *testing.T) {
	handledAt := time.Date(2026, 4, 13, 15, 4, 5, 0, time.UTC)
	msg := CachedMessage{
		MessageID:    1,
		Namespace:    "dreamina",
		ThreadID:     2,
		SenderType:   "user",
		SenderID:     "u1",
		MessageType:  "input",
		Status:       "pending",
		Payload:      `{"task":"payload"}`,
		MetadataJSON: `{"k":"v"}`,
		CreatedAt:    time.Date(2026, 4, 13, 15, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 4, 13, 15, 1, 0, 0, time.UTC),
		HandledAt:    &handledAt,
	}

	raw, err := sonic.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal cached message: %v", err)
	}

	var decoded CachedMessage
	if err := sonic.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal cached message: %v", err)
	}

	if decoded.MessageID != msg.MessageID || decoded.ThreadID != msg.ThreadID || decoded.Namespace != msg.Namespace {
		t.Fatalf("decoded message mismatch: %+v", decoded)
	}
	if decoded.Payload != msg.Payload {
		t.Fatalf("decoded payload = %q, want %q", decoded.Payload, msg.Payload)
	}
	if decoded.HandledAt == nil || !decoded.HandledAt.Equal(*msg.HandledAt) {
		t.Fatalf("decoded handled_at = %v, want %v", decoded.HandledAt, msg.HandledAt)
	}
}

func TestStreamQueueMetaJSONRoundTrip(t *testing.T) {
	deliveredAt := time.Date(2026, 4, 16, 10, 2, 0, 0, time.UTC)
	meta := StreamQueueMeta{
		QueueID:               "9001",
		Namespace:             "dreamina",
		SessionID:             "sess-1",
		Status:                "active",
		ConnectedAt:           time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC),
		LeaseExpireAt:         time.Date(2026, 4, 16, 10, 0, 30, 0, time.UTC),
		RecoverUntil:          time.Date(2026, 4, 16, 10, 2, 0, 0, time.UTC),
		ConsumerToken:         "consumer-1",
		LastDeliveredEventID:  101,
		LastDeliveredSequence: 7,
		LastDeliveredAt:       &deliveredAt,
	}

	raw, err := sonic.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal stream queue meta: %v", err)
	}

	var decoded StreamQueueMeta
	if err := sonic.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal stream queue meta: %v", err)
	}
	if decoded.QueueID != meta.QueueID || decoded.SessionID != meta.SessionID || decoded.ConsumerToken != meta.ConsumerToken {
		t.Fatalf("decoded meta mismatch: %+v", decoded)
	}
	if decoded.LastDeliveredSequence != meta.LastDeliveredSequence {
		t.Fatalf("decoded delivered sequence = %d, want %d", decoded.LastDeliveredSequence, meta.LastDeliveredSequence)
	}
	if decoded.LastDeliveredAt == nil || !decoded.LastDeliveredAt.Equal(*meta.LastDeliveredAt) {
		t.Fatalf("decoded delivered_at = %v, want %v", decoded.LastDeliveredAt, meta.LastDeliveredAt)
	}
}
