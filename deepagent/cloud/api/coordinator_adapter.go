package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"eino-cli/deepagent/cloud/protocol/timeline"
	"eino-cli/deepagent/coordinator"
)

type CoordinatorAdapter struct {
	Namespace   string
	Env         string
	Coordinator *coordinator.Coordinator
}

func (c CoordinatorAdapter) CreateThread(ctx context.Context, request CreateThreadRequest) (result *CreateThreadResult, err error) {
	if c.Coordinator == nil {
		return nil, fmt.Errorf("coordinator is required")
	}
	userID, err := parseOptionalInt64(request.UserID, "user_id")
	if err != nil {
		return nil, err
	}
	created, err := c.Coordinator.CreateThread(ctx, coordinator.CreateThreadRequest{
		Namespace: c.Namespace,
		Env:       c.Env,
		UserID:    userID,
		SessionID: request.SessionID,
		Title:     request.Title,
		Metadata:  cloneStringMap(request.Metadata),
		Profile:   coordinatorProfile(request.Profile),
		InitialMessage: coordinatorInitialMessage(
			request.UserID,
			request.ParentThreadID,
			request.InitialMessage,
		),
	})
	if err != nil {
		return nil, err
	}
	result = &CreateThreadResult{Thread: threadFromCoordinator(created.Thread)}
	if result.Thread != nil && created.InitialMessage != nil {
		result.Thread.InitialMessageID = int64String(created.InitialMessage.MessageID)
	}
	return result, nil
}

