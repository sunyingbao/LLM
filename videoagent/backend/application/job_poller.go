package application

import (
	"context"
	"fmt"
	"log"
	"time"
)

type JobPoller struct {
	store     *Store
	publisher MessagePublisher
	interval  time.Duration
}

func NewJobPoller(store *Store, publisher MessagePublisher, interval time.Duration) (*JobPoller, error) {
	if store == nil || publisher == nil {
		return nil, fmt.Errorf("job poller store and publisher are required")
	}
	if interval <= 0 {
		return nil, fmt.Errorf("job poll interval must be positive")
	}
	return &JobPoller{store: store, publisher: publisher, interval: interval}, nil
}

func (poller *JobPoller) Run(ctx context.Context) error {
	ticker := time.NewTicker(poller.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := poller.publishWaiting(ctx); err != nil {
				log.Printf("poll waiting video jobs: %v", err)
			}
		}
	}
}

func (poller *JobPoller) publishWaiting(ctx context.Context) error {
	runs, err := poller.store.List(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.Canceled {
			continue
		}
		for _, node := range run.NodeRuns {
			if node.State != Waiting || node.Provider == "" {
				continue
			}
			message := CallbackMessage{Provider: node.Provider, JobID: node.JobID}
			if node.JobID != "" {
				message.EventID = "poll:" + node.JobID
			} else if node.SubmitStarted && node.SubmitKey != "" {
				message.EventID = "reconcile:" + node.SubmitKey
				message.SubmitKey = node.SubmitKey
			} else {
				continue
			}
			if err := poller.publisher.Publish(ctx, message); err != nil {
				return err
			}
		}
	}
	return nil
}
