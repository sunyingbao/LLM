//go:build !windows

package worker

import (
	"context"
	"strconv"

	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	"eino-cli/deepagent/cloud/worker/policy"
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

func threadInfoFromCoordinator(thread *ac.Thread) *ThreadInfo {
	if thread == nil {
		return nil
	}
	metadata := make(map[string]string, len(thread.GetMetadata()))
	for key, value := range thread.GetMetadata() {
		metadata[key] = value
	}
	info := &ThreadInfo{
		ThreadID:          thread.GetThreadId(),
		Namespace:         thread.GetNamespace(),
		SessionID:         thread.GetSessionId(),
		Title:             thread.GetTitle(),
		Status:            ThreadStatus(thread.GetStatus().String()),
		StatusReason:      thread.GetStatusReason(),
		LeaseDeadlineAtMS: thread.GetLeaseDeadlineAtMs(),
		LeaseOwnerHint:    thread.GetLeaseOwnerHint(),
		CreatedBy:         thread.GetCreatedBy(),
		Metadata:          metadata,
		CreatedAtMS:       thread.GetCreatedAtMs(),
		UpdatedAtMS:       thread.GetUpdatedAtMs(),
		ClosedAtMS:        thread.GetClosedAtMs(),
		Env:               thread.GetEnv(),
		UserID:            thread.GetUserId(),
		LastActiveAtMS:    thread.GetLastActiveAtMs(),
	}
	if profile := thread.GetProfile(); profile != nil {
		info.Role = profile.GetRole()
		info.CWD = profile.GetCwd()
	}
	return info
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
