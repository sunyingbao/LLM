//go:build !windows

package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"code.byted.org/kite/kitex/client"
	acsvc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator/agentcoordinatorservice"
	"eino-cli/deepagent/worker/cloud"
)

// Worker is the Agent Coordinator-backed cloud agent worker. Its implementation is
// intentionally hidden so the stable CloudAgent API does not expose generated
// Agent Coordinator types.
type Worker struct {
	inner *cloud.Worker
}

// Validate checks whether the worker is ready to run.
func (w *Worker) Validate() error {
	if w == nil || w.inner == nil {
		return fmt.Errorf("cloudagent: worker is nil")
	}
	return w.inner.Validate()
}

// Run starts the worker loop and blocks until ctx is canceled or the worker
// returns an error.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.Validate(); err != nil {
		return err
	}
	return w.inner.Run(ctx)
}

// CoordinatorClient is the SDK-owned handle for Agent Coordinator RPCs.
// Generated Kitex client types stay behind this boundary.
type CoordinatorClient struct {
	client acsvc.Client
}

func newCoordinatorClient(client acsvc.Client) *CoordinatorClient {
	if client == nil {
		return nil
	}
	return &CoordinatorClient{client: client}
}

func (c *CoordinatorClient) rawClient() acsvc.Client {
	if c == nil {
		return nil
	}
	return c.client
}

// CoordinatorConfig describes how the worker reaches Agent Coordinator.
type CoordinatorConfig struct {
	// PSM is the Agent Coordinator service PSM.
	PSM string
	// Cluster optionally selects the target cluster through Kitex mesh.
	Cluster string
	// DirectHostPorts enables local direct connection, for example
	// 127.0.0.1:8888. It is mainly for local debugging.
	DirectHostPorts []string
}

// HostConfig contains the host-level worker settings that are independent of
// business agent construction.
type HostConfig struct {
	// Namespace selects the Agent Coordinator namespace to scan.
	Namespace string
	// Env is passed through to Agent Coordinator. Leave empty unless the target
	// Coordinator deployment requires an environment partition.
	Env string

	// Coordinator configures how New creates the RPC client when
	// Deps.CoordinatorClient is nil.
	Coordinator CoordinatorConfig

	// Concurrency is the number of claimed threads this worker can run in
	// parallel.
	Concurrency int
	// ScanLimit limits each Coordinator scan.
	ScanLimit int32
	// MessageLimit limits each message pull.
	MessageLimit int32
	// LeaseMS is the Coordinator lease duration in milliseconds.
	LeaseMS int64
	// LeaseOwnerHint is an optional human-readable lease owner suffix.
	LeaseOwnerHint string
	// ScanInterval is the delay between scans when no immediate work is found.
	ScanInterval time.Duration
	// RenewInterval controls lease renewal cadence while a thread is running.
	RenewInterval time.Duration
	// MessagePollInterval controls message polling cadence for a claimed thread.
	MessagePollInterval time.Duration
	// IdleTimeout controls how long an idle claimed thread is held before
	// release.
	IdleTimeout time.Duration
	// ShutdownDrainTimeout is the maximum time spent waiting for active turns
	// during worker shutdown before interrupting.
	ShutdownDrainTimeout time.Duration
	// ShutdownInterruptDrainTimeout is the extra wait after shutdown interrupt
	// before the worker gives up draining a thread.
	ShutdownInterruptDrainTimeout time.Duration
	// InterruptDrainTimeout is the normal interrupt drain timeout for a claimed
	// thread.
	InterruptDrainTimeout time.Duration
	// RuntimeInterruptTimeout controls how long AgentThread.Interrupt waits
	// before surfacing an interrupted result for a blocked turn.
	RuntimeInterruptTimeout time.Duration
}

// NewCoordinatorClient creates the Agent Coordinator RPC client used by a
// worker process.
func NewCoordinatorClient(cfg CoordinatorConfig) (*CoordinatorClient, error) {
	psm := strings.TrimSpace(cfg.PSM)
	if psm == "" {
		return nil, fmt.Errorf("cloudagent: coordinator psm is required")
	}
	opts := make([]client.Option, 0, 2)
	if cluster := strings.TrimSpace(cfg.Cluster); cluster != "" {
		opts = append(opts, client.WithCluster(cluster))
	}
	if len(cfg.DirectHostPorts) > 0 {
		opts = append(opts, client.WithHostPorts(nonEmptyStrings(cfg.DirectHostPorts)...))
	}
	rawClient, err := acsvc.NewClient(psm, opts...)
	if err != nil {
		return nil, err
	}
	return newCoordinatorClient(rawClient), nil
}

func newWorker(cfg HostConfig, coordinatorClient *CoordinatorClient, factory cloud.AgentThreadFactory) (*Worker, error) {
	inner := &cloud.Worker{
		Namespace:                     cfg.Namespace,
		Env:                           cfg.Env,
		Client:                        coordinatorClient.rawClient(),
		AgentThreadFactory:            factory,
		Concurrency:                   cfg.Concurrency,
		ScanLimit:                     cfg.ScanLimit,
		MessageLimit:                  cfg.MessageLimit,
		LeaseMS:                       cfg.LeaseMS,
		LeaseOwnerHint:                cfg.LeaseOwnerHint,
		ScanInterval:                  cfg.ScanInterval,
		RenewInterval:                 cfg.RenewInterval,
		MessagePollInterval:           cfg.MessagePollInterval,
		IdleTimeout:                   cfg.IdleTimeout,
		ShutdownDrainTimeout:          cfg.ShutdownDrainTimeout,
		ShutdownInterruptDrainTimeout: cfg.ShutdownInterruptDrainTimeout,
		InterruptDrainTimeout:         cfg.InterruptDrainTimeout,
		RuntimeInterruptTimeout:       cfg.RuntimeInterruptTimeout,
	}
	if err := inner.Validate(); err != nil {
		return nil, err
	}
	return &Worker{inner: inner}, nil
}

// ParseHostPorts parses a comma-separated hostport list for local direct
// connection configs.
func ParseHostPorts(raw string) []string {
	return nonEmptyStrings(strings.Split(raw, ","))
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
