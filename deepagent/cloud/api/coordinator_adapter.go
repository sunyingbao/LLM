//go:build !windows

package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/gopkg/thrift"
	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	acsvc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator/agentcoordinatorservice"
	acbase "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/base"
	"eino-cli/deepagent/cloud/protocol/timeline"
)

type CoordinatorAdapter struct {
	Namespace string
	Env       string
	Client    acsvc.Client
	Stream    acsvc.StreamClient
}

func (c CoordinatorAdapter) CreateThread(ctx context.Context, req CreateThreadRequest) (*CreateThreadResult, error) {
	if c.Client == nil {
		return nil, fmt.Errorf("agent coordinator client is required")
	}
	userID, err := parseOptionalInt64(req.UserID, "user_id")
	if err != nil {
		return nil, err
	}
	acReq := &ac.CreateThreadRequest{
		Namespace: c.Namespace,
		UserId:    userID,
		SessionId: stringPtrIfNotEmpty(req.SessionID),
		Title:     stringPtrIfNotEmpty(req.Title),
		Metadata:  cloneStringMap(req.Metadata),
		Profile:   acThreadProfile(req.Profile),
		Env:       stringPtrIfNotEmpty(c.Env),
		Base:      acbase.NewBase(),
	}
	if req.InitialMessage != nil {
		acReq.InitialMessage = &ac.InitialMessage{
			Sender:      initialMessageSender(req.UserID, req.ParentThreadID),
			MessageType: strings.TrimSpace(req.InitialMessage.MessageType),
			Payload:     append([]byte(nil), req.InitialMessage.Payload...),
			Metadata:    cloneStringMap(req.InitialMessage.Metadata),
		}
	}
	startedAt := time.Now()
	logs.CtxInfo(ctx,
		"[cloudagent_ac] rpc CreateThread start: namespace=%s env=%s user_id=%d dialog_stream_id=%s parent_thread_id=%s role=%s cwd=%q initial_message=%t initial_message_type=%s metadata_keys=%v",
		c.Namespace, c.Env, userID, req.SessionID, req.ParentThreadID, req.Profile.Role, req.Profile.Cwd, req.InitialMessage != nil, initialMessageType(req.InitialMessage), stringMapKeys(req.Metadata),
	)
	resp, err := c.Client.CreateThread(ctx, acReq)
	if err := rpcError("agent_coordinator.CreateThread", resp, err); err != nil {
		logs.CtxError(ctx,
			"[cloudagent_ac] rpc CreateThread failed: namespace=%s env=%s dialog_stream_id=%s elapsed=%s err=%v",
			c.Namespace, c.Env, req.SessionID, time.Since(startedAt), err,
		)
		return nil, err
	}
	out := &CreateThreadResult{Thread: threadFromAC(resp.GetThread())}
	if resp.GetInitialMessage() != nil && out.Thread != nil {
		out.Thread.InitialMessageID = strconv.FormatInt(resp.GetInitialMessage().GetMessageId(), 10)
	}
	logs.CtxInfo(ctx,
		"[cloudagent_ac] rpc CreateThread success: namespace=%s env=%s dialog_stream_id=%s thread_id=%s initial_message_id=%s elapsed=%s",
		c.Namespace, c.Env, req.SessionID, threadIDFromResult(out), initialMessageIDFromResult(out), time.Since(startedAt),
	)
	return out, nil
}

