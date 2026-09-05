//go:build !windows

package cloud

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"code.byted.org/lang/gg/gmap"
	"code.byted.org/lang/gg/gslice"
	"eino-cli/deepagent/coordinator"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/tasktool"
)

// CoordinatorTaskHost adapts Coordinator operations to tasktool.TaskHost.
type CoordinatorTaskHost struct {
	// Namespace is used by all Coordinator requests.
	Namespace string
	// Env is written to CreateThreadRequest.Env when creating task threads.
	Env string
	// Client optionally overrides the process-wide Coordinator client.
	Client CoordinatorClient
	// UserID is forwarded to CreateThreadRequest.UserId for created threads.
	UserID int64
}

var fallbackCoordinatorClient CoordinatorClient

func (h CoordinatorTaskHost) coordinator() (client CoordinatorClient, err error) {
	client = h.Client
	if client == nil {
		client = fallbackCoordinatorClient
	}
	if client == nil {
		return nil, fmt.Errorf("coordinator is required")
	}
	return client, nil
}

func (h CoordinatorTaskHost) CreateThread(ctx context.Context, req tasktool.CreateThreadRequest) (*tasktool.Thread, error) {
	userID := h.UserID
	if req.UserID != 0 {
		if userID != 0 && userID != req.UserID {
			return nil, fmt.Errorf("user_id mismatch: host=%d request=%d", userID, req.UserID)
		}
		userID = req.UserID
	}
	metadata := gmap.Clone(req.Metadata)
	profile := taskToolProfileForCloud(req.Profile)
	createReq := coordinator.CreateThreadRequest{
		Namespace: h.Namespace,
		UserID:    userID,
		SessionID: req.SessionID,
		Metadata:  metadata,
		Profile:   coordinatorProfileFromTaskTool(profile),
		Title:     req.Title,
		Env:       h.Env,
	}
	if req.InitialMessage != nil {
		senderType := coordinator.SenderType("")
		if req.ParentThreadID != "" {
			senderType = coordinator.SenderTypeAgent
		}
		createReq.InitialMessage = &coordinator.InitialMessage{
			SenderType:  senderType,
			SenderID:    req.ParentThreadID,
			MessageType: taskToolMessageType(req.InitialMessage),
			Payload:     gslice.Clone(req.InitialMessage.Payload),
			Metadata:    gmap.Clone(req.InitialMessage.Metadata),
		}
	}

	client, err := h.coordinator()
	if err != nil {
		return nil, err
	}
	result, err := client.CreateThread(ctx, createReq)
	if err = coordinatorError("CreateThread", err); err != nil {
		return nil, err
	}
	if result.Thread == nil {
		return nil, fmt.Errorf("CreateThread returned empty thread")
	}
	thread := result.Thread
	outProfile := taskToolProfileFromCoordinator(thread.Profile)
	if outProfile.Role == "" {
		outProfile.Role = profile.Role
	}
	if outProfile.Cwd == "" {
		outProfile.Cwd = profile.Cwd
	}
	out := &tasktool.Thread{
		ID:        taskToolInt64String(thread.ThreadID),
		UserID:    thread.UserID,
		SessionID: thread.SessionID,
		Title:     thread.Title,
		Profile:   outProfile,
		Metadata:  gmap.Clone(thread.Metadata),
	}
	if out.Title == "" {
		out.Title = req.Title
	}
	if out.Metadata == nil {
		out.Metadata = gmap.Clone(metadata)
	}
	if result.InitialMessage != nil {
		out.InitialMessageID = taskToolInt64String(result.InitialMessage.MessageID)
	}
	if req.InitialMessage != nil && out.InitialMessageID == "" {
		return nil, fmt.Errorf("CreateThread returned empty initial_message")
	}
	return out, nil
}

