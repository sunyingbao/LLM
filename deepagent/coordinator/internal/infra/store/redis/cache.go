package redis

import "time"

type CachedMessage struct {
	MessageID     int64      `json:"message_id"`
	Namespace     string     `json:"namespace"`
	ThreadID      int64      `json:"thread_id"`
	SenderType    string     `json:"sender_type"`
	SenderID      string     `json:"sender_id"`
	MessageType   string     `json:"message_type"`
	Status        string     `json:"status"`
	Payload       string     `json:"payload"`
	MetadataJSON  string     `json:"metadata_json"`
	TriggerTurnID string     `json:"trigger_turn_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	HandledAt     *time.Time `json:"handled_at,omitempty"`
}

type StreamQueueMeta struct {
	QueueID               string     `json:"queue_id"`
	Namespace             string     `json:"namespace"`
	SessionID             string     `json:"session_id"`
	Status                string     `json:"status"`
	ConnectedAt           time.Time  `json:"connected_at"`
	LeaseExpireAt         time.Time  `json:"lease_expire_at"`
	RecoverUntil          time.Time  `json:"recover_until"`
	ConsumerToken         string     `json:"consumer_token"`
	LastDeliveredEventID  int64      `json:"last_delivered_event_id,omitempty"`
	LastDeliveredSequence int64      `json:"last_delivered_sequence,omitempty"`
	LastDeliveredAt       *time.Time `json:"last_delivered_at,omitempty"`
}