func (c CoordinatorAdapter) SendMessage(ctx context.Context, req SendMessageRequest) (*MessageRef, error) {
	threadID, err := parseRequiredInt64(req.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	wake := req.WakeThread
	startedAt := time.Now()
	logs.CtxInfo(ctx,
		"[cloudagent_ac] rpc SendMessage start: namespace=%s env=%s thread_id=%d user_id=%s message_type=%s wake_thread=%t payload_bytes=%d metadata_keys=%v",
		c.Namespace, c.Env, threadID, req.UserID, req.MessageType, wake, len(req.Payload), stringMapKeys(req.Metadata),
	)
	resp, err := c.Client.SendMessage(ctx, &ac.SendMessageRequest{
		Namespace:   c.Namespace,
		ThreadId:    threadID,
		Sender:      sender(req.UserID),
		MessageType: req.MessageType,
		Payload:     append([]byte(nil), req.Payload...),
		Metadata:    cloneStringMap(req.Metadata),
		WakeThread:  &wake,
		Base:        acbase.NewBase(),
	})
	if err := rpcError("agent_coordinator.SendMessage", resp, err); err != nil {
		logs.CtxError(ctx,
			"[cloudagent_ac] rpc SendMessage failed: namespace=%s env=%s thread_id=%d message_type=%s elapsed=%s err=%v",
			c.Namespace, c.Env, threadID, req.MessageType, time.Since(startedAt), err,
		)
		return nil, err
	}
	ref := messageRefFromAC(threadID, resp.GetMessage())
	logs.CtxInfo(ctx,
		"[cloudagent_ac] rpc SendMessage success: namespace=%s env=%s thread_id=%d message_type=%s message_thread_id=%s message_id=%s elapsed=%s",
		c.Namespace, c.Env, threadID, req.MessageType, ref.ThreadID, ref.MessageID, time.Since(startedAt),
	)
	return ref, nil
}

func (c CoordinatorAdapter) ResumeFromBlock(ctx context.Context, req ResumeFromBlockRequest) (*MessageRef, error) {
	threadID, err := parseRequiredInt64(req.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(req.Reason)
	startedAt := time.Now()
	logs.CtxInfo(ctx,
		"[cloudagent_ac] rpc ResumeFromBlock start: namespace=%s env=%s thread_id=%d user_id=%s reason=%q message_type=%s payload_bytes=%d metadata_keys=%v",
		c.Namespace, c.Env, threadID, req.UserID, reason, req.MessageType, len(req.Payload), stringMapKeys(req.Metadata),
	)
	resp, err := c.Client.ResumeFromBlock(ctx, &ac.ResumeFromBlockRequest{
		Namespace: c.Namespace,
		ThreadId:  threadID,
		Reason:    stringPtrIfNotEmpty(reason),
		ResumeMessage: &ac.InitialMessage{
			Sender:      sender(req.UserID),
			MessageType: req.MessageType,
			Payload:     append([]byte(nil), req.Payload...),
			Metadata:    cloneStringMap(req.Metadata),
		},
		Base: acbase.NewBase(),
	})
	if err := rpcError("agent_coordinator.ResumeFromBlock", resp, err); err != nil {
		logs.CtxError(ctx,
			"[cloudagent_ac] rpc ResumeFromBlock failed: namespace=%s env=%s thread_id=%d reason=%q elapsed=%s err=%v",
			c.Namespace, c.Env, threadID, reason, time.Since(startedAt), err,
		)
		return nil, err
	}
	ref := messageRefFromAC(threadID, resp.GetMessage())
	logs.CtxInfo(ctx,
		"[cloudagent_ac] rpc ResumeFromBlock success: namespace=%s env=%s thread_id=%d reason=%q message_thread_id=%s message_id=%s elapsed=%s",
		c.Namespace, c.Env, threadID, reason, ref.ThreadID, ref.MessageID, time.Since(startedAt),
	)
	return ref, nil
}

func (c CoordinatorAdapter) CancelInput(ctx context.Context, req CancelInputRequest) (*MessageRef, error) {
	threadID, err := parseRequiredInt64(req.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc CancelInput start: namespace=%s env=%s thread_id=%d reason=%q",
		c.Namespace, c.Env, threadID, req.Reason)
	resp, err := c.Client.CancelInput(ctx, &ac.CancelInputRequest{
		Namespace: c.Namespace,
		ThreadId:  threadID,
		Reason:    stringPtrIfNotEmpty(req.Reason),
		Base:      acbase.NewBase(),
	})
	if err := rpcError("agent_coordinator.CancelInput", resp, err); err != nil {
		logs.CtxError(ctx, "[cloudagent_ac] rpc CancelInput failed: namespace=%s env=%s thread_id=%d elapsed=%s err=%v",
			c.Namespace, c.Env, threadID, time.Since(startedAt), err)
		return nil, err
	}
	ref := messageRefFromAC(threadID, resp.GetControlMessage())
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc CancelInput success: namespace=%s env=%s thread_id=%d control_thread_id=%s control_message_id=%s elapsed=%s",
		c.Namespace, c.Env, threadID, ref.ThreadID, ref.MessageID, time.Since(startedAt))
	return ref, nil
}

func (c CoordinatorAdapter) ListSessionThreads(ctx context.Context, req ListSessionThreadsRequest) (*ListSessionThreadsResult, error) {
	cursor, err := parseOptionalInt64Ptr(req.Cursor, "cursor")
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	startedAt := time.Now()
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc ListSessionThreads start: namespace=%s env=%s dialog_stream_id=%s cursor=%q limit=%d",
		c.Namespace, c.Env, req.SessionID, req.Cursor, limit)
	resp, err := c.Client.ListSessionThreads(ctx, &ac.ListSessionThreadsRequest{
		Namespace: c.Namespace,
		SessionId: req.SessionID,
		Cursor:    cursor,
		Limit:     &limit,
		Base:      acbase.NewBase(),
	})
	if err := rpcError("agent_coordinator.ListSessionThreads", resp, err); err != nil {
		logs.CtxError(ctx, "[cloudagent_ac] rpc ListSessionThreads failed: namespace=%s env=%s dialog_stream_id=%s elapsed=%s err=%v",
			c.Namespace, c.Env, req.SessionID, time.Since(startedAt), err)
		return nil, err
	}
	threads := make([]*Thread, 0, len(resp.GetThreads()))
	for _, thread := range resp.GetThreads() {
		threads = append(threads, threadFromAC(thread))
	}
	out := &ListSessionThreadsResult{
		Threads:    threads,
		NextCursor: cursorString(resp.GetNextCursor(), resp.IsSetNextCursor()),
		HasMore:    resp.GetHasMore(),
	}
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc ListSessionThreads success: namespace=%s env=%s dialog_stream_id=%s thread_count=%d next_cursor=%q has_more=%t elapsed=%s",
		c.Namespace, c.Env, req.SessionID, len(out.Threads), out.NextCursor, out.HasMore, time.Since(startedAt))
	return out, nil
}

