package remote

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	cloudapi "eino-cli/deepagent/cloud/api"
	"eino-cli/deepagent/cloud/protocol/timeline"
	"eino-cli/deepagent/definition"
	runtimeclient "eino-cli/deepagent/runtime"
)

const (
	metadataDefinitionName    = "runtime.definition.name"
	metadataDefinitionVersion = "runtime.definition.version"
)

type Client struct {
	API    *cloudapi.AgentAPI
	UserID string
}

func New(api *cloudapi.AgentAPI, userID string) (client *Client, err error) {
	if api == nil || api.Coordinator == nil {
		return nil, fmt.Errorf("cloud api coordinator is required")
	}
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("remote runtime user id is required")
	}
	client = &Client{API: api, UserID: userID}
	return client, nil
}

func (client *Client) CreateThread(ctx context.Context, req runtimeclient.CreateThreadRequest) (result *runtimeclient.CreateThreadResult, err error) {
	if req.Runtime != runtimeclient.RuntimeRemote {
		return nil, invalidRuntime("create_thread", req.Runtime)
	}
	if err = agentdefinition.Validate(req.Definition); err != nil {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "create_thread", Runtime: runtimeclient.RuntimeRemote, Cause: err}
	}
	metadata := map[string]string{
		metadataDefinitionName: req.Definition.Name, metadataDefinitionVersion: req.Definition.Version,
	}
	parentID := ""
	if req.ParentRef != nil {
		if req.ParentRef.Runtime != runtimeclient.RuntimeRemote {
			return nil, invalidRuntime("create_thread", req.ParentRef.Runtime)
		}
		parentID = req.ParentRef.ThreadID
	}
	created, err := client.API.CreateThread(ctx, cloudapi.CreateThreadRequest{
		UserID: client.UserID, SessionID: req.Namespace, ParentThreadID: parentID, Title: req.Title,
		Profile: cloudapi.ThreadProfile{Cwd: req.Workspace.Cwd}, Metadata: metadata,
	})
	if err != nil {
		return nil, wrapError("create_thread", err)
	}
	if created == nil || created.Thread == nil {
		return nil, wrapError("create_thread", fmt.Errorf("cloud api returned no thread"))
	}
	result = &runtimeclient.CreateThreadResult{Thread: threadFromCloud(created.Thread, req.Namespace)}
	return result, nil
}

func (client *Client) Submit(ctx context.Context, req runtimeclient.SubmitRequest) (result *runtimeclient.SubmitResult, err error) {
	if err = validateRef(req.Ref, "submit"); err != nil {
		return nil, err
	}
	if err = req.Input.Validate(); err != nil {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "submit", Runtime: runtimeclient.RuntimeRemote, Cause: err}
	}
	_, err = client.API.Submit(ctx, cloudapi.SubmitRequest{
		UserID: client.UserID, ThreadID: req.Ref.ThreadID, Metadata: req.Metadata,
		Input: &cloudapi.SubmitInput{Parts: req.Input.Parts, Mode: req.Input.Mode, Extra: req.Input.Extra},
	})
	if err != nil {
		return nil, wrapError("submit", err)
	}
	// Agent Coordinator assigns the turn asynchronously. SubscribeTimeline emits
	// TURN_STARTED with the authoritative id; the interactive stream locks to it.
	return &runtimeclient.SubmitResult{Ref: req.Ref}, nil
}

func (client *Client) Resume(ctx context.Context, req runtimeclient.ResumeRequest) (result *runtimeclient.SubmitResult, err error) {
	if err = validateRef(req.Ref, "resume"); err != nil {
		return nil, err
	}
	payload := req.Payload
	_, err = client.API.Submit(ctx, cloudapi.SubmitRequest{UserID: client.UserID, ThreadID: req.Ref.ThreadID, Resume: &cloudapi.ResumeInput{
		TurnID: payload.TurnID, CheckpointID: payload.CheckpointID, InterruptID: payload.InterruptID,
		ToolName: payload.ToolName, ArgumentsInJSON: payload.ArgumentsInJSON,
		ConsumedMessageIDs: payload.ConsumedMessageIDs, Approval: payload.Approval,
		RequestUserInput: payload.RequestUserInput, Interrupt: payload.Interrupt,
	}})
	if err != nil {
		return nil, wrapError("resume", err)
	}
	return &runtimeclient.SubmitResult{Ref: req.Ref, TurnID: payload.TurnID}, nil
}

