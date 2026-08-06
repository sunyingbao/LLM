package application

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestApplicationRunsInjectedCallbackConsumerUntilClose(t *testing.T) {
	runner, store, _, _, _ := testRunner(t)
	application, err := NewApplication(store, runner.handler.clients, nil)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}
	consumer := &blockingConsumer{started: make(chan struct{})}
	application.SetMessageConsumer(consumer)
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-consumer.started
	if err := application.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestLocalQueueCloseIsIdempotent(t *testing.T) {
	queue := NewLocalQueue(nil, nil)
	queue.Start()
	queue.Close()
	queue.Close()
}

func TestLocalApplicationRequiresMessagePublisher(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()
	if err := application.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "publisher") {
		t.Fatalf("Start() error = %v, want missing publisher", err)
	}
}

func TestLocalQueuePublishesCompletedJob(t *testing.T) {
	jobs := NewLocalJobs(t.TempDir() + "/jobs.json")
	job, _, err := jobs.Submit("image", CompetitionReferenceNode, "submit-once")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	published := make(chan CallbackMessage, 1)
	queue := NewLocalQueue(jobs, MessagePublisherFunc(func(_ context.Context, message CallbackMessage) error {
		published <- message
		return nil
	}))
	queue.Start()
	defer queue.Close()
	queue.Enqueue(job.ID)

	select {
	case message := <-published:
		if message.Provider != "image" || message.JobID != job.ID || message.EventID != "local:"+job.ID {
			t.Fatalf("published message = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("local job completion was not published")
	}
}

func TestMessageConsumerResumesWaitingNode(t *testing.T) {
	runner, store, _, tts, _ := testRunner(t)
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	ttsNode := nodeRun(run, "tts", "speaker-1")
	processed := make(chan struct{})
	consumer := messageConsumerFunc(func(ctx context.Context, handle func(context.Context, CallbackMessage) error) error {
		tts.jobs[ttsNode.JobID] = JobStatus{State: JobSucceeded, ExampleURI: "example://speaker-1"}
		err := handle(ctx, CallbackMessage{Provider: "tts", EventID: "event-1", JobID: ttsNode.JobID})
		close(processed)
		return err
	})
	application := &Application{Runner: runner, Store: store, callbackConsumer: consumer}
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-processed
	if err := application.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	stored, err := store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if node := nodeRun(stored, "tts", "speaker-1"); node.State != Succeeded {
		t.Fatalf("tts state = %s, want succeeded", node.State)
	}
}

type blockingConsumer struct {
	started chan struct{}
}

type messageConsumerFunc func(context.Context, func(context.Context, CallbackMessage) error) error

func (consumer messageConsumerFunc) Consume(ctx context.Context, handle func(context.Context, CallbackMessage) error) error {
	return consumer(ctx, handle)
}

func (consumer *blockingConsumer) Consume(ctx context.Context, _ func(context.Context, CallbackMessage) error) error {
	close(consumer.started)
	<-ctx.Done()
	return ctx.Err()
}
