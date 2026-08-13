package control

import (
	"context"
	"strconv"

	"code.byted.org/gopkg/logs"
	cloudapi "eino-cli/deepagent/cloud/api"
	httpapi "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_api"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_common"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/common"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/deps"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/session"
)

func StopRunning(ctx context.Context, uid int64, req *httpapi.StopRunningHTTPRequest) (*httpapi.StopRunningHTTPResponse, error) {
	if req.GetSessionID() == 0 {
		return nil, common.InvalidArgument("session_id is required")
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] stop running start: uid=%d dialog_stream_id=%d reason=%q", uid, req.GetSessionID(), req.GetReason())
	view, err := session.RequireView(ctx, uid, req.GetSessionID(), true)
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] stop running load session failed: uid=%d dialog_stream_id=%d err=%v", uid, req.GetSessionID(), err)
		return nil, err
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] stop running session loaded: uid=%d dialog_stream_id=%d main_thread_id=%d",
		uid, req.GetSessionID(), mainThreadID(view))
	agentAPI, err := deps.AgentAPI()
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] stop running create cloudagent api failed: uid=%d dialog_stream_id=%d err=%v", uid, req.GetSessionID(), err)
		return nil, common.Downstream("create cloudagent api", err)
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] stop running call cloudagent.StopRunning: uid=%d dialog_stream_id=%d reason=%q",
		uid, req.GetSessionID(), req.GetReason())
	result, err := agentAPI.StopRunning(ctx, cloudapi.StopRunningRequest{
		UserID:    strconv.FormatInt(uid, 10),
		SessionID: strconv.FormatInt(req.GetSessionID(), 10),
		Reason:    req.GetReason(),
	})
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] stop running cloudagent.StopRunning failed: uid=%d dialog_stream_id=%d err=%v", uid, req.GetSessionID(), err)
		return nil, common.Downstream("cloudagent.StopRunning", err)
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] stop running success: uid=%d dialog_stream_id=%d control_message_count=%d refs=%v",
		uid, req.GetSessionID(), len(result.ControlMessages), result.ControlMessages)
	return &httpapi.StopRunningHTTPResponse{
		SessionView:     view,
		ControlMessages: messageRefs(result.ControlMessages),
		BaseResp:        common.BaseRespOK(),
	}, nil
}

func mainThreadID(view *httpcommon.AgentSessionView) int64 {
	if view == nil || view.Session == nil {
		return 0
	}
	return view.Session.GetMainThreadID()
}

func messageRefs(refs []cloudapi.MessageRef) []*httpcommon.MessageRef {
	out := make([]*httpcommon.MessageRef, 0, len(refs))
	for _, ref := range refs {
		threadID, _ := strconv.ParseInt(ref.ThreadID, 10, 64)
		out = append(out, &httpcommon.MessageRef{
			ThreadID:  threadID,
			MessageID: ref.MessageID,
		})
	}
	return out
}
