package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"code.byted.org/gopkg/metainfo"
	sessiondal "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/dal/session"
	aic_agent_sdk_common "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/kitex_gen/aic_agent_sdk_common"
	aic_agent_sdk_session "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/kitex_gen/aic_agent_sdk_session"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/kitex_gen/base"
	sessionservice "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/service/session"
)

// AICAgentSDKSessionServiceImpl adapts Kitex requests to the session service.
type AICAgentSDKSessionServiceImpl struct {
	svc *sessionservice.Service
}

const userEmailMetaKey = "x-aic-agent-sdk-user-email"

func NewAICAgentSDKSessionServiceImpl(svc *sessionservice.Service) *AICAgentSDKSessionServiceImpl {
	return &AICAgentSDKSessionServiceImpl{svc: svc}
}

func (s *AICAgentSDKSessionServiceImpl) CreateSession(ctx context.Context, req *aic_agent_sdk_session.CreateSessionRequest) (*aic_agent_sdk_session.CreateSessionResponse, error) {
	resp := &aic_agent_sdk_session.CreateSessionResponse{BaseResp: okBaseResp()}
	if req == nil {
		resp.BaseResp = errorBaseResp(fmt.Errorf("request is required"))
		return resp, nil
	}
	svc, err := s.service()
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	view, err := svc.CreateSession(ctx, req.GetUid(), req.Title, req.ProjectName, req.ProjectPath, userEmailFromContext(ctx))
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	resp.SessionView = view
	return resp, nil
}

func userEmailFromContext(ctx context.Context) string {
	if email, ok := metainfo.GetPersistentValue(ctx, userEmailMetaKey); ok && strings.TrimSpace(email) != "" {
		return strings.TrimSpace(email)
	}
	if email, ok := metainfo.GetValue(ctx, userEmailMetaKey); ok && strings.TrimSpace(email) != "" {
		return strings.TrimSpace(email)
	}
	return ""
}

func (s *AICAgentSDKSessionServiceImpl) ListSessions(ctx context.Context, req *aic_agent_sdk_session.ListSessionsRequest) (*aic_agent_sdk_session.ListSessionsResponse, error) {
	resp := &aic_agent_sdk_session.ListSessionsResponse{BaseResp: okBaseResp(), PageInfo: &aic_agent_sdk_common.PageInfo{}}
	if req == nil {
		resp.BaseResp = errorBaseResp(fmt.Errorf("request is required"))
		return resp, nil
	}
	svc, err := s.service()
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	var (
		cursor string
		limit  int32
	)
	if req.GetPage() != nil {
		cursor = req.GetPage().GetCursor()
		limit = req.GetPage().GetLimit()
	}
	sessions, pageInfo, err := svc.ListSessions(ctx, req.GetUid(), req.Status, req.ProjectName, cursor, limit)
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	resp.Sessions = sessions
	resp.PageInfo = pageInfo
	return resp, nil
}

func (s *AICAgentSDKSessionServiceImpl) GetSession(ctx context.Context, req *aic_agent_sdk_session.GetSessionRequest) (*aic_agent_sdk_session.GetSessionResponse, error) {
	resp := &aic_agent_sdk_session.GetSessionResponse{BaseResp: okBaseResp()}
	if req == nil {
		resp.BaseResp = errorBaseResp(fmt.Errorf("request is required"))
		return resp, nil
	}
	svc, err := s.service()
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	view, err := svc.GetSession(ctx, req.GetUid(), req.GetSessionId(), req.GetIncludeThreads())
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	resp.SessionView = view
	return resp, nil
}

func (s *AICAgentSDKSessionServiceImpl) UpdateSession(ctx context.Context, req *aic_agent_sdk_session.UpdateSessionRequest) (*aic_agent_sdk_session.UpdateSessionResponse, error) {
	resp := &aic_agent_sdk_session.UpdateSessionResponse{BaseResp: okBaseResp()}
	if req == nil {
		resp.BaseResp = errorBaseResp(fmt.Errorf("request is required"))
		return resp, nil
	}
	svc, err := s.service()
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	view, err := svc.UpdateSession(ctx, req.GetUid(), req.GetSessionId(), req.Title, req.Status)
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	resp.SessionView = view
	return resp, nil
}

