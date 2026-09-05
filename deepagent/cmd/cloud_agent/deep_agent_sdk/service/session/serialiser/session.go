package serialiser

import (
	"errors"
	"strings"

	sessiondal "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/dal/session"
	sessionservice "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/service/session"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/deep_agent_sdk_common"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/common"
)

func View(view *sessionservice.View) (response *httpcommon.AgentSessionView) {
	if view == nil {
		return nil
	}
	response = &httpcommon.AgentSessionView{Session: Session(view.Session)}
	if view.Threads != nil {
		response.Threads = make([]*httpcommon.AgentThread, 0, len(view.Threads))
		for _, thread := range view.Threads {
			role := httpcommon.AgentThreadRole_CHILD
			if thread.IsMain {
				role = httpcommon.AgentThreadRole_MAIN
			}
			response.Threads = append(response.Threads, &httpcommon.AgentThread{
				ThreadID: thread.ThreadID, AcNamespace: thread.Namespace, SessionID: thread.SessionID, UID: thread.UID,
				Title: &thread.Title, Status: threadStatus(thread.Status), StatusReason: &thread.StatusReason, Role: &role,
				CreatedAtMs: &thread.CreatedAtMS, UpdatedAtMs: &thread.UpdatedAtMS,
				ParentThreadID: thread.ParentThreadID, RootThreadID: thread.RootThreadID,
			})
		}
	}
	return response
}

func Session(session *sessiondal.Session) (response *httpcommon.AgentSession) {
	if session == nil {
		return nil
	}
	status := httpcommon.AgentSessionStatus_ACTIVE
	if session.Status == sessiondal.StatusArchived {
		status = httpcommon.AgentSessionStatus_ARCHIVED
	}
	if session.Status == sessiondal.StatusClosed {
		status = httpcommon.AgentSessionStatus_CLOSED
	}
	return &httpcommon.AgentSession{
		SessionID: session.SessionID, UID: session.UID, Title: &session.Title, MainThreadID: &session.MainThreadID,
		ProjectName: &session.ProjectName, ProjectPath: &session.ProjectPath, Status: status,
		LastMessagePreview: &session.LastMessagePreview, LastActiveAtMs: &session.LastActiveAtMS,
		CreatedAtMs: &session.CreatedAtMS, UpdatedAtMs: &session.UpdatedAtMS,
	}
}

func Project(project *sessiondal.SessionProject) (response *httpcommon.SessionProject) {
	if project == nil {
		return nil
	}
	return &httpcommon.SessionProject{ProjectName: project.ProjectName, ProjectPath: project.ProjectPath,
		SessionCount: &project.SessionCount, LastActiveAtMs: &project.LastActiveAtMS}
}

func Status(status *httpcommon.AgentSessionStatus) (value *int64) {
	if status == nil {
		return nil
	}
	number := int64(*status)
	return &number
}

func Error(method string, cause error) (err error) {
	if cause == nil {
		return nil
	}
	code := int32(500)
	switch {
	case errors.Is(cause, sessiondal.ErrNotFound):
		code = 404
	case errors.Is(cause, sessiondal.ErrClosed), errors.Is(cause, sessiondal.ErrMainThreadBusy):
		code = 409
	default:
		message := strings.ToLower(cause.Error())
		if strings.Contains(message, "required") || strings.Contains(message, "invalid") || strings.Contains(message, "must be set") {
			code = 400
		}
	}
	// 保持现有 HTTP 状态与业务码，内部调用不再传递 BaseResp。
	return common.NewError(502, code, "deep_agent_sdk_session."+method+" returned non-zero BaseResp: "+cause.Error(), nil)
}

func threadStatus(status string) (value httpcommon.AgentThreadStatus) {
	switch status {
	case "ready":
		return httpcommon.AgentThreadStatus_READY
	case "running":
		return httpcommon.AgentThreadStatus_RUNNING
	case "blocked":
		return httpcommon.AgentThreadStatus_BLOCKED
	case "closing":
		return httpcommon.AgentThreadStatus_CLOSING
	case "closed":
		return httpcommon.AgentThreadStatus_CLOSED
	default:
		return httpcommon.AgentThreadStatus_IDLE
	}
}
