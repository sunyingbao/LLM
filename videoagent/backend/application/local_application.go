package application

import (
	"context"
	"fmt"
	"path/filepath"

	"eino-cli/videoagent/backend/messaging"
)

// LocalApplication adds the local queue and restart recovery to Application.
type LocalApplication struct {
	*Application
	Queue *LocalQueue
}

func NewLocalApplication(dataDir string) (*LocalApplication, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("local data directory is empty")
	}
	store := NewStore(filepath.Join(dataDir, "workflow.json"))
	return newLocalApplication(dataDir, store, NewLocalJobs(filepath.Join(dataDir, "jobs.json")), nil)
}

func newLocalApplication(dataDir string, store *Store, jobs *LocalJobs, closeStore func() error) (*LocalApplication, error) {
	if dataDir == "" || store == nil || jobs == nil {
		return nil, fmt.Errorf("local data directory, store and jobs are required")
	}
	clients := &LocalClients{jobs: jobs}
	application, err := NewApplication(store, clients.boundaries(), nil)
	if err != nil {
		return nil, err
	}
	application.runtime.Mode = "local"
	application.SetCallbackVerifier(messaging.AllowAllCallbackVerifier{})
	application.SetClose(closeStore)
	queue := NewLocalQueue(jobs, nil)
	clients.queue = queue
	if err := EnsureProject(context.Background(), store, "demo"); err != nil {
		return nil, err
	}
	agent, err := NewCanvasAgent(nil, store)
	if err != nil {
		return nil, err
	}
	application.Agent = agent
	return &LocalApplication{Application: application, Queue: queue}, nil
}

func (clients *LocalClients) boundaries() Clients {
	return Clients{
		Planner: clients,
		Image:   clients,
		TTS:     clients,
		Video:   clients,
		Audit:   clients,
		Shield:  clients,
	}
}

// SetMessageQueue routes local job completion and provider callbacks through the same durable queue.
func (application *LocalApplication) SetMessageQueue(publisher MessagePublisher, consumer MessageConsumer) {
	if application == nil || application.Application == nil {
		return
	}
	application.SetMessagePublisher(publisher)
	application.SetMessageConsumer(consumer)
	if application.Queue != nil {
		application.Queue.publisher = publisher
	}
}

func (application *LocalApplication) Start(ctx context.Context) error {
	if application == nil || application.Runner == nil || application.Store == nil || application.Queue == nil {
		return fmt.Errorf("local application is not initialized")
	}
	if application.Queue.publisher == nil {
		return fmt.Errorf("local message publisher is not configured")
	}
	application.Queue.Start()
	if err := application.Application.Start(ctx); err != nil {
		return err
	}
	return application.Queue.Recover()
}

func (application *LocalApplication) Close() {
	if application != nil && application.Queue != nil {
		application.Queue.Close()
	}
	if application != nil && application.Application != nil {
		_ = application.Application.Close()
	}
}