func (c CoordinatorAdapter) ListEvents(ctx context.Context, req ListEventsRequest) (*ListEventsResult, error) {
	threadID, err := parseRequiredInt64(req.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	direction := eventListDirection(req.Backward)
	order := ac.EventListOrder_CREATED_AT_IN_THREAD_SEQ
	startedAt := time.Now()
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc ListEvents start: namespace=%s env=%s thread_id=%d cursor=%q limit=%d backward=%t",
		c.Namespace, c.Env, threadID, req.Cursor, limit, req.Backward)
	resp, err := c.Client.ListEvents(ctx, &ac.ListEventsRequest{
		Namespace:  c.Namespace,
		ThreadId:   threadID,
		PageCursor: stringPtrIfNotEmpty(req.Cursor),
		Limit:      &limit,
		Direction:  &direction,
		OrderBy:    &order,
		Base:       acbase.NewBase(),
	})
	if err := rpcError("agent_coordinator.ListEvents", resp, err); err != nil {
		logs.CtxError(ctx, "[cloudagent_ac] rpc ListEvents failed: namespace=%s env=%s thread_id=%d elapsed=%s err=%v",
			c.Namespace, c.Env, threadID, time.Since(startedAt), err)
		return nil, err
	}
	out := listEventsResultFromAC(resp.GetEvents(), resp.GetNextPageCursor(), resp.IsSetNextPageCursor(), resp.GetHasMore())
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc ListEvents success: namespace=%s env=%s thread_id=%d event_count=%d next_cursor=%q has_more=%t elapsed=%s",
		c.Namespace, c.Env, threadID, len(out.Events), out.NextCursor, out.HasMore, time.Since(startedAt))
	return out, nil
}

func (c CoordinatorAdapter) ListSessionEvents(ctx context.Context, req ListSessionEventsRequest) (*ListEventsResult, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	limit := req.Limit
	direction := eventListDirection(req.Backward)
	startedAt := time.Now()
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc ListSessionEvents start: namespace=%s env=%s dialog_stream_id=%s cursor=%q limit=%d backward=%t",
		c.Namespace, c.Env, req.SessionID, req.Cursor, limit, req.Backward)
	resp, err := c.Client.ListSessionEvents(ctx, &ac.ListSessionEventsRequest{
		Namespace: c.Namespace,
		SessionId: req.SessionID,
		Cursor:    stringPtrIfNotEmpty(req.Cursor),
		Limit:     &limit,
		Direction: &direction,
		Base:      acbase.NewBase(),
	})
	if err := rpcError("agent_coordinator.ListSessionEvents", resp, err); err != nil {
		logs.CtxError(ctx, "[cloudagent_ac] rpc ListSessionEvents failed: namespace=%s env=%s dialog_stream_id=%s elapsed=%s err=%v",
			c.Namespace, c.Env, req.SessionID, time.Since(startedAt), err)
		return nil, err
	}
	out := listSessionEventsResultFromAC(resp)
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc ListSessionEvents success: namespace=%s env=%s dialog_stream_id=%s event_count=%d next_cursor=%q has_more=%t elapsed=%s",
		c.Namespace, c.Env, req.SessionID, len(out.Events), out.NextCursor, out.HasMore, time.Since(startedAt))
	return out, nil
}

