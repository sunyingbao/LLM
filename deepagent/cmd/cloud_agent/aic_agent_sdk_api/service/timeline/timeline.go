package timeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"code.byted.org/gopkg/logs"
	cloudapi "eino-cli/deepagent/cloud/api"
	prototimeline "eino-cli/deepagent/cloud/protocol/timeline"
	httpapi "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_api"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_common"
	httpbase "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/base"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/common"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/deps"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/session"
)

type TimelineEvent = prototimeline.Event

type ListResponse struct {
	Events   []*TimelineEvent     `json:"events,omitempty"`
	PageInfo *httpcommon.PageInfo `json:"page_info"`
	BaseResp *httpbase.BaseResp   `json:"BaseResp"`
}

type SubscribeResponse struct {
	QueueID  *string            `json:"queue_id,omitempty"`
	Event    *TimelineEvent     `json:"event,omitempty"`
	BaseResp *httpbase.BaseResp `json:"BaseResp"`
}

func List(ctx context.Context, uid int64, req *httpapi.ListTimelineHTTPRequest) (*ListResponse, error) {
	if req.GetSessionID() == 0 {
		return nil, common.InvalidArgument("session_id is required")
	}
	logs.CtxInfo(ctx,
		"[aic_agent_sdk_api] list timeline start: uid=%d dialog_stream_id=%d thread_id=%d turn_id=%q cursor=%q limit=%d backward=%t",
		uid, req.GetSessionID(), req.GetThreadID(), req.GetTurnID(), stringValue(req.Cursor), int32Value(req.Limit), req.GetBackward(),
	)
	if _, err := session.RequireView(ctx, uid, req.GetSessionID(), false); err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] list timeline load session failed: uid=%d dialog_stream_id=%d err=%v", uid, req.GetSessionID(), err)
		return nil, err
	}
	cursor := stringValue(req.Cursor)
	agentAPI, err := deps.AgentAPI()
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] list timeline create cloudagent api failed: uid=%d dialog_stream_id=%d err=%v", uid, req.GetSessionID(), err)
		return nil, common.Downstream("create cloudagent api", err)
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] list timeline call cloudagent.ListTimeline: uid=%d dialog_stream_id=%d", uid, req.GetSessionID())
	result, err := agentAPI.ListTimeline(ctx, cloudapi.ListTimelineRequest{
		SessionID: strconv.FormatInt(req.GetSessionID(), 10),
		ThreadID:  int64String(req.GetThreadID()),
		TurnID:    strings.TrimSpace(req.GetTurnID()),
		Cursor:    cursor,
		Limit:     int32Value(req.Limit),
		Backward:  req.GetBackward(),
	})
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] list timeline cloudagent.ListTimeline failed: uid=%d dialog_stream_id=%d err=%v", uid, req.GetSessionID(), err)
		return nil, common.Downstream("cloudagent.ListTimeline", err)
	}
	logs.CtxInfo(ctx,
		"[aic_agent_sdk_api] list timeline success: uid=%d dialog_stream_id=%d event_count=%d next_cursor=%q has_more=%t",
		uid, req.GetSessionID(), len(result.Events), result.NextCursor, result.HasMore,
	)
	return &ListResponse{
		Events: result.Events,
		PageInfo: &httpcommon.PageInfo{
			NextCursor: stringPtrIfNotEmpty(result.NextCursor),
			HasMore:    result.HasMore,
		},
		BaseResp: common.BaseRespOK(),
	}, nil
}

