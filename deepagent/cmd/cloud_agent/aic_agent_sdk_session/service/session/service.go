package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"code.byted.org/gopkg/logs"
	coordinator "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	sessiondal "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/dal/session"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/infra/idgen"
	aic_agent_sdk_common "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/kitex_gen/aic_agent_sdk_common"
)

const (
	maxProjectNameRunes = 256
	maxProjectPathRunes = 1024
	maxTitleRunes       = 256
	maxPreviewRunes     = 512
	maxEmailRunes       = 320
	maxCloseProjectRows = 100
)

type CoordinatorClient interface {
	ListSessionThreads(ctx context.Context, sessionID int64) ([]*coordinator.Thread, error)
	CloseSessionThreads(ctx context.Context, sessionID int64, reason string) ([]*coordinator.Thread, error)
}

type Service struct {
	store *sessiondal.Store
	ids   idgen.Generator
	ac    CoordinatorClient
}

type CloseProjectResult struct {
	Project            *aic_agent_sdk_common.SessionProject
	ClosedSessionIDs   []int64
	ClosedSessionCount int64
	ClosedThreadCount  int64
}

func New(store *sessiondal.Store, ids idgen.Generator, ac CoordinatorClient) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("session store is required")
	}
	if ids == nil {
		return nil, fmt.Errorf("id generator is required")
	}
	return &Service{store: store, ids: ids, ac: ac}, nil
}

func (s *Service) CreateSession(ctx context.Context, uid int64, title, projectName, projectPath *string, email string) (*aic_agent_sdk_common.AgentSessionView, error) {
	if uid <= 0 {
		return nil, fmt.Errorf("uid is required")
	}
	cleanProjectName := cleanString(projectName, maxProjectNameRunes)
	if cleanProjectName == "" {
		return nil, fmt.Errorf("project_name is required")
	}
	cleanProjectPath := cleanString(projectPath, maxProjectPathRunes)
	if cleanProjectPath == "" {
		return nil, fmt.Errorf("project_path is required")
	}
	sessionID, err := s.ids.SessionID(ctx)
	if err != nil {
		return nil, fmt.Errorf("generate session_id: %w", err)
	}
	if sessionID <= 0 {
		return nil, fmt.Errorf("generated session_id is invalid")
	}
	now := nowMS()
	sess := &sessiondal.Session{
		SessionID:      sessionID,
		UID:            uid,
		ProjectName:    cleanProjectName,
		ProjectPath:    cleanProjectPath,
		Title:          cleanString(title, maxTitleRunes),
		Status:         sessiondal.StatusActive,
		LastActiveAtMS: now,
		CreatedAtMS:    now,
		UpdatedAtMS:    now,
		MetadataJSON:   sessionMetadataJSON(cleanValue(email, maxEmailRunes)),
	}
	if err := s.store.Create(ctx, sess); err != nil {
		return nil, err
	}
	return sessionView(sess, nil), nil
}

