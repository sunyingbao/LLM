package local

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
	"eino-cli/deepagent/definition"
	runtimeclient "eino-cli/deepagent/runtime"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/inprocess"
	"github.com/google/uuid"
)

const (
	metadataDefinitionName    = "runtime.definition.name"
	metadataDefinitionVersion = "runtime.definition.version"
)

type Options struct {
	UserID           int64
	SubscriberBuffer int
}

type Client struct {
	worker  *inprocess.Worker
	options Options
}

func New(worker *inprocess.Worker, options Options) (client *Client, err error) {
	if worker == nil {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "local.new", Runtime: runtimeclient.RuntimeLocal, Message: "worker is required"}
	}
	if options.UserID == 0 {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "local.new", Runtime: runtimeclient.RuntimeLocal, Message: "user id is required"}
	}
	client = &Client{worker: worker, options: options}
	return client, nil
}

func (client *Client) CreateThread(ctx context.Context, req runtimeclient.CreateThreadRequest) (result *runtimeclient.CreateThreadResult, err error) {
	if req.Runtime != runtimeclient.RuntimeLocal {
		return nil, invalidRuntime("create_thread", req.Runtime)
	}
	if err = agentdefinition.Validate(req.Definition); err != nil {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "create_thread", Runtime: runtimeclient.RuntimeLocal, Cause: err}
	}
	parentID := ""
	if req.ParentRef != nil {
		if req.ParentRef.Runtime != runtimeclient.RuntimeLocal {
			return nil, invalidRuntime("create_thread", req.ParentRef.Runtime)
		}
		parentID = req.ParentRef.ThreadID
	}
	state, err := client.worker.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID: client.options.UserID, SessionID: req.Namespace, ParentThreadID: parentID,
		Title: req.Title, Profile: inprocess.ThreadProfile{Cwd: req.Workspace.Cwd},
		Metadata: map[string]string{metadataDefinitionName: req.Definition.Name, metadataDefinitionVersion: req.Definition.Version},
	})
	if err != nil {
		return nil, wrapError("create_thread", err)
	}
	result = &runtimeclient.CreateThreadResult{Thread: threadFromState(state)}
	return result, nil
}

func (client *Client) Submit(ctx context.Context, req runtimeclient.SubmitRequest) (result *runtimeclient.SubmitResult, err error) {
	if err = validateLocalRef(req.Ref, "submit"); err != nil {
		return nil, err
	}
	if err = req.Input.Validate(); err != nil {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "submit", Runtime: runtimeclient.RuntimeLocal, Cause: err}
	}
	payload, err := json.Marshal(req.Input)
	if err != nil {
		return nil, wrapError("submit", err)
	}
	postResult, err := client.worker.PostMessageWithResult(ctx, req.Ref.ThreadID, &agentworker.Message{
		ID: uuid.NewString(), Sender: &agentworker.Sender{Type: agentworker.SenderTypeUser},
		Type: agentworker.MessageType(protoinput.MessageTypeInput), Payload: payload, Metadata: req.Metadata,
	})
	if err != nil {
		return nil, wrapError("submit", err)
	}
	if postResult == nil || strings.TrimSpace(postResult.TurnID) == "" {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInternal, Op: "submit", Runtime: runtimeclient.RuntimeLocal, Message: "runtime returned no turn id"}
	}
	return &runtimeclient.SubmitResult{Ref: req.Ref, TurnID: postResult.TurnID}, nil
}

func (client *Client) Resume(ctx context.Context, req runtimeclient.ResumeRequest) (result *runtimeclient.SubmitResult, err error) {
	if err = validateLocalRef(req.Ref, "resume"); err != nil {
		return nil, err
	}
	if err = req.Payload.Validate(); err != nil {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "resume", Runtime: runtimeclient.RuntimeLocal, Cause: err}
	}
	state, err := client.worker.GetThread(ctx, req.Ref.ThreadID)
	if err != nil {
		return nil, wrapError("resume", err)
	}
	if state.PendingBlock == nil || state.PendingBlock.TurnID != req.Payload.TurnID || state.PendingBlock.CheckpointID != req.Payload.CheckpointID || state.PendingBlock.InterruptID != req.Payload.InterruptID {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeConflict, Op: "resume", Runtime: runtimeclient.RuntimeLocal, Message: "pending block identity changed"}
	}
	payload, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, wrapError("resume", err)
	}
	if err = client.worker.ResumeFromBlock(ctx, inprocess.ResumeFromBlockRequest{ThreadID: req.Ref.ThreadID, InterruptID: req.Payload.InterruptID}); err != nil {
		return nil, wrapError("resume", err)
	}
	postResult, err := client.worker.PostMessageWithResult(ctx, req.Ref.ThreadID, &agentworker.Message{ID: uuid.NewString(), Sender: &agentworker.Sender{Type: agentworker.SenderTypeUser}, Type: agentworker.MessageType(protoinput.MessageTypeResume), Payload: payload})
	if err != nil {
		return nil, wrapError("resume", err)
	}
	turnID := req.Payload.TurnID
	if postResult != nil && postResult.TurnID != "" {
		turnID = postResult.TurnID
	}
	return &runtimeclient.SubmitResult{Ref: req.Ref, TurnID: turnID}, nil
}