func (client *Client) Stop(ctx context.Context, req runtimeclient.StopRequest) (result *runtimeclient.StopResult, err error) {
	if err = validateRef(req.Ref, "stop"); err != nil {
		return nil, err
	}
	_, err = client.API.StopRunning(ctx, cloudapi.StopRunningRequest{UserID: client.UserID, SessionID: req.Ref.Namespace, ThreadIDs: []string{req.Ref.ThreadID}, Reason: "runtime client stop"})
	if err != nil {
		return nil, wrapError("stop", err)
	}
	return &runtimeclient.StopResult{Ref: req.Ref, Stopped: true}, nil
}

func (client *Client) GetThread(ctx context.Context, ref runtimeclient.GlobalThreadRef) (thread *runtimeclient.Thread, err error) {
	if err = validateRef(ref, "get_thread"); err != nil {
		return nil, err
	}
	listed, err := client.ListThreads(ctx, runtimeclient.ListThreadsQuery{Runtime: runtimeclient.RuntimeRemote, Namespace: ref.Namespace, Limit: 500})
	if err != nil {
		return nil, err
	}
	for _, candidate := range listed.Threads {
		if candidate != nil && candidate.Ref.ThreadID == ref.ThreadID {
			return candidate, nil
		}
	}
	return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeNotFound, Op: "get_thread", Runtime: runtimeclient.RuntimeRemote, Message: "thread not found"}
}

func (client *Client) ListThreads(ctx context.Context, query runtimeclient.ListThreadsQuery) (result *runtimeclient.ListThreadsResult, err error) {
	if query.Runtime != "" && query.Runtime != runtimeclient.RuntimeRemote {
		return nil, invalidRuntime("list_threads", query.Runtime)
	}
	if strings.TrimSpace(query.Namespace) == "" {
		return nil, &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: "list_threads", Runtime: runtimeclient.RuntimeRemote, Message: "namespace is required"}
	}
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	listed, err := client.API.Coordinator.ListSessionThreads(ctx, cloudapi.ListSessionThreadsRequest{SessionID: query.Namespace, Cursor: query.Cursor, Limit: int32(limit)})
	if err != nil {
		return nil, wrapError("list_threads", err)
	}
	result = &runtimeclient.ListThreadsResult{}
	if listed == nil {
		return result, nil
	}
	result.NextCursor = listed.NextCursor
	for _, item := range listed.Threads {
		if item != nil {
			result.Threads = append(result.Threads, threadFromCloud(item, query.Namespace))
		}
	}
	return result, nil
}

func (client *Client) ListTimeline(ctx context.Context, query runtimeclient.TimelineQuery) (result *runtimeclient.TimelineResult, err error) {
	if err = validateRef(query.Ref, "list_timeline"); err != nil {
		return nil, err
	}
	listed, err := client.API.ListTimeline(ctx, cloudapi.ListTimelineRequest{SessionID: query.Ref.Namespace, ThreadID: query.Ref.ThreadID, Cursor: query.AfterEventID, Limit: int32(query.Limit)})
	if err != nil {
		return nil, wrapError("list_timeline", err)
	}
	result = &runtimeclient.TimelineResult{}
	if listed == nil {
		return result, nil
	}
	result.NextCursor = listed.NextCursor
	for _, event := range listed.Events {
		if event != nil {
			result.Events = append(result.Events, *event)
		}
	}
	return result, nil
}

func (client *Client) SubscribeTimeline(ctx context.Context, query runtimeclient.TimelineQuery) (subscription runtimeclient.TimelineSubscription, err error) {
	if err = validateRef(query.Ref, "subscribe_timeline"); err != nil {
		return nil, err
	}
	subscriptionCtx, cancel := context.WithCancel(ctx)
	remoteSubscription := &timelineSubscription{events: make(chan timeline.Event, 256), cancel: cancel}
	go remoteSubscription.run(subscriptionCtx, client.API, query)
	return remoteSubscription, nil
}

type timelineSubscription struct {
	events chan timeline.Event
	cancel context.CancelFunc
	mu     sync.Mutex
	err    error
	once   sync.Once
}

