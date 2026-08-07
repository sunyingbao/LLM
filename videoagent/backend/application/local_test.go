package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestApplicationStartContinuesAfterOneRunFailsToRestore(t *testing.T) {
	data := emptyStoreData()
	data.Runs["run-1"] = Run{ID: "run-1", NodeRuns: []NodeRun{{NodeID: "done-1", State: Succeeded}}}
	data.Runs["run-2"] = Run{ID: "run-2", NodeRuns: []NodeRun{{NodeID: "done-2", State: Succeeded}}}
	backend := &failFirstStateSave{data: data}
	store := &Store{Backend: backend}
	runner, err := NewRunner(store, Clients{
		Planner: testPlanner{}, Image: newFakeImages(), TTS: newFakeTTS(), Video: newFakeVideos(), Audit: allowAudit{}, Shield: allowShield{},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	application := &Application{Runner: runner, Store: store, pollInterval: time.Millisecond}
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for backend.saves.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if saves := backend.saves.Load(); saves < 3 {
		t.Fatalf("restore saves = %d, want failed run to be retried", saves)
	}
	if got := runner.Metrics.Snapshot()[MonitorRestoreFailed]; got != 1 {
		t.Fatalf("restore failure events = %d, want 1", got)
	}
	if err := application.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

type failFirstStateSave struct {
	data  StoreData
	saves atomic.Int64
}

func (backend *failFirstStateSave) Load() (StoreData, error) { return backend.data, nil }

func (backend *failFirstStateSave) Save(data StoreData) error {
	if backend.saves.Add(1) == 1 {
		return errors.New("injected restore failure")
	}
	backend.data = data
	return nil
}

func TestLocalHTTPWorkflowCompletesFromRequirementToFinalVideo(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	useDirectCallbackPublisher(application)
	defer application.Close()
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	handler := NewHTTPHandler(application)
	payload := []byte(`{"project_id":"new-run-project","product_name":"soft sole shoe","brief":"15 second product video"}`)
	request := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("POST /runs status = %d, want %d", response.Code, http.StatusAccepted)
	}

	var operation CanvasOperation
	if err := json.NewDecoder(response.Body).Decode(&operation); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	confirmRequest := httptest.NewRequest(http.MethodPost, "/operations/"+operation.ID+"/confirm", nil)
	confirm := httptest.NewRecorder()
	handler.ServeHTTP(confirm, confirmRequest)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm run operation status = %d, want %d", confirm.Code, http.StatusOK)
	}
	var started struct {
		Run Run `json:"run"`
	}
	if err := json.NewDecoder(confirm.Body).Decode(&started); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if started.Run.ProjectID != "new-run-project" {
		t.Fatalf("run project = %q, want new-run-project", started.Run.ProjectID)
	}
	run := waitForFinalVideo(t, application, started.Run.ID)
	requirement := artifactByKind(t, run, "requirement")
	var requirementOutput Requirement
	if err := json.Unmarshal(requirement.Data, &requirementOutput); err != nil {
		t.Fatalf("decode requirement artifact: %v", err)
	}
	if !strings.Contains(requirementOutput.Markdown, "15 second product video") {
		t.Fatalf("requirement markdown = %q, want model result", requirementOutput.Markdown)
	}
	clipScript := artifactByKind(t, run, "clipscript")
	preview := artifactByKind(t, run, "preview_video")
	final := artifactByKind(t, run, "finalvideo")
	if !contains(clipScript.ParentIDs, requirement.ID) {
		t.Fatalf("clipscript parents = %#v, want requirement %s", clipScript.ParentIDs, requirement.ID)
	}
	if !contains(preview.ParentIDs, "clipscript") || len(preview.ParentIDs) < 2 {
		t.Fatalf("preview parents = %#v, want clipscript and generated resources", preview.ParentIDs)
	}
	if !contains(final.ParentIDs, preview.ID) {
		t.Fatalf("final parents = %#v, want preview %s", final.ParentIDs, preview.ID)
	}
	if !contains(final.ParentIDs, "clipscript") {
		t.Fatalf("final parents = %#v, want editable clipscript snapshot", final.ParentIDs)
	}

	tts := artifactByKind(t, run, "voice_preview")
	var audio struct {
		PreviewURI string `json:"preview_audio_uri"`
		ExampleURI string `json:"example_audio_uri"`
	}
	if err := json.Unmarshal(tts.Data, &audio); err != nil {
		t.Fatalf("decode tts artifact: %v", err)
	}
	if audio.PreviewURI != audio.ExampleURI {
		t.Fatalf("preview audio = %q, want example audio %q", audio.PreviewURI, audio.ExampleURI)
	}
}

func TestEnsureProjectUpgradesOldDefaultWorkflow(t *testing.T) {
	store := NewStore(t.TempDir() + "/workflow.json")
	old := VideoWorkflow()
	old.Edges = old.Edges[:len(old.Edges)-2]
	project := Project{
		ID: "demo",
		WorkflowVersions: []WorkflowVersion{{
			ID: "workflow-v1", ProjectID: "demo", Revision: 1, Workflow: old,
		}},
		CurrentWorkflowVersion: "workflow-v1",
	}
	if err := store.SaveProject(context.Background(), project); err != nil {
		t.Fatalf("SaveProject() error = %v", err)
	}

	if err := EnsureProject(context.Background(), store, "demo"); err != nil {
		t.Fatalf("EnsureProject() error = %v", err)
	}
	upgraded, err := store.GetProject(context.Background(), "demo")
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if len(upgraded.WorkflowVersions) != 2 {
		t.Fatalf("workflow versions = %d, want 2", len(upgraded.WorkflowVersions))
	}
	latest := upgraded.WorkflowVersions[len(upgraded.WorkflowVersions)-1]
	if err := defaultNodeCatalog().Validate(latest.Workflow); err != nil {
		t.Fatalf("upgraded workflow is invalid: %v", err)
	}
	if !containsWorkflowEdge(latest.Edges, WorkflowEdge{
		FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "finalvideo", ToPort: "clipscript",
	}) {
		t.Fatal("upgraded workflow did not connect clipscript to finalvideo")
	}

	if err := EnsureProject(context.Background(), store, "demo"); err != nil {
		t.Fatalf("second EnsureProject() error = %v", err)
	}
	unchanged, err := store.GetProject(context.Background(), "demo")
	if err != nil {
		t.Fatalf("second GetProject() error = %v", err)
	}
	if len(unchanged.WorkflowVersions) != 2 {
		t.Fatalf("second workflow versions = %d, want 2", len(unchanged.WorkflowVersions))
	}
}

func TestEnsureProjectUpgradesCurrentWorkflowInsteadOfLastVersion(t *testing.T) {
	store := NewStore(t.TempDir() + "/workflow.json")
	old := VideoWorkflow()
	old.Edges = old.Edges[:len(old.Edges)-2]
	unselected := cloneWorkflow(old)
	unselected.Nodes[0].Config = json.RawMessage(`{"instruction":"unselected"}`)
	project := Project{
		ID: "demo",
		WorkflowVersions: []WorkflowVersion{
			{ID: "current", ProjectID: "demo", Revision: 1, Workflow: old},
			{ID: "newer-but-unselected", ProjectID: "demo", Revision: 2, Workflow: unselected},
		},
		CurrentWorkflowVersion: "current",
	}
	if err := store.SaveProject(context.Background(), project); err != nil {
		t.Fatalf("SaveProject() error = %v", err)
	}

	if err := EnsureProject(context.Background(), store, "demo"); err != nil {
		t.Fatalf("EnsureProject() error = %v", err)
	}
	upgraded, err := store.GetProject(context.Background(), "demo")
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	current, err := currentWorkflow(upgraded)
	if err != nil {
		t.Fatalf("currentWorkflow() error = %v", err)
	}
	if string(current.Workflow.Nodes[0].Config) == `{"instruction":"unselected"}` {
		t.Fatal("EnsureProject upgraded the last workflow instead of the selected workflow")
	}
}

func TestHTTPReturnsNodeDefinitions(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	request := httptest.NewRequest(http.MethodGet, "/workflow/node-definitions", nil)
	response := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("node definitions status = %d, want %d", response.Code, http.StatusOK)
	}
	var definitions map[NodeKind]NodeDefinition
	if err := json.NewDecoder(response.Body).Decode(&definitions); err != nil {
		t.Fatalf("decode node definitions: %v", err)
	}
	if _, exists := definitions[ClipScriptNode]; !exists {
		t.Fatalf("clipscript definition is missing: %#v", definitions)
	}
}

