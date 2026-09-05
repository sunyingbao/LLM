package session

import (
	"context"
	"strings"

	"code.byted.org/gopkg/metainfo"
	cloudbackend "eino-cli/deepagent/cloud/backend"
	httpapi "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/deep_agent_sdk"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/deep_agent_sdk_common"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/common"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/deps"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/session/serialiser"
	sessiondal "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/dal/session"
)

const userEmailMetaKey = "x-deep-agent-sdk-user-email"

func WithUserEmail(ctx context.Context, email string) (withEmail context.Context) {
	email = strings.TrimSpace(email)
	if email == "" {
		return ctx
	}
	return metainfo.WithPersistentValue(ctx, userEmailMetaKey, email)
}

func userEmailFromContext(ctx context.Context) (email string) {
	if email, ok := metainfo.GetPersistentValue(ctx, userEmailMetaKey); ok && strings.TrimSpace(email) != "" {
		return strings.TrimSpace(email)
	}
	if email, ok := metainfo.GetValue(ctx, userEmailMetaKey); ok && strings.TrimSpace(email) != "" {
		return strings.TrimSpace(email)
	}
	return ""
}

func Create(ctx context.Context, uid int64, req *httpapi.CreateSessionHTTPRequest) (response *httpapi.CreateSessionHTTPResponse, err error) {
	svc, err := deps.SessionService()
	if err != nil {
		return nil, common.Downstream("create deep_agent_sdk_session client", err)
	}
	project, err := cloudbackend.ResolveProject(deps.Config().Backend, uid, req.GetProjectName())
	if err != nil {
		return nil, common.InvalidArgument(err.Error())
	}
	view, err := svc.CreateSession(ctx, uid, req.Title, &project.Name, &project.Path, userEmailFromContext(ctx))
	if err != nil {
		return nil, serialiser.Error("CreateSession", err)
	}
	return &httpapi.CreateSessionHTTPResponse{SessionView: serialiser.View(view), BaseResp: common.BaseRespOK()}, nil
}

func List(ctx context.Context, uid int64, req *httpapi.ListSessionsHTTPRequest) (response *httpapi.ListSessionsHTTPResponse, err error) {
	svc, err := deps.SessionService()
	if err != nil {
		return nil, common.Downstream("create deep_agent_sdk_session client", err)
	}
	sessions, page, err := svc.ListSessions(ctx, uid, serialiser.Status(req.Status), req.ProjectName, req.GetCursor(), req.GetLimit())
	if err != nil {
		return nil, serialiser.Error("ListSessions", err)
	}
	response = &httpapi.ListSessionsHTTPResponse{Sessions: make([]*httpcommon.AgentSession, 0, len(sessions)),
		PageInfo: &httpcommon.PageInfo{HasMore: page.HasMore}, BaseResp: common.BaseRespOK()}
	if page.NextCursor != "" {
		response.PageInfo.NextCursor = &page.NextCursor
	}
	for _, session := range sessions {
		response.Sessions = append(response.Sessions, serialiser.Session(session))
	}
	return response, nil
}

func ListProjects(ctx context.Context, uid int64, _ *httpapi.ListProjectsHTTPRequest) (response *httpapi.ListProjectsHTTPResponse, err error) {
	svc, err := deps.SessionService()
	if err != nil {
		return nil, common.Downstream("create deep_agent_sdk_session client", err)
	}
	status := sessiondal.StatusActive
	projects, err := svc.ListProjects(ctx, uid, &status)
	if err != nil {
		return nil, serialiser.Error("ListProjects", err)
	}
	response = &httpapi.ListProjectsHTTPResponse{Projects: make([]*httpcommon.SessionProject, 0, len(projects)), BaseResp: common.BaseRespOK()}
	for _, project := range projects {
		response.Projects = append(response.Projects, serialiser.Project(project))
	}
	return response, nil
}

