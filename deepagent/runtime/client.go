package runtime

import (
	"context"

	"eino-cli/deepagent/cloud/protocol/timeline"
)

type TimelineSubscription interface {
	Events() (events <-chan timeline.Event)
	Err() (err error)
	Close() (err error)
}

type Client interface {
	CreateThread(ctx context.Context, req CreateThreadRequest) (result *CreateThreadResult, err error)
	Submit(ctx context.Context, req SubmitRequest) (result *SubmitResult, err error)
	Resume(ctx context.Context, req ResumeRequest) (result *SubmitResult, err error)
	Stop(ctx context.Context, req StopRequest) (result *StopResult, err error)
	GetThread(ctx context.Context, ref GlobalThreadRef) (thread *Thread, err error)
	ListThreads(ctx context.Context, query ListThreadsQuery) (result *ListThreadsResult, err error)
	ListTimeline(ctx context.Context, query TimelineQuery) (result *TimelineResult, err error)
	SubscribeTimeline(ctx context.Context, query TimelineQuery) (subscription TimelineSubscription, err error)
}