func sessionMetadataJSON(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return "{}"
	}
	payload, err := json.Marshal(map[string]string{"email": email})
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func (s *Service) ListSessions(ctx context.Context, uid int64, status *aic_agent_sdk_common.AgentSessionStatus, projectName *string, cursorRaw string, limit int32) ([]*aic_agent_sdk_common.AgentSession, *aic_agent_sdk_common.PageInfo, error) {
	if uid <= 0 {
		return nil, nil, fmt.Errorf("uid is required")
	}
	var statusValue *int64
	if status != nil {
		value, err := sessionStatusToDAL(*status)
		if err != nil {
			return nil, nil, err
		}
		statusValue = &value
	}
	cursor, err := sessiondal.DecodeCursor(cursorRaw)
	if err != nil {
		return nil, nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	rows, next, err := s.store.List(ctx, sessiondal.ListFilter{
		UID:         uid,
		ProjectName: cleanString(projectName, maxProjectNameRunes),
		Status:      statusValue,
		Cursor:      cursor,
		Limit:       int(limit),
	})
	if err != nil {
		return nil, nil, err
	}
	out := make([]*aic_agent_sdk_common.AgentSession, 0, len(rows))
	for _, row := range rows {
		out = append(out, sessionToView(row))
	}
	nextCursor := sessiondal.EncodeCursor(next)
	pageInfo := &aic_agent_sdk_common.PageInfo{
		HasMore: next != nil,
	}
	if nextCursor != "" {
		pageInfo.NextCursor = &nextCursor
	}
	return out, pageInfo, nil
}

func (s *Service) ListProjects(ctx context.Context, uid int64, status *aic_agent_sdk_common.AgentSessionStatus) ([]*aic_agent_sdk_common.SessionProject, error) {
	if uid <= 0 {
		return nil, fmt.Errorf("uid is required")
	}
	var statusValue *int64
	if status != nil {
		value, err := sessionStatusToDAL(*status)
		if err != nil {
			return nil, err
		}
		statusValue = &value
	}
	rows, err := s.store.ListProjects(ctx, uid, statusValue)
	if err != nil {
		return nil, err
	}
	out := make([]*aic_agent_sdk_common.SessionProject, 0, len(rows))
	for _, row := range rows {
		out = append(out, &aic_agent_sdk_common.SessionProject{
			ProjectName:    row.ProjectName,
			ProjectPath:    row.ProjectPath,
			SessionCount:   int64Ptr(row.SessionCount),
			LastActiveAtMs: int64Ptr(row.LastActiveAtMS),
		})
	}
	return out, nil
}

func (s *Service) GetSession(ctx context.Context, uid, sessionID int64, includeThreads bool) (*aic_agent_sdk_common.AgentSessionView, error) {
	if err := validateOwnerKeys(uid, sessionID); err != nil {
		return nil, err
	}
	sess, err := s.store.Get(ctx, uid, sessionID)
	if err != nil {
		return nil, err
	}
	var threads []*aic_agent_sdk_common.AgentThread
	if includeThreads {
		threads, err = s.listThreads(ctx, sess)
		if err != nil {
			return nil, err
		}
	}
	return sessionView(sess, threads), nil
}

func (s *Service) UpdateSession(ctx context.Context, uid, sessionID int64, title *string, status *aic_agent_sdk_common.AgentSessionStatus) (*aic_agent_sdk_common.AgentSessionView, error) {
	if err := validateOwnerKeys(uid, sessionID); err != nil {
		return nil, err
	}
	patch := sessiondal.UpdatePatch{UpdatedAt: nowMS()}
	if title != nil {
		cleaned := cleanString(title, maxTitleRunes)
		patch.Title = &cleaned
	}
	if status != nil {
		value, err := sessionStatusToDAL(*status)
		if err != nil {
			return nil, err
		}
		if value == sessiondal.StatusClosed {
			return nil, fmt.Errorf("status CLOSED must be set by CloseSession")
		}
		patch.Status = &value
	}
	sess, err := s.store.Update(ctx, uid, sessionID, patch)
	if err != nil {
		return nil, err
	}
	return sessionView(sess, nil), nil
}

func (s *Service) CloseSession(ctx context.Context, uid, sessionID int64, reason string) (*aic_agent_sdk_common.AgentSessionView, error) {
	if err := validateOwnerKeys(uid, sessionID); err != nil {
		return nil, err
	}
	if _, err := s.store.Get(ctx, uid, sessionID); err != nil {
		return nil, err
	}
	var threads []*aic_agent_sdk_common.AgentThread
	if s.ac != nil {
		acThreads, err := s.ac.CloseSessionThreads(ctx, sessionID, reason)
		if err != nil {
			return nil, err
		}
		threads = mapThreads(sessionID, uid, 0, acThreads)
	}
	sess, err := s.store.Close(ctx, uid, sessionID, nowMS())
	if err != nil {
		return nil, err
	}
	return sessionView(sess, threads), nil
}

func (s *Service) CloseProject(ctx context.Context, uid int64, projectName, reason string) (*CloseProjectResult, error) {
	if uid <= 0 {
		return nil, fmt.Errorf("uid is required")
	}
	cleanProjectName := cleanValue(projectName, maxProjectNameRunes)
	if cleanProjectName == "" {
		return nil, fmt.Errorf("project_name is required")
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_session] close project start: uid=%d project_name=%q reason=%q", uid, cleanProjectName, strings.TrimSpace(reason))
	sessions, err := s.store.ListProjectSessions(ctx, uid, cleanProjectName, sessiondal.StatusActive, maxCloseProjectRows+1)
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_session] close project list active sessions failed: uid=%d project_name=%q err=%v", uid, cleanProjectName, err)
		return nil, err
	}
	if len(sessions) > maxCloseProjectRows {
		err := fmt.Errorf("project has more than %d active sessions; close sessions individually", maxCloseProjectRows)
		logs.CtxWarn(ctx, "[aic_agent_sdk_session] close project refused too many active sessions: uid=%d project_name=%q active_session_count_over_limit=%d", uid, cleanProjectName, len(sessions))
		return nil, err
	}
	result := &CloseProjectResult{
		Project:          projectSummary(cleanProjectName, sessions),
		ClosedSessionIDs: make([]int64, 0, len(sessions)),
	}
	if len(sessions) == 0 {
		logs.CtxInfo(ctx, "[aic_agent_sdk_session] close project no active sessions: uid=%d project_name=%q", uid, cleanProjectName)
		return result, nil
	}

	for _, sess := range sessions {
		result.ClosedSessionIDs = append(result.ClosedSessionIDs, sess.SessionID)
		if s.ac == nil {
			continue
		}
		logs.CtxInfo(ctx, "[aic_agent_sdk_session] close project close session threads start: uid=%d project_name=%q dialog_stream_id=%d", uid, cleanProjectName, sess.SessionID)
		acThreads, err := s.ac.CloseSessionThreads(ctx, sess.SessionID, reason)
		if err != nil {
			logs.CtxError(ctx, "[aic_agent_sdk_session] close project close session threads failed: uid=%d project_name=%q dialog_stream_id=%d err=%v", uid, cleanProjectName, sess.SessionID, err)
			return nil, err
		}
		closedThreads := countClosableThreads(acThreads)
		result.ClosedThreadCount += closedThreads
		logs.CtxInfo(ctx, "[aic_agent_sdk_session] close project close session threads done: uid=%d project_name=%q dialog_stream_id=%d thread_count=%d closed_thread_count=%d",
			uid, cleanProjectName, sess.SessionID, len(acThreads), closedThreads)
	}
	now := nowMS()
	if err := s.store.CloseProjectSessions(ctx, uid, cleanProjectName, result.ClosedSessionIDs, now); err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_session] close project update sessions failed: uid=%d project_name=%q closed_session_count=%d err=%v",
			uid, cleanProjectName, len(result.ClosedSessionIDs), err)
		return nil, err
	}
	result.ClosedSessionCount = int64(len(result.ClosedSessionIDs))
	if result.Project != nil {
		result.Project.SessionCount = int64Ptr(result.ClosedSessionCount)
		result.Project.LastActiveAtMs = int64Ptr(now)
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_session] close project success: uid=%d project_name=%q closed_session_count=%d closed_thread_count=%d",
		uid, cleanProjectName, result.ClosedSessionCount, result.ClosedThreadCount)
	return result, nil
}