func (client *Client) Stop(ctx context.Context, req runtimeclient.StopRequest) (result *runtimeclient.StopResult, err error) {
	if err = validateLocalRef(req.Ref, "stop"); err != nil {
		return nil, err
	}
	interrupt, err := client.worker.InterruptThread(ctx, inprocess.InterruptThreadRequest{ThreadID: req.Ref.ThreadID, ThreadInterruptRequest: agentworker.ThreadInterruptRequest{Kind: agentworker.ThreadInterruptKindCancelInput, Reason: "runtime client stop"}})
	if err != nil {
		return nil, wrapError("stop", err)
	}
	result = &runtimeclient.StopResult{Ref: req.Ref, Stopped: interrupt.Status == inprocess.InterruptThreadAccepted || interrupt.Status == inprocess.InterruptThreadNoActiveTurn}
	return result, nil
}

func (client *Client) GetThread(ctx context.Context, ref runtimeclient.GlobalThreadRef) (thread *runtimeclient.Thread, err error) {
	if err = validateLocalRef(ref, "get_thread"); err != nil {
		return nil, err
	}
	state, err := client.worker.GetThread(ctx, ref.ThreadID)
	if err != nil {
		return nil, wrapError("get_thread", err)
	}
	return threadFromState(state), nil
}

func (client *Client) ListThreads(ctx context.Context, query runtimeclient.ListThreadsQuery) (result *runtimeclient.ListThreadsResult, err error) {
	if query.Runtime != "" && query.Runtime != runtimeclient.RuntimeLocal {
		return nil, invalidRuntime("list_threads", query.Runtime)
	}
	states, err := client.worker.ListThreads(ctx, inprocess.ListThreadsOptions{UserID: client.options.UserID, SessionID: query.Namespace, OrderBy: inprocess.ListThreadsOrderByUpdatedAt, Desc: true, Limit: query.Limit, Cursor: query.Cursor})
	if err != nil {
		return nil, wrapError("list_threads", err)
	}
	result = &runtimeclient.ListThreadsResult{Threads: make([]*runtimeclient.Thread, 0, len(states))}
	for _, state := range states {
		result.Threads = append(result.Threads, threadFromState(state))
	}
	return result, nil
}

func (client *Client) ListTimeline(ctx context.Context, query runtimeclient.TimelineQuery) (result *runtimeclient.TimelineResult, err error) {
	if err = validateLocalRef(query.Ref, "list_timeline"); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 10000
	}
	events, err := client.worker.ListEvents(ctx, query.Ref.ThreadID, inprocess.ListEventsOptions{Limit: limit})
	if err != nil {
		return nil, wrapError("list_timeline", err)
	}
	events = workerEventsAfter(events, query.AfterEventID)
	result = &runtimeclient.TimelineResult{Events: make([]timeline.Event, 0, len(events))}
	for _, event := range events {
		result.Events = append(result.Events, timelineEventFromWorker(event))
	}
	return result, nil
}

func (client *Client) SubscribeTimeline(ctx context.Context, query runtimeclient.TimelineQuery) (subscription runtimeclient.TimelineSubscription, err error) {
	if err = validateLocalRef(query.Ref, "subscribe_timeline"); err != nil {
		return nil, err
	}
	live, err := client.worker.SubscribeThreadEvents(ctx, query.Ref.ThreadID, client.options.SubscriberBuffer)
	if err != nil {
		return nil, wrapError("subscribe_timeline", err)
	}
	replay, err := client.worker.ListEvents(ctx, query.Ref.ThreadID, inprocess.ListEventsOptions{Limit: 10000})
	if err != nil {
		live.Close()
		return nil, wrapError("subscribe_timeline", err)
	}
	replay = workerEventsAfter(replay, query.AfterEventID)
	stream := &subscriptionStream{events: make(chan timeline.Event, len(replay)+16), cancel: live.Close}
	go stream.forward(ctx, replay, live.Events)
	return stream, nil
}

func invalidRuntime(operation string, kind runtimeclient.RuntimeKind) (err error) {
	err = &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: operation, Runtime: kind, Message: "local client requires local runtime"}
	return err
}

func validateLocalRef(ref runtimeclient.GlobalThreadRef, operation string) (err error) {
	if err = ref.Validate(); err != nil || ref.Runtime != runtimeclient.RuntimeLocal {
		return invalidRuntime(operation, ref.Runtime)
	}
	return nil
}

func wrapError(operation string, cause error) (err error) {
	code := runtimeclient.ErrorCodeInternal
	if errors.Is(cause, inprocess.ErrThreadNotFound) {
		code = runtimeclient.ErrorCodeNotFound
	} else if errors.Is(cause, inprocess.ErrThreadBlocked) || errors.Is(cause, inprocess.ErrThreadClosed) {
		code = runtimeclient.ErrorCodeConflict
	}
	err = &runtimeclient.Error{Code: code, Op: operation, Runtime: runtimeclient.RuntimeLocal, Cause: cause}
	return err
}

func definitionMetadata(metadata map[string]string, key string) (value string) {
	value = metadata[key]
	return value
}
