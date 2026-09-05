//go:build !windows

package worker

import (
	"context"
	"strconv"
	"strings"
	"time"

	"eino-cli/deepagent/cloud/worker/policy"
	"eino-cli/deepagent/coordinator"
)

// ThreadStatus is the SDK-owned lifecycle status of a Coordinator thread.
type ThreadStatus string

const (
	ThreadStatusIdle    ThreadStatus = "IDLE"
	ThreadStatusReady   ThreadStatus = "READY"
	ThreadStatusRunning ThreadStatus = "RUNNING"
	ThreadStatusBlocked ThreadStatus = "BLOCKED"
	ThreadStatusClosing ThreadStatus = "CLOSING"
	ThreadStatusClosed  ThreadStatus = "CLOSED"
)

// ThreadInfo is the stable, read-only SDK view of a Coordinator thread. It is
// copied before being passed to business callbacks, including its Metadata map.
type ThreadInfo struct {
	ThreadID          int64
	Namespace         string
	SessionID         string
	Title             string
	Status            ThreadStatus
	StatusReason      string
	LeaseDeadlineAtMS int64
	LeaseOwnerHint    string
	CreatedBy         string
	Metadata          map[string]string
	CreatedAtMS       int64
	UpdatedAtMS       int64
	ClosedAtMS        int64
	Env               string
	UserID            int64
	LastActiveAtMS    int64
	Role              string
	CWD               string
}

func threadInfoFromCoordinator(thread *coordinator.Thread) (info *ThreadInfo) {
	if thread == nil {
		return nil
	}
	metadata := make(map[string]string, len(thread.Metadata))
	for key, value := range thread.Metadata {
		metadata[key] = value
	}
	info = &ThreadInfo{
		ThreadID:          thread.ThreadID,
		Namespace:         thread.Namespace,
		SessionID:         thread.SessionID,
		Title:             thread.Title,
		Status:            ThreadStatus(strings.ToUpper(string(thread.Status))),
		StatusReason:      thread.StatusReason,
		LeaseDeadlineAtMS: timeToMilliseconds(thread.LeaseDeadlineAt),
		LeaseOwnerHint:    thread.LeaseOwnerHint,
		CreatedBy:         thread.CreatedBy,
		Metadata:          metadata,
		CreatedAtMS:       timeToMilliseconds(thread.CreatedAt),
		UpdatedAtMS:       timeToMilliseconds(thread.UpdatedAt),
		ClosedAtMS:        timeToMilliseconds(thread.ClosedAt),
		Env:               thread.Env,
		UserID:            thread.UserID,
		LastActiveAtMS:    timeToMilliseconds(thread.LastActiveAt),
	}
	if thread.Profile != nil {
		info.Role = thread.Profile.Role
		info.CWD = thread.Profile.Cwd
	}
	return info
}

func timeToMilliseconds(value time.Time) (milliseconds int64) {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}

type threadApprovalStore struct {
	store *policy.SessionApprovalStore
}

func (s threadApprovalStore) Allow(_ context.Context, threadInfo *ThreadInfo, toolName string, argumentsJSON string) {
	if s.store == nil {
		return
	}
	s.store.Allow(threadApprovalScope(threadInfo), toolName, argumentsJSON)
}

func (s threadApprovalStore) IsAllowed(_ context.Context, threadInfo *ThreadInfo, toolName string, argumentsJSON string) bool {
	if s.store == nil {
		return false
	}
	return s.store.IsAllowed(threadApprovalScope(threadInfo), toolName, argumentsJSON)
}

func threadApprovalScope(threadInfo *ThreadInfo) string {
	if threadInfo == nil {
		return ""
	}
	if threadInfo.SessionID != "" {
		return "session:" + threadInfo.SessionID
	}
	if threadInfo.ThreadID != 0 {
		return "thread:" + strconv.FormatInt(threadInfo.ThreadID, 10)
	}
	return ""
}