func (s *Service) BindMainThread(ctx context.Context, uid, sessionID, mainThreadID int64) (*aic_agent_sdk_common.AgentSessionView, error) {
	if err := validateOwnerKeys(uid, sessionID); err != nil {
		return nil, err
	}
	if mainThreadID <= 0 {
		return nil, fmt.Errorf("main_thread_id is required")
	}
	sess, err := s.store.BindMainThread(ctx, uid, sessionID, mainThreadID, nowMS())
	if err != nil {
		return nil, err
	}
	return sessionView(sess, nil), nil
}

func (s *Service) TouchSession(ctx context.Context, uid, sessionID int64, preview, titleIfEmpty *string, lastActiveAtMS *int64) (*aic_agent_sdk_common.AgentSessionView, error) {
	if err := validateOwnerKeys(uid, sessionID); err != nil {
		return nil, err
	}
	now := nowMS()
	activeAt := now
	if lastActiveAtMS != nil && *lastActiveAtMS > 0 {
		activeAt = *lastActiveAtMS
	}
	patch := sessiondal.TouchPatch{
		LastActiveAtMS: activeAt,
		UpdatedAtMS:    now,
	}
	if preview != nil {
		cleaned := cleanString(preview, maxPreviewRunes)
		patch.LastMessagePreview = &cleaned
	}
	if titleIfEmpty != nil {
		cleaned := cleanString(titleIfEmpty, maxTitleRunes)
		if cleaned != "" {
			patch.TitleIfEmpty = &cleaned
		}
	}
	sess, err := s.store.Touch(ctx, uid, sessionID, patch)
	if err != nil {
		return nil, err
	}
	return sessionView(sess, nil), nil
}