func Subscribe(ctx context.Context, uid int64, req *httpapi.SubscribeTimelineHTTPRequest, w io.Writer) error {
	if req.GetSessionID() == 0 {
		return common.InvalidArgument("session_id is required")
	}
	startedAt := time.Now()
	logs.CtxInfo(ctx,
		"[aic_agent_sdk_api] subscribe timeline start: uid=%d dialog_stream_id=%d thread_id=%s turn_id=%q recover_queue_id=%q",
		uid, req.GetSessionID(), int64PtrString(req.ThreadID), stringPtrValue(req.TurnID), stringPtrValue(req.RecoverQueueID),
	)
	if _, err := session.RequireView(ctx, uid, req.GetSessionID(), false); err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] subscribe timeline load session failed: uid=%d dialog_stream_id=%d err=%v", uid, req.GetSessionID(), err)
		return err
	}
	agentAPI, err := deps.AgentAPI()
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] subscribe timeline create cloudagent api failed: uid=%d dialog_stream_id=%d err=%v", uid, req.GetSessionID(), err)
		return common.Downstream("create cloudagent api", err)
	}
	frameCount := 0
	err = agentAPI.SubscribeTimeline(ctx, cloudapi.SubscribeTimelineRequest{
		SessionID:      strconv.FormatInt(req.GetSessionID(), 10),
		ThreadID:       int64PtrString(req.ThreadID),
		TurnID:         stringPtrValue(req.TurnID),
		RecoverQueueID: stringPtrValue(req.RecoverQueueID),
	}, func(ctx context.Context, frame cloudapi.TimelineFrame) error {
		if frame.QueueID != "" {
			frameCount++
			logs.CtxInfo(ctx, "[aic_agent_sdk_api] subscribe timeline emit queue: uid=%d dialog_stream_id=%d queue_id=%s frame_count=%d",
				uid, req.GetSessionID(), frame.QueueID, frameCount)
			return writeSSE(w, "queue", &SubscribeResponse{QueueID: &frame.QueueID, BaseResp: common.BaseRespOK()})
		}
		if frame.Event != nil {
			frameCount++
			logs.CtxInfo(ctx,
				"[aic_agent_sdk_api] subscribe timeline emit event: uid=%d dialog_stream_id=%d event_id=%s event_type=%s thread_id=%s turn_id=%s created_at_ms=%d payload_bytes=%d frame_count=%d",
				uid, req.GetSessionID(), frame.Event.EventID, frame.Event.EventType, frame.Event.ThreadID, frame.Event.TurnID, frame.Event.CreatedAtMs, len(frame.Event.Payload), frameCount,
			)
			return writeSSE(w, "event", &SubscribeResponse{Event: frame.Event, BaseResp: common.BaseRespOK()})
		}
		return nil
	})
	if err != nil {
		if isExpectedStreamClose(ctx, err) {
			logs.CtxInfo(ctx,
				"[aic_agent_sdk_api] subscribe timeline closed: uid=%d dialog_stream_id=%d elapsed=%s frame_count=%d err=%v",
				uid, req.GetSessionID(), time.Since(startedAt), frameCount, err,
			)
			return nil
		}
		logs.CtxError(ctx,
			"[aic_agent_sdk_api] subscribe timeline failed: uid=%d dialog_stream_id=%d elapsed=%s frame_count=%d err=%v",
			uid, req.GetSessionID(), time.Since(startedAt), frameCount, err,
		)
		return err
	}
	logs.CtxInfo(ctx,
		"[aic_agent_sdk_api] subscribe timeline finished: uid=%d dialog_stream_id=%d elapsed=%s frame_count=%d",
		uid, req.GetSessionID(), time.Since(startedAt), frameCount,
	)
	return nil
}

func writeStreamError(w io.Writer, err error) {
	baseResp, _ := common.BaseRespFromError(err)
	_ = writeSSE(w, "error", &SubscribeResponse{BaseResp: baseResp})
}

func WriteStreamError(w io.Writer, err error) {
	writeStreamError(w, err)
}

func isExpectedStreamClose(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled") || strings.Contains(msg, "closed pipe")
}

func writeSSE(w io.Writer, eventName string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, data)
	return err
}

func int64String(value int64) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func int64PtrString(value *int64) string {
	if value == nil || *value == 0 {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func int32Value(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func stringPtrValue(value *string) string {
	return stringValue(value)
}

func stringPtrIfNotEmpty(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
