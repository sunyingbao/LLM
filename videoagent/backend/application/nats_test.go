package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"eino-cli/videoagent/backend/messaging"
)

func TestNATSMessageBusRetriesAndDeduplicatesCallbacks(t *testing.T) {
	if os.Getenv("VIDEO_AGENT_NATS_TEST") == "" {
		t.Skip("set VIDEO_AGENT_NATS_TEST=1 to run the local JetStream integration test")
	}
	name := newID("TEST")
	config := messaging.NATSConfig{
		URL:      messaging.DefaultNATSURL,
		Stream:   name,
		Subject:  "video_agent.test." + name,
		Consumer: "consumer_" + name,
	}
	bus, err := messaging.NewNATSMessageBus(context.Background(), config)
	if err != nil {
		t.Fatalf("NewNATSMessageBus() error = %v", err)
	}
	defer bus.Close()
	defer bus.DeleteStream(context.Background(), config.Stream)

	consumeContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	processed := make(chan struct{})
	consumerDone := make(chan error, 1)
	go func() {
		consumerDone <- bus.Consume(consumeContext, func(_ context.Context, message CallbackMessage) error {
			if message.EventID != "event-1" || message.JobID != "job-1" {
				return errors.New("unexpected callback")
			}
			if attempts.Add(1) == 1 {
				return errors.New("retry once")
			}
			close(processed)
			return nil
		})
	}()

	message := CallbackMessage{Provider: "image", EventID: "event-1", JobID: "job-1"}
	if err := bus.Publish(context.Background(), message); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := bus.Publish(context.Background(), message); err != nil {
		t.Fatalf("duplicate Publish() error = %v", err)
	}
	select {
	case <-processed:
	case <-time.After(5 * time.Second):
		t.Fatal("callback was not retried and processed")
	}
	time.Sleep(200 * time.Millisecond)
	if got := attempts.Load(); got != 2 {
		t.Fatalf("callback attempts = %d, want 2", got)
	}
	cancel()
	if err := <-consumerDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume() error = %v", err)
	}
}

