package session

import (
	"context"
	"strings"

	"code.byted.org/gopkg/logs"
	"code.byted.org/gopkg/metainfo"
	cloudbackend "eino-cli/deepagent/cloud/backend"
	httpapi "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_api"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_common"
	httpbase "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/base"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/common"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/deps"
	sessioncommon "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/kitex_gen/aic_agent_sdk_common"
	sessionsvc "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/kitex_gen/aic_agent_sdk_session"
	sessionbase "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/kitex_gen/base"
)

const userEmailMetaKey = "x-aic-agent-sdk-user-email"

func WithUserEmail(ctx context.Context, email string) context.Context {
	email = strings.TrimSpace(email)
	if email == "" {
		return ctx
	}
	return metainfo.WithPersistentValue(ctx, userEmailMetaKey, email)
}

func sessionIDFromView(view *httpcommon.AgentSessionView) int64 {
	if view == nil || view.Session == nil {
		return 0
	}
	return view.Session.GetSessionID()
}

func Create(ctx context.Context, uid int64, req *httpapi.CreateSessionHTTPRequest) (*httpapi.CreateSessionHTTPResponse, error) {
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] create session start: uid=%d project_name=%q title_set=%t", uid, req.GetProjectName(), req.IsSetTitle())
	cli, err := deps.SessionClient()
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] create session create aic_agent_sdk_session client failed: uid=%d project_name=%q err=%v", uid, req.GetProjectName(), err)
		return nil, common.Downstream("create aic_agent_sdk_session client", err)
	}
	project, err := cloudbackend.ResolveProject(deps.Config().Backend, uid, req.GetProjectName())
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] create session resolve project failed: uid=%d requested_project_name=%q err=%v", uid, req.GetProjectName(), err)
		return nil, common.InvalidArgument(err.Error())
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] create session project resolved: uid=%d project_name=%q project_path=%q", uid, project.Name, project.Path)
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] create session call aic_agent_sdk_session.CreateSession: uid=%d project_name=%q project_path=%q", uid, project.Name, project.Path)
	downstream, err := cli.CreateSession(ctx, &sessionsvc.CreateSessionRequest{
		Uid:         uid,
		Title:       req.Title,
		ProjectName: stringPtr(project.Name),
		ProjectPath: stringPtr(project.Path),
		Base:        sessionbase.NewBase(),
	})
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] create session aic_agent_sdk_session.CreateSession failed: uid=%d project_name=%q project_path=%q err=%v",
			uid, project.Name, project.Path, err)
		return nil, common.Downstream("aic_agent_sdk_session.CreateSession", err)
	}
	view, err := common.Convert[*httpcommon.AgentSessionView](downstream.GetSessionView())
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] create session convert response failed: uid=%d project_name=%q err=%v", uid, project.Name, err)
		return nil, common.Internal("convert CreateSession response", err)
	}
	resp := &httpapi.CreateSessionHTTPResponse{
		SessionView: view,
		BaseResp:    common.BaseRespFromSession(downstream.GetBaseResp()),
	}
	if err := common.CheckHTTPBaseResp("aic_agent_sdk_session.CreateSession", resp.BaseResp); err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] create session downstream baseresp failed: uid=%d project_name=%q err=%v", uid, project.Name, err)
		return resp, err
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] create session success: uid=%d dialog_stream_id=%d project_name=%q project_path=%q",
		uid, sessionIDFromView(view), project.Name, project.Path)
	return resp, nil
}

func List(ctx context.Context, uid int64, req *httpapi.ListSessionsHTTPRequest) (*httpapi.ListSessionsHTTPResponse, error) {
	cli, err := deps.SessionClient()
	if err != nil {
		return nil, common.Downstream("create aic_agent_sdk_session client", err)
	}
	sessionStatus, err := common.Convert[*sessioncommon.AgentSessionStatus](req.Status)
	if err != nil {
		return nil, common.Internal("convert ListSessions downstream status", err)
	}
	sessionPage, err := common.Convert[*sessioncommon.PageCursor](&httpcommon.PageCursor{Cursor: req.Cursor, Limit: req.Limit})
	if err != nil {
		return nil, common.Internal("convert ListSessions downstream page", err)
	}
	downstream, err := cli.ListSessions(ctx, &sessionsvc.ListSessionsRequest{
		Uid:         uid,
		Status:      sessionStatus,
		Page:        sessionPage,
		ProjectName: req.ProjectName,
		Base:        sessionbase.NewBase(),
	})
	if err != nil {
		return nil, common.Downstream("aic_agent_sdk_session.ListSessions", err)
	}
	sessions, err := common.Convert[[]*httpcommon.AgentSession](downstream.GetSessions())
	if err != nil {
		return nil, common.Internal("convert ListSessions sessions", err)
	}
	pageInfo, err := common.Convert[*httpcommon.PageInfo](downstream.GetPageInfo())
	if err != nil {
		return nil, common.Internal("convert ListSessions page_info", err)
	}
	resp := &httpapi.ListSessionsHTTPResponse{
		Sessions: sessions,
		PageInfo: pageInfo,
		BaseResp: common.BaseRespFromSession(downstream.GetBaseResp()),
	}
	return resp, common.CheckHTTPBaseResp("aic_agent_sdk_session.ListSessions", resp.BaseResp)
}