func Get(ctx context.Context, uid int64, req *httpapi.GetSessionHTTPRequest) (response *httpapi.GetSessionHTTPResponse, err error) {
	view, err := RequireView(ctx, uid, req.GetSessionID(), req.GetIncludeThreads())
	if err != nil {
		return nil, err
	}
	return &httpapi.GetSessionHTTPResponse{SessionView: view, BaseResp: common.BaseRespOK()}, nil
}

func Update(ctx context.Context, uid int64, req *httpapi.UpdateSessionHTTPRequest) (response *httpapi.UpdateSessionHTTPResponse, err error) {
	svc, err := deps.SessionService()
	if err != nil {
		return nil, common.Downstream("create deep_agent_sdk_session client", err)
	}
	view, err := svc.UpdateSession(ctx, uid, req.GetSessionID(), req.Title, serialiser.Status(req.Status))
	if err != nil {
		return nil, serialiser.Error("UpdateSession", err)
	}
	return &httpapi.UpdateSessionHTTPResponse{SessionView: serialiser.View(view), BaseResp: common.BaseRespOK()}, nil
}

func Close(ctx context.Context, uid int64, req *httpapi.CloseSessionHTTPRequest) (response *httpapi.CloseSessionHTTPResponse, err error) {
	svc, err := deps.SessionService()
	if err != nil {
		return nil, common.Downstream("create deep_agent_sdk_session client", err)
	}
	view, err := svc.CloseSession(ctx, uid, req.GetSessionID(), req.GetReason())
	if err != nil {
		return nil, serialiser.Error("CloseSession", err)
	}
	return &httpapi.CloseSessionHTTPResponse{SessionView: serialiser.View(view), BaseResp: common.BaseRespOK()}, nil
}

func CloseProject(ctx context.Context, uid int64, req *httpapi.CloseProjectHTTPRequest) (response *httpapi.CloseProjectHTTPResponse, err error) {
	svc, err := deps.SessionService()
	if err != nil {
		return nil, common.Downstream("create deep_agent_sdk_session client", err)
	}
	closed, err := svc.CloseProject(ctx, uid, req.GetProjectName(), req.GetReason())
	if err != nil {
		return nil, serialiser.Error("CloseProject", err)
	}
	return &httpapi.CloseProjectHTTPResponse{Project: serialiser.Project(closed.Project), ClosedSessionIds: closed.ClosedSessionIDs,
		ClosedSessionCount: &closed.ClosedSessionCount, ClosedThreadCount: &closed.ClosedThreadCount, BaseResp: common.BaseRespOK()}, nil
}

func RequireView(ctx context.Context, uid int64, sessionID int64, includeThreads bool) (response *httpcommon.AgentSessionView, err error) {
	svc, err := deps.SessionService()
	if err != nil {
		return nil, common.Downstream("create deep_agent_sdk_session client", err)
	}
	view, err := svc.GetSession(ctx, uid, sessionID, includeThreads)
	if err != nil {
		return nil, serialiser.Error("GetSession", err)
	}
	return serialiser.View(view), nil
}

func BindMainThread(ctx context.Context, uid int64, sessionID int64, threadID int64) (err error) {
	svc, err := deps.SessionService()
	if err != nil {
		return common.Downstream("create deep_agent_sdk_session client", err)
	}
	_, err = svc.BindMainThread(ctx, uid, sessionID, threadID)
	return serialiser.Error("BindMainThread", err)
}

func Touch(ctx context.Context, uid int64, sessionID int64, preview string, titleIfEmpty string, activeAtMs int64) (response *httpcommon.AgentSessionView, err error) {
	svc, err := deps.SessionService()
	if err != nil {
		return nil, common.Downstream("create deep_agent_sdk_session client", err)
	}
	var previewValue, titleValue *string
	if preview != "" {
		previewValue = &preview
	}
	if titleIfEmpty != "" {
		titleValue = &titleIfEmpty
	}
	view, err := svc.TouchSession(ctx, uid, sessionID, previewValue, titleValue, &activeAtMs)
	if err != nil {
		return nil, serialiser.Error("TouchSession", err)
	}
	return serialiser.View(view), nil
}
