package api

import (
	"context"
	"encoding/json"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
)

type AgentAPI struct {
	Namespace    string
	Env          string
	Coordinator  Coordinator
	DefaultLimit int32
	MaxLimit     int32
}

const MetadataParentThreadID = "parent_thread_id"

type ThreadProfile struct {
	Role string
	Cwd  string
}

type Thread struct {
	ID               string
	UserID           string
	SessionID        string
	ParentThreadID   string
	Title            string
	Profile          ThreadProfile
	Metadata         map[string]string
	InitialMessageID string
	Status           string
}

type MessageRef struct {
	ThreadID  string
	MessageID string
}

type CreateThreadRequest struct {
	UserID         string
	SessionID      string
	ParentThreadID string
	Title          string
	Profile        ThreadProfile
	Metadata       map[string]string
	InitialInput   *SubmitInput
	InitialMessage *InitialMessage
}

type InitialMessage struct {
	MessageType string
	Payload     []byte
	Metadata    map[string]string
}

type CreateThreadResult struct {
	Thread *Thread
}

type SubmitRequest struct {
	UserID   string
	ThreadID string
	Input    *SubmitInput
	Resume   *ResumeInput
	Compact  *CompactInput
	Metadata map[string]string
}

type SubmitInput struct {
	Parts []protoinput.MessagePart
	Mode  protoinput.UserMessageMode
	Extra map[string]json.RawMessage
}

type ResumeInput struct {
	TurnID             string
	CheckpointID       string
	InterruptID        string
	ToolName           string
	ArgumentsInJSON    string
	ConsumedMessageIDs []string
	Approval           *protoinput.ApprovalDecision
	RequestUserInput   *protoinput.RequestUserInputResponse
	Interrupt          *protoinput.InterruptResumePayload
}

type CompactInput struct{}

type SubmitResult struct {
	Message MessageRef
}

type ListTimelineRequest struct {
	SessionID string
	ThreadID  string
	TurnID    string
	Cursor    string
	Limit     int32
	Backward  bool
}

type ListTimelineResult struct {
	Events     []*timeline.Event
	NextCursor string
	HasMore    bool
}

type SubscribeTimelineRequest struct {
	SessionID      string
	ThreadID       string
	TurnID         string
	RecoverQueueID string
}

type TimelineFrame struct {
	QueueID string
	Event   *timeline.Event
}

type StopRunningRequest struct {
	UserID    string
	SessionID string
	ThreadIDs []string
	Reason    string
}

type StopRunningResult struct {
	ControlMessages []MessageRef
}

type Coordinator interface {
	CreateThread(ctx context.Context, req CreateThreadRequest) (*CreateThreadResult, error)
	SendMessage(ctx context.Context, req SendMessageRequest) (*MessageRef, error)
	ResumeFromBlock(ctx context.Context, req ResumeFromBlockRequest) (*MessageRef, error)
	CancelInput(ctx context.Context, req CancelInputRequest) (*MessageRef, error)
	ListSessionThreads(ctx context.Context, req ListSessionThreadsRequest) (*ListSessionThreadsResult, error)
	ListSessionEvents(ctx context.Context, req ListSessionEventsRequest) (*ListEventsResult, error)
	ListEvents(ctx context.Context, req ListEventsRequest) (*ListEventsResult, error)
	ListTurnEvents(ctx context.Context, req ListTurnEventsRequest) (*ListEventsResult, error)
	SubscribeSession(ctx context.Context, req SubscribeSessionRequest) (TimelineStream, error)
}

type SendMessageRequest struct {
	UserID      string
	ThreadID    string
	MessageType string
	Payload     []byte
	Metadata    map[string]string
	WakeThread  bool
}

type ResumeFromBlockRequest struct {
	UserID      string
	ThreadID    string
	Reason      string
	MessageType string
	Payload     []byte
	Metadata    map[string]string
}

type CancelInputRequest struct {
	ThreadID string
	Reason   string
}

type ListSessionThreadsRequest struct {
	SessionID string
	Limit     int32
	Cursor    string
}

type ListSessionThreadsResult struct {
	Threads    []*Thread
	NextCursor string
	HasMore    bool
}

type ListSessionEventsRequest struct {
	SessionID string
	Cursor    string
	Limit     int32
	Backward  bool
}

type ListEventsRequest struct {
	ThreadID string
	Cursor   string
	Limit    int32
	Backward bool
}

type ListTurnEventsRequest struct {
	ThreadID string
	TurnID   string
	Cursor   string
	Limit    int32
	Backward bool
}

type ListEventsResult struct {
	Events     []*timeline.Event
	NextCursor string
	HasMore    bool
}

type SubscribeSessionRequest struct {
	SessionID      string
	RecoverQueueID string
}

type TimelineStream interface {
	Recv() (*TimelineFrame, error)
}