func ListProjects(ctx context.Context, uid int64, _ *httpapi.ListProjectsHTTPRequest) (*httpapi.ListProjectsHTTPResponse, error) {
	cli, err := deps.SessionClient()
	if err != nil {
		return nil, common.Downstream("create aic_agent_sdk_session client", err)
	}
	status := sessioncommon.AgentSessionStatus_ACTIVE
	downstream, err := cli.ListProjects(ctx, &sessionsvc.ListProjectsRequest{
		Uid:    uid,
		Status: &status,
		Base:   sessionbase.NewBase(),
	})
	if err != nil {
		return nil, common.Downstream("aic_agent_sdk_session.ListProjects", err)
	}
	projects, err := common.Convert[[]*httpcommon.SessionProject](downstream.GetProjects())
	if err != nil {
		return nil, common.Internal("convert ListProjects projects", err)
	}
	resp := &httpapi.ListProjectsHTTPResponse{
		Projects: projects,
		BaseResp: common.BaseRespFromSession(downstream.GetBaseResp()),
	}
	return resp, common.CheckHTTPBaseResp("aic_agent_sdk_session.ListProjects", resp.BaseResp)
}

func Get(ctx context.Context, uid int64, req *httpapi.GetSessionHTTPRequest) (*httpapi.GetSessionHTTPResponse, error) {
	view, baseResp, err := getView(ctx, uid, req.GetSessionID(), req.IncludeThreads)
	if err != nil {
		return nil, err
	}
	resp := &httpapi.GetSessionHTTPResponse{SessionView: view, BaseResp: baseResp}
	return resp, common.CheckHTTPBaseResp("aic_agent_sdk_session.GetSession", resp.BaseResp)
}

func Update(ctx context.Context, uid int64, req *httpapi.UpdateSessionHTTPRequest) (*httpapi.UpdateSessionHTTPResponse, error) {
	cli, err := deps.SessionClient()
	if err != nil {
		return nil, common.Downstream("create aic_agent_sdk_session client", err)
	}
	status, err := common.Convert[*sessioncommon.AgentSessionStatus](req.Status)
	if err != nil {
		return nil, common.Internal("convert UpdateSession status", err)
	}
	downstream, err := cli.UpdateSession(ctx, &sessionsvc.UpdateSessionRequest{
		Uid:       uid,
		SessionId: req.GetSessionID(),
		Title:     req.Title,
		Status:    status,
		Base:      sessionbase.NewBase(),
	})
	if err != nil {
		return nil, common.Downstream("aic_agent_sdk_session.UpdateSession", err)
	}
	view, err := common.Convert[*httpcommon.AgentSessionView](downstream.GetSessionView())
	if err != nil {
		return nil, common.Internal("convert UpdateSession response", err)
	}
	resp := &httpapi.UpdateSessionHTTPResponse{
		SessionView: view,
		BaseResp:    common.BaseRespFromSession(downstream.GetBaseResp()),
	}
	return resp, common.CheckHTTPBaseResp("aic_agent_sdk_session.UpdateSession", resp.BaseResp)
}

func Close(ctx context.Context, uid int64, req *httpapi.CloseSessionHTTPRequest) (*httpapi.CloseSessionHTTPResponse, error) {
	cli, err := deps.SessionClient()
	if err != nil {
		return nil, common.Downstream("create aic_agent_sdk_session client", err)
	}
	downstream, err := cli.CloseSession(ctx, &sessionsvc.CloseSessionRequest{
		Uid:       uid,
		SessionId: req.GetSessionID(),
		Reason:    req.Reason,
		Base:      sessionbase.NewBase(),
	})
	if err != nil {
		return nil, common.Downstream("aic_agent_sdk_session.CloseSession", err)
	}
	view, err := common.Convert[*httpcommon.AgentSessionView](downstream.GetSessionView())
	if err != nil {
		return nil, common.Internal("convert CloseSession response", err)
	}
	resp := &httpapi.CloseSessionHTTPResponse{
		SessionView: view,
		BaseResp:    common.BaseRespFromSession(downstream.GetBaseResp()),
	}
	return resp, common.CheckHTTPBaseResp("aic_agent_sdk_session.CloseSession", resp.BaseResp)
}

