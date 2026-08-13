package tasktool

import (
	"context"
	"time"
)

// TaskHost is the minimal backend interface required by the generic task tool.
//
// It is intentionally backend-agnostic so it can be implemented by in-process
// and remote worker hosts alike.
type TaskHost interface {
	CreateThread(ctx context.Context, req CreateThreadRequest) (*Thread, error)
	SendMessage(ctx context.Context, req SendMessageRequest) (*Message, error)
	CloseThread(ctx context.Context, req CloseThreadRequest) (*ClosedThreadRsp, error)
	ListEvents(ctx context.Context, req ListEventsRequest) ([]*Event, error)
}

// ThreadProfile carries optional host-defined execution profile for a thread.
//
// The SDK treats these fields as opaque collaboration profile data. Role
// semantics and cwd policy remain host-defined.
type ThreadProfile struct {
	// Role is an optional host-defined role for the created thread.
	Role string `json:"role,omitempty"`
	// Cwd is the working directory associated with the created thread.
	Cwd string `json:"cwd,omitempty"`
}

func (p ThreadProfile) Empty() bool {
	return p.Role == "" && p.Cwd == ""
}

// CreateThreadRequest describes one backend-agnostic thread creation request.
type CreateThreadRequest struct {
	// UserID owns the created thread. Hosts may inherit it from ParentThreadID
	// when omitted for child thread creation.
	UserID int64
	// SessionID groups related task threads under one host-defined session.
	SessionID string
	// ParentThreadID optionally links the new thread to its parent thread.
	ParentThreadID string
	// Title is a short human-readable label for the thread.
	Title string
	// Profile carries optional host-defined execution profile for the thread.
	Profile ThreadProfile
	// Metadata carries host- or business-specific thread attributes.
	Metadata map[string]string
	// InitialMessage optionally seeds the new thread with its first input.
	InitialMessage *Message
}

// Thread is the minimal thread descriptor returned by TaskHost.
type Thread struct {
	// ID is the backend thread identifier.
	ID string
	// UserID is the durable owner of this thread.
	UserID int64
	// SessionID is the backend-defined session identifier for this thread.
	SessionID string
	// Title is the current short human-readable label of the thread.
	Title string
	// Profile carries optional host-defined execution profile for this thread.
	Profile ThreadProfile
	// Metadata carries backend- or business-specific thread attributes.
	Metadata map[string]string
	// InitialMessageID is the backend identifier for the initial message when
	// CreateThread was called with InitialMessage.
	InitialMessageID string
}

// Message is the backend-agnostic message envelope used by TaskHost.
type Message struct {
	// ID is the caller-supplied or generated message identifier.
	ID string
	// MessageType is the host message type. Empty means the host default.
	MessageType string
	// Payload is the serialized message body.
	Payload []byte
	// Metadata carries optional message attributes.
	Metadata map[string]string
}

// SendMessageRequest describes one backend-agnostic message delivery request.
type SendMessageRequest struct {
	// ThreadID is the target thread identifier.
	ThreadID string
	// FromThreadID identifies the source thread when the host wants to encode a
	// backend-specific sender envelope.
	FromThreadID string
	// Message is the message content to deliver.
	Message *Message
}

// CloseThreadRequest describes one backend-agnostic thread close request.
type CloseThreadRequest struct {
	// ThreadID is the target thread identifier.
	ThreadID string
	// Reason optionally records why the thread is being closed.
	Reason string
}

// ClosedThreadRsp acknowledges that the close request was accepted.
type ClosedThreadRsp struct{}

// Event is the backend-agnostic event envelope returned by TaskHost.
type Event struct {
	// ID is the backend event identifier.
	ID string
	// ThreadID identifies the thread that emitted the event.
	ThreadID string
	// TurnID identifies the logical turn or run associated with the event.
	TurnID string
	// Type is the backend event type discriminator.
	Type string
	// Payload is the serialized event body.
	Payload []byte
	// Metadata carries optional event attributes.
	Metadata map[string]string
	// TS is the event timestamp.
	TS time.Time
}

// ListEventsRequest describes one backend-agnostic event listing request.
type ListEventsRequest struct {
	// ThreadID selects the thread whose events should be returned.
	ThreadID string
	// Limit bounds the number of returned events. Zero means backend default.
	Limit int
	// Reverse requests newest-first ordering when the backend supports it.
	Reverse bool
}
