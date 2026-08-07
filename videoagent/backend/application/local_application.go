package application

import (
	"context"
	"fmt"
	"path/filepath"

	"eino-cli/videoagent/backend/messaging"
)

func NewLocalApplication(dataDir string) (*Application, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("local data directory is empty")
	}
	store := NewStore(filepath.Join(dataDir, "workflow.json"))
	return newLocalApplication(store, NewLocalJobs(filepath.Join(dataDir, "jobs.json")), nil)
}

func newLocalApplication(store *Store, jobs *LocalJobs, closeStore func() error) (*Application, error) {
	if store == nil || jobs == nil {
		return nil, fmt.Errorf("local store and jobs are required")
	}
	queue := NewLocalQueue(jobs, nil)
	clients := &LocalClients{jobs: jobs, queue: queue}
	application, err := NewApplication(store, Clients{
		Planner: clients,
		Image:   clients,
		TTS:     clients,
		Video:   clients,
		Audit:   clients,
		Shield:  clients,
	})
	if err != nil {
		return nil, err
	}
	application.Queue = queue
	application.SetCallbackVerifier(messaging.AllowAllCallbackVerifier{})
	application.SetClose(closeStore)
	if err := EnsureProject(context.Background(), store, "demo"); err != nil {
		return nil, err
	}
	return application, nil
}