func (subscription *timelineSubscription) run(ctx context.Context, api *cloudapi.AgentAPI, query runtimeclient.TimelineQuery) {
	defer close(subscription.events)
	defer subscription.cancel()
	liveEvents := make(chan timeline.Event, 256)
	liveCompleted := make(chan error, 1)
	go func() {
		defer close(liveEvents)
		err := api.SubscribeTimeline(ctx, cloudapi.SubscribeTimelineRequest{SessionID: query.Ref.Namespace, ThreadID: query.Ref.ThreadID}, func(ctx context.Context, frame cloudapi.TimelineFrame) (err error) {
			if frame.Event == nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case liveEvents <- *frame.Event:
				return nil
			}
		})
		liveCompleted <- err
	}()

	seen := make(map[string]struct{})
	if query.AfterEventID != "" {
		seen[query.AfterEventID] = struct{}{}
	}
	cursor := query.AfterEventID
	emit := func(event timeline.Event) (err error) {
		if event.EventID != "" {
			if _, exists := seen[event.EventID]; exists {
				return nil
			}
			seen[event.EventID] = struct{}{}
			cursor = event.EventID
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case subscription.events <- event:
			return nil
		}
	}
	replay := func() (err error) {
		for {
			listed, listErr := api.ListTimeline(ctx, cloudapi.ListTimelineRequest{SessionID: query.Ref.Namespace, ThreadID: query.Ref.ThreadID, Cursor: cursor, Limit: int32(query.Limit)})
			if listErr != nil {
				return listErr
			}
			if listed == nil {
				return nil
			}
			for _, event := range listed.Events {
				if event != nil {
					if err = emit(*event); err != nil {
						return err
					}
				}
			}
			if !listed.HasMore || listed.NextCursor == "" || listed.NextCursor == cursor {
				return nil
			}
			cursor = listed.NextCursor
		}
	}
	if err := replay(); err != nil && ctx.Err() == nil {
		subscription.setError(err)
		return
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-liveEvents:
			if !ok {
				err := <-liveCompleted
				if replayErr := replay(); replayErr != nil && ctx.Err() == nil {
					subscription.setError(replayErr)
				} else if err != nil && ctx.Err() == nil {
					subscription.setError(err)
				}
				return
			}
			if err := emit(event); err != nil {
				return
			}
		case <-ticker.C:
			if err := replay(); err != nil && ctx.Err() == nil {
				subscription.setError(err)
				return
			}
		}
	}
}

func (subscription *timelineSubscription) setError(err error) {
	subscription.mu.Lock()
	subscription.err = err
	subscription.mu.Unlock()
}

func (subscription *timelineSubscription) Events() (events <-chan timeline.Event) {
	return subscription.events
}
func (subscription *timelineSubscription) Err() (err error) {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	return subscription.err
}
func (subscription *timelineSubscription) Close() (err error) {
	subscription.once.Do(subscription.cancel)
	return nil
}

func threadFromCloud(thread *cloudapi.Thread, namespace string) (result *runtimeclient.Thread) {
	metadata := thread.Metadata
	result = &runtimeclient.Thread{
		Ref:            runtimeclient.GlobalThreadRef{Runtime: runtimeclient.RuntimeRemote, Namespace: namespace, ThreadID: thread.ID},
		DefinitionName: metadata[metadataDefinitionName], DefinitionVersion: metadata[metadataDefinitionVersion],
		Workspace: runtimeclient.WorkspaceSpec{Cwd: thread.Profile.Cwd}, Title: thread.Title, State: stateFromCloud(thread.Status),
	}
	return result
}

func stateFromCloud(status string) (state runtimeclient.ThreadState) {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "RUNNING":
		return runtimeclient.ThreadStateRunning
	case "BLOCKED":
		return runtimeclient.ThreadStateBlocked
	case "INTERRUPTED", "CANCELLED", "CANCELED":
		return runtimeclient.ThreadStateInterrupted
	case "FAILED", "ERROR":
		return runtimeclient.ThreadStateFailed
	default:
		return runtimeclient.ThreadStateIdle
	}
}

func validateRef(ref runtimeclient.GlobalThreadRef, operation string) (err error) {
	if err = ref.Validate(); err != nil || ref.Runtime != runtimeclient.RuntimeRemote || strings.TrimSpace(ref.Namespace) == "" {
		return &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: operation, Runtime: ref.Runtime, Message: "remote thread reference requires runtime, namespace, and thread_id", Cause: err}
	}
	return nil
}

func invalidRuntime(operation string, kind runtimeclient.RuntimeKind) (err error) {
	return &runtimeclient.Error{Code: runtimeclient.ErrorCodeInvalidArgument, Op: operation, Runtime: kind, Message: "remote client requires remote runtime"}
}

func wrapError(operation string, cause error) (err error) {
	return &runtimeclient.Error{Code: runtimeclient.ErrorCodeUnavailable, Op: operation, Runtime: runtimeclient.RuntimeRemote, Message: strconv.Quote(cause.Error()), Cause: cause}
}

var _ runtimeclient.Client = (*Client)(nil)
