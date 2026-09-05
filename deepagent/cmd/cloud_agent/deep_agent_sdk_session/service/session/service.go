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
	sessiondal "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/dal/session"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/infra/idgen"
	"eino-cli/deepagent/coordinator"
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
	Project            *sessiondal.SessionProject
	ClosedSessionIDs   []int64
	ClosedSessionCount int64
	ClosedThreadCount  int64
}

func New(store *sessiondal.Store, ids idgen.Generator, ac CoordinatorClient) (service *Service, err error) {
	if store == nil {
		return nil, fmt.Errorf("session store is required")
	}
	if ids == nil {
		return nil, fmt.Errorf("id generator is required")
	}
	return &Service{store: store, ids: ids, ac: ac}, nil
}

func (s *Service) CreateSession(ctx context.Context, uid int64, title, projectName, projectPath *string, email string) (view *View, err error) {
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
	return &View{Session: sess}, nil
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

func (s *Service) ListSessions(ctx context.Context, uid int64, status *int64, projectName *string, cursorRaw string, limit int32) (sessions []*sessiondal.Session, page *PageInfo, err error) {
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
	nextCursor := sessiondal.EncodeCursor(next)
	pageInfo := &PageInfo{NextCursor: nextCursor, HasMore: next != nil}
	return rows, pageInfo, nil
}

func (s *Service) ListProjects(ctx context.Context, uid int64, status *int64) (projects []*sessiondal.SessionProject, err error) {
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
	return rows, nil
}

func (s *Service) GetSession(ctx context.Context, uid, sessionID int64, includeThreads bool) (view *View, err error) {
	if err := validateOwnerKeys(uid, sessionID); err != nil {
		return nil, err
	}
	sess, err := s.store.Get(ctx, uid, sessionID)
	if err != nil {
		return nil, err
	}
	var threads []*Thread
	if includeThreads {
		threads, err = s.listThreads(ctx, sess)
		if err != nil {
			return nil, err
		}
	}
	return &View{Session: sess, Threads: threads}, nil
}

func (s *Service) UpdateSession(ctx context.Context, uid, sessionID int64, title *string, status *int64) (view *View, err error) {
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
	return &View{Session: sess}, nil
}

func (s *Service) CloseSession(ctx context.Context, uid, sessionID int64, reason string) (view *View, err error) {
	if err := validateOwnerKeys(uid, sessionID); err != nil {
		return nil, err
	}
	if _, err := s.store.Get(ctx, uid, sessionID); err != nil {
		return nil, err
	}
	var threads []*Thread
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
	return &View{Session: sess, Threads: threads}, nil
}

func (s *Service) CloseProject(ctx context.Context, uid int64, projectName, reason string) (closed *CloseProjectResult, err error) {
	if uid <= 0 {
		return nil, fmt.Errorf("uid is required")
	}
	cleanProjectName := cleanValue(projectName, maxProjectNameRunes)
	if cleanProjectName == "" {
		return nil, fmt.Errorf("project_name is required")
	}
	logs.CtxInfo(ctx, "[deep_agent_sdk_session] close project start: uid=%d project_name=%q reason=%q", uid, cleanProjectName, strings.TrimSpace(reason))
	sessions, err := s.store.ListProjectSessions(ctx, uid, cleanProjectName, sessiondal.StatusActive, maxCloseProjectRows+1)
	if err != nil {
		logs.CtxError(ctx, "[deep_agent_sdk_session] close project list active sessions failed: uid=%d project_name=%q err=%v", uid, cleanProjectName, err)
		return nil, err
	}
	if len(sessions) > maxCloseProjectRows {
		err := fmt.Errorf("project has more than %d active sessions; close sessions individually", maxCloseProjectRows)
		logs.CtxWarn(ctx, "[deep_agent_sdk_session] close project refused too many active sessions: uid=%d project_name=%q active_session_count_over_limit=%d", uid, cleanProjectName, len(sessions))
		return nil, err
	}
	result := &CloseProjectResult{
		Project:          projectSummary(cleanProjectName, sessions),
		ClosedSessionIDs: make([]int64, 0, len(sessions)),
	}
	if len(sessions) == 0 {
		logs.CtxInfo(ctx, "[deep_agent_sdk_session] close project no active sessions: uid=%d project_name=%q", uid, cleanProjectName)
		return result, nil
	}

	for _, sess := range sessions {
		result.ClosedSessionIDs = append(result.ClosedSessionIDs, sess.SessionID)
		if s.ac == nil {
			continue
		}
		logs.CtxInfo(ctx, "[deep_agent_sdk_session] close project close session threads start: uid=%d project_name=%q dialog_stream_id=%d", uid, cleanProjectName, sess.SessionID)
		acThreads, err := s.ac.CloseSessionThreads(ctx, sess.SessionID, reason)
		if err != nil {
			logs.CtxError(ctx, "[deep_agent_sdk_session] close project close session threads failed: uid=%d project_name=%q dialog_stream_id=%d err=%v", uid, cleanProjectName, sess.SessionID, err)
			return nil, err
		}
		closedThreads := countClosableThreads(acThreads)
		result.ClosedThreadCount += closedThreads
		logs.CtxInfo(ctx, "[deep_agent_sdk_session] close project close session threads done: uid=%d project_name=%q dialog_stream_id=%d thread_count=%d closed_thread_count=%d",
			uid, cleanProjectName, sess.SessionID, len(acThreads), closedThreads)
	}
	now := nowMS()
	if err := s.store.CloseProjectSessions(ctx, uid, cleanProjectName, result.ClosedSessionIDs, now); err != nil {
		logs.CtxError(ctx, "[deep_agent_sdk_session] close project update sessions failed: uid=%d project_name=%q closed_session_count=%d err=%v",
			uid, cleanProjectName, len(result.ClosedSessionIDs), err)
		return nil, err
	}
	result.ClosedSessionCount = int64(len(result.ClosedSessionIDs))
	if result.Project != nil {
		result.Project.SessionCount = result.ClosedSessionCount
		result.Project.LastActiveAtMS = now
	}
	logs.CtxInfo(ctx, "[deep_agent_sdk_session] close project success: uid=%d project_name=%q closed_session_count=%d closed_thread_count=%d",
		uid, cleanProjectName, result.ClosedSessionCount, result.ClosedThreadCount)
	return result, nil
}

func (s *Service) BindMainThread(ctx context.Context, uid, sessionID, mainThreadID int64) (view *View, err error) {
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
	return &View{Session: sess}, nil
}

func (s *Service) TouchSession(ctx context.Context, uid, sessionID int64, preview, titleIfEmpty *string, lastActiveAtMS *int64) (view *View, err error) {
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
	return &View{Session: sess}, nil
}

func (s *Service) listThreads(ctx context.Context, sess *sessiondal.Session) (views []*Thread, err error) {
	if s.ac == nil {
		return nil, nil
	}
	threads, err := s.ac.ListSessionThreads(ctx, sess.SessionID)
	if err != nil {
		return nil, err
	}
	return mapThreads(sess.SessionID, sess.UID, sess.MainThreadID, threads), nil
}

func projectSummary(projectName string, sessions []*sessiondal.Session) (summary *sessiondal.SessionProject) {
	project := &sessiondal.SessionProject{ProjectName: projectName, SessionCount: int64(len(sessions))}
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		if project.ProjectPath == "" {
			project.ProjectPath = sess.ProjectPath
		}
		if sess.LastActiveAtMS > project.LastActiveAtMS {
			project.LastActiveAtMS = sess.LastActiveAtMS
		}
	}
	return project
}

