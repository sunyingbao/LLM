package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
	sdkruntime "eino-cli/deepagent/runtime"
)

const (
	httpAPIPath          = "/ad/deep_agent_sdk"
	httpUserTokenHeader  = "X-Bytedance-User"
	httpResponseMaxBytes = 4 << 20
	httpListLimit        = 500
)

type SessionInfo struct {
	ID           string `json:"session_id"`
	Title        string `json:"title"`
	ProjectName  string `json:"project_name"`
	ProjectPath  string `json:"project_path"`
	MainThreadID string `json:"main_thread_id"`
}

type HTTPRuntime struct {
	apiBaseURL string
	project    string
	token      string
	client     *http.Client

	mu               sync.Mutex
	session          SessionInfo
	ownerID          string
	expectedOwnerID  string
	mainThreadStatus int
	sessionValidated bool
	planMode         bool
}

type httpSession struct {
	SessionID    string `json:"session_id"`
	UID          string `json:"uid"`
	Title        string `json:"title"`
	MainThreadID string `json:"main_thread_id"`
	ProjectName  string `json:"project_name"`
	ProjectPath  string `json:"project_path"`
	Status       int    `json:"status"`
}

type httpThread struct {
	ThreadID string `json:"thread_id"`
	Status   int    `json:"status"`
	Role     int    `json:"role"`
}

type httpSessionView struct {
	Session *httpSession `json:"session"`
	Threads []httpThread `json:"threads"`
}

type httpBaseResp struct {
	StatusCode         int32  `json:"StatusCode"`
	StatusMessage      string `json:"StatusMessage"`
	StatusCodeLower    int32  `json:"status_code"`
	StatusMessageLower string `json:"status_message"`
}

type httpResponseBase struct {
	BaseResp      *httpBaseResp `json:"BaseResp"`
	BaseRespLower *httpBaseResp `json:"base_resp"`
}

type httpPageInfo struct {
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

type httpThreadReference struct {
	Runtime     sdkruntime.RuntimeKind `json:"runtime"`
	BaseURL     string                 `json:"base_url"`
	ProjectName string                 `json:"project_name"`
	SessionID   string                 `json:"session_id"`
	OwnerID     string                 `json:"owner_id,omitempty"`
	ThreadID    string                 `json:"thread_id"`
}

func NewHTTPRuntime(baseURL, project, token string) (runtime *HTTPRuntime, err error) {
	apiBaseURL, err := canonicalHTTPAPIBase(baseURL)
	if err != nil {
		return nil, err
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("remote project is required")
	}
	runtime = &HTTPRuntime{
		apiBaseURL: apiBaseURL,
		project:    project,
		token:      strings.TrimSpace(token),
		client: &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) (err error) {
			return http.ErrUseLastResponse
		}},
	}
	return runtime, nil
}

func (runtime *HTTPRuntime) subscribe(ctx context.Context, sessionID string) (subscription *httpSubscription, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	subscription = &httpSubscription{
		runtime: runtime, sessionID: sessionID, ctx: streamCtx, cancel: cancel,
		rawEvents: make(chan timeline.Event, httpEventBufferSize),
		events:    make(chan timeline.Event, httpEventBufferSize),
		config:    make(chan httpSubscriptionConfig, 1),
		ready:     make(chan struct{}),
		done:      make(chan struct{}),
	}
	go subscription.read()
	go subscription.forward()
	return subscription, nil
}

func (runtime *HTTPRuntime) OpenSession(ctx context.Context, sessionID string) (err error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || sessionID == "0" {
		return fmt.Errorf("session_id is required")
	}
	view, err := runtime.getSession(ctx, sessionID)
	if err != nil {
		return err
	}
	err = runtime.storeSessionView(view, sessionID, "")
	return err
}

