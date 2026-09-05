//go:build !windows

package cloud

import (
	"context"
	"errors"
	"testing"
	"time"

	"eino-cli/deepagent/coordinator"
	agentworker "eino-cli/deepagent/worker"
	"github.com/stretchr/testify/require"
)

func TestRunClaimStopsWhenLeaseRenewalFails(t *testing.T) {
	lost := errors.New("lease no longer owned")
	var runtimeContext context.Context
	closed := false
	client := &fakeClient{renewThreadLeaseFunc: func(context.Context, coordinator.RenewThreadLeaseRequest) (lease *coordinator.Lease, err error) {
		return nil, lost
	}}
	worker := &Worker{Namespace: "ns", Client: client, RenewInterval: time.Millisecond, MessagePollInterval: time.Second,
		AgentThreadFactory: func(ctx context.Context, _ *coordinator.Thread) (runtime agentworker.AgentThread, err error) {
			runtimeContext = ctx
			return &testAgentThreadRuntime{
				InitFunc: func(context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: make(chan agentworker.ThreadOutputItem)}, nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn { return &agentworker.ActiveTurn{TurnID: "run"} },
				CloseFunc:      func(context.Context) error { closed = true; return nil },
			}, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := worker.runClaim(ctx, ctx, &claimResult{thread: &coordinator.Thread{ThreadID: 42}, lease: &coordinator.Lease{ThreadID: 42, LeaseToken: "old"}})
	require.ErrorIs(t, err, lost)
	require.ErrorIs(t, context.Cause(runtimeContext), lost)
	require.True(t, closed)
	require.Empty(t, client.releaseRequests)
	require.Empty(t, client.completeCloseRequests)
}

func TestRenewLoopStopsAtDeadlineWhileRenewalIsStuck(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)
	client := &fakeClient{renewThreadLeaseFunc: func(context.Context, coordinator.RenewThreadLeaseRequest) (lease *coordinator.Lease, err error) {
		<-blocked
		return nil, nil
	}}
	worker := &Worker{Client: client, RenewInterval: time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	deadline := time.Now().Add(30 * time.Millisecond)
	err := worker.renewLoop(ctx, &coordinator.Lease{ThreadID: 42, LeaseToken: "old", LeaseDeadlineAt: deadline})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(deadline), 300*time.Millisecond)
}

func TestRenewLoopExpiresBeforeFirstRenewal(t *testing.T) {
	worker := &Worker{Client: &fakeClient{}, RenewInterval: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := worker.renewLoop(ctx, &coordinator.Lease{ThreadID: 42, LeaseToken: "old", LeaseDeadlineAt: time.Now().Add(10 * time.Millisecond)})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWorkerEventCarriesClaimLease(t *testing.T) {
	client := &fakeClient{}
	worker := &Worker{Namespace: "ns", Client: client}
	processor := &outputItemProcessor{lifecycle: &claimCoordinator{
		worker: worker,
		ctx:    context.Background(),
		claim:  &claimResult{thread: &coordinator.Thread{ThreadID: 42}, lease: &coordinator.Lease{LeaseToken: "current"}},
	}}
	processor.handleThreadOutput(agentworker.ThreadOutputItem{Event: &agentworker.Event{ThreadID: "42", TurnID: "run", Type: "event"}})
	require.Len(t, client.appendEventsRequests, 1)
	require.Equal(t, "current", client.appendEventsRequests[0].LeaseToken)
}