func TestNATSMessageSurvivesClientRestart(t *testing.T) {
	if os.Getenv("VIDEO_AGENT_NATS_TEST") == "" {
		t.Skip("set VIDEO_AGENT_NATS_TEST=1 to run the local JetStream integration test")
	}
	name := newID("TEST")
	config := messaging.NATSConfig{
		URL:      messaging.DefaultNATSURL,
		Stream:   name,
		Subject:  "video_agent.test." + name,
		Consumer: "consumer_" + name,
	}
	publisher, err := messaging.NewNATSMessageBus(context.Background(), config)
	if err != nil {
		t.Fatalf("NewNATSMessageBus(publisher) error = %v", err)
	}
	message := CallbackMessage{Provider: "video", EventID: "event-restart", JobID: "job-restart"}
	if err := publisher.Publish(context.Background(), message); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := publisher.Close(); err != nil {
		t.Fatalf("publisher Close() error = %v", err)
	}

	consumer, err := messaging.NewNATSMessageBus(context.Background(), config)
	if err != nil {
		t.Fatalf("NewNATSMessageBus(consumer) error = %v", err)
	}
	defer consumer.Close()
	defer consumer.DeleteStream(context.Background(), config.Stream)
	consumeContext, cancel := context.WithCancel(context.Background())
	consumerDone := make(chan error, 1)
	received := make(chan CallbackMessage, 1)
	go func() {
		consumerDone <- consumer.Consume(consumeContext, func(_ context.Context, message CallbackMessage) error {
			received <- message
			return nil
		})
	}()
	select {
	case got := <-received:
		if got != message {
			t.Fatalf("received message = %#v, want %#v", got, message)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("persisted callback was not delivered after client restart")
	}
	deadline := time.Now().Add(time.Second)
	acked := false
	for time.Now().Before(deadline) {
		info, infoErr := consumer.ConsumerInfo(context.Background())
		if infoErr == nil && info.NumAckPending == 0 && info.NumPending == 0 {
			acked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if !acked {
		t.Fatal("persisted callback was not acknowledged")
	}
	if err := <-consumerDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume() error = %v", err)
	}
}

func TestNATSApplicationCompletesWorkflow(t *testing.T) {
	if os.Getenv("VIDEO_AGENT_NATS_TEST") == "" {
		t.Skip("set VIDEO_AGENT_NATS_TEST=1 to run the local JetStream integration test")
	}
	name := newID("TEST")
	config := messaging.NATSConfig{
		URL:      messaging.DefaultNATSURL,
		Stream:   name,
		Subject:  "video_agent.test." + name,
		Consumer: "consumer_" + name,
	}
	bus, err := messaging.NewNATSMessageBus(context.Background(), config)
	if err != nil {
		t.Fatalf("NewNATSMessageBus() error = %v", err)
	}
	defer bus.Close()
	defer bus.DeleteStream(context.Background(), config.Stream)

	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	application.SetMessageQueue(bus, bus)
	defer application.Close()
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	run, err := application.Runner.StartRun(context.Background(), "demo", RunInput{ProductName: "shoe", Brief: "short video"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	completed := waitForFinalVideoInStore(t, application.Store, run.ID)
	if !hasArtifact(completed, "finalvideo") {
		t.Fatal("NATS workflow did not produce finalvideo")
	}
}

func TestNATSPollerCompletesProvidersWithoutCallbacks(t *testing.T) {
	if os.Getenv("VIDEO_AGENT_NATS_TEST") == "" {
		t.Skip("set VIDEO_AGENT_NATS_TEST=1 to run the local JetStream integration test")
	}
	name := newID("TEST")
	config := messaging.NATSConfig{
		URL:      messaging.DefaultNATSURL,
		Stream:   name,
		Subject:  "video_agent.test." + name,
		Consumer: "consumer_" + name,
	}
	bus, err := messaging.NewNATSMessageBus(context.Background(), config)
	if err != nil {
		t.Fatalf("NewNATSMessageBus() error = %v", err)
	}
	defer bus.Close()
	defer bus.DeleteStream(context.Background(), config.Stream)

	images, tts, videos := newFakeImages(), newFakeTTS(), newFakeVideos()
	store := NewStore(t.TempDir() + "/workflow.json")
	application, err := NewApplication(store, Clients{
		Planner: testPlanner{}, Image: images, TTS: tts, Video: videos,
		Audit: allowAudit{}, Shield: allowShield{},
	}, nil)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}
	if err := EnsureProject(context.Background(), store, "demo"); err != nil {
		t.Fatalf("EnsureProject() error = %v", err)
	}
	application.SetMessagePublisher(bus)
	application.SetMessageConsumer(bus)
	application.SetJobPollInterval(10 * time.Millisecond)
	defer application.Close()
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				images.succeedAll()
				tts.succeedAll()
				videos.succeedAll()
			case <-done:
				return
			}
		}
	}()

	run, err := application.Runner.StartRun(context.Background(), "demo", RunInput{ProductName: "shoe", Brief: "short video"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	completed := waitForFinalVideoInStore(t, application.Store, run.ID)
	if !hasArtifact(completed, "finalvideo") {
		t.Fatal("poller workflow did not produce finalvideo")
	}
}

func TestNATSMessageMovesToDeadLetter(t *testing.T) {
	if os.Getenv("VIDEO_AGENT_NATS_TEST") == "" {
		t.Skip("set VIDEO_AGENT_NATS_TEST=1 to run the local JetStream integration test")
	}
	name := newID("TEST")
	config := messaging.NATSConfig{
		URL:        messaging.DefaultNATSURL,
		Stream:     name,
		Subject:    "video_agent.test." + name,
		Consumer:   "consumer_" + name,
		MaxRetries: 1,
	}
	bus, err := messaging.NewNATSMessageBus(context.Background(), config)
	if err != nil {
		t.Fatalf("NewNATSMessageBus() error = %v", err)
	}
	defer bus.Close()
	defer bus.DeleteStream(context.Background(), config.Stream)

	consumeContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	consumerDone := make(chan error, 1)
	go func() {
		consumerDone <- bus.Consume(consumeContext, func(context.Context, CallbackMessage) error {
			return errors.New("permanent callback failure")
		})
	}()
	callback := CallbackMessage{Provider: "video", EventID: "event-dead", JobID: "job-dead"}
	if err := bus.Publish(context.Background(), callback); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	stream, err := bus.Stream(context.Background(), config.Stream)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		message, getErr := stream.GetLastMsgForSubject(context.Background(), config.Subject+".dead")
		if getErr == nil {
			var got CallbackMessage
			if err := json.Unmarshal(message.Data, &got); err != nil {
				t.Fatalf("decode dead-letter callback: %v", err)
			}
			if got != callback {
				t.Fatalf("dead-letter callback = %#v, want %#v", got, callback)
			}
			cancel()
			if err := <-consumerDone; err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Consume() error = %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("callback was not moved to the dead-letter subject")
}

func TestNATSApplicationResumesAfterRestart(t *testing.T) {
	if os.Getenv("VIDEO_AGENT_NATS_TEST") == "" {
		t.Skip("set VIDEO_AGENT_NATS_TEST=1 to run the local JetStream integration test")
	}
	name := newID("TEST")
	config := messaging.NATSConfig{
		URL:      messaging.DefaultNATSURL,
		Stream:   name,
		Subject:  "video_agent.test." + name,
		Consumer: "consumer_" + name,
	}
	dataDir := t.TempDir()
	firstBus, err := messaging.NewNATSMessageBus(context.Background(), config)
	if err != nil {
		t.Fatalf("NewNATSMessageBus(first) error = %v", err)
	}
	firstApplication, err := NewLocalApplication(dataDir)
	if err != nil {
		t.Fatalf("NewLocalApplication(first) error = %v", err)
	}
	firstApplication.SetMessageQueue(firstBus, firstBus)
	run, err := firstApplication.Runner.StartRun(context.Background(), "demo", RunInput{ProductName: "shoe", Brief: "restart video"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	var ttsNode NodeRun
	for _, node := range run.NodeRuns {
		if node.NodeID == "tts" && node.InstanceKey != "" {
			ttsNode = node
			break
		}
	}
	if ttsNode.InstanceKey == "" {
		t.Fatalf("run has no concrete TTS node: %#v", run.NodeRuns)
	}
	if _, err := firstApplication.Queue.jobs.Complete(ttsNode.JobID); err != nil {
		t.Fatalf("Complete(tts) error = %v", err)
	}
	callback := CallbackMessage{Provider: "tts", EventID: "local:" + ttsNode.JobID, JobID: ttsNode.JobID}
	if err := firstBus.Publish(context.Background(), callback); err != nil {
		t.Fatalf("Publish(tts callback) error = %v", err)
	}
	if err := firstApplication.Queue.jobs.MarkDelivered(ttsNode.JobID); err != nil {
		t.Fatalf("MarkDelivered(tts) error = %v", err)
	}
	firstApplication.Close()
	if err := firstBus.Close(); err != nil {
		t.Fatalf("first bus Close() error = %v", err)
	}

	secondBus, err := messaging.NewNATSMessageBus(context.Background(), config)
	if err != nil {
		t.Fatalf("NewNATSMessageBus(second) error = %v", err)
	}
	defer secondBus.Close()
	defer secondBus.DeleteStream(context.Background(), config.Stream)
	secondApplication, err := NewLocalApplication(dataDir)
	if err != nil {
		t.Fatalf("NewLocalApplication(second) error = %v", err)
	}
	secondApplication.SetMessageQueue(secondBus, secondBus)
	defer secondApplication.Close()
	if err := secondApplication.Start(context.Background()); err != nil {
		t.Fatalf("Start(second) error = %v", err)
	}
	completed := waitForFinalVideo(t, secondApplication, run.ID)
	if !hasArtifact(completed, "finalvideo") {
		t.Fatal("restarted NATS application did not produce finalvideo")
	}
	if secondApplication.Runner.Metrics.Snapshot()[MonitorCallback] == 0 {
		t.Fatal("restarted application did not consume the persisted NATS callback")
	}
}