func (h CoordinatorTaskHost) SendMessage(ctx context.Context, req tasktool.SendMessageRequest) (message *tasktool.Message, err error) {
	targetThreadID, err := parseInt64(req.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	if req.Message == nil {
		return nil, fmt.Errorf("message is required")
	}
	senderType := coordinator.SenderType("")
	if req.FromThreadID != "" {
		senderType = coordinator.SenderTypeAgent
	}
	sendReq := coordinator.SubmitInputRequest{
		Namespace:   h.Namespace,
		ThreadID:    targetThreadID,
		SenderType:  senderType,
		SenderID:    req.FromThreadID,
		MessageType: taskToolMessageType(req.Message),
		Payload:     gslice.Clone(req.Message.Payload),
		Metadata:    gmap.Clone(req.Message.Metadata),
		WakeThread:  true,
	}
	client, err := h.coordinator()
	if err != nil {
		return nil, err
	}
	result, err := client.SubmitInput(ctx, sendReq)
	if err = coordinatorError("SendMessage", err); err != nil {
		return nil, err
	}
	if result.Message == nil {
		return nil, fmt.Errorf("SendMessage returned empty message")
	}
	sent := result.Message
	return &tasktool.Message{
		ID:       taskToolInt64String(sent.MessageID),
		Payload:  gslice.Clone(sent.Payload),
		Metadata: gmap.Clone(sent.Metadata),
	}, nil
}

func taskToolMessageType(message *tasktool.Message) string {
	if message != nil && strings.TrimSpace(message.MessageType) != "" {
		return strings.TrimSpace(message.MessageType)
	}
	return string(agentworker.MessageTypeText)
}

func (h CoordinatorTaskHost) CloseThread(ctx context.Context, req tasktool.CloseThreadRequest) (closed *tasktool.ClosedThreadRsp, err error) {
	targetThreadID, err := parseInt64(req.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	closeReq := coordinator.RequestThreadCloseRequest{
		Namespace: h.Namespace,
		ThreadID:  targetThreadID,
		Reason:    strings.TrimSpace(req.Reason),
	}
	client, err := h.coordinator()
	if err != nil {
		return nil, err
	}
	_, err = client.RequestThreadClose(ctx, closeReq)
	if err = coordinatorError("CloseThread", err); err != nil {
		return nil, err
	}
	return &tasktool.ClosedThreadRsp{}, nil
}

func (h CoordinatorTaskHost) ListEvents(ctx context.Context, req tasktool.ListEventsRequest) (events []*tasktool.Event, err error) {
	threadID, err := parseInt64(req.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	limit := int32(req.Limit)
	if limit <= 0 {
		limit = defaultTaskToolListEventLimit
	}
	listReq := coordinator.ListEventsRequest{
		Namespace: h.Namespace,
		ThreadID:  threadID,
		Limit:     limit,
		Order:     coordinator.ListOrderCreatedAt,
	}
	if req.Reverse {
		listReq.Direction = coordinator.ListDirectionBackward
	}
	client, err := h.coordinator()
	if err != nil {
		return nil, err
	}
	result, err := client.ListEvents(ctx, listReq)
	if err = coordinatorError("ListEvents", err); err != nil {
		return nil, err
	}
	return taskToolEventsFromCoordinator(result.Events), nil
}

const defaultTaskToolListEventLimit = int32(100)

func taskToolEventsFromCoordinator(events []coordinator.Event) (out []*tasktool.Event) {
	out = make([]*tasktool.Event, 0, len(events))
	for _, event := range events {
		out = append(out, &tasktool.Event{
			ID:       taskToolInt64String(event.EventID),
			ThreadID: taskToolInt64String(event.ThreadID),
			TurnID:   event.TurnID,
			Type:     event.EventType,
			Payload:  gslice.Clone(event.Payload),
			Metadata: gmap.Clone(event.Metadata),
			TS:       event.CreatedAt,
		})
	}
	return out
}

func taskToolProfileForCloud(profile tasktool.ThreadProfile) tasktool.ThreadProfile {
	return tasktool.ThreadProfile{
		Role: strings.TrimSpace(profile.Role),
		Cwd:  profile.Cwd,
	}
}

func coordinatorProfileFromTaskTool(profile tasktool.ThreadProfile) (result *coordinator.Profile) {
	if profile.Empty() {
		return nil
	}
	return &coordinator.Profile{
		Role: profile.Role,
		Cwd:  profile.Cwd,
	}
}

func taskToolProfileFromCoordinator(profile *coordinator.Profile) (result tasktool.ThreadProfile) {
	if profile == nil {
		return tasktool.ThreadProfile{}
	}
	return tasktool.ThreadProfile{
		Role: profile.Role,
		Cwd:  profile.Cwd,
	}
}

func ThreadProfileFromCoordinator(thread *coordinator.Thread) (result tasktool.ThreadProfile) {
	if thread == nil {
		return tasktool.ThreadProfile{}
	}
	return taskToolProfileFromCoordinator(thread.Profile)
}

func taskToolInt64String(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func TurnIDFromMessageID(messageID int64) string {
	return fmt.Sprintf("turn_%d", messageID)
}

var _ tasktool.TaskHost = CoordinatorTaskHost{}
