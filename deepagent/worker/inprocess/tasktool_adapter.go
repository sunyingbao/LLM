package inprocess

import (
	"context"

	"code.byted.org/lang/gg/gmap"
	"code.byted.org/lang/gg/gslice"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/tasktool"
)

// TaskToolHostAdapter adapts Worker to the generic tasktool.TaskHost.
type TaskToolHostAdapter struct {
	Worker *Worker
	// UserID is the default owner used when CreateThreadRequest does not carry
	// one and the request is not inheriting from a parent thread.
	UserID int64
}

func (a TaskToolHostAdapter) CreateThread(ctx context.Context, req tasktool.CreateThreadRequest) (*tasktool.Thread, error) {
	userID := req.UserID
	if userID == 0 {
		userID = a.UserID
	}
	thread, err := a.Worker.CreateThread(ctx, CreateThreadSpec{
		UserID:         userID,
		SessionID:      req.SessionID,
		ParentThreadID: req.ParentThreadID,
		Title:          req.Title,
		Profile:        req.Profile,
		Metadata:       req.Metadata,
	})
	if err != nil {
		return nil, err
	}
	out := &tasktool.Thread{
		ID:        thread.ID,
		UserID:    thread.UserID,
		SessionID: thread.SessionID,
		Title:     thread.Title,
		Profile:   thread.Profile,
		Metadata:  gmap.Clone(thread.Metadata),
	}
	if req.InitialMessage != nil {
		sent, err := a.SendMessage(ctx, tasktool.SendMessageRequest{
			ThreadID:     thread.ID,
			FromThreadID: req.ParentThreadID,
			Message:      req.InitialMessage,
		})
		if err != nil {
			return nil, err
		}
		out.InitialMessageID = req.InitialMessage.ID
		if sent != nil && sent.ID != "" {
			out.InitialMessageID = sent.ID
		}
	}
	return out, nil
}

func (a TaskToolHostAdapter) SendMessage(ctx context.Context, req tasktool.SendMessageRequest) (*tasktool.Message, error) {
	if req.Message == nil {
		return nil, ErrInvalidMessage
	}
	var sender *agentworker.Sender
	if req.FromThreadID != "" {
		sender = &agentworker.Sender{Type: agentworker.SenderTypeAgent, ID: req.FromThreadID}
	}
	messageType := agentworker.MessageType(req.Message.MessageType)
	if messageType == "" {
		messageType = agentworker.MessageTypeText
	}
	if err := a.Worker.PostMessage(ctx, req.ThreadID, &agentworker.Message{
		ID:       req.Message.ID,
		Sender:   sender,
		Type:     messageType,
		Payload:  gslice.Clone(req.Message.Payload),
		Metadata: gmap.Clone(req.Message.Metadata),
	}); err != nil {
		return nil, err
	}
	return req.Message, nil
}

func (a TaskToolHostAdapter) CloseThread(ctx context.Context, req tasktool.CloseThreadRequest) (*tasktool.ClosedThreadRsp, error) {
	if err := a.Worker.CloseThread(ctx, req.ThreadID); err != nil {
		return nil, err
	}
	return &tasktool.ClosedThreadRsp{}, nil
}

func (a TaskToolHostAdapter) ListEvents(ctx context.Context, req tasktool.ListEventsRequest) ([]*tasktool.Event, error) {
	events, err := a.Worker.ListEvents(ctx, req.ThreadID, ListEventsOptions{
		Limit:   req.Limit,
		Reverse: req.Reverse,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*tasktool.Event, 0, len(events))
	for _, ev := range events {
		out = append(out, &tasktool.Event{
			ID:       ev.ID,
			ThreadID: ev.ThreadID,
			TurnID:   ev.TurnID,
			Type:     string(ev.Type),
			Payload:  gslice.Clone(ev.Payload),
			Metadata: gmap.Clone(ev.Metadata),
			TS:       ev.TS,
		})
	}
	return out, nil
}

var _ tasktool.TaskHost = TaskToolHostAdapter{}
