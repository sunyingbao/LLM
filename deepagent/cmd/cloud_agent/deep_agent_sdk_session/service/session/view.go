package session

import sessiondal "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/dal/session"

type View struct {
	Session *sessiondal.Session
	Threads []*Thread
}

type Thread struct {
	ThreadID       int64
	Namespace      string
	SessionID      int64
	UID            int64
	Title          string
	Status         string
	StatusReason   string
	IsMain         bool
	CreatedAtMS    int64
	UpdatedAtMS    int64
	ParentThreadID *string
	RootThreadID   *string
}

type PageInfo struct {
	NextCursor string
	HasMore    bool
}
