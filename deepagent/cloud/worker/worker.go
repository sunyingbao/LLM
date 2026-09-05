//go:build !windows

package worker

import (
	"context"
	"fmt"
	"time"

	"eino-cli/deepagent/worker/cloud"
)

// Worker executes Coordinator threads through the CloudAgent runtime.
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

// HostConfig contains the host-level worker settings that are independent of
// business agent construction.
type HostConfig struct {
	// Namespace selects the Coordinator namespace to scan.
	Namespace string
	// Env optionally partitions threads within the namespace.
	Env string

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

func newWorker(cfg HostConfig, coordinatorClient cloud.CoordinatorClient, factory cloud.AgentThreadFactory) (worker *Worker, err error) {
	inner := &cloud.Worker{
		Namespace:                     cfg.Namespace,
		Env:                           cfg.Env,
		Client:                        coordinatorClient,
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