func (s *Service) listThreads(ctx context.Context, sess *sessiondal.Session) ([]*aic_agent_sdk_common.AgentThread, error) {
	if s.ac == nil {
		return nil, nil
	}
	threads, err := s.ac.ListSessionThreads(ctx, sess.SessionID)
	if err != nil {
		return nil, err
	}
	return mapThreads(sess.SessionID, sess.UID, sess.MainThreadID, threads), nil
}

func projectSummary(projectName string, sessions []*sessiondal.Session) *aic_agent_sdk_common.SessionProject {
	project := &aic_agent_sdk_common.SessionProject{
		ProjectName:    projectName,
		SessionCount:   int64Ptr(int64(len(sessions))),
		LastActiveAtMs: int64Ptr(0),
	}
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		if project.ProjectPath == "" {
			project.ProjectPath = sess.ProjectPath
		}
		if project.LastActiveAtMs == nil || sess.LastActiveAtMS > *project.LastActiveAtMs {
			project.LastActiveAtMs = int64Ptr(sess.LastActiveAtMS)
		}
	}
	return project
}

func countClosableThreads(threads []*coordinator.Thread) int64 {
	var count int64
	for _, thread := range threads {
		if thread == nil || thread.GetStatus() == coordinator.ThreadStatus_CLOSED {
			continue
		}
		count++
	}
	return count
}

func sessionView(sess *sessiondal.Session, threads []*aic_agent_sdk_common.AgentThread) *aic_agent_sdk_common.AgentSessionView {
	view := &aic_agent_sdk_common.AgentSessionView{
		Session: sessionToView(sess),
	}
	if threads != nil {
		view.Threads = threads
	}
	return view
}

func sessionToView(sess *sessiondal.Session) *aic_agent_sdk_common.AgentSession {
	if sess == nil {
		return nil
	}
	return &aic_agent_sdk_common.AgentSession{
		SessionId:          sess.SessionID,
		Uid:                sess.UID,
		Title:              stringPtr(sess.Title),
		MainThreadId:       int64Ptr(sess.MainThreadID),
		ProjectName:        stringPtr(sess.ProjectName),
		LastMessagePreview: stringPtr(sess.LastMessagePreview),
		LastActiveAtMs:     int64Ptr(sess.LastActiveAtMS),
		Status:             dalStatusToSessionStatus(sess.Status),
		ProjectPath:        stringPtr(sess.ProjectPath),
		CreatedAtMs:        int64Ptr(sess.CreatedAtMS),
		UpdatedAtMs:        int64Ptr(sess.UpdatedAtMS),
	}
}

