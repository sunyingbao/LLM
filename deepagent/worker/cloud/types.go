//go:build !windows

package cloud

import (
	"context"
	"errors"

	"eino-cli/deepagent/coordinator"
	"eino-cli/deepagent/worker"
)

type CoordinatorClient interface {
	CreateThread(context.Context, coordinator.CreateThreadRequest) (coordinator.CreateThreadResult, error)
	ScanRunnableThreads(context.Context, coordinator.ScanRunnableThreadsRequest) (coordinator.ScanRunnableThreadsResult, error)
	ClaimThread(context.Context, coordinator.ClaimThreadRequest) (coordinator.ClaimThreadResult, error)
	RenewThreadLease(context.Context, coordinator.RenewThreadLeaseRequest) (*coordinator.Lease, error)
	ReadPendingInputs(context.Context, coordinator.ReadPendingInputsRequest) (coordinator.ReadPendingInputsResult, error)
	ConfirmInputDelivery(context.Context, coordinator.ConfirmInputDeliveryRequest) ([]*coordinator.Message, error)
	ReleaseThread(context.Context, coordinator.ReleaseThreadRequest) (*coordinator.Thread, error)
	SubmitInput(context.Context, coordinator.SubmitInputRequest) (coordinator.SubmitInputResult, error)
	RequestThreadClose(context.Context, coordinator.RequestThreadCloseRequest) (*coordinator.RequestThreadCloseResult, error)
	ConfirmThreadClosed(context.Context, coordinator.ConfirmThreadClosedRequest) (*coordinator.ConfirmThreadClosedResult, error)
	PublishEvents(context.Context, coordinator.PublishEventsRequest) (coordinator.PublishEventsResult, error)
	ListEvents(context.Context, coordinator.ListEventsRequest) (coordinator.ListEventsResult, error)
}

var (
	ErrMissingNamespace          = errors.New("agentworker/cloud: namespace is required")
	ErrMissingClient             = errors.New("agentworker/cloud: client is required")
	ErrMissingAgentThreadFactory = errors.New("agentworker/cloud: agent thread factory is required")
	ErrMissingThread             = errors.New("agentworker/cloud: thread is required")
	ErrMissingLease              = errors.New("agentworker/cloud: lease is required")
)

// AgentThreadFactory creates a thread-scoped runtime for one claimed Agent
// Coordinator thread.
type AgentThreadFactory func(ctx context.Context, threadInfo *coordinator.Thread) (agentworker.AgentThread, error)
