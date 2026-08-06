package application

import (
	"context"
	"testing"
	"time"
)

type recordingMessagePublisher struct {
	messages []CallbackMessage
}

func (publisher *recordingMessagePublisher) Publish(_ context.Context, message CallbackMessage) error {
	publisher.messages = append(publisher.messages, message)
	return nil
}

func TestJobPollerPublishesOnlyWaitingRemoteJobs(t *testing.T) {
	store := NewStore(t.TempDir() + "/workflow.json")
	run := Run{ID: "run-1", NodeRuns: []NodeRun{
		{NodeID: "preview", InstanceKey: "scene-1", State: Waiting, Provider: "seedance", JobID: "job-1"},
		{NodeID: "preview", InstanceKey: "scene-2", State: Succeeded, Provider: "seedance", JobID: "job-2"},
		{NodeID: "preview", InstanceKey: "scene-3", State: Waiting, Provider: "seedance", SubmitStarted: true, SubmitKey: "submit-3"},
	}}
	if err := store.Create(context.Background(), run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	publisher := &recordingMessagePublisher{}
	poller, err := NewJobPoller(store, publisher, time.Second)
	if err != nil {
		t.Fatalf("NewJobPoller() error = %v", err)
	}
	if err := poller.publishWaiting(context.Background()); err != nil {
		t.Fatalf("publishWaiting() error = %v", err)
	}
	if len(publisher.messages) != 2 {
		t.Fatalf("messages = %#v", publisher.messages)
	}
	message := publisher.messages[0]
	if message.Provider != "seedance" || message.JobID != "job-1" || message.EventID == "" {
		t.Fatalf("message = %#v", message)
	}
	reconcile := publisher.messages[1]
	if reconcile.Provider != "seedance" || reconcile.SubmitKey != "submit-3" || reconcile.EventID != "reconcile:submit-3" {
		t.Fatalf("reconciliation message = %#v", reconcile)
	}
}

func TestJobPollerContinuesPendingCancellation(t *testing.T) {
	store := NewStore(t.TempDir() + "/workflow.json")
	run := Run{ID: "run-canceling", CancelRequested: true, NodeRuns: []NodeRun{{
		NodeID: "preview", InstanceKey: "scene-1", State: Waiting, Provider: "seedance", JobID: "job-1",
	}}}
	if err := store.Create(context.Background(), run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	publisher := &recordingMessagePublisher{}
	poller, err := NewJobPoller(store, publisher, time.Second)
	if err != nil {
		t.Fatalf("NewJobPoller() error = %v", err)
	}
	if err := poller.publishWaiting(context.Background()); err != nil {
		t.Fatalf("publishWaiting() error = %v", err)
	}
	if len(publisher.messages) != 1 || publisher.messages[0].JobID != "job-1" {
		t.Fatalf("messages = %#v, want cancel-pending job", publisher.messages)
	}
}

var _ MessagePublisher = (*recordingMessagePublisher)(nil)
