package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestRouterUsesImmutableRuntimeBinding(t *testing.T) {
	t.Parallel()

	local := &routingClient{kind: RuntimeLocal}
	remoteFailure := errors.New("remote unavailable")
	remote := &routingClient{kind: RuntimeRemote, submitErr: remoteFailure}
	router := &Router{Local: local, Remote: remote}

	created, err := router.CreateThread(context.Background(), CreateThreadRequest{Runtime: RuntimeRemote})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if remote.createCalls != 1 || local.createCalls != 0 {
		t.Fatalf("create calls local=%d remote=%d", local.createCalls, remote.createCalls)
	}

	_, err = router.Submit(context.Background(), SubmitRequest{Ref: created.Thread.Ref})
	if !errors.Is(err, remoteFailure) {
		t.Fatalf("Submit() error = %v, want remote failure", err)
	}
	if remote.submitCalls != 1 || local.submitCalls != 0 {
		t.Fatalf("submit calls local=%d remote=%d", local.submitCalls, remote.submitCalls)
	}
}

func TestRouterReportsMissingRuntimeClient(t *testing.T) {
	t.Parallel()

	router := &Router{Local: &routingClient{kind: RuntimeLocal}}
	_, err := router.GetThread(context.Background(), GlobalThreadRef{Runtime: RuntimeRemote, ThreadID: "r1"})
	if !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("GetThread() error = %v, want capability unavailable", err)
	}
}

type routingClient struct {
	kind        RuntimeKind
	createCalls int
	submitCalls int
	submitErr   error
}

func (c *routingClient) CreateThread(ctx context.Context, req CreateThreadRequest) (result *CreateThreadResult, err error) {
	c.createCalls++
	result = &CreateThreadResult{Thread: &Thread{Ref: GlobalThreadRef{Runtime: c.kind, Namespace: req.Namespace, ThreadID: "thread-1"}}}
	return result, nil
}

func (c *routingClient) Submit(ctx context.Context, req SubmitRequest) (result *SubmitResult, err error) {
	c.submitCalls++
	return nil, c.submitErr
}

func (c *routingClient) Resume(context.Context, ResumeRequest) (result *SubmitResult, err error) {
	return nil, nil
}
func (c *routingClient) Stop(context.Context, StopRequest) (result *StopResult, err error) {
	return nil, nil
}
func (c *routingClient) GetThread(context.Context, GlobalThreadRef) (thread *Thread, err error) {
	return nil, nil
}
func (c *routingClient) ListThreads(context.Context, ListThreadsQuery) (result *ListThreadsResult, err error) {
	return nil, nil
}
func (c *routingClient) ListTimeline(context.Context, TimelineQuery) (result *TimelineResult, err error) {
	return nil, nil
}
func (c *routingClient) SubscribeTimeline(context.Context, TimelineQuery) (subscription TimelineSubscription, err error) {
	return nil, nil
}
