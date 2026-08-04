package videoagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMongoLocalWorkflowCompletes(t *testing.T) {
	uri := os.Getenv("VIDEO_AGENT_MONGO_URI")
	if uri == "" {
		t.Skip("VIDEO_AGENT_MONGO_URI is not set")
	}

	ctx := context.Background()
	database := "video_agent_test_" + newID("mongo")
	dataDir := t.TempDir()
	application, err := NewMongoLocalApplication(dataDir, uri, database, "workflow_state")
	if err != nil {
		t.Fatalf("NewMongoLocalApplication() error = %v", err)
	}
	useDirectCallbackPublisher(application)
	defer application.Close()

	if err := application.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	run, err := application.Runner.StartRun(ctx, "demo", RunInput{ProductName: "shoe", Brief: "short product video"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := application.Store.Get(ctx, run.ID)
		if getErr != nil {
			t.Fatalf("Get() error = %v", getErr)
		}
		if hasArtifact(current, "finalvideo") {
			if _, statErr := os.Stat(filepath.Join(dataDir, "jobs.json")); !os.IsNotExist(statErr) {
				t.Fatalf("Mongo mode should not create jobs.json, stat error = %v", statErr)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Mongo-backed workflow did not produce finalvideo")
}

func TestMongoNodeClaimIsSharedAcrossInstances(t *testing.T) {
	uri := os.Getenv("VIDEO_AGENT_MONGO_URI")
	if uri == "" {
		t.Skip("VIDEO_AGENT_MONGO_URI is not set")
	}

	ctx := context.Background()
	database := "video_agent_claim_test_" + newID("mongo")
	first, err := NewMongoLocalApplication(t.TempDir(), uri, database, "workflow_state")
	if err != nil {
		t.Fatalf("NewMongoLocalApplication(first) error = %v", err)
	}
	defer first.Close()
	second, err := NewMongoLocalApplication(t.TempDir(), uri, database, "workflow_state")
	if err != nil {
		t.Fatalf("NewMongoLocalApplication(second) error = %v", err)
	}
	defer second.Close()

	run := Run{ID: "run-shared-claim", NodeRuns: []NodeRun{{NodeID: "preview", Kind: PreviewNode, InstanceKey: "scene-1", State: Waiting, SubmitStarted: true, SubmitKey: "submit-1"}}}
	if err = first.Store.Create(ctx, run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, claimed, claimErr := first.Store.claimWaiting(run.ID, map[string]bool{}); claimErr != nil || !claimed {
		t.Fatalf("first claimWaiting() = (%t, %v), want claim", claimed, claimErr)
	}
	if _, claimed, claimErr := second.Store.claimSubmitted(run.ID); claimErr != nil || claimed {
		t.Fatalf("second claimSubmitted() = (%t, %v), want active lease", claimed, claimErr)
	}
}
