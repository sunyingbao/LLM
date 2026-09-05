package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"code.byted.org/gopkg/logs/v2"
	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
)

const (
	defaultListLimit = int32(100)
	defaultMaxLimit  = int32(500)
)

func (a *AgentAPI) CreateThread(ctx context.Context, req CreateThreadRequest) (*CreateThreadResult, error) {
	if a == nil || a.Coordinator == nil {
		return nil, fmt.Errorf("cloudagent api coordinator is required")
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if parentThreadID := strings.TrimSpace(req.ParentThreadID); parentThreadID != "" {
		req.ParentThreadID = parentThreadID
		req.Metadata = cloneStringMap(req.Metadata)
		req.Metadata[MetadataParentThreadID] = parentThreadID
	}
	if req.InitialInput != nil {
		payload, err := inputPayload(*req.InitialInput)
		if err != nil {
			return nil, err
		}
		req.InitialInput = nil
		req.InitialMessage = &InitialMessage{
			MessageType: protoinput.MessageTypeInput,
			Payload:     payload,
		}
	}
	startedAt := time.Now()
	logs.CtxInfo(ctx,
		"[cloudagent_api] create thread start: namespace=%s env=%s user_id=%s dialog_stream_id=%s parent_thread_id=%s role=%s cwd=%q initial_message=%t initial_message_type=%s metadata_keys=%v",
		a.Namespace, a.Env, req.UserID, req.SessionID, req.ParentThreadID, req.Profile.Role, req.Profile.Cwd, req.InitialMessage != nil, initialMessageType(req.InitialMessage), stringMapKeys(req.Metadata),
	)
	result, err := a.Coordinator.CreateThread(ctx, req)
	if err != nil {
		logs.CtxError(ctx,
			"[cloudagent_api] create thread failed: namespace=%s env=%s dialog_stream_id=%s elapsed=%s err=%v",
			a.Namespace, a.Env, req.SessionID, time.Since(startedAt), err,
		)
		return nil, err
	}
	logs.CtxInfo(ctx,
		"[cloudagent_api] create thread success: namespace=%s env=%s dialog_stream_id=%s thread_id=%s initial_message_id=%s elapsed=%s",
		a.Namespace, a.Env, req.SessionID, threadIDFromResult(result), initialMessageIDFromResult(result), time.Since(startedAt),
	)
	return result, nil
}

func (a *AgentAPI) Submit(ctx context.Context, req SubmitRequest) (*SubmitResult, error) {
	if a == nil || a.Coordinator == nil {
		return nil, fmt.Errorf("cloudagent api coordinator is required")
	}
	if strings.TrimSpace(req.ThreadID) == "" {
		return nil, fmt.Errorf("thread_id is required")
	}
	kinds := 0
	if req.Input != nil {
		kinds++
	}
	if req.Resume != nil {
		kinds++
	}
	if req.Compact != nil {
		kinds++
	}
	if kinds != 1 {
		return nil, fmt.Errorf("submit requires exactly one of input, resume, compact")
	}

	startedAt := time.Now()
	kind := submitRequestKind(req)
	logs.CtxInfo(ctx,
		"[cloudagent_api] submit start: namespace=%s env=%s user_id=%s thread_id=%s submit_kind=%s metadata_keys=%v",
		a.Namespace, a.Env, req.UserID, req.ThreadID, kind, stringMapKeys(req.Metadata),
	)
	switch {
	case req.Input != nil:
		payload, err := inputPayload(*req.Input)
		if err != nil {
			return nil, err
		}
		logs.CtxInfo(ctx,
			"[cloudagent_api] submit call SendMessage: namespace=%s env=%s thread_id=%s message_type=%s payload_bytes=%d wake_thread=%t mode=%s",
			a.Namespace, a.Env, req.ThreadID, protoinput.MessageTypeInput, len(payload), true, req.Input.Mode,
		)
		ref, err := a.Coordinator.SendMessage(ctx, SendMessageRequest{
			UserID:      req.UserID,
			ThreadID:    req.ThreadID,
			MessageType: protoinput.MessageTypeInput,
			Payload:     payload,
			Metadata:    submitMetadata(req.Metadata, req.Input.Mode),
			WakeThread:  true,
		})
		return logSubmitResult(ctx, a, req, startedAt, kind, ref, err), err
	case req.Resume != nil:
		payload, err := resumePayload(*req.Resume)
		if err != nil {
			return nil, err
		}
		logs.CtxInfo(ctx,
			"[cloudagent_api] submit call ResumeFromBlock: namespace=%s env=%s thread_id=%s turn_id=%s checkpoint_id=%s interrupt_id=%s payload_bytes=%d",
			a.Namespace, a.Env, req.ThreadID, req.Resume.TurnID, req.Resume.CheckpointID, req.Resume.InterruptID, len(payload),
		)
		ref, err := a.Coordinator.ResumeFromBlock(ctx, ResumeFromBlockRequest{
			UserID:      req.UserID,
			ThreadID:    req.ThreadID,
			Reason:      "user_resume",
			MessageType: protoinput.MessageTypeResume,
			Payload:     payload,
			Metadata:    cloneStringMap(req.Metadata),
		})
		return logSubmitResult(ctx, a, req, startedAt, kind, ref, err), err
	case req.Compact != nil:
		logs.CtxInfo(ctx,
			"[cloudagent_api] submit call SendMessage: namespace=%s env=%s thread_id=%s message_type=%s payload_bytes=%d wake_thread=%t",
			a.Namespace, a.Env, req.ThreadID, protoinput.MessageTypeCompact, len([]byte("{}")), true,
		)
		ref, err := a.Coordinator.SendMessage(ctx, SendMessageRequest{
			UserID:      req.UserID,
			ThreadID:    req.ThreadID,
			MessageType: protoinput.MessageTypeCompact,
			Payload:     []byte("{}"),
			Metadata:    cloneStringMap(req.Metadata),
			WakeThread:  true,
		})
		return logSubmitResult(ctx, a, req, startedAt, kind, ref, err), err
	default:
		return nil, fmt.Errorf("unreachable submit branch")
	}
}

func (a *AgentAPI) ListTimeline(ctx context.Context, req ListTimelineRequest) (*ListTimelineResult, error) {
	if a == nil || a.Coordinator == nil {
		return nil, fmt.Errorf("cloudagent api coordinator is required")
	}
	limit := a.normalizeLimit(req.Limit)
	startedAt := time.Now()
	logs.CtxInfo(ctx,
		"[cloudagent_api] list timeline start: namespace=%s env=%s dialog_stream_id=%s thread_id=%s turn_id=%s cursor=%q limit=%d backward=%t",
		a.Namespace, a.Env, req.SessionID, req.ThreadID, req.TurnID, req.Cursor, limit, req.Backward,
	)
	if strings.TrimSpace(req.ThreadID) != "" && strings.TrimSpace(req.TurnID) != "" {
		result, err := a.Coordinator.ListTurnEvents(ctx, ListTurnEventsRequest{
			ThreadID: req.ThreadID,
			TurnID:   req.TurnID,
			Cursor:   req.Cursor,
			Limit:    limit,
			Backward: req.Backward,
		})
		out := listTimelineResult(result, req.SessionID)
		logListTimelineResult(ctx, a, req, startedAt, "turn", out, err)
		return out, err
	}
	if strings.TrimSpace(req.ThreadID) != "" {
		result, err := a.Coordinator.ListEvents(ctx, ListEventsRequest{
			ThreadID: req.ThreadID,
			Cursor:   req.Cursor,
			Limit:    limit,
			Backward: req.Backward,
		})
		out := listTimelineResult(result, req.SessionID)
		logListTimelineResult(ctx, a, req, startedAt, "thread", out, err)
		return out, err
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("session_id or thread_id is required")
	}
	result, err := a.Coordinator.ListSessionEvents(ctx, ListSessionEventsRequest{
		SessionID: req.SessionID,
		Cursor:    req.Cursor,
		Limit:     limit,
		Backward:  req.Backward,
	})
	out := listTimelineResult(result, req.SessionID)
	logListTimelineResult(ctx, a, req, startedAt, "session", out, err)
	return out, err
}

func (a *AgentAPI) SubscribeTimeline(ctx context.Context, req SubscribeTimelineRequest, emit func(context.Context, TimelineFrame) error) error {
	if a == nil || a.Coordinator == nil {
		return fmt.Errorf("cloudagent api coordinator is required")
	}
	if emit == nil {
		return fmt.Errorf("emit is required")
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return fmt.Errorf("session_id is required")
	}
	startedAt := time.Now()
	logs.CtxInfo(ctx,
		"[cloudagent_api] subscribe timeline start: namespace=%s env=%s dialog_stream_id=%s thread_id=%s turn_id=%s recover_queue_id=%s",
		a.Namespace, a.Env, req.SessionID, req.ThreadID, req.TurnID, req.RecoverQueueID,
	)
	recoverQueueID := req.RecoverQueueID
	recvCount := 0
	emittedCount := 0
	for {
		logs.CtxInfo(ctx,
			"[cloudagent_api] subscribe timeline call SubscribeSession: namespace=%s env=%s dialog_stream_id=%s recover_queue_id=%s",
			a.Namespace, a.Env, req.SessionID, recoverQueueID,
		)
		stream, err := a.Coordinator.SubscribeSession(ctx, SubscribeSessionRequest{
			SessionID:      req.SessionID,
			RecoverQueueID: recoverQueueID,
		})
		if err != nil {
			if recoverQueueID != "" && isRecoverExpired(err) {
				logs.CtxInfo(ctx,
					"[cloudagent_api] subscribe timeline recover expired, retry without recover_queue_id: namespace=%s env=%s dialog_stream_id=%s err=%v",
					a.Namespace, a.Env, req.SessionID, err,
				)
				recoverQueueID = ""
				continue
			}
			if isExpectedSubscribeClose(ctx, err) {
				logs.CtxInfo(ctx,
					"[cloudagent_api] subscribe timeline SubscribeSession closed: namespace=%s env=%s dialog_stream_id=%s elapsed=%s recv_count=%d emitted_count=%d err=%v",
					a.Namespace, a.Env, req.SessionID, time.Since(startedAt), recvCount, emittedCount, err,
				)
				return err
			}
			logs.CtxError(ctx,
				"[cloudagent_api] subscribe timeline SubscribeSession failed: namespace=%s env=%s dialog_stream_id=%s elapsed=%s recv_count=%d emitted_count=%d err=%v",
				a.Namespace, a.Env, req.SessionID, time.Since(startedAt), recvCount, emittedCount, err,
			)
			return err
		}
		for {
			frame, err := stream.Recv()
			if err != nil {
				if recoverQueueID != "" && isRecoverExpired(err) {
					logs.CtxInfo(ctx,
						"[cloudagent_api] subscribe timeline recv recover expired, retry without recover_queue_id: namespace=%s env=%s dialog_stream_id=%s err=%v",
						a.Namespace, a.Env, req.SessionID, err,
					)
					recoverQueueID = ""
					break
				}
				if isExpectedSubscribeClose(ctx, err) {
					logs.CtxInfo(ctx,
						"[cloudagent_api] subscribe timeline recv closed: namespace=%s env=%s dialog_stream_id=%s elapsed=%s recv_count=%d emitted_count=%d err=%v",
						a.Namespace, a.Env, req.SessionID, time.Since(startedAt), recvCount, emittedCount, err,
					)
					return err
				}
				logs.CtxError(ctx,
					"[cloudagent_api] subscribe timeline recv failed: namespace=%s env=%s dialog_stream_id=%s elapsed=%s recv_count=%d emitted_count=%d err=%v",
					a.Namespace, a.Env, req.SessionID, time.Since(startedAt), recvCount, emittedCount, err,
				)
				return err
			}
			if frame == nil {
				continue
			}
			recvCount++
			if frame.QueueID != "" {
				recoverQueueID = frame.QueueID
				logs.CtxInfo(ctx,
					"[cloudagent_api] subscribe timeline recv queue: namespace=%s env=%s dialog_stream_id=%s queue_id=%s recv_count=%d emitted_count=%d",
					a.Namespace, a.Env, req.SessionID, frame.QueueID, recvCount, emittedCount,
				)
				if err := emit(ctx, *frame); err != nil {
					logs.CtxError(ctx,
						"[cloudagent_api] subscribe timeline emit queue failed: namespace=%s env=%s dialog_stream_id=%s queue_id=%s err=%v",
						a.Namespace, a.Env, req.SessionID, frame.QueueID, err,
					)
					return err
				}
				emittedCount++
				continue
			}
			if !matchesTimelineFilter(frame.Event, req.ThreadID, req.TurnID) {
				logs.CtxInfo(ctx,
					"[cloudagent_api] subscribe timeline skip filtered event: namespace=%s env=%s dialog_stream_id=%s event=%s filter_thread_id=%s filter_turn_id=%s",
					a.Namespace, a.Env, req.SessionID, timelineEventSummary(frame.Event), req.ThreadID, req.TurnID,
				)
				continue
			}
			fillTimelineSession([]*timeline.Event{frame.Event}, req.SessionID)
			logs.CtxInfo(ctx,
				"[cloudagent_api] subscribe timeline recv event: namespace=%s env=%s dialog_stream_id=%s event=%s recv_count=%d emitted_count=%d",
				a.Namespace, a.Env, req.SessionID, timelineEventSummary(frame.Event), recvCount, emittedCount,
			)
			if err := emit(ctx, *frame); err != nil {
				logs.CtxError(ctx,
					"[cloudagent_api] subscribe timeline emit event failed: namespace=%s env=%s dialog_stream_id=%s event=%s err=%v",
					a.Namespace, a.Env, req.SessionID, timelineEventSummary(frame.Event), err,
				)
				return err
			}
			emittedCount++
		}
	}
}

func (a *AgentAPI) StopRunning(ctx context.Context, req StopRunningRequest) (*StopRunningResult, error) {
	if a == nil || a.Coordinator == nil {
		return nil, fmt.Errorf("cloudagent api coordinator is required")
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "user_cancel"
	}
	startedAt := time.Now()
	logs.CtxInfo(ctx,
		"[cloudagent_api] stop running start: namespace=%s env=%s user_id=%s dialog_stream_id=%s explicit_thread_ids=%v reason=%q",
		a.Namespace, a.Env, req.UserID, req.SessionID, req.ThreadIDs, reason,
	)
	targets, err := a.stopTargets(ctx, req)
	if err != nil {
		logs.CtxError(ctx,
			"[cloudagent_api] stop running resolve targets failed: namespace=%s env=%s dialog_stream_id=%s elapsed=%s err=%v",
			a.Namespace, a.Env, req.SessionID, time.Since(startedAt), err,
		)
		return nil, err
	}
	logs.CtxInfo(ctx,
		"[cloudagent_api] stop running targets resolved: namespace=%s env=%s dialog_stream_id=%s targets=%v",
		a.Namespace, a.Env, req.SessionID, threadIDs(targets),
	)
	out := &StopRunningResult{ControlMessages: make([]MessageRef, 0, len(targets))}
	for _, thread := range targets {
		if thread == nil || thread.ID == "" {
			continue
		}
		logs.CtxInfo(ctx, "[cloudagent_api] stop running call CancelInput: namespace=%s env=%s thread_id=%s reason=%q", a.Namespace, a.Env, thread.ID, reason)
		ref, err := a.Coordinator.CancelInput(ctx, CancelInputRequest{ThreadID: thread.ID, Reason: reason})
		if err != nil {
			if !isThreadBlocked(err) {
				logs.CtxError(ctx, "[cloudagent_api] stop running CancelInput failed: namespace=%s env=%s thread_id=%s err=%v", a.Namespace, a.Env, thread.ID, err)
				return nil, err
			}
			logs.CtxInfo(ctx, "[cloudagent_api] stop running thread blocked, reject latest approval: namespace=%s env=%s thread_id=%s reason=%q", a.Namespace, a.Env, thread.ID, reason)
			ref, err = a.rejectLatestApproval(ctx, req, thread.ID, reason)
			if err != nil {
				logs.CtxError(ctx, "[cloudagent_api] stop running reject latest approval failed: namespace=%s env=%s thread_id=%s err=%v", a.Namespace, a.Env, thread.ID, err)
				return nil, err
			}
		}
		if ref != nil {
			out.ControlMessages = append(out.ControlMessages, *ref)
			logs.CtxInfo(ctx, "[cloudagent_api] stop running control message accepted: namespace=%s env=%s thread_id=%s message_id=%s",
				a.Namespace, a.Env, ref.ThreadID, ref.MessageID)
		}
	}
	logs.CtxInfo(ctx,
		"[cloudagent_api] stop running success: namespace=%s env=%s dialog_stream_id=%s elapsed=%s control_message_count=%d",
		a.Namespace, a.Env, req.SessionID, time.Since(startedAt), len(out.ControlMessages),
	)
	return out, nil
}

func inputPayload(input SubmitInput) ([]byte, error) {
	msg := protoinput.UserMessage{Mode: input.Mode, Parts: input.Parts, Extra: cloneRawJSONMap(input.Extra)}
	if err := msg.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

func resumePayload(input ResumeInput) ([]byte, error) {
	payload := protoinput.ResumeTurnPayload{
		TurnID:             input.TurnID,
		CheckpointID:       input.CheckpointID,
		InterruptID:        input.InterruptID,
		ToolName:           input.ToolName,
		ArgumentsInJSON:    input.ArgumentsInJSON,
		ConsumedMessageIDs: input.ConsumedMessageIDs,
		Approval:           input.Approval,
		RequestUserInput:   input.RequestUserInput,
		Interrupt:          input.Interrupt,
	}
	if err := payload.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func submitMetadata(metadata map[string]string, mode protoinput.UserMessageMode) map[string]string {
	out := cloneStringMap(metadata)
	if mode == protoinput.UserMessageModeImplPlan {
		out[protoinput.MetadataTurnMode] = protoinput.TurnModePlan
	}
	return out
}

func submitResult(ref *MessageRef) *SubmitResult {
	if ref == nil {
		return &SubmitResult{}
	}
	return &SubmitResult{Message: *ref}
}

func logSubmitResult(ctx context.Context, a *AgentAPI, req SubmitRequest, startedAt time.Time, kind string, ref *MessageRef, err error) *SubmitResult {
	if err != nil {
		logs.CtxError(ctx,
			"[cloudagent_api] submit failed: namespace=%s env=%s thread_id=%s submit_kind=%s elapsed=%s err=%v",
			a.Namespace, a.Env, req.ThreadID, kind, time.Since(startedAt), err,
		)
		return submitResult(ref)
	}
	logs.CtxInfo(ctx,
		"[cloudagent_api] submit success: namespace=%s env=%s thread_id=%s submit_kind=%s message_thread_id=%s message_id=%s elapsed=%s",
		a.Namespace, a.Env, req.ThreadID, kind, messageRefThreadID(ref), messageRefID(ref), time.Since(startedAt),
	)
	return submitResult(ref)
}

func submitRequestKind(req SubmitRequest) string {
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

func messageRefThreadID(ref *MessageRef) string {
	if ref == nil {
		return ""
	}
	return ref.ThreadID
}

func messageRefID(ref *MessageRef) string {
	if ref == nil {
		return ""
	}
	return ref.MessageID
}

func initialMessageType(message *InitialMessage) string {
	if message == nil {
		return ""
	}
	return message.MessageType
}

func threadIDFromResult(result *CreateThreadResult) string {
	if result == nil || result.Thread == nil {
		return ""
	}
	return result.Thread.ID
}

func initialMessageIDFromResult(result *CreateThreadResult) string {
	if result == nil || result.Thread == nil {
		return ""
	}
	return result.Thread.InitialMessageID
}

func stringMapKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func listTimelineResult(result *ListEventsResult, sessionID string) *ListTimelineResult {
	if result == nil {
		return &ListTimelineResult{}
	}
	fillTimelineSession(result.Events, sessionID)
	return &ListTimelineResult{
		Events:     result.Events,
		NextCursor: result.NextCursor,
		HasMore:    result.HasMore,
	}
}

func logListTimelineResult(ctx context.Context, a *AgentAPI, req ListTimelineRequest, startedAt time.Time, scope string, result *ListTimelineResult, err error) {
	if err != nil {
		logs.CtxError(ctx,
			"[cloudagent_api] list timeline failed: namespace=%s env=%s scope=%s dialog_stream_id=%s thread_id=%s turn_id=%s elapsed=%s err=%v",
			a.Namespace, a.Env, scope, req.SessionID, req.ThreadID, req.TurnID, time.Since(startedAt), err,
		)
		return
	}
	logs.CtxInfo(ctx,
		"[cloudagent_api] list timeline success: namespace=%s env=%s scope=%s dialog_stream_id=%s thread_id=%s turn_id=%s event_count=%d next_cursor=%q has_more=%t elapsed=%s",
		a.Namespace, a.Env, scope, req.SessionID, req.ThreadID, req.TurnID, len(result.Events), result.NextCursor, result.HasMore, time.Since(startedAt),
	)
}

func timelineEventSummary(event *timeline.Event) string {
	if event == nil {
		return "{}"
	}
	data, err := json.Marshal(map[string]interface{}{
		"event_id":      event.EventID,
		"event_type":    event.EventType,
		"thread_id":     event.ThreadID,
		"turn_id":       event.TurnID,
		"created_at_ms": event.CreatedAtMs,
		"payload_bytes": len(event.Payload),
	})
	if err != nil {
		return fmt.Sprintf("event_id=%s event_type=%s thread_id=%s turn_id=%s payload_bytes=%d summary_error=%v",
			event.EventID, event.EventType, event.ThreadID, event.TurnID, len(event.Payload), err)
	}
	return string(data)
}

func threadIDs(threads []*Thread) []string {
	out := make([]string, 0, len(threads))
	for _, thread := range threads {
		if thread != nil && thread.ID != "" {
			out = append(out, thread.ID)
		}
	}
	return out
}

func fillTimelineSession(events []*timeline.Event, sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	for _, event := range events {
		if event != nil && event.SessionID == "" {
			event.SessionID = sessionID
		}
	}
}

func (a *AgentAPI) listAllSessionThreads(ctx context.Context, sessionID string) ([]*Thread, error) {
	var cursor string
	threads := make([]*Thread, 0)
	for {
		result, err := a.Coordinator.ListSessionThreads(ctx, ListSessionThreadsRequest{
			SessionID: sessionID,
			Cursor:    cursor,
			Limit:     a.normalizeLimit(100),
		})
		if err != nil {
			return nil, err
		}
		if result == nil {
			return threads, nil
		}
		threads = append(threads, result.Threads...)
		if !result.HasMore {
			return threads, nil
		}
		cursor = result.NextCursor
		if cursor == "" {
			return threads, nil
		}
	}
}

func (a *AgentAPI) stopTargets(ctx context.Context, req StopRunningRequest) ([]*Thread, error) {
	if len(req.ThreadIDs) > 0 {
		out := make([]*Thread, 0, len(req.ThreadIDs))
		for _, threadID := range req.ThreadIDs {
			threadID = strings.TrimSpace(threadID)
			if threadID != "" {
				out = append(out, &Thread{ID: threadID})
			}
		}
		return out, nil
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("session_id or thread_ids is required")
	}
	threads, err := a.listAllSessionThreads(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	return pickCancelableThreads(threads), nil
}

func pickCancelableThreads(threads []*Thread) []*Thread {
	running := make([]*Thread, 0)
	blocked := make([]*Thread, 0)
	for _, thread := range threads {
		if thread == nil {
			continue
		}
		switch strings.ToUpper(thread.Status) {
		case "RUNNING":
			running = append(running, thread)
		case "BLOCKED":
			blocked = append(blocked, thread)
		}
	}
	if len(running) > 0 {
		return running
	}
	return blocked
}

func (a *AgentAPI) rejectLatestApproval(ctx context.Context, req StopRunningRequest, threadID string, reason string) (*MessageRef, error) {
	result, err := a.Coordinator.ListEvents(ctx, ListEventsRequest{
		ThreadID: threadID,
		Limit:    a.normalizeLimit(100),
		Backward: true,
	})
	if err != nil {
		return nil, err
	}
	event, payload, err := latestApprovalRequired(result)
	if err != nil {
		return nil, err
	}
	if event == nil || payload == nil {
		return nil, fmt.Errorf("thread %s is blocked and has no pending approval request", threadID)
	}
	resumeBytes, err := resumePayload(ResumeInput{
		TurnID:             event.TurnID,
		CheckpointID:       payload.CheckpointID,
		InterruptID:        payload.InterruptID,
		ToolName:           payload.ToolName,
		ArgumentsInJSON:    stringPtrValue(payload.ArgumentsJSON),
		ConsumedMessageIDs: payload.ConsumedMessageIDs,
		Approval:           &protoinput.ApprovalDecision{Approved: false, Reason: reason, CancelTurn: true},
	})
	if err != nil {
		return nil, err
	}
	return a.Coordinator.ResumeFromBlock(ctx, ResumeFromBlockRequest{
		UserID:      req.UserID,
		ThreadID:    threadID,
		Reason:      "user_cancel",
		MessageType: protoinput.MessageTypeResume,
		Payload:     resumeBytes,
	})
}

func latestApprovalRequired(result *ListEventsResult) (*timeline.Event, *protoevent.ApprovalRequiredEventPayload, error) {
	if result == nil {
		return nil, nil, nil
	}
	var latestEvent *timeline.Event
	var latestPayload *protoevent.ApprovalRequiredEventPayload
	for _, event := range result.Events {
		if event == nil || event.EventType != protoevent.EventTypeApprovalRequired.String() {
			continue
		}
		var payload protoevent.ApprovalRequiredEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, nil, fmt.Errorf("decode approval_required payload: %w", err)
		}
		if latestEvent == nil || timelineEventAfter(event, latestEvent) {
			payloadCopy := payload
			latestEvent = event
			latestPayload = &payloadCopy
		}
	}
	return latestEvent, latestPayload, nil
}

func timelineEventAfter(left *timeline.Event, right *timeline.Event) bool {
	if right == nil {
		return true
	}
	if left == nil {
		return false
	}
	if left.CreatedAtMs != right.CreatedAtMs {
		return left.CreatedAtMs > right.CreatedAtMs
	}
	return left.EventID > right.EventID
}

func isThreadBlocked(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "thread blocked")
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func matchesTimelineFilter(event *timeline.Event, threadID string, turnID string) bool {
	if event == nil {
		return false
	}
	if strings.TrimSpace(threadID) != "" && event.ThreadID != strings.TrimSpace(threadID) {
		return false
	}
	if strings.TrimSpace(turnID) != "" && event.TurnID != strings.TrimSpace(turnID) {
		return false
	}
	return true
}

func isRecoverExpired(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "recover expired")
}

func isExpectedSubscribeClose(ctx context.Context, err error) (expected bool) {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "context canceled")
}

func (a *AgentAPI) normalizeLimit(limit int32) int32 {
	if limit <= 0 {
		limit = a.DefaultLimit
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	maxLimit := a.MaxLimit
	if maxLimit <= 0 {
		maxLimit = defaultMaxLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneRawJSONMap(in map[string]json.RawMessage) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		if v == nil {
			out[k] = nil
			continue
		}
		out[k] = append(json.RawMessage(nil), v...)
	}
	return out
}
