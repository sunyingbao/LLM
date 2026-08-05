package videoagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func TestMongoConcurrentProjectUpdatesKeepBothVersions(t *testing.T) {
	uri := os.Getenv("VIDEO_AGENT_MONGO_URI")
	if uri == "" {
		t.Skip("VIDEO_AGENT_MONGO_URI is not set")
	}

	ctx := context.Background()
	database := "video_agent_project_test_" + newID("mongo")
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
	if err := EnsureProject(ctx, first.Store, "demo"); err != nil {
		t.Fatalf("EnsureProject() error = %v", err)
	}

	stores := []*Store{first.Store, second.Store}
	var group sync.WaitGroup
	errors := make(chan error, len(stores))
	for index, store := range stores {
		group.Add(1)
		go func(index int, store *Store) {
			defer group.Done()
			_, updateErr := store.updateProject("demo", false, func(project *Project) error {
				version := WorkflowVersion{
					ID:        fmt.Sprintf("concurrent-%d", index),
					ProjectID: "demo",
					Revision:  len(project.WorkflowVersions) + 1,
					Workflow:  cloneWorkflow(VideoWorkflow()),
				}
				project.WorkflowVersions = append(project.WorkflowVersions, version)
				project.CurrentWorkflowVersion = version.ID
				return nil
			})
			errors <- updateErr
		}(index, store)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("updateProject() error = %v", err)
		}
	}

	project, err := first.Store.GetProject(ctx, "demo")
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if len(project.WorkflowVersions) != 3 {
		t.Fatalf("workflow versions = %d, want initial plus two concurrent versions", len(project.WorkflowVersions))
	}
}

func TestMongoEnsureProjectDoesNotOverwriteConcurrentWorkflow(t *testing.T) {
	uri := os.Getenv("VIDEO_AGENT_MONGO_URI")
	if uri == "" {
		t.Skip("VIDEO_AGENT_MONGO_URI is not set")
	}

	ctx := context.Background()
	database := "video_agent_ensure_project_test_" + newID("mongo")
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

	var group sync.WaitGroup
	group.Add(2)
	var ensureErr error
	var saved WorkflowVersion
	var saveErr error
	go func() {
		defer group.Done()
		ensureErr = EnsureProject(ctx, first.Store, "demo")
	}()
	go func() {
		defer group.Done()
		saved, saveErr = second.Runner.SaveWorkflow(ctx, "demo", VideoWorkflow())
	}()
	group.Wait()
	if ensureErr != nil || saveErr != nil {
		t.Fatalf("concurrent initialization errors = (%v, %v)", ensureErr, saveErr)
	}

	project, err := first.Store.GetProject(ctx, "demo")
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	found := false
	for _, version := range project.WorkflowVersions {
		found = found || version.ID == saved.ID
	}
	if !found {
		t.Fatalf("concurrently saved workflow %s was overwritten: %#v", saved.ID, project.WorkflowVersions)
	}
}

func TestMongoClaimTakeoverCancelsPreviousExecution(t *testing.T) {
	uri := os.Getenv("VIDEO_AGENT_MONGO_URI")
	if uri == "" {
		t.Skip("VIDEO_AGENT_MONGO_URI is not set")
	}

	ctx := context.Background()
	database := "video_agent_claim_fencing_test_" + newID("mongo")
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

	run := Run{ID: "run-claim-fencing", NodeRuns: []NodeRun{{NodeID: "requirement", Kind: RequirementNode, State: Pending}}}
	if err := first.Store.Create(ctx, run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	command, claimed, err := first.Store.claimReady(run.ID)
	if err != nil || !claimed {
		t.Fatalf("claimReady() = (%#v, %t, %v)", command, claimed, err)
	}
	if err := second.Store.update(func(data *storeData) error {
		current := data.Runs[run.ID]
		current.NodeRuns[0].ClaimToken = "replacement-claim"
		now := time.Now().UTC()
		current.NodeRuns[0].ClaimedAt = &now
		data.Runs[run.ID] = current
		return nil
	}); err != nil {
		t.Fatalf("replace claim error = %v", err)
	}

	first.Runner.claimHeartbeat = time.Millisecond
	_, err = first.Runner.withClaimHeartbeat(ctx, command, func(executeCtx context.Context, _ Command) (Result, error) {
		<-executeCtx.Done()
		return Result{}, context.Cause(executeCtx)
	})
	if !errors.Is(err, ErrClaimHeartbeat) {
		t.Fatalf("withClaimHeartbeat() error = %v, want ErrClaimHeartbeat", err)
	}
}
