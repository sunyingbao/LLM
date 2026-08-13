//go:build !windows

package cloud

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"code.byted.org/gopkg/thrift"
	"code.byted.org/lang/gg/choose"
	"code.byted.org/lang/gg/gmap"
	"code.byted.org/lang/gg/gslice"
	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	acsvc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator/agentcoordinatorservice"
	coordinatorrpc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/rpc/ad_creative_aic_agent_coordinator"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/tasktool"
)

// CoordinatorTaskHost adapts Agent Coordinator RPCs to tasktool.TaskHost.
type CoordinatorTaskHost struct {
	// Namespace is the Agent Coordinator namespace used by all RPC requests.
	Namespace string
	// Env is written to CreateThreadRequest.Env when creating task threads.
	Env string
	// Client optionally overrides the default Agent Coordinator RawCall client.
	Client acsvc.Client
	// UserID is forwarded to CreateThreadRequest.UserId for created threads.
	UserID int64
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
	createReq := &ac.CreateThreadRequest{
		Namespace: h.Namespace,
		UserId:    userID,
		SessionId: choose.If[*string](req.SessionID == "", nil, thrift.StringPtr(req.SessionID)),
		Metadata:  metadata,
		Profile:   acThreadProfileFromTaskTool(profile),
	}
	if req.Title != "" {
		createReq.Title = thrift.StringPtr(req.Title)
	}
	if h.Env != "" {
		createReq.Env = thrift.StringPtr(h.Env)
	}
	if req.InitialMessage != nil {
		createReq.InitialMessage = &ac.InitialMessage{
			Sender: choose.If[*ac.Sender](req.ParentThreadID == "", nil, &ac.Sender{
				SenderType: ac.SenderType_AGENT,
				SenderId:   req.ParentThreadID,
			}),
			MessageType: taskToolMessageType(req.InitialMessage),
			Payload:     gslice.Clone(req.InitialMessage.Payload),
			Metadata:    gmap.Clone(req.InitialMessage.Metadata),
		}
	}
	createReq.GetOrSetBase()

	var resp *ac.CreateThreadResponse
	var err error
	if h.Client != nil {
		resp, err = h.Client.CreateThread(ctx, createReq)
	} else {
		resp, err = coordinatorrpc.RawCall.CreateThread(ctx, createReq)
	}
	if err := rpcError("CreateThread", resp, err); err != nil {
		return nil, err
	}
	if resp == nil || resp.GetThread() == nil {
		return nil, fmt.Errorf("CreateThread returned empty thread")
	}
	thread := resp.GetThread()
	outProfile := taskToolProfileFromAC(thread.GetProfile())
	if outProfile.Role == "" {
		outProfile.Role = profile.Role
	}
	if outProfile.Cwd == "" {
		outProfile.Cwd = profile.Cwd
	}
	out := &tasktool.Thread{
		ID:        taskToolInt64String(thread.GetThreadId()),
		UserID:    thread.GetUserId(),
		SessionID: thread.GetSessionId(),
		Title:     thread.GetTitle(),
		Profile:   outProfile,
		Metadata:  gmap.Clone(thread.GetMetadata()),
	}
	if out.Title == "" {
		out.Title = req.Title
	}
	if out.Metadata == nil {
		out.Metadata = gmap.Clone(metadata)
	}
	if resp.GetInitialMessage() != nil {
		out.InitialMessageID = taskToolInt64String(resp.GetInitialMessage().GetMessageId())
	}
	if req.InitialMessage != nil && out.InitialMessageID == "" {
		return nil, fmt.Errorf("CreateThread returned empty initial_message")
	}
	return out, nil
}