func CloseProject(ctx context.Context, uid int64, req *httpapi.CloseProjectHTTPRequest) (*httpapi.CloseProjectHTTPResponse, error) {
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] close project start: uid=%d project_name=%q reason=%q", uid, req.GetProjectName(), req.GetReason())
	cli, err := deps.SessionClient()
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] close project create aic_agent_sdk_session client failed: uid=%d project_name=%q err=%v", uid, req.GetProjectName(), err)
		return nil, common.Downstream("create aic_agent_sdk_session client", err)
	}
	downstream, err := cli.CloseProject(ctx, &sessionsvc.CloseProjectRequest{
		Uid:         uid,
		ProjectName: req.GetProjectName(),
		Reason:      req.Reason,
		Base:        sessionbase.NewBase(),
	})
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] close project aic_agent_sdk_session.CloseProject failed: uid=%d project_name=%q err=%v", uid, req.GetProjectName(), err)
		return nil, common.Downstream("aic_agent_sdk_session.CloseProject", err)
	}
	project, err := common.Convert[*httpcommon.SessionProject](downstream.GetProject())
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] close project convert response failed: uid=%d project_name=%q err=%v", uid, req.GetProjectName(), err)
		return nil, common.Internal("convert CloseProject response", err)
	}
	resp := &httpapi.CloseProjectHTTPResponse{
		Project:            project,
		ClosedSessionIds:   downstream.GetClosedSessionIds(),
		ClosedSessionCount: int64Ptr(downstream.GetClosedSessionCount()),
		ClosedThreadCount:  int64Ptr(downstream.GetClosedThreadCount()),
		BaseResp:           common.BaseRespFromSession(downstream.GetBaseResp()),
	}
	if err := common.CheckHTTPBaseResp("aic_agent_sdk_session.CloseProject", resp.BaseResp); err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] close project downstream baseresp failed: uid=%d project_name=%q err=%v", uid, req.GetProjectName(), err)
		return resp, err
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] close project success: uid=%d project_name=%q closed_session_count=%d closed_thread_count=%d",
		uid, req.GetProjectName(), resp.GetClosedSessionCount(), resp.GetClosedThreadCount())
	return resp, nil
}

func RequireView(ctx context.Context, uid int64, sessionID int64, includeThreads bool) (*httpcommon.AgentSessionView, error) {
	view, baseResp, err := getView(ctx, uid, sessionID, &includeThreads)
	if err != nil {
		return nil, err
	}
	if err := common.CheckHTTPBaseResp("aic_agent_sdk_session.GetSession", baseResp); err != nil {
		return nil, err
	}
	return view, nil
}

func BindMainThread(ctx context.Context, uid int64, sessionID int64, threadID int64) error {
	cli, err := deps.SessionClient()
	if err != nil {
		return common.Downstream("create aic_agent_sdk_session client", err)
	}
	downstream, err := cli.BindMainThread(ctx, &sessionsvc.BindMainThreadRequest{
		Uid:          uid,
		SessionId:    sessionID,
		MainThreadId: threadID,
		Base:         sessionbase.NewBase(),
	})
	if err != nil {
		return common.Downstream("aic_agent_sdk_session.BindMainThread", err)
	}
	return common.CheckHTTPBaseResp("aic_agent_sdk_session.BindMainThread", common.BaseRespFromSession(downstream.GetBaseResp()))
}

func Touch(ctx context.Context, uid int64, sessionID int64, preview string, titleIfEmpty string, activeAtMs int64) (*httpcommon.AgentSessionView, error) {
	cli, err := deps.SessionClient()
	if err != nil {
		return nil, common.Downstream("create aic_agent_sdk_session client", err)
	}
	downstream, err := cli.TouchSession(ctx, &sessionsvc.TouchSessionRequest{
		Uid:                uid,
		SessionId:          sessionID,
		LastMessagePreview: stringPtr(preview),
		TitleIfEmpty:       stringPtr(titleIfEmpty),
		LastActiveAtMs:     &activeAtMs,
		Base:               sessionbase.NewBase(),
	})
	if err != nil {
		return nil, common.Downstream("aic_agent_sdk_session.TouchSession", err)
	}
	baseResp := common.BaseRespFromSession(downstream.GetBaseResp())
	if err := common.CheckHTTPBaseResp("aic_agent_sdk_session.TouchSession", baseResp); err != nil {
		return nil, err
	}
	view, err := common.Convert[*httpcommon.AgentSessionView](downstream.GetSessionView())
	if err != nil {
		return nil, common.Internal("convert TouchSession response", err)
	}
	return view, nil
}

func getView(ctx context.Context, uid int64, sessionID int64, includeThreads *bool) (*httpcommon.AgentSessionView, *httpbase.BaseResp, error) {
	cli, err := deps.SessionClient()
	if err != nil {
		return nil, nil, common.Downstream("create aic_agent_sdk_session client", err)
	}
	downstream, err := cli.GetSession(ctx, &sessionsvc.GetSessionRequest{
		Uid:            uid,
		SessionId:      sessionID,
		IncludeThreads: includeThreads,
		Base:           sessionbase.NewBase(),
	})
	if err != nil {
		return nil, nil, common.Downstream("aic_agent_sdk_session.GetSession", err)
	}
	view, err := common.Convert[*httpcommon.AgentSessionView](downstream.GetSessionView())
	if err != nil {
		return nil, nil, common.Internal("convert GetSession response", err)
	}
	return view, common.BaseRespFromSession(downstream.GetBaseResp()), nil
}

func stringPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func int64Ptr(v int64) *int64 {
	return &v
}
