package runtime

import "context"

type Router struct {
	Local  Client
	Remote Client
}

func (router *Router) CreateThread(ctx context.Context, req CreateThreadRequest) (result *CreateThreadResult, err error) {
	client, err := router.clientFor(req.Runtime, "create_thread")
	if err != nil {
		return nil, err
	}
	if result, err = client.CreateThread(ctx, req); err != nil {
		return nil, err
	}
	if result == nil || result.Thread == nil {
		return nil, &Error{Code: ErrorCodeInternal, Op: "create_thread", Runtime: req.Runtime, Message: "runtime returned no thread"}
	}
	if result.Thread.Ref.Runtime != req.Runtime {
		return nil, &Error{Code: ErrorCodeInternal, Op: "create_thread", Runtime: req.Runtime, Message: "runtime returned a mismatched thread reference"}
	}
	return result, nil
}

func (router *Router) Submit(ctx context.Context, req SubmitRequest) (result *SubmitResult, err error) {
	client, err := router.clientFor(req.Ref.Runtime, "submit")
	if err != nil {
		return nil, err
	}
	result, err = client.Submit(ctx, req)
	return result, err
}

func (router *Router) Resume(ctx context.Context, req ResumeRequest) (result *SubmitResult, err error) {
	client, err := router.clientFor(req.Ref.Runtime, "resume")
	if err != nil {
		return nil, err
	}
	result, err = client.Resume(ctx, req)
	return result, err
}

func (router *Router) Stop(ctx context.Context, req StopRequest) (result *StopResult, err error) {
	client, err := router.clientFor(req.Ref.Runtime, "stop")
	if err != nil {
		return nil, err
	}
	result, err = client.Stop(ctx, req)
	return result, err
}

func (router *Router) GetThread(ctx context.Context, ref GlobalThreadRef) (thread *Thread, err error) {
	client, err := router.clientFor(ref.Runtime, "get_thread")
	if err != nil {
		return nil, err
	}
	thread, err = client.GetThread(ctx, ref)
	return thread, err
}

func (router *Router) ListThreads(ctx context.Context, query ListThreadsQuery) (result *ListThreadsResult, err error) {
	client, err := router.clientFor(query.Runtime, "list_threads")
	if err != nil {
		return nil, err
	}
	result, err = client.ListThreads(ctx, query)
	return result, err
}

func (router *Router) ListTimeline(ctx context.Context, query TimelineQuery) (result *TimelineResult, err error) {
	client, err := router.clientFor(query.Ref.Runtime, "list_timeline")
	if err != nil {
		return nil, err
	}
	result, err = client.ListTimeline(ctx, query)
	return result, err
}

func (router *Router) SubscribeTimeline(ctx context.Context, query TimelineQuery) (subscription TimelineSubscription, err error) {
	client, err := router.clientFor(query.Ref.Runtime, "subscribe_timeline")
	if err != nil {
		return nil, err
	}
	subscription, err = client.SubscribeTimeline(ctx, query)
	return subscription, err
}

func (router *Router) clientFor(kind RuntimeKind, operation string) (client Client, err error) {
	switch kind {
	case RuntimeLocal:
		client = router.Local
	case RuntimeRemote:
		client = router.Remote
	default:
		return nil, &Error{Code: ErrorCodeInvalidArgument, Op: operation, Runtime: kind, Message: "unsupported runtime"}
	}
	if client == nil {
		return nil, &Error{Code: ErrorCodeCapabilityUnavailable, Op: operation, Runtime: kind, Message: "runtime client is not configured"}
	}
	return client, nil
}