func countClosableThreads(threads []*coordinator.Thread) int64 {
	var count int64
	for _, thread := range threads {
		if thread == nil || thread.Status == "closed" {
			continue
		}
		count++
	}
	return count
}

func mapThreads(sessionID, uid, mainThreadID int64, threads []*coordinator.Thread) (views []*Thread) {
	out := make([]*Thread, 0, len(threads))
	for _, thread := range threads {
		if thread == nil {
			continue
		}
		threadSessionID := sessionID
		if parsed, err := strconv.ParseInt(thread.SessionID, 10, 64); err == nil && parsed > 0 {
			threadSessionID = parsed
		}
		isMain := false
		if thread.ThreadID == mainThreadID || strings.EqualFold(threadProfileRole(thread), "main") {
			isMain = true
		}
		metadata := threadMetadata(thread)
		mapped := &Thread{
			ThreadID:       thread.ThreadID,
			Namespace:      thread.Namespace,
			SessionID:      threadSessionID,
			UID:            thread.UserID,
			Title:          thread.Title,
			Status:         string(thread.Status),
			StatusReason:   thread.StatusReason,
			IsMain:         isMain,
			CreatedAtMS:    thread.CreatedAt.UnixMilli(),
			UpdatedAtMS:    thread.UpdatedAt.UnixMilli(),
			ParentThreadID: stringMapPtr(metadata, "parent_thread_id"),
			RootThreadID:   stringMapPtr(metadata, "root_thread_id"),
		}
		if mapped.UID == 0 {
			mapped.UID = uid
		}
		out = append(out, mapped)
	}
	return out
}

func sessionStatusToDAL(status int64) (value int64, err error) {
	switch status {
	case sessiondal.StatusActive:
		return sessiondal.StatusActive, nil
	case sessiondal.StatusArchived:
		return sessiondal.StatusArchived, nil
	case sessiondal.StatusClosed:
		return sessiondal.StatusClosed, nil
	default:
		return 0, fmt.Errorf("invalid session status %d", status)
	}
}

func threadMetadata(thread *coordinator.Thread) (metadata map[string]string) {
	metadata = map[string]string{}
	if thread == nil {
		return metadata
	}
	for key, value := range thread.Metadata {
		metadata[key] = value
	}
	return metadata
}

func threadProfileRole(thread *coordinator.Thread) (role string) {
	if thread == nil || thread.Profile == nil {
		return ""
	}
	return thread.Profile.Role
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

func stringMapPtr(values map[string]string, key string) *string {
	if value := strings.TrimSpace(values[key]); value != "" {
		return &value
	}
	return nil
}
