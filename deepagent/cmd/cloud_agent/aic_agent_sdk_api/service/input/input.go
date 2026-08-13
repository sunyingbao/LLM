package input

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"code.byted.org/gopkg/logs"
	"code.byted.org/kite/kitutil"
	"code.byted.org/kite/kitutil/logid"
	cloudapi "eino-cli/deepagent/cloud/api"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	httpapi "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_api"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_common"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/common"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/deps"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/session"
)

const (
	threadRoleMain     = "main"
	metadataThreadRole = "thread_role"
	metadataProject    = "project_name"
)

const inputModeCompactContext = httpcommon.InputMode(2)

type resumeInterruptContextKey struct{}

type ResumeInterruptInput struct {
	Kind     string          `json:"kind,omitempty"`
	InfoType string          `json:"info_type,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

func ContextWithResumeInterrupt(ctx context.Context, input *ResumeInterruptInput) context.Context {
	if input == nil {
		return ctx
	}
	return context.WithValue(ctx, resumeInterruptContextKey{}, input)
}

func resumeInterruptFromContext(ctx context.Context) *ResumeInterruptInput {
	input, _ := ctx.Value(resumeInterruptContextKey{}).(*ResumeInterruptInput)
	return input
}

func Submit(ctx context.Context, uid int64, req *httpapi.SubmitInputHTTPRequest) (*httpapi.SubmitInputHTTPResponse, error) {
	if req.GetSessionID() == 0 {
		return nil, common.InvalidArgument("session_id is required")
	}
	resuming := req.IsSetResumeRef()
	compactMode := req.IsSetMode() && req.GetMode() == inputModeCompactContext
	if req.IsSetMode() {
		switch req.GetMode() {
		case httpcommon.InputMode_IMPL_PLAN, inputModeCompactContext:
		default:
			return nil, common.InvalidArgument("unsupported input mode")
		}
	}
	if compactMode && resuming {
		return nil, common.InvalidArgument("compact mode cannot resume a blocked turn")
	}
	if compactMode && strings.TrimSpace(req.GetContent()) != "" {
		return nil, common.InvalidArgument("compact mode does not accept content")
	}
	if !resuming && !compactMode && strings.TrimSpace(req.GetContent()) == "" {
		return nil, common.InvalidArgument("content is required")
	}
	if resuming && req.GetThreadID() == 0 {
		return nil, common.InvalidArgument("thread_id is required when resume_ref is set")
	}

	ctx, requestLogID := ensureRequestLogID(ctx)
	logs.CtxInfo(ctx,
		"[aic_agent_sdk_api] submit input start: uid=%d dialog_stream_id=%d requested_thread_id=%d mode_set=%t mode=%d resume=%t compact=%t content_bytes=%d approval=%t logid=%s",
		uid, req.GetSessionID(), req.GetThreadID(), req.IsSetMode(), req.GetMode(), resuming, compactMode, len(req.GetContent()), req.GetApproval() != nil, requestLogID,
	)
	view, err := session.RequireView(ctx, uid, req.GetSessionID(), true)
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] submit input load session failed: uid=%d dialog_stream_id=%d err=%v", uid, req.GetSessionID(), err)
		return nil, err
	}
	logs.CtxInfo(ctx,
		"[aic_agent_sdk_api] submit input session loaded: uid=%d dialog_stream_id=%d main_thread_id=%d project_name=%q project_path=%q",
		uid, req.GetSessionID(), mainThreadID(view), projectName(view), projectPath(view),
	)
	threadID, err := resolveThread(ctx, uid, view, req, !compactMode)
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] submit input resolve thread failed: uid=%d dialog_stream_id=%d requested_thread_id=%d err=%v",
			uid, req.GetSessionID(), req.GetThreadID(), err)
		return nil, err
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] submit input thread resolved: uid=%d dialog_stream_id=%d thread_id=%d", uid, req.GetSessionID(), threadID)

	agentAPI, err := deps.AgentAPI()
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] submit input create cloudagent api failed: uid=%d dialog_stream_id=%d thread_id=%d err=%v",
			uid, req.GetSessionID(), threadID, err)
		return nil, common.Downstream("create agent_coordinator client", err)
	}
	metadata := buildMessageMetadata(requestLogID, req)

	var submitReq cloudapi.SubmitRequest
	switch {
	case compactMode:
		submitReq = cloudapi.SubmitRequest{UserID: strconv.FormatInt(uid, 10), ThreadID: strconv.FormatInt(threadID, 10), Compact: &cloudapi.CompactInput{}, Metadata: metadata}
	case resuming:
		resume, err := resumeInput(ctx, req)
		if err != nil {
			return nil, err
		}
		submitReq = cloudapi.SubmitRequest{UserID: strconv.FormatInt(uid, 10), ThreadID: strconv.FormatInt(threadID, 10), Resume: resume, Metadata: metadata}
	default:
		input, err := userInput(req)
		if err != nil {
			return nil, err
		}
		submitReq = cloudapi.SubmitRequest{UserID: strconv.FormatInt(uid, 10), ThreadID: strconv.FormatInt(threadID, 10), Input: input, Metadata: metadata}
	}
	logs.CtxInfo(ctx,
		"[aic_agent_sdk_api] submit input call cloudagent.Submit: uid=%d dialog_stream_id=%d thread_id=%d submit_kind=%s metadata=%v",
		uid, req.GetSessionID(), threadID, submitKind(submitReq), metadata,
	)
	submitted, err := agentAPI.Submit(ctx, submitReq)
	if err != nil {
		logs.CtxError(ctx,
			"[aic_agent_sdk_api] submit input cloudagent.Submit failed: uid=%d dialog_stream_id=%d thread_id=%d submit_kind=%s err=%v",
			uid, req.GetSessionID(), threadID, submitKind(submitReq), err,
		)
		return nil, common.Downstream("cloudagent.Submit", err)
	}
	logs.CtxInfo(ctx,
		"[aic_agent_sdk_api] submit input cloudagent.Submit success: uid=%d dialog_stream_id=%d thread_id=%d message_thread_id=%s message_id=%s",
		uid, req.GetSessionID(), threadID, submitted.Message.ThreadID, submitted.Message.MessageID,
	)

	messageRef := messageRefFromAPI(threadID, submitted.Message)
	latestView := view
	if !resuming && !compactMode && strings.TrimSpace(req.GetContent()) != "" {
		touched, err := session.Touch(ctx, uid, req.GetSessionID(), preview(req.GetContent()), titleFromContent(req.GetContent()), time.Now().UnixMilli())
		if err == nil && touched != nil {
			latestView = touched
			logs.CtxInfo(ctx, "[aic_agent_sdk_api] submit input session touched: uid=%d dialog_stream_id=%d message_id=%s",
				uid, req.GetSessionID(), messageRef.GetMessageID())
		} else if err != nil {
			logs.CtxError(ctx, "[aic_agent_sdk_api] submit input session touch failed after submit success: uid=%d dialog_stream_id=%d message_id=%s err=%v",
				uid, req.GetSessionID(), messageRef.GetMessageID(), err)
		}
	}
	logs.CtxInfo(ctx,
		"[aic_agent_sdk_api] submit input success: uid=%d dialog_stream_id=%d thread_id=%d message_id=%s",
		uid, req.GetSessionID(), messageRef.GetThreadID(), messageRef.GetMessageID(),
	)
	return &httpapi.SubmitInputHTTPResponse{
		Message:     messageRef,
		SessionView: latestView,
		BaseResp:    common.BaseRespOK(),
	}, nil
}

func ensureRequestLogID(ctx context.Context) (context.Context, string) {
	if logID, ok := kitutil.GetCtxLogID(ctx); ok && strings.TrimSpace(logID) != "" {
		return ctx, logID
	}
	logID := logid.GetNginxID()
	return kitutil.NewCtxWithLogID(ctx, logID), logID
}

func buildMessageMetadata(logID string, req *httpapi.SubmitInputHTTPRequest) map[string]string {
	metadata := map[string]string{protoinput.MetadataLogID: logID}
	if req.IsSetMode() && req.GetMode() == httpcommon.InputMode_IMPL_PLAN {
		metadata[protoinput.MetadataTurnMode] = protoinput.TurnModePlan
	}
	return metadata
}

func userInput(req *httpapi.SubmitInputHTTPRequest) (*cloudapi.SubmitInput, error) {
	mode := protoinput.UserMessageMode("")
	if req.IsSetMode() && req.GetMode() == httpcommon.InputMode_IMPL_PLAN {
		mode = protoinput.UserMessageModeImplPlan
	}
	input := &cloudapi.SubmitInput{
		Mode: mode,
		Parts: []protoinput.MessagePart{{
			Type: protoinput.MessagePartTypeText,
			Text: strings.TrimSpace(req.GetContent()),
		}},
	}
	msg := protoinput.UserMessage{Mode: input.Mode, Parts: input.Parts}
	if err := msg.Validate(); err != nil {
		return nil, common.InvalidArgument(err.Error())
	}
	return input, nil
}

func resolveThread(ctx context.Context, uid int64, view *httpcommon.AgentSessionView, req *httpapi.SubmitInputHTTPRequest, createIfMissing bool) (int64, error) {
	if req.GetThreadID() != 0 {
		logs.CtxInfo(ctx, "[aic_agent_sdk_api] resolve thread use request thread: uid=%d dialog_stream_id=%d thread_id=%d", uid, req.GetSessionID(), req.GetThreadID())
		return req.GetThreadID(), nil
	}
	if view != nil && view.Session != nil && view.Session.IsSetMainThreadID() && view.Session.GetMainThreadID() != 0 {
		logs.CtxInfo(ctx, "[aic_agent_sdk_api] resolve thread use session main thread: uid=%d dialog_stream_id=%d thread_id=%d",
			uid, req.GetSessionID(), view.Session.GetMainThreadID())
		return view.Session.GetMainThreadID(), nil
	}
	if !createIfMissing {
		logs.CtxInfo(ctx, "[aic_agent_sdk_api] resolve thread missing main thread and create disabled: uid=%d dialog_stream_id=%d", uid, req.GetSessionID())
		return 0, common.InvalidArgument("thread_id is required when session has no main thread")
	}

	agentAPI, err := deps.AgentAPI()
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] resolve thread create cloudagent api failed: uid=%d dialog_stream_id=%d err=%v", uid, req.GetSessionID(), err)
		return 0, common.Downstream("create agent_coordinator client", err)
	}
	sessionID := strconv.FormatInt(req.GetSessionID(), 10)
	title := titleFromContent(req.GetContent())
	cwd := strings.TrimSpace(view.GetSession().GetProjectPath())
	if cwd == "" {
		return 0, common.InvalidArgument("session project_path is required")
	}
	projectName := strings.TrimSpace(view.GetSession().GetProjectName())
	logs.CtxInfo(ctx,
		"[aic_agent_sdk_api] resolve thread create main thread start: uid=%d dialog_stream_id=%s project_name=%q cwd=%q title_bytes=%d",
		uid, sessionID, projectName, cwd, len(title),
	)
	created, err := agentAPI.CreateThread(ctx, cloudapi.CreateThreadRequest{
		UserID:    strconv.FormatInt(uid, 10),
		SessionID: sessionID,
		Title:     title,
		Metadata: map[string]string{
			metadataThreadRole: threadRoleMain,
			metadataProject:    projectName,
		},
		Profile: cloudapi.ThreadProfile{
			Role: threadRoleMain,
			Cwd:  cwd,
		},
	})
	if err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] resolve thread cloudagent.CreateThread failed: uid=%d dialog_stream_id=%s project_name=%q cwd=%q err=%v",
			uid, sessionID, projectName, cwd, err)
		return 0, common.Downstream("cloudagent.CreateThread", err)
	}
	thread := created.Thread
	if thread == nil || thread.ID == "" {
		logs.CtxError(ctx, "[aic_agent_sdk_api] resolve thread cloudagent.CreateThread returned empty thread: uid=%d dialog_stream_id=%s", uid, sessionID)
		return 0, common.Internal("agent_coordinator.CreateThread returned empty thread", nil)
	}
	threadID, err := strconv.ParseInt(thread.ID, 10, 64)
	if err != nil || threadID == 0 {
		logs.CtxError(ctx, "[aic_agent_sdk_api] resolve thread cloudagent.CreateThread returned invalid thread: uid=%d dialog_stream_id=%s thread_id=%q err=%v",
			uid, sessionID, thread.ID, err)
		return 0, common.Internal("agent_coordinator.CreateThread returned invalid thread", err)
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] resolve thread create main thread success: uid=%d dialog_stream_id=%s thread_id=%d initial_message_id=%s",
		uid, sessionID, threadID, thread.InitialMessageID)
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] resolve thread bind main thread start: uid=%d dialog_stream_id=%s thread_id=%d", uid, sessionID, threadID)
	if err := session.BindMainThread(ctx, uid, req.GetSessionID(), threadID); err != nil {
		logs.CtxError(ctx, "[aic_agent_sdk_api] resolve thread bind main thread failed: uid=%d dialog_stream_id=%s thread_id=%d err=%v",
			uid, sessionID, threadID, err)
		return 0, err
	}
	logs.CtxInfo(ctx, "[aic_agent_sdk_api] resolve thread bind main thread success: uid=%d dialog_stream_id=%s thread_id=%d", uid, sessionID, threadID)
	return threadID, nil
}

func resumeInput(ctx context.Context, req *httpapi.SubmitInputHTTPRequest) (*cloudapi.ResumeInput, error) {
	ref := req.GetResumeRef()
	if ref == nil || ref.GetTurnID() == "" || ref.GetCheckpointID() == "" || ref.GetInterruptID() == "" {
		return nil, common.InvalidArgument("resume_ref requires turn_id, checkpoint_id and interrupt_id")
	}
	payload := &cloudapi.ResumeInput{
		TurnID:       ref.GetTurnID(),
		CheckpointID: ref.GetCheckpointID(),
		InterruptID:  ref.GetInterruptID(),
	}
	if approval := req.GetApproval(); approval != nil {
		payload.Approval = &protoinput.ApprovalDecision{
			Approved:       approval.GetApproved(),
			Reason:         approval.GetReason(),
			AllowInSession: approval.GetAllowInSession(),
		}
		payload.ToolName = approval.GetToolName()
		payload.ArgumentsInJSON = approval.GetArgumentsJSON()
	} else if interrupt := resumeInterruptFromContext(ctx); interrupt != nil {
		payload.Interrupt = &protoinput.InterruptResumePayload{
			Kind:     interrupt.Kind,
			InfoType: interrupt.InfoType,
			Data:     append([]byte(nil), interrupt.Data...),
		}
	} else if strings.TrimSpace(req.GetContent()) != "" {
		payload.RequestUserInput = &protoinput.RequestUserInputResponse{
			Answers: map[string]protoinput.RequestUserInputAnswer{
				protoinput.DefaultTextAnswerID: {Answers: []string{strings.TrimSpace(req.GetContent())}},
			},
		}
	} else {
		return nil, common.InvalidArgument("resume input requires approval, interrupt or content")
	}
	check := protoinput.ResumeTurnPayload{
		TurnID:           payload.TurnID,
		CheckpointID:     payload.CheckpointID,
		InterruptID:      payload.InterruptID,
		ToolName:         payload.ToolName,
		ArgumentsInJSON:  payload.ArgumentsInJSON,
		Approval:         payload.Approval,
		RequestUserInput: payload.RequestUserInput,
		Interrupt:        payload.Interrupt,
	}
	if err := check.Validate(); err != nil {
		return nil, common.InvalidArgument(err.Error())
	}
	return payload, nil
}

func messageRefFromAPI(threadID int64, message cloudapi.MessageRef) *httpcommon.MessageRef {
	if message.MessageID == "" {
		return &httpcommon.MessageRef{ThreadID: threadID}
	}
	gotThreadID, err := strconv.ParseInt(message.ThreadID, 10, 64)
	if err != nil || gotThreadID == 0 {
		gotThreadID = threadID
	}
	return &httpcommon.MessageRef{
		ThreadID:  gotThreadID,
		MessageID: message.MessageID,
	}
}

func submitKind(req cloudapi.SubmitRequest) string {
	switch {
	case req.Input != nil:
		return "input"
	case req.Resume != nil:
		return "resume"
	case req.Compact != nil:
		return "compact"
	default:
		return "unknown"
	}
}

func mainThreadID(view *httpcommon.AgentSessionView) int64 {
	if view == nil || view.Session == nil {
		return 0
	}
	return view.Session.GetMainThreadID()
}

func projectName(view *httpcommon.AgentSessionView) string {
	if view == nil || view.Session == nil {
		return ""
	}
	return view.Session.GetProjectName()
}

func projectPath(view *httpcommon.AgentSessionView) string {
	if view == nil || view.Session == nil {
		return ""
	}
	return view.Session.GetProjectPath()
}

func titleFromContent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	return preview(content)
}

func preview(content string) string {
	content = strings.TrimSpace(content)
	if len([]rune(content)) <= 80 {
		return content
	}
	return string([]rune(content)[:80])
}