func (c CoordinatorAdapter) ListTurnEvents(ctx context.Context, req ListTurnEventsRequest) (*ListEventsResult, error) {
	threadID, err := parseRequiredInt64(req.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	direction := eventListDirection(req.Backward)
	order := ac.EventListOrder_CREATED_AT_IN_THREAD_SEQ
	startedAt := time.Now()
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc ListTurnEvents start: namespace=%s env=%s thread_id=%d turn_id=%s cursor=%q limit=%d backward=%t",
		c.Namespace, c.Env, threadID, req.TurnID, req.Cursor, limit, req.Backward)
	resp, err := c.Client.ListTurnEvents(ctx, &ac.ListTurnEventsRequest{
		Namespace:  c.Namespace,
		ThreadId:   threadID,
		TurnId:     req.TurnID,
		PageCursor: stringPtrIfNotEmpty(req.Cursor),
		Limit:      &limit,
		Direction:  &direction,
		OrderBy:    &order,
		Base:       acbase.NewBase(),
	})
	if err := rpcError("agent_coordinator.ListTurnEvents", resp, err); err != nil {
		logs.CtxError(ctx, "[cloudagent_ac] rpc ListTurnEvents failed: namespace=%s env=%s thread_id=%d turn_id=%s elapsed=%s err=%v",
			c.Namespace, c.Env, threadID, req.TurnID, time.Since(startedAt), err)
		return nil, err
	}
	out := listEventsResultFromAC(resp.GetEvents(), resp.GetNextPageCursor(), resp.IsSetNextPageCursor(), resp.GetHasMore())
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc ListTurnEvents success: namespace=%s env=%s thread_id=%d turn_id=%s event_count=%d next_cursor=%q has_more=%t elapsed=%s",
		c.Namespace, c.Env, threadID, req.TurnID, len(out.Events), out.NextCursor, out.HasMore, time.Since(startedAt))
	return out, nil
}

func (c CoordinatorAdapter) SubscribeSession(ctx context.Context, req SubscribeSessionRequest) (TimelineStream, error) {
	if c.Stream == nil {
		return nil, fmt.Errorf("Agent Coordinator stream client is required")
	}
	startedAt := time.Now()
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc SubscribeSession start: namespace=%s env=%s dialog_stream_id=%s recover_queue_id=%s",
		c.Namespace, c.Env, req.SessionID, req.RecoverQueueID)
	stream, err := c.Stream.SubscribeSession(ctx, &ac.SubscribeSessionRequest{
		Namespace:      c.Namespace,
		SessionId:      req.SessionID,
		RecoverQueueId: stringPtrIfNotEmpty(req.RecoverQueueID),
		Base:           acbase.NewBase(),
	})
	if err != nil {
		logs.CtxError(ctx, "[cloudagent_ac] rpc SubscribeSession failed: namespace=%s env=%s dialog_stream_id=%s elapsed=%s err=%v",
			c.Namespace, c.Env, req.SessionID, time.Since(startedAt), err)
		return nil, fmt.Errorf("agent_coordinator.SubscribeSession: %w", err)
	}
	logs.CtxInfo(ctx, "[cloudagent_ac] rpc SubscribeSession opened: namespace=%s env=%s dialog_stream_id=%s recover_queue_id=%s elapsed=%s",
		c.Namespace, c.Env, req.SessionID, req.RecoverQueueID, time.Since(startedAt))
	return acTimelineStream{stream: stream}, nil
}

type acTimelineStream struct {
	stream acsvc.AgentCoordinatorService_SubscribeSessionClient
}

func (s acTimelineStream) Recv() (*TimelineFrame, error) {
	resp, err := s.stream.Recv()
	if err := rpcError("agent_coordinator.SubscribeSession", resp, err); err != nil {
		return nil, err
	}
	if resp.IsSetQueueId() {
		return &TimelineFrame{QueueID: resp.GetQueueId()}, nil
	}
	return &TimelineFrame{Event: timelineEventFromAC(resp.GetEvent())}, nil
}

func listEventsResultFromAC(events []*ac.Event, nextCursor string, hasCursor bool, hasMore bool) *ListEventsResult {
	out := make([]*timeline.Event, 0, len(events))
	for _, ev := range events {
		out = append(out, timelineEventFromAC(ev))
	}
	if !hasCursor {
		nextCursor = ""
	}
	return &ListEventsResult{
		Events:     out,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}
}

