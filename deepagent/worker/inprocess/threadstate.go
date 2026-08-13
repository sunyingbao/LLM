package inprocess

import (
	"time"

	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/tasktool"
)

// ThreadState is the persisted thread aggregate for the local worker host.
//
// It stores durable identity, hierarchy, and resume-critical state while
// excluding live runtime facts such as whether a goroutine is currently
// running.
type ThreadState struct {
	ID             string
	UserID         int64
	SessionID      string
	ParentThreadID string
	RootThreadID   string

	Title    string
	Profile  ThreadProfile
	Metadata map[string]string

	PendingBlock *agentworker.PendingBlock
	ClosedAt     *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateThreadSpec describes the worker-level request to create one thread.
//
// ID may be left empty when the backing store or caller is responsible for
// allocation.
//
// ParentThreadID controls whether the new thread is a root thread or a child
// thread. RootThreadID should be derived by the worker from parent lineage
// rather than supplied by the caller in normal flows.
type CreateThreadSpec struct {
	ID             string
	UserID         int64
	SessionID      string
	ParentThreadID string

	Title    string
	Profile  ThreadProfile
	Metadata map[string]string
}

// ThreadProfile carries optional host-defined execution profile for a thread.
type ThreadProfile = tasktool.ThreadProfile

// ResumeFromBlockRequest is the worker-level request to continue one blocked
// thread after external input is available.
//
// The local worker only validates and updates persisted thread state. How the
// business encodes the actual follow-up input is left to a later PostMessage.
type ResumeFromBlockRequest struct {
	ThreadID    string
	InterruptID string
}

// UpdateThreadStatePatch describes the persisted mutable thread fields.
//
// PendingBlock may be set while moving into blocked state. ClearPendingBlock
// clears the persisted block context explicitly.
type UpdateThreadStatePatch struct {
	Title             *string
	Cwd               *string
	Metadata          map[string]string
	PendingBlock      *agentworker.PendingBlock
	ClearPendingBlock bool
	ClosedAt          *time.Time
}

// ListThreadsOrderBy selects the durable timestamp used for thread catalog
// ordering.
type ListThreadsOrderBy string

const (
	ListThreadsOrderByUpdatedAt ListThreadsOrderBy = "updated_at"
	ListThreadsOrderByCreatedAt ListThreadsOrderBy = "created_at"
)

// ListThreadsOptions configures one thread catalog listing request.
type ListThreadsOptions struct {
	UserID        int64
	SessionID     string
	Cwd           string
	RootOnly      bool
	IncludeClosed bool
	OrderBy       ListThreadsOrderBy
	Desc          bool
	Limit         int
	Cursor        string
}
