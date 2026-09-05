package coordinator

import (
	"eino-cli/deepagent/coordinator/internal/service/eventlog"
)

type ListDirection string

type ListOrder string

const (
	ListDirectionForward  ListDirection = "forward"
	ListDirectionBackward ListDirection = "backward"
	ListOrderEventID      ListOrder     = "event_id"
	ListOrderCreatedAt    ListOrder     = "created_at_in_thread_seq"
)

type CreateThreadRequest struct {
	Namespace      string
	Env            string
	UserID         int64
	SessionID      string
	Title          string
	Metadata       map[string]string
	Profile        *Profile
	InitialMessage *InitialMessage
}

type ListSessionThreadsRequest struct {
	Namespace string
	SessionID string
	Cursor    int64
	Limit     int32
}

type ListThreadsResult struct {
	Threads    []*Thread
	NextCursor int64
	HasMore    bool
}

type ScanRunnableThreadsRequest struct {
	Namespace string
	Env       string
	Cursor    string
	Limit     int32
}

type ScanRunnableThreadsResult struct {
	Threads    []*Thread
	NextCursor string
	HasMore    bool
}

type ClaimThreadRequest struct {
	Namespace    string
	ThreadID     int64
	LeaseMS      int64
	MessageLimit int32
	LeaseOwner   string
}

type ClaimThreadResult struct {
	Thread          *Thread
	Lease           *Lease
	PendingMessages []*Message
	ServerTimeMS    int64
}

type RenewThreadLeaseRequest struct {
	Namespace  string
	ThreadID   int64
	LeaseToken string
	LeaseMS    int64
	LeaseOwner string
}

type ReleaseThreadRequest struct {
	Namespace  string
	ThreadID   int64
	LeaseToken string
	Reason     string
	Status     ThreadStatus
}

type SubmitInputRequest struct {
	Namespace   string
	ThreadID    int64
	SenderType  SenderType
	SenderID    string
	MessageType string
	Payload     []byte
	Metadata    map[string]string
	WakeThread  bool
}

type SubmitInputResult struct {
	Message *Message
	Thread  *Thread
}

type ReadPendingInputsRequest struct {
	Namespace  string
	ThreadID   int64
	LeaseToken string
	Limit      int32
}

type ReadPendingInputsResult struct {
	Messages     []*Message
	ServerTimeMS int64
}

type ConfirmInputDeliveryRequest struct {
	Namespace    string
	ThreadID     int64
	LeaseToken   string
	MessageIDs   []int64
	TriggerRunID string
}

type ResumeFromBlockRequest struct {
	Namespace          string
	ThreadID           int64
	Reason             string
	ActivationMetadata map[string]string
	ResumeMessage      *InitialMessage
}

type RequestInputCancelRequest struct {
	Namespace       string
	ThreadID        int64
	CutoffMessageID *int64
	Reason          string
	Metadata        map[string]string
}

type RequestThreadCloseRequest struct {
	Namespace string
	ThreadID  int64
	Reason    string
	Metadata  map[string]string
}

type ConfirmThreadClosedRequest struct {
	Namespace        string
	ThreadID         int64
	LeaseToken       string
	ControlMessageID int64
	Reason           string
}

type PublishEventsRequest struct {
	// Workers provide their claim token; other event producers leave it empty.
	LeaseToken string
	Namespace  string
	ThreadID   int64
	RunID      string
	Events     []Event
}

type ListEventsRequest struct {
	Namespace  string
	ThreadID   int64
	Cursor     int64
	PageCursor string
	Limit      int32
	RunID      string
	EventType  string
	Direction  ListDirection
	Order      ListOrder
}

func (r ListEventsRequest) request() (request eventlog.ListEventsRequest) {
	request = eventlog.ListEventsRequest{
		Namespace:  r.Namespace,
		ThreadID:   r.ThreadID,
		Cursor:     r.Cursor,
		PageCursor: r.PageCursor,
		Limit:      r.Limit,
		TurnID:     r.RunID,
		EventType:  r.EventType,
		Direction:  eventlog.ListDirection(r.Direction),
		Order:      eventlog.ListOrder(r.Order),
	}
	return request
}

type ListEventsResult struct {
	Events         []Event
	NextCursor     int64
	NextPageCursor string
	HasMore        bool
}

type ListSessionEventsRequest struct {
	Namespace string
	SessionID string
	Cursor    string
	Limit     int32
	RunID     string
	EventType string
	Direction ListDirection
}

type ListSessionEventsResult struct {
	Events     []Event
	NextCursor string
	HasMore    bool
}

type SubscribeSessionRequest struct {
	Namespace      string
	SessionID      string
	RecoverQueueID string
}