func listSessionEventsResultFromAC(resp *ac.ListSessionEventsResponse) *ListEventsResult {
	if resp == nil {
		return &ListEventsResult{}
	}
	out := make([]*timeline.Event, 0, len(resp.GetEvents()))
	for _, ev := range resp.GetEvents() {
		out = append(out, timelineEventFromAC(ev))
	}
	return &ListEventsResult{
		Events:     out,
		NextCursor: resp.GetNextCursor(),
		HasMore:    resp.GetHasMore(),
	}
}

func timelineEventFromAC(ev *ac.Event) *timeline.Event {
	if ev == nil {
		return &timeline.Event{Payload: timeline.NormalizePayload(nil)}
	}
	out := &timeline.Event{
		EventType:   ev.GetEventType(),
		ThreadID:    int64String(ev.GetThreadId()),
		TurnID:      ev.GetTurnId(),
		CreatedAtMs: ev.GetCreatedAtMs(),
		Payload:     timeline.NormalizePayload(ev.GetPayload()),
	}
	if ev.IsSetEventId() {
		out.EventID = int64String(ev.GetEventId())
	}
	return out
}

func threadFromAC(thread *ac.Thread) *Thread {
	if thread == nil {
		return nil
	}
	profile := ThreadProfile{}
	if thread.GetProfile() != nil {
		profile.Role = thread.GetProfile().GetRole()
		profile.Cwd = thread.GetProfile().GetCwd()
	}
	return &Thread{
		ID:             int64String(thread.GetThreadId()),
		UserID:         int64String(thread.GetUserId()),
		SessionID:      thread.GetSessionId(),
		ParentThreadID: thread.GetMetadata()[MetadataParentThreadID],
		Title:          thread.GetTitle(),
		Profile:        profile,
		Metadata:       cloneStringMap(thread.GetMetadata()),
		Status:         strings.TrimPrefix(fmt.Sprint(thread.GetStatus()), "ThreadStatus_"),
	}
}

func acThreadProfile(profile ThreadProfile) *ac.ThreadProfile {
	if strings.TrimSpace(profile.Role) == "" && strings.TrimSpace(profile.Cwd) == "" {
		return nil
	}
	return &ac.ThreadProfile{Role: strings.TrimSpace(profile.Role), Cwd: profile.Cwd}
}

func sender(userID string) *ac.Sender {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil
	}
	return &ac.Sender{SenderType: ac.SenderType_USER, SenderId: userID}
}

func initialMessageSender(userID string, parentThreadID string) *ac.Sender {
	parentThreadID = strings.TrimSpace(parentThreadID)
	if parentThreadID != "" {
		return &ac.Sender{SenderType: ac.SenderType_AGENT, SenderId: parentThreadID}
	}
	return sender(userID)
}

func messageRefFromAC(defaultThreadID int64, msg *ac.Message) *MessageRef {
	if msg == nil {
		return &MessageRef{ThreadID: int64String(defaultThreadID)}
	}
	return &MessageRef{ThreadID: int64String(msg.GetThreadId()), MessageID: int64String(msg.GetMessageId())}
}

func eventListDirection(backward bool) ac.EventListDirection {
	if backward {
		return ac.EventListDirection_BACKWARD
	}
	return ac.EventListDirection_FORWARD
}

func parseRequiredInt64(value string, name string) (int64, error) {
	parsed, err := parseOptionalInt64(value, name)
	if err != nil {
		return 0, err
	}
	if parsed == 0 {
		return 0, fmt.Errorf("%s is required", name)
	}
	return parsed, nil
}

func parseOptionalInt64(value string, name string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", name, value, err)
	}
	return parsed, nil
}

func parseOptionalInt64Ptr(value string, name string) (*int64, error) {
	parsed, err := parseOptionalInt64(value, name)
	if err != nil || parsed == 0 {
		return nil, err
	}
	return &parsed, nil
}

func stringPtrIfNotEmpty(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return thrift.StringPtr(value)
}

func cursorString(cursor int64, ok bool) string {
	if !ok {
		return ""
	}
	return int64String(cursor)
}

func int64String(value int64) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

type baseRespGetter interface {
	GetBaseResp() *acbase.BaseResp
}

func rpcError(op string, resp baseRespGetter, err error) error {
	if resp != nil {
		if baseResp := resp.GetBaseResp(); baseResp != nil && baseResp.GetStatusCode() != 0 {
			msg := fmt.Sprintf("%s status_code=%d status_message=%q", op, baseResp.GetStatusCode(), baseResp.GetStatusMessage())
			if err != nil {
				return fmt.Errorf("%s: %w", msg, err)
			}
			return fmt.Errorf("%s", msg)
		}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