func (s *AICAgentSDKSessionServiceImpl) CloseSession(ctx context.Context, req *aic_agent_sdk_session.CloseSessionRequest) (*aic_agent_sdk_session.CloseSessionResponse, error) {
	resp := &aic_agent_sdk_session.CloseSessionResponse{BaseResp: okBaseResp()}
	if req == nil {
		resp.BaseResp = errorBaseResp(fmt.Errorf("request is required"))
		return resp, nil
	}
	svc, err := s.service()
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	view, err := svc.CloseSession(ctx, req.GetUid(), req.GetSessionId(), req.GetReason())
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	resp.SessionView = view
	return resp, nil
}

func (s *AICAgentSDKSessionServiceImpl) CloseProject(ctx context.Context, req *aic_agent_sdk_session.CloseProjectRequest) (*aic_agent_sdk_session.CloseProjectResponse, error) {
	resp := &aic_agent_sdk_session.CloseProjectResponse{BaseResp: okBaseResp()}
	if req == nil {
		resp.BaseResp = errorBaseResp(fmt.Errorf("request is required"))
		return resp, nil
	}
	svc, err := s.service()
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	result, err := svc.CloseProject(ctx, req.GetUid(), req.GetProjectName(), req.GetReason())
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	resp.Project = result.Project
	resp.ClosedSessionIds = result.ClosedSessionIDs
	resp.ClosedSessionCount = int64Ptr(result.ClosedSessionCount)
	resp.ClosedThreadCount = int64Ptr(result.ClosedThreadCount)
	return resp, nil
}

func (s *AICAgentSDKSessionServiceImpl) BindMainThread(ctx context.Context, req *aic_agent_sdk_session.BindMainThreadRequest) (*aic_agent_sdk_session.BindMainThreadResponse, error) {
	resp := &aic_agent_sdk_session.BindMainThreadResponse{BaseResp: okBaseResp()}
	if req == nil {
		resp.BaseResp = errorBaseResp(fmt.Errorf("request is required"))
		return resp, nil
	}
	svc, err := s.service()
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	view, err := svc.BindMainThread(ctx, req.GetUid(), req.GetSessionId(), req.GetMainThreadId())
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	resp.SessionView = view
	return resp, nil
}

func (s *AICAgentSDKSessionServiceImpl) TouchSession(ctx context.Context, req *aic_agent_sdk_session.TouchSessionRequest) (*aic_agent_sdk_session.TouchSessionResponse, error) {
	resp := &aic_agent_sdk_session.TouchSessionResponse{BaseResp: okBaseResp()}
	if req == nil {
		resp.BaseResp = errorBaseResp(fmt.Errorf("request is required"))
		return resp, nil
	}
	svc, err := s.service()
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	view, err := svc.TouchSession(ctx, req.GetUid(), req.GetSessionId(), req.LastMessagePreview, req.TitleIfEmpty, req.LastActiveAtMs)
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	resp.SessionView = view
	return resp, nil
}

func (s *AICAgentSDKSessionServiceImpl) ListProjects(ctx context.Context, req *aic_agent_sdk_session.ListProjectsRequest) (*aic_agent_sdk_session.ListProjectsResponse, error) {
	resp := &aic_agent_sdk_session.ListProjectsResponse{BaseResp: okBaseResp()}
	if req == nil {
		resp.BaseResp = errorBaseResp(fmt.Errorf("request is required"))
		return resp, nil
	}
	svc, err := s.service()
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	projects, err := svc.ListProjects(ctx, req.GetUid(), req.Status)
	if err != nil {
		resp.BaseResp = errorBaseResp(err)
		return resp, nil
	}
	resp.Projects = projects
	return resp, nil
}

func (s *AICAgentSDKSessionServiceImpl) service() (*sessionservice.Service, error) {
	if s == nil || s.svc == nil {
		return nil, fmt.Errorf("aic_agent_sdk_session service is not initialized")
	}
	return s.svc, nil
}

func okBaseResp() *base.BaseResp {
	return &base.BaseResp{StatusCode: 0, StatusMessage: "OK"}
}

func int64Ptr(value int64) *int64 {
	return &value
}

func errorBaseResp(err error) *base.BaseResp {
	return &base.BaseResp{
		StatusCode:    statusCode(err),
		StatusMessage: err.Error(),
	}
}

func statusCode(err error) int32 {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, sessiondal.ErrNotFound):
		return 404
	case errors.Is(err, sessiondal.ErrClosed), errors.Is(err, sessiondal.ErrMainThreadBusy):
		return 409
	case isBadRequest(err):
		return 400
	default:
		return 500
	}
}

func isBadRequest(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "required") ||
		strings.Contains(msg, "invalid") ||
		strings.Contains(msg, "must be set")
}