func mapThreads(sessionID, uid, mainThreadID int64, threads []*coordinator.Thread) []*aic_agent_sdk_common.AgentThread {
	out := make([]*aic_agent_sdk_common.AgentThread, 0, len(threads))
	for _, thread := range threads {
		if thread == nil {
			continue
		}
		threadSessionID := sessionID
		if parsed, err := strconv.ParseInt(thread.GetSessionId(), 10, 64); err == nil && parsed > 0 {
			threadSessionID = parsed
		}
		role := aic_agent_sdk_common.AgentThreadRole_CHILD
		if thread.GetThreadId() == mainThreadID || strings.EqualFold(thread.GetProfile().GetRole(), "main") {
			role = aic_agent_sdk_common.AgentThreadRole_MAIN
		}
		mapped := &aic_agent_sdk_common.AgentThread{
			ThreadId:       thread.GetThreadId(),
			AcNamespace:    thread.GetNamespace(),
			SessionId:      threadSessionID,
			Uid:            thread.GetUserId(),
			Title:          stringPtr(thread.GetTitle()),
			Status:         mapThreadStatus(thread.GetStatus()),
			StatusReason:   stringPtr(thread.GetStatusReason()),
			Role:           &role,
			CreatedAtMs:    int64Ptr(thread.GetCreatedAtMs()),
			UpdatedAtMs:    int64Ptr(thread.GetUpdatedAtMs()),
			ParentThreadId: stringMapPtr(thread.GetMetadata(), "parent_thread_id"),
			RootThreadId:   stringMapPtr(thread.GetMetadata(), "root_thread_id"),
		}
		if mapped.Uid == 0 {
			mapped.Uid = uid
		}
		out = append(out, mapped)
	}
	return out
}

func sessionStatusToDAL(status aic_agent_sdk_common.AgentSessionStatus) (int64, error) {
	switch status {
	case aic_agent_sdk_common.AgentSessionStatus_ACTIVE:
		return sessiondal.StatusActive, nil
	case aic_agent_sdk_common.AgentSessionStatus_ARCHIVED:
		return sessiondal.StatusArchived, nil
	case aic_agent_sdk_common.AgentSessionStatus_CLOSED:
		return sessiondal.StatusClosed, nil
	default:
		return 0, fmt.Errorf("invalid session status %d", status)
	}
}

func dalStatusToSessionStatus(status int64) aic_agent_sdk_common.AgentSessionStatus {
	switch status {
	case sessiondal.StatusArchived:
		return aic_agent_sdk_common.AgentSessionStatus_ARCHIVED
	case sessiondal.StatusClosed:
		return aic_agent_sdk_common.AgentSessionStatus_CLOSED
	default:
		return aic_agent_sdk_common.AgentSessionStatus_ACTIVE
	}
}

func mapThreadStatus(status coordinator.ThreadStatus) aic_agent_sdk_common.AgentThreadStatus {
	switch status {
	case coordinator.ThreadStatus_READY:
		return aic_agent_sdk_common.AgentThreadStatus_READY
	case coordinator.ThreadStatus_RUNNING:
		return aic_agent_sdk_common.AgentThreadStatus_RUNNING
	case coordinator.ThreadStatus_BLOCKED:
		return aic_agent_sdk_common.AgentThreadStatus_BLOCKED
	case coordinator.ThreadStatus_CLOSING:
		return aic_agent_sdk_common.AgentThreadStatus_CLOSING
	case coordinator.ThreadStatus_CLOSED:
		return aic_agent_sdk_common.AgentThreadStatus_CLOSED
	default:
		return aic_agent_sdk_common.AgentThreadStatus_IDLE
	}
}

func validateOwnerKeys(uid, sessionID int64) error {
	if uid <= 0 {
		return fmt.Errorf("uid is required")
	}
	if sessionID <= 0 {
		return fmt.Errorf("session_id is required")
	}
	return nil
}

func cleanString(value *string, limit int) string {
	if value == nil {
		return ""
	}
	return cleanValue(*value, limit)
}

func cleanValue(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if limit <= 0 || utf8.RuneCountInString(trimmed) <= limit {
		return trimmed
	}
	runes := []rune(trimmed)
	return string(runes[:limit])
}

func nowMS() int64 {
	return time.Now().UnixMilli()
}

func stringPtr(value string) *string {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}

func stringMapPtr(values map[string]string, key string) *string {
	if value := strings.TrimSpace(values[key]); value != "" {
		return &value
	}
	return nil
}