func TestHTTPRunCreationIsIdempotent(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	handler := NewHTTPHandler(application)
	firstID := ""
	for index, wantStatus := range []int{http.StatusAccepted, http.StatusOK} {
		request := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewBufferString(`{"project_id":"demo","product_name":"shoe"}`))
		request.Header.Set("Idempotency-Key", "run-once")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var operation CanvasOperation
		decodeErr := json.NewDecoder(response.Body).Decode(&operation)
		if response.Code != wantStatus || decodeErr != nil || operation.ID == "" {
			t.Fatalf("request %d: status=%d operation=%#v decode=%v", index, response.Code, operation, decodeErr)
		}
		if index == 0 {
			firstID = operation.ID
			continue
		}
		if operation.ID != firstID {
			t.Fatalf("reused operation id = %q, want %q", operation.ID, firstID)
		}
	}
}

func TestLocalJobsCancelMarksJobFailed(t *testing.T) {
	jobs := NewLocalJobs(t.TempDir() + "/jobs.json")
	job, _, err := jobs.Submit(PreviewNode, "cancel-once")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := jobs.Cancel(job.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	status, err := jobs.Status(job.ID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != JobFailed || status.Message != "job canceled" {
		t.Fatalf("canceled status = %#v, want failed job canceled", status)
	}
}

func waitForFinalVideo(t *testing.T, application *Application, runID string) Run {
	return waitForFinalVideoInStore(t, application.Store, runID)
}

func waitForFinalVideoInStore(t *testing.T, store *Store, runID string) Run {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, err := store.Get(context.Background(), runID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if hasArtifact(run, "finalvideo") {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("workflow did not produce finalvideo within timeout")
	return Run{}
}

func artifactByKind(t *testing.T, run Run, kind string) Artifact {
	t.Helper()
	for _, node := range run.NodeRuns {
		for _, artifact := range node.Artifacts {
			if artifact.Kind == kind && artifact.Status == string(Succeeded) {
				return artifact
			}
		}
	}
	t.Fatalf("artifact not found: %s", kind)
	return Artifact{}
}

func hasArtifact(run Run, kind string) bool {
	for _, node := range run.NodeRuns {
		for _, artifact := range node.Artifacts {
			if artifact.Kind == kind && artifact.Status == string(Succeeded) {
				return true
			}
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