func (c CoordinatorAdapter) SendMessage(ctx context.Context, request SendMessageRequest) (reference *MessageRef, err error) {
	threadID, err := parseRequiredInt64(request.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	senderType, senderID := coordinatorSender(request.UserID, "")
	result, err := c.Coordinator.SubmitInput(ctx, coordinator.SubmitInputRequest{
		Namespace:   c.Namespace,
		ThreadID:    threadID,
		SenderType:  senderType,
		SenderID:    senderID,
		MessageType: request.MessageType,
		Payload:     append([]byte(nil), request.Payload...),
		Metadata:    cloneStringMap(request.Metadata),
		WakeThread:  request.WakeThread,
	})
	if err != nil {
		return nil, err
	}
	return messageReference(threadID, result.Message), nil
}

func (c CoordinatorAdapter) ResumeFromBlock(ctx context.Context, request ResumeFromBlockRequest) (reference *MessageRef, err error) {
	threadID, err := parseRequiredInt64(request.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	senderType, senderID := coordinatorSender(request.UserID, "")
	result, err := c.Coordinator.ResumeFromBlock(ctx, coordinator.ResumeFromBlockRequest{
		Namespace: c.Namespace,
		ThreadID:  threadID,
		Reason:    strings.TrimSpace(request.Reason),
		ResumeMessage: &coordinator.InitialMessage{
			SenderType:  senderType,
			SenderID:    senderID,
			MessageType: request.MessageType,
			Payload:     append([]byte(nil), request.Payload...),
			Metadata:    cloneStringMap(request.Metadata),
		},
	})
	if err != nil {
		return nil, err
	}
	return messageReference(threadID, result.Message), nil
}

func (c CoordinatorAdapter) CancelInput(ctx context.Context, request CancelInputRequest) (reference *MessageRef, err error) {
	threadID, err := parseRequiredInt64(request.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	result, err := c.Coordinator.RequestInputCancel(ctx, coordinator.RequestInputCancelRequest{
		Namespace: c.Namespace,
		ThreadID:  threadID,
		Reason:    request.Reason,
	})
	if err != nil {
		return nil, err
	}
	return messageReference(threadID, result.ControlMessage), nil
}

func (c CoordinatorAdapter) ListSessionThreads(ctx context.Context, request ListSessionThreadsRequest) (result *ListSessionThreadsResult, err error) {
	cursor, err := parseOptionalInt64(request.Cursor, "cursor")
	if err != nil {
		return nil, err
	}
	listed, err := c.Coordinator.ListSessionThreads(ctx, coordinator.ListSessionThreadsRequest{
		Namespace: c.Namespace,
		SessionID: request.SessionID,
		Cursor:    cursor,
		Limit:     request.Limit,
	})
	if err != nil {
		return nil, err
	}
	result = &ListSessionThreadsResult{
		Threads:    make([]*Thread, 0, len(listed.Threads)),
		NextCursor: int64String(listed.NextCursor),
		HasMore:    listed.HasMore,
	}
	for _, thread := range listed.Threads {
		result.Threads = append(result.Threads, threadFromCoordinator(thread))
	}
	return result, nil
}

func (c CoordinatorAdapter) ListSessionEvents(ctx context.Context, request ListSessionEventsRequest) (result *ListEventsResult, err error) {
	listed, err := c.Coordinator.ListSessionEvents(ctx, coordinator.ListSessionEventsRequest{
		Namespace: c.Namespace,
		SessionID: request.SessionID,
		Cursor:    request.Cursor,
		Limit:     request.Limit,
		Direction: coordinatorDirection(request.Backward),
	})
	if err != nil {
		return nil, err
	}
	return timelineResult(listed.Events, listed.NextCursor, listed.HasMore), nil
}

func (c CoordinatorAdapter) ListEvents(ctx context.Context, request ListEventsRequest) (result *ListEventsResult, err error) {
	return c.listEvents(ctx, request.ThreadID, "", request.Cursor, request.Limit, request.Backward)
}

func (c CoordinatorAdapter) ListTurnEvents(ctx context.Context, request ListTurnEventsRequest) (result *ListEventsResult, err error) {
	return c.listEvents(ctx, request.ThreadID, request.TurnID, request.Cursor, request.Limit, request.Backward)
}

func (c CoordinatorAdapter) listEvents(ctx context.Context, rawThreadID string, runID string, cursor string, limit int32, backward bool) (result *ListEventsResult, err error) {
	threadID, err := parseRequiredInt64(rawThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	listed, err := c.Coordinator.ListEvents(ctx, coordinator.ListEventsRequest{
		Namespace:  c.Namespace,
		ThreadID:   threadID,
		PageCursor: cursor,
		Limit:      limit,
		RunID:      runID,
		Direction:  coordinatorDirection(backward),
		Order:      coordinator.ListOrderCreatedAt,
	})
	if err != nil {
		return nil, err
	}
	return timelineResult(listed.Events, listed.NextPageCursor, listed.HasMore), nil
}

func (c CoordinatorAdapter) SubscribeSession(ctx context.Context, request SubscribeSessionRequest) (stream TimelineStream, err error) {
	subscription, err := c.Coordinator.SubscribeSession(ctx, coordinator.SubscribeSessionRequest{
		Namespace:      c.Namespace,
		SessionID:      request.SessionID,
		RecoverQueueID: request.RecoverQueueID,
	})
	if err != nil {
		return nil, err
	}
	return coordinatorTimelineStream{subscription: subscription}, nil
}

type coordinatorTimelineStream struct {
	subscription *coordinator.Subscription
}

func (s coordinatorTimelineStream) Recv() (frame *TimelineFrame, err error) {
	received, err := s.subscription.Recv()
	if err != nil {
		return nil, err
	}
	frame = &TimelineFrame{QueueID: received.QueueID}
	if received.Event != nil {
		frame.Event = timelineEvent(received.Event)
	}
	return frame, nil
}

func timelineResult(events []coordinator.Event, nextCursor string, hasMore bool) (result *ListEventsResult) {
	result = &ListEventsResult{
		Events:     make([]*timeline.Event, 0, len(events)),
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}
	for index := range events {
		result.Events = append(result.Events, timelineEvent(&events[index]))
	}
	return result
}

func timelineEvent(event *coordinator.Event) (result *timeline.Event) {
	if event == nil {
		return &timeline.Event{Payload: timeline.NormalizePayload(nil)}
	}
	return &timeline.Event{
		EventID:     int64String(event.EventID),
		EventType:   event.EventType,
		ThreadID:    int64String(event.ThreadID),
		TurnID:      event.TurnID,
		CreatedAtMs: event.CreatedAt.UnixMilli(),
		Payload:     timeline.NormalizePayload(event.Payload),
	}
}

func threadFromCoordinator(thread *coordinator.Thread) (result *Thread) {
	if thread == nil {
		return nil
	}
	metadata := cloneStringMap(thread.Metadata)
	profile := ThreadProfile{}
	if thread.Profile != nil {
		profile.Role = thread.Profile.Role
		profile.Cwd = thread.Profile.Cwd
	}
	return &Thread{
		ID:             int64String(thread.ThreadID),
		UserID:         int64String(thread.UserID),
		SessionID:      thread.SessionID,
		ParentThreadID: metadata[MetadataParentThreadID],
		Title:          thread.Title,
		Profile:        profile,
		Metadata:       metadata,
		Status:         string(thread.Status),
	}
}

func coordinatorProfile(profile ThreadProfile) (result *coordinator.Profile) {
	if strings.TrimSpace(profile.Role) == "" && strings.TrimSpace(profile.Cwd) == "" {
		return nil
	}
	return &coordinator.Profile{Role: strings.TrimSpace(profile.Role), Cwd: profile.Cwd}
}

func coordinatorInitialMessage(userID string, parentThreadID string, message *InitialMessage) (result *coordinator.InitialMessage) {
	if message == nil {
		return nil
	}
	senderType, senderID := coordinatorSender(userID, parentThreadID)
	return &coordinator.InitialMessage{
		SenderType:  senderType,
		SenderID:    senderID,
		MessageType: strings.TrimSpace(message.MessageType),
		Payload:     append([]byte(nil), message.Payload...),
		Metadata:    cloneStringMap(message.Metadata),
	}
}

func coordinatorSender(userID string, parentThreadID string) (senderType coordinator.SenderType, senderID string) {
	if parentThreadID = strings.TrimSpace(parentThreadID); parentThreadID != "" {
		return coordinator.SenderTypeAgent, parentThreadID
	}
	return coordinator.SenderTypeUser, strings.TrimSpace(userID)
}

func messageReference(defaultThreadID int64, message *coordinator.Message) (reference *MessageRef) {
	if message == nil {
		return &MessageRef{ThreadID: int64String(defaultThreadID)}
	}
	return &MessageRef{ThreadID: int64String(message.ThreadID), MessageID: int64String(message.MessageID)}
}

func coordinatorDirection(backward bool) (direction coordinator.ListDirection) {
	if backward {
		return coordinator.ListDirectionBackward
	}
	return coordinator.ListDirectionForward
}

func parseRequiredInt64(value string, name string) (parsed int64, err error) {
	parsed, err = parseOptionalInt64(value, name)
	if err != nil {
		return 0, err
	}
	if parsed == 0 {
		return 0, fmt.Errorf("%s is required", name)
	}
	return parsed, nil
}

func parseOptionalInt64(value string, name string) (parsed int64, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	parsed, err = strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", name, value, err)
	}
	return parsed, nil
}

func int64String(value int64) (result string) {
	if value == 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}