func (runtime *HTTPRuntime) OpenLatestSession(ctx context.Context) (opened bool, err error) {
	active := 1
	sessions, _, err := runtime.listSessions(ctx, &active)
	if err != nil {
		return false, err
	}
	if len(sessions) == 0 {
		return false, nil
	}
	err = runtime.OpenSession(ctx, sessions[0].ID)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (runtime *HTTPRuntime) ListSessions(ctx context.Context) (sessions []SessionInfo, err error) {
	sessions, _, err = runtime.listSessions(ctx, nil)
	return sessions, err
}

func (runtime *HTTPRuntime) Session() (session SessionInfo) {
	if runtime == nil {
		return session
	}
	runtime.mu.Lock()
	session = runtime.session
	runtime.mu.Unlock()
	return session
}

func (runtime *HTTPRuntime) FollowSession(ctx context.Context) (history []timeline.Event, stream *TurnStream, err error) {
	session := runtime.Session()
	if session.ID == "" {
		return nil, nil, nil
	}
	subscription, err := runtime.subscribe(ctx, session.ID)
	if err != nil {
		return nil, nil, err
	}
	closeSubscription := true
	defer func() {
		if closeSubscription {
			_ = subscription.Close()
		}
	}()
	if err = subscription.waitReady(ctx); err != nil {
		return nil, nil, err
	}
	view, err := runtime.getSession(ctx, session.ID)
	if err != nil {
		return nil, nil, err
	}
	if err = runtime.storeSessionView(view, session.ID, runtime.expectedOwner()); err != nil {
		return nil, nil, err
	}
	session = runtime.Session()
	if session.MainThreadID == "" {
		return nil, nil, nil
	}
	subscription.setThread(session.MainThreadID)
	history, err = runtime.listTimelineAll(ctx, session.ID, session.MainThreadID)
	if err != nil {
		return nil, nil, err
	}
	history = timelineForThread(history, session.MainThreadID)
	status := runtime.threadStatus()
	if !isActiveHTTPThread(status) {
		return history, nil, nil
	}
	activeTurnID := unfinishedTurnID(history)
	if activeTurnID == "" && status != 2 && timelineEndsWithTerminal(history) {
		return history, nil, nil
	}
	completed, replay := splitActiveTurn(history, activeTurnID)
	subscription.configure(session.MainThreadID, activeTurnID, replay, history)
	ref := runtime.threadRef(session.MainThreadID)
	stream = &TurnStream{
		Ref: ref, TurnID: activeTurnID, Events: subscription.Events(), subscription: subscription,
		stop: func(stopCtx context.Context) (stopErr error) {
			stopErr = runtime.stopSession(stopCtx, session.ID)
			return stopErr
		},
	}
	closeSubscription = false
	return completed, stream, nil
}

func (runtime *HTTPRuntime) StartTurn(ctx context.Context, prompt string) (stream *TurnStream, err error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if err = runtime.ensureSession(ctx); err != nil {
		return nil, err
	}
	session := runtime.Session()
	subscription, err := runtime.subscribe(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	closeSubscription := true
	defer func() {
		if closeSubscription {
			_ = subscription.Close()
		}
	}()
	if err = subscription.waitReady(ctx); err != nil {
		return nil, err
	}
	runtime.mu.Lock()
	planMode := runtime.planMode
	runtime.mu.Unlock()
	body := map[string]any{"session_id": session.ID, "content": prompt}
	if session.MainThreadID != "" {
		body["thread_id"] = session.MainThreadID
	}
	if planMode {
		body["mode"] = 1
	}
	var response struct {
		Message struct {
			ThreadID  string `json:"thread_id"`
			MessageID string `json:"message_id"`
		} `json:"message"`
		SessionView *httpSessionView `json:"session_view"`
	}
	if err = runtime.postJSON(ctx, "submit_input", body, &response); err != nil {
		return nil, err
	}
	threadID := strings.TrimSpace(response.Message.ThreadID)
	messageID := strings.TrimSpace(response.Message.MessageID)
	if threadID == "" || threadID == "0" || messageID == "" {
		return nil, fmt.Errorf("submit_input returned an incomplete message reference")
	}
	if response.SessionView != nil {
		if err = runtime.storeSessionView(response.SessionView, session.ID, runtime.expectedOwner()); err != nil {
			return nil, err
		}
	}
	runtime.mu.Lock()
	runtime.session.MainThreadID = threadID
	runtime.mainThreadStatus = 3
	runtime.mu.Unlock()
	subscription.configure(threadID, "", nil, nil)
	ref := runtime.threadRef(threadID)
	stream = &TurnStream{
		Ref: ref, Events: subscription.Events(), subscription: subscription, expectedMessageID: messageID,
		stop: func(stopCtx context.Context) (stopErr error) {
			stopErr = runtime.stopSession(stopCtx, session.ID)
			return stopErr
		},
	}
	closeSubscription = false
	return stream, nil
}

func (runtime *HTTPRuntime) Resume(ctx context.Context, ref sdkruntime.GlobalThreadRef, payload protoinput.ResumeTurnPayload) (err error) {
	if err = payload.Validate(); err != nil {
		return err
	}
	if err = runtime.ensureSession(ctx); err != nil {
		return err
	}
	if err = runtime.checkThreadRef(ref); err != nil {
		return err
	}
	session := runtime.Session()
	body := map[string]any{
		"session_id": session.ID,
		"thread_id":  ref.ThreadID,
		"resume_ref": map[string]string{
			"turn_id": payload.TurnID, "checkpoint_id": payload.CheckpointID, "interrupt_id": payload.InterruptID,
		},
	}
	if payload.ToolName != "" {
		body["tool_name"] = payload.ToolName
	}
	if payload.ArgumentsInJSON != "" {
		body["arguments_in_json"] = payload.ArgumentsInJSON
	}
	if len(payload.ConsumedMessageIDs) > 0 {
		body["consumed_message_ids"] = payload.ConsumedMessageIDs
	}
	if payload.Approval != nil {
		approval := map[string]any{
			"approved": payload.Approval.Approved,
		}
		if payload.Approval.Reason != "" {
			approval["reason"] = payload.Approval.Reason
		}
		if payload.Approval.AllowInSession {
			approval["allow_in_session"] = true
		}
		if payload.Approval.CancelTurn {
			approval["cancel_turn"] = true
		}
		if payload.ToolName != "" {
			approval["tool_name"] = payload.ToolName
		}
		if payload.ArgumentsInJSON != "" {
			approval["arguments_json"] = payload.ArgumentsInJSON
		}
		body["approval"] = approval
	}
	if payload.RequestUserInput != nil {
		body["request_user_input"] = payload.RequestUserInput
	}
	if payload.Interrupt != nil {
		body["interrupt"] = payload.Interrupt
	}
	var response struct {
		Message json.RawMessage `json:"message"`
	}
	err = runtime.postJSON(ctx, "submit_input", body, &response)
	return err
}

func (runtime *HTTPRuntime) ClearThread() {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.session = SessionInfo{}
	runtime.ownerID = ""
	runtime.expectedOwnerID = ""
	runtime.mainThreadStatus = 0
	runtime.sessionValidated = false
	runtime.mu.Unlock()
}

func (runtime *HTTPRuntime) ExportThreadRef() (payload []byte, err error) {
	if runtime == nil {
		return nil, fmt.Errorf("HTTP runtime is required")
	}
	runtime.mu.Lock()
	reference := httpThreadReference{
		Runtime: sdkruntime.RuntimeRemote, BaseURL: runtime.apiBaseURL, ProjectName: runtime.project,
		SessionID: runtime.session.ID, OwnerID: runtime.ownerID, ThreadID: runtime.session.MainThreadID,
	}
	runtime.mu.Unlock()
	if reference.SessionID == "" || reference.ThreadID == "" {
		return nil, fmt.Errorf("no remote thread is selected")
	}
	payload, err = json.Marshal(reference)
	return payload, err
}

func (runtime *HTTPRuntime) ImportThreadRef(payload []byte) (err error) {
	if runtime == nil {
		return fmt.Errorf("HTTP runtime is required")
	}
	var reference httpThreadReference
	if err = json.Unmarshal(payload, &reference); err != nil {
		return fmt.Errorf("decode HTTP thread reference: %w", err)
	}
	if reference.Runtime != sdkruntime.RuntimeRemote {
		return fmt.Errorf("cannot import %s thread into remote runtime", reference.Runtime)
	}
	baseURL, err := canonicalHTTPAPIBase(reference.BaseURL)
	if err != nil {
		return err
	}
	if baseURL != runtime.apiBaseURL {
		return fmt.Errorf("remote endpoint does not match this runtime")
	}
	if strings.TrimSpace(reference.ProjectName) != runtime.project {
		return fmt.Errorf("remote project does not match this runtime")
	}
	if strings.TrimSpace(reference.SessionID) == "" || reference.SessionID == "0" || strings.TrimSpace(reference.ThreadID) == "" || reference.ThreadID == "0" {
		return fmt.Errorf("remote reference requires separate session_id and thread_id")
	}
	runtime.mu.Lock()
	runtime.session = SessionInfo{ID: reference.SessionID, ProjectName: reference.ProjectName, MainThreadID: reference.ThreadID}
	runtime.expectedOwnerID = strings.TrimSpace(reference.OwnerID)
	runtime.ownerID = ""
	runtime.mainThreadStatus = 0
	runtime.sessionValidated = false
	runtime.mu.Unlock()
	return nil
}

func (runtime *HTTPRuntime) SetPlanMode(ctx context.Context, enabled bool) (result bool, err error) {
	if err = ctx.Err(); err != nil {
		return false, err
	}
	runtime.mu.Lock()
	runtime.planMode = enabled
	runtime.mu.Unlock()
	return enabled, nil
}

func (runtime *HTTPRuntime) Name() (name string) {
	if runtime == nil {
		return "remote"
	}
	parsed, err := url.Parse(runtime.apiBaseURL)
	if err != nil || parsed.Host == "" {
		return "remote"
	}
	return "remote:" + parsed.Host
}

func (runtime *HTTPRuntime) RuntimeKind() (kind sdkruntime.RuntimeKind) {
	return sdkruntime.RuntimeRemote
}

func (runtime *HTTPRuntime) ensureSession(ctx context.Context) (err error) {
	runtime.mu.Lock()
	sessionID := runtime.session.ID
	validated := runtime.sessionValidated
	runtime.mu.Unlock()
	if sessionID != "" {
		if validated {
			return nil
		}
		return runtime.OpenSession(ctx, sessionID)
	}
	var response struct {
		SessionView *httpSessionView `json:"session_view"`
	}
	body := map[string]any{"project_name": runtime.project}
	if err = runtime.postJSON(ctx, "create_session", body, &response); err != nil {
		return err
	}
	if response.SessionView == nil || response.SessionView.Session == nil {
		return fmt.Errorf("create_session returned no session")
	}
	err = runtime.storeSessionView(response.SessionView, "", "")
	return err
}

func (runtime *HTTPRuntime) getSession(ctx context.Context, sessionID string) (view *httpSessionView, err error) {
	var response struct {
		SessionView *httpSessionView `json:"session_view"`
	}
	body := map[string]any{"session_id": sessionID, "include_threads": true}
	if err = runtime.postJSON(ctx, "get_session", body, &response); err != nil {
		return nil, err
	}
	if response.SessionView == nil || response.SessionView.Session == nil {
		return nil, fmt.Errorf("get_session returned no session")
	}
	return response.SessionView, nil
}

func (runtime *HTTPRuntime) storeSessionView(view *httpSessionView, expectedSessionID string, expectedOwnerID string) (err error) {
	if view == nil || view.Session == nil {
		return fmt.Errorf("session view is required")
	}
	remoteSession := view.Session
	if remoteSession.SessionID == "" || remoteSession.SessionID == "0" {
		return fmt.Errorf("backend session_id is required")
	}
	if expectedSessionID != "" && remoteSession.SessionID != expectedSessionID {
		return fmt.Errorf("backend returned session %q for %q", remoteSession.SessionID, expectedSessionID)
	}
	if strings.TrimSpace(remoteSession.ProjectName) != runtime.project {
		return fmt.Errorf("session project %q does not match configured project %q", remoteSession.ProjectName, runtime.project)
	}
	if expectedOwnerID != "" && remoteSession.UID != expectedOwnerID {
		return fmt.Errorf("session owner does not match imported reference")
	}
	mainThreadID := strings.TrimSpace(remoteSession.MainThreadID)
	if mainThreadID == "0" {
		mainThreadID = ""
	}
	status := 0
	for _, thread := range view.Threads {
		if thread.ThreadID == mainThreadID {
			status = thread.Status
			break
		}
	}
	runtime.mu.Lock()
	runtime.session = SessionInfo{
		ID: remoteSession.SessionID, Title: remoteSession.Title, ProjectName: remoteSession.ProjectName,
		ProjectPath: remoteSession.ProjectPath, MainThreadID: mainThreadID,
	}
	runtime.ownerID = remoteSession.UID
	runtime.expectedOwnerID = remoteSession.UID
	runtime.mainThreadStatus = status
	runtime.sessionValidated = true
	runtime.mu.Unlock()
	return nil
}

func (runtime *HTTPRuntime) listSessions(ctx context.Context, status *int) (sessions []SessionInfo, owners []string, err error) {
	cursor := ""
	for {
		body := map[string]any{"project_name": runtime.project, "limit": 100}
		if cursor != "" {
			body["cursor"] = cursor
		}
		if status != nil {
			body["status"] = *status
		}
		var response struct {
			Sessions []httpSession `json:"sessions"`
			PageInfo httpPageInfo  `json:"page_info"`
		}
		if err = runtime.postJSON(ctx, "list_sessions", body, &response); err != nil {
			return nil, nil, err
		}
		for _, remoteSession := range response.Sessions {
			if strings.TrimSpace(remoteSession.ProjectName) != runtime.project {
				return nil, nil, fmt.Errorf("list_sessions returned project %q for %q", remoteSession.ProjectName, runtime.project)
			}
			mainThreadID := strings.TrimSpace(remoteSession.MainThreadID)
			if mainThreadID == "0" {
				mainThreadID = ""
			}
			sessions = append(sessions, SessionInfo{
				ID: remoteSession.SessionID, Title: remoteSession.Title, ProjectName: remoteSession.ProjectName,
				ProjectPath: remoteSession.ProjectPath, MainThreadID: mainThreadID,
			})
			owners = append(owners, remoteSession.UID)
		}
		if !response.PageInfo.HasMore {
			return sessions, owners, nil
		}
		nextCursor := strings.TrimSpace(response.PageInfo.NextCursor)
		if nextCursor == "" || nextCursor == cursor {
			return nil, nil, fmt.Errorf("list_sessions pagination did not advance")
		}
		cursor = nextCursor
	}
}

func (runtime *HTTPRuntime) listTimelineAll(ctx context.Context, sessionID string, threadID string) (events []timeline.Event, err error) {
	cursor := ""
	seen := make(map[string]bool)
	for {
		body := map[string]any{"session_id": sessionID, "limit": httpListLimit}
		if threadID != "" {
			body["thread_id"] = threadID
		}
		if cursor != "" {
			body["cursor"] = cursor
		}
		var response struct {
			Events   []timeline.Event `json:"events"`
			PageInfo httpPageInfo     `json:"page_info"`
		}
		if err = runtime.postJSON(ctx, "list_timeline", body, &response); err != nil {
			return nil, err
		}
		for _, event := range response.Events {
			if event.EventID != "" && seen[event.EventID] {
				continue
			}
			if event.EventID != "" {
				seen[event.EventID] = true
			}
			events = append(events, event)
		}
		if !response.PageInfo.HasMore {
			return events, nil
		}
		nextCursor := strings.TrimSpace(response.PageInfo.NextCursor)
		if nextCursor == "" || nextCursor == cursor {
			return nil, fmt.Errorf("list_timeline pagination did not advance")
		}
		cursor = nextCursor
	}
}

func (runtime *HTTPRuntime) stopSession(ctx context.Context, sessionID string) (err error) {
	var response struct {
		SessionView json.RawMessage `json:"session_view"`
	}
	err = runtime.postJSON(ctx, "stop_running", map[string]any{"session_id": sessionID, "reason": "user_stop"}, &response)
	return err
}

func (runtime *HTTPRuntime) checkThreadRef(ref sdkruntime.GlobalThreadRef) (err error) {
	if err = ref.Validate(); err != nil {
		return err
	}
	if ref.Runtime != sdkruntime.RuntimeRemote {
		return fmt.Errorf("cannot use %s thread with remote runtime", ref.Runtime)
	}
	if ref.Namespace != runtime.project {
		return fmt.Errorf("thread project does not match this runtime")
	}
	session := runtime.Session()
	if session.MainThreadID == "" || ref.ThreadID != session.MainThreadID {
		return fmt.Errorf("thread does not match selected session")
	}
	return nil
}

func (runtime *HTTPRuntime) threadRef(threadID string) (ref sdkruntime.GlobalThreadRef) {
	return sdkruntime.GlobalThreadRef{Runtime: sdkruntime.RuntimeRemote, Namespace: runtime.project, ThreadID: threadID}
}

func (runtime *HTTPRuntime) expectedOwner() (ownerID string) {
	runtime.mu.Lock()
	ownerID = runtime.expectedOwnerID
	runtime.mu.Unlock()
	return ownerID
}

func (runtime *HTTPRuntime) threadStatus() (status int) {
	runtime.mu.Lock()
	status = runtime.mainThreadStatus
	runtime.mu.Unlock()
	return status
}

func (runtime *HTTPRuntime) postJSON(ctx context.Context, operation string, body any, output any) (err error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", operation, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, runtime.apiBaseURL+"/"+operation, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create %s request: %w", operation, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if runtime.token != "" {
		request.Header.Set(httpUserTokenHeader, runtime.token)
	}
	response, err := runtime.client.Do(request)
	if err != nil {
		return fmt.Errorf("%s request failed: %w", operation, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, httpResponseMaxBytes+1))
	if err != nil {
		return fmt.Errorf("read %s response: %w", operation, err)
	}
	if len(data) > httpResponseMaxBytes {
		return fmt.Errorf("%s response exceeds %d bytes", operation, httpResponseMaxBytes)
	}
	var base httpResponseBase
	if len(bytes.TrimSpace(data)) > 0 {
		if err = json.Unmarshal(data, &base); err != nil {
			return fmt.Errorf("decode %s response: %w", operation, err)
		}
	}
	statusCode, statusMessage := responseStatus(base)
	if response.StatusCode < 200 || response.StatusCode >= 300 || statusCode != 0 {
		if statusMessage == "" {
			statusMessage = strings.TrimSpace(response.Status)
		}
		return fmt.Errorf("%s failed: HTTP %d, code %d: %s", operation, response.StatusCode, statusCode, statusMessage)
	}
	if output != nil && len(bytes.TrimSpace(data)) > 0 {
		if err = json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("decode %s response: %w", operation, err)
		}
	}
	return nil
}

func canonicalHTTPAPIBase(baseURL string) (apiBaseURL string, err error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("remote base URL is required")
	}
	parsed, parseErr := url.Parse(baseURL)
	if parseErr != nil {
		return "", fmt.Errorf("invalid remote base URL")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("remote base URL must not include user information")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("remote base URL must use http or https")
	}
	if parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("remote base URL must contain a host and no query or fragment")
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if !strings.HasSuffix(path, httpAPIPath) {
		path += httpAPIPath
	}
	parsed.Path = path
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func responseStatus(response httpResponseBase) (code int32, message string) {
	base := response.BaseResp
	if base == nil {
		base = response.BaseRespLower
	}
	if base == nil {
		return 0, ""
	}
	code = base.StatusCode
	if code == 0 {
		code = base.StatusCodeLower
	}
	message = base.StatusMessage
	if message == "" {
		message = base.StatusMessageLower
	}
	return code, message
}

func timelineForThread(events []timeline.Event, threadID string) (filtered []timeline.Event) {
	for _, event := range events {
		if event.ThreadID == threadID {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func unfinishedTurnID(events []timeline.Event) (turnID string) {
	terminal := make(map[string]bool)
	for _, event := range events {
		eventType := protoevent.EventType(event.EventType)
		if isTerminalHTTPEvent(eventType) && event.TurnID != "" {
			terminal[event.TurnID] = true
		}
	}
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if protoevent.EventType(event.EventType) == protoevent.EventTypeTurnStarted && event.TurnID != "" && !terminal[event.TurnID] {
			return event.TurnID
		}
	}
	return ""
}

func splitActiveTurn(events []timeline.Event, turnID string) (completed []timeline.Event, replay []timeline.Event) {
	if turnID == "" {
		return events, nil
	}
	lastBlockingIndex := -1
	lastActiveIndex := -1
	for index, event := range events {
		if event.TurnID != turnID {
			continue
		}
		lastActiveIndex = index
		if isBlockingHTTPEvent(protoevent.EventType(event.EventType)) {
			lastBlockingIndex = index
		}
	}
	for index, event := range events {
		if event.TurnID == turnID {
			if lastBlockingIndex >= 0 && (index < lastBlockingIndex || index == lastBlockingIndex && lastActiveIndex > lastBlockingIndex) {
				completed = append(completed, event)
			} else {
				replay = append(replay, event)
			}
			continue
		}
		completed = append(completed, event)
	}
	return completed, replay
}

func isBlockingHTTPEvent(eventType protoevent.EventType) (blocking bool) {
	switch eventType {
	case protoevent.EventTypeApprovalRequired, protoevent.EventTypeInterruptRequired, protoevent.EventTypePlanInputRequired:
		return true
	default:
		return false
	}
}

func isActiveHTTPThread(status int) (active bool) {
	return status == 2 || status == 3 || status == 4 || status == 5
}

func isTerminalHTTPEvent(eventType protoevent.EventType) (terminal bool) {
	switch eventType {
	case protoevent.EventTypeTurnFinished, protoevent.EventTypeTurnInterrupted, protoevent.EventTypeError:
		return true
	default:
		return false
	}
}

func timelineEndsWithTerminal(events []timeline.Event) (terminal bool) {
	if len(events) == 0 {
		return false
	}
	return isTerminalHTTPEvent(protoevent.EventType(events[len(events)-1].EventType))
}

var _ InteractiveRuntime = (*HTTPRuntime)(nil)