func (h CoordinatorTaskHost) SendMessage(ctx context.Context, req tasktool.SendMessageRequest) (*tasktool.Message, error) {
	targetThreadID, err := parseTaskToolInt64(req.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	if req.Message == nil {
		return nil, fmt.Errorf("message is required")
	}
	sendReq := &ac.SendMessageRequest{
		Namespace: h.Namespace,
		ThreadId:  targetThreadID,
		Sender: choose.If[*ac.Sender](req.FromThreadID == "", nil, &ac.Sender{
			SenderType: ac.SenderType_AGENT,
			SenderId:   req.FromThreadID,
		}),
		MessageType: taskToolMessageType(req.Message),
		Payload:     gslice.Clone(req.Message.Payload),
		Metadata:    gmap.Clone(req.Message.Metadata),
		WakeThread:  thrift.BoolPtr(true),
	}
	sendReq.GetOrSetBase()
	var resp *ac.SendMessageResponse
	if h.Client != nil {
		resp, err = h.Client.SendMessage(ctx, sendReq)
	} else {
		resp, err = coordinatorrpc.RawCall.SendMessage(ctx, sendReq)
	}
	if err := rpcError("SendMessage", resp, err); err != nil {
		return nil, err
	}
	if resp == nil || resp.GetMessage() == nil {
		return nil, fmt.Errorf("SendMessage returned empty message")
	}
	sent := resp.GetMessage()
	return &tasktool.Message{
		ID:       taskToolInt64String(sent.GetMessageId()),
		Payload:  gslice.Clone(sent.GetPayload()),
		Metadata: gmap.Clone(sent.GetMetadata()),
	}, nil
}

func taskToolMessageType(message *tasktool.Message) string {
	if message != nil && strings.TrimSpace(message.MessageType) != "" {
		return strings.TrimSpace(message.MessageType)
	}
	return string(agentworker.MessageTypeText)
}

func (h CoordinatorTaskHost) CloseThread(ctx context.Context, req tasktool.CloseThreadRequest) (*tasktool.ClosedThreadRsp, error) {
	targetThreadID, err := parseTaskToolInt64(req.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	closeReq := &ac.CloseThreadRequest{
		Namespace: h.Namespace,
		ThreadId:  targetThreadID,
	}
	if reason := strings.TrimSpace(req.Reason); reason != "" {
		closeReq.Reason = thrift.StringPtr(reason)
	}
	closeReq.GetOrSetBase()
	var resp *ac.CloseThreadResponse
	if h.Client != nil {
		resp, err = h.Client.CloseThread(ctx, closeReq)
	} else {
		resp, err = coordinatorrpc.RawCall.CloseThread(ctx, closeReq)
	}
	if err := rpcError("CloseThread", resp, err); err != nil {
		return nil, err
	}
	return &tasktool.ClosedThreadRsp{}, nil
}

func (h CoordinatorTaskHost) ListEvents(ctx context.Context, req tasktool.ListEventsRequest) ([]*tasktool.Event, error) {
	threadID, err := parseTaskToolInt64(req.ThreadID, "thread_id")
	if err != nil {
		return nil, err
	}
	limit := int32(req.Limit)
	if limit <= 0 {
		limit = defaultTaskToolListEventLimit
	}
	order := ac.EventListOrder_CREATED_AT_IN_THREAD_SEQ
	listReq := &ac.ListEventsRequest{
		Namespace: h.Namespace,
		ThreadId:  threadID,
		Limit:     thrift.Int32Ptr(limit),
		OrderBy:   &order,
	}
	if req.Reverse {
		direction := ac.EventListDirection_BACKWARD
		listReq.Direction = &direction
	}
	listReq.GetOrSetBase()
	var resp *ac.ListEventsResponse
	if h.Client != nil {
		resp, err = h.Client.ListEvents(ctx, listReq)
	} else {
		resp, err = coordinatorrpc.RawCall.ListEvents(ctx, listReq)
	}
	if err := rpcError("ListEvents", resp, err); err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("ListEvents returned empty response")
	}
	return taskToolEventsFromAC(resp.GetEvents()), nil
}

const defaultTaskToolListEventLimit = int32(100)

func taskToolEventsFromAC(events []*ac.Event) []*tasktool.Event {
	out := make([]*tasktool.Event, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		out = append(out, &tasktool.Event{
			ID:       taskToolInt64String(ev.GetEventId()),
			ThreadID: taskToolInt64String(ev.GetThreadId()),
			TurnID:   ev.GetTurnId(),
			Type:     ev.GetEventType(),
			Payload:  gslice.Clone(ev.GetPayload()),
			Metadata: gmap.Clone(ev.GetMetadata()),
			TS:       timeFromMs(ev.GetCreatedAtMs()),
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

func acThreadProfileFromTaskTool(profile tasktool.ThreadProfile) *ac.ThreadProfile {
	if profile.Empty() {
		return nil
	}
	return &ac.ThreadProfile{
		Role: profile.Role,
		Cwd:  profile.Cwd,
	}
}

func taskToolProfileFromAC(profile *ac.ThreadProfile) tasktool.ThreadProfile {
	if profile == nil {
		return tasktool.ThreadProfile{}
	}
	return tasktool.ThreadProfile{
		Role: profile.GetRole(),
		Cwd:  profile.GetCwd(),
	}
}

// ThreadProfileFromWire extracts a tasktool thread profile from an AC thread.
func ThreadProfileFromWire(thread *ac.Thread) tasktool.ThreadProfile {
	if thread == nil {
		return tasktool.ThreadProfile{}
	}
	return taskToolProfileFromAC(thread.GetProfile())
}

func taskToolInt64String(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func parseTaskToolInt64(value string, name string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("invalid %s %q", name, value)
	}
	return id, nil
}

func TurnIDFromMessageID(messageID int64) string {
	return fmt.Sprintf("turn_%d", messageID)
}

var _ tasktool.TaskHost = CoordinatorTaskHost{}
