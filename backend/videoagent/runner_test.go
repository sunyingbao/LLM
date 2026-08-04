package videoagent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestRunCompletesAndReusesTTSExampleAudio(t *testing.T) {
	runner, store, images, tts, videos := testRunner(t)
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe", Brief: "summer video"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	for range 3 {
		images.succeedAll()
		tts.succeedAll()
		videos.succeedAll()
		if err := runner.Poll(context.Background(), run.ID); err != nil {
			t.Fatalf("Poll() error = %v", err)
		}
	}

	run, err = store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	for _, nodeID := range []string{"requirement", "clipscript", "competition", "tts", "character_reference", "preview", "finalvideo"} {
		if node := nodeRun(run, nodeID, ""); node.State != Succeeded {
			t.Fatalf("%s state = %s, want succeeded", nodeID, node.State)
		}
	}

	ttsArtifact := nodeRun(run, "tts", "speaker-1").Artifacts[0]
	var audio map[string]string
	if err := json.Unmarshal(ttsArtifact.Data, &audio); err != nil {
		t.Fatalf("decode tts artifact: %v", err)
	}
	if audio["preview_audio_uri"] != "example://speaker-1" {
		t.Fatalf("preview audio = %q, want callback example audio", audio["preview_audio_uri"])
	}

	competition := nodeRun(run, "competition", "competition-1").Artifacts
	if len(competition) != 2 || competition[1].Kind != "clipscript_annotation" {
		t.Fatalf("competition artifacts = %#v, want image plus separate annotation", competition)
	}
	if got := len(images.submissions); got != 2 {
		t.Fatalf("image submissions = %d, want 2", got)
	}
	if got := len(tts.submissions); got != 1 {
		t.Fatalf("tts submissions = %d, want 1", got)
	}
}

func TestUncertainSubmissionIsRecoveredBySubmitKeyWithoutResubmitting(t *testing.T) {
	runner, store, _, tts, _ := testRunner(t)
	tts.submitErr = fmt.Errorf("connection reset after submit")
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}

	ttsNode := nodeRun(run, "tts", "speaker-1")
	if ttsNode.JobID != "" || ttsNode.State != Waiting {
		t.Fatalf("uncertain tts node = %#v, want waiting without job id", ttsNode)
	}
	tts.submitErr = nil
	tts.jobsByKey[ttsNode.SubmitKey] = SubmittedJob{Provider: "tts", JobID: "tts-recovered"}
	tts.jobs["tts-recovered"] = JobStatus{State: JobSucceeded, ExampleURI: "example://recovered"}
	resumedRunner, err := NewRunner(NewStore(store.path), runner.handler.clients)
	if err != nil {
		t.Fatalf("NewRunner() after restart error = %v", err)
	}
	if err := resumedRunner.Poll(context.Background(), run.ID); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}

	run, err = store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if node := nodeRun(run, "tts", "speaker-1"); node.State != Succeeded || node.JobID != "tts-recovered" {
		t.Fatalf("recovered tts node = %#v", node)
	}
	if got := len(tts.submissions); got != 1 {
		t.Fatalf("tts submissions = %d, want 1", got)
	}
}

func TestReconcileRecoversSubmissionBeforeJobIDIsPersisted(t *testing.T) {
	runner, store, _, tts, _ := testRunner(t)
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	ttsNode := nodeRun(run, "tts", "speaker-1")
	tts.jobs[ttsNode.JobID] = JobStatus{State: JobSucceeded, ExampleURI: "example://speaker-1"}
	if err := store.update(func(data *storeData) error {
		persisted := data.Runs[run.ID]
		index := findNodeRun(persisted, ttsNode)
		persisted.NodeRuns[index].State = Running
		persisted.NodeRuns[index].JobID = ""
		data.Runs[run.ID] = persisted
		return nil
	}); err != nil {
		t.Fatalf("simulate crash window: %v", err)
	}

	resumedRunner, err := NewRunner(NewStore(store.path), runner.handler.clients)
	if err != nil {
		t.Fatalf("NewRunner() after restart error = %v", err)
	}
	if err := resumedRunner.Recover(context.Background(), run.ID); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	run, err = store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if node := nodeRun(run, "tts", "speaker-1"); node.State != Succeeded || node.JobID != ttsNode.JobID {
		t.Fatalf("reconciled tts node = %#v", node)
	}
	if got := len(tts.submissions); got != 1 {
		t.Fatalf("tts submissions = %d, want 1", got)
	}
}

func TestFailedResourceDoesNotBlockItsController(t *testing.T) {
	runner, store, images, tts, _ := testRunner(t)
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	competition := nodeRun(run, "competition", "competition-1")
	character := nodeRun(run, "character_reference", "character-1")
	ttsNode := nodeRun(run, "tts", "speaker-1")
	images.jobs[competition.JobID] = JobStatus{State: JobFailed, Message: "image provider failed"}
	images.jobs[character.JobID] = JobStatus{State: JobSucceeded, URI: "image://character"}
	tts.jobs[ttsNode.JobID] = JobStatus{State: JobSucceeded, ExampleURI: "example://speaker-1"}
	if err := runner.Poll(context.Background(), run.ID); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}

	run, err = store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if node := nodeRun(run, "competition", "competition-1"); node.State != Failed {
		t.Fatalf("competition child state = %s, want failed", node.State)
	}
	if node := nodeRun(run, "competition", ""); node.State != Succeeded {
		t.Fatalf("competition controller state = %s, want succeeded", node.State)
	}
}

func TestCallbackIsIdempotentAndCharacterUsesOneFallback(t *testing.T) {
	runner, store, images, tts, _ := testRunner(t)
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}

	ttsNode := nodeRun(run, "tts", "speaker-1")
	tts.jobs[ttsNode.JobID] = JobStatus{State: JobSucceeded, ExampleURI: "example://speaker-1"}
	if err := runner.OnCallback(context.Background(), "tts", "event-1", ttsNode.JobID); err != nil {
		t.Fatalf("OnCallback() error = %v", err)
	}
	if err := runner.OnCallback(context.Background(), "tts", "event-1", ttsNode.JobID); err != nil {
		t.Fatalf("duplicate OnCallback() error = %v", err)
	}
	if tts.gets != 1 {
		t.Fatalf("tts refresh count = %d, want 1", tts.gets)
	}

	character := nodeRun(run, "character_reference", "character-1")
	images.jobs[character.JobID] = JobStatus{State: JobFailed, Message: "primary failed"}
	images.submitErrors["fallback"] = fmt.Errorf("connection reset after fallback submit")
	if err := runner.Poll(context.Background(), run.ID); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	run, err = store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	character = nodeRun(run, "character_reference", "character-1")
	if !character.FallbackSubmitted || character.JobID != "" {
		t.Fatalf("character fallback = %#v, want uncertain fallback without a job id", character)
	}
	delete(images.submitErrors, "fallback")
	images.jobsByKey[character.SubmitKey+":fallback"] = SubmittedJob{Provider: "image", JobID: "image-fallback"}
	images.jobs["image-fallback"] = JobStatus{State: JobSucceeded, URI: "image://fallback", URL: "https://example/fallback"}
	if err := runner.Poll(context.Background(), run.ID); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	run, err = store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	character = nodeRun(run, "character_reference", "character-1")
	if character.State != Succeeded || !character.FallbackSubmitted {
		t.Fatalf("character result = %#v, want succeeded after one fallback", character)
	}
	if got := len(images.submissions); got != 3 {
		t.Fatalf("image submissions = %d, want competition + primary + fallback", got)
	}
}

func testRunner(t *testing.T) (*Runner, *Store, *fakeImages, *fakeTTS, *fakeVideos) {
	t.Helper()
	images := newFakeImages()
	tts := newFakeTTS()
	videos := newFakeVideos()
	runner, err := NewRunner(NewStore(t.TempDir()+"/workflow.json"), Clients{
		Planner: testPlanner{},
		Image:   images,
		TTS:     tts,
		Video:   videos,
		Audit:   allowAudit{},
		Shield:  allowShield{},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	return runner, runner.store, images, tts, videos
}

func nodeRun(run Run, nodeID, instanceKey string) NodeRun {
	for _, node := range run.NodeRuns {
		if node.NodeID == nodeID && node.InstanceKey == instanceKey {
			return node
		}
	}
	panic(fmt.Sprintf("node not found: %s/%s", nodeID, instanceKey))
}

type testPlanner struct{}

func (testPlanner) AnalyzeRequirement(context.Context, RunInput) (Requirement, error) {
	return Requirement{Objective: "sell shoes"}, nil
}

func (testPlanner) CreateClipScript(context.Context, Requirement) (ClipScript, error) {
	return ClipScript{Title: "shoe story", Scenes: []Scene{{ID: "scene-1", Voiceover: "comfortable shoes"}}}, nil
}

func (testPlanner) PlanCompetition(context.Context, ClipScript, RunInput) ([]ResourcePlan, error) {
	return []ResourcePlan{{ID: "competition-1", Prompt: "shoe on street", Model: "primary"}}, nil
}

func (testPlanner) PlanTTS(context.Context, ClipScript) ([]ResourcePlan, error) {
	return []ResourcePlan{{ID: "speaker-1", Speaker: "narrator", Text: "comfortable shoes"}}, nil
}

func (testPlanner) PlanCharacterReferences(context.Context, ClipScript, RunInput) ([]ResourcePlan, error) {
	return []ResourcePlan{{ID: "character-1", Prompt: "young woman", Model: "primary", FallbackModel: "fallback"}}, nil
}

type fakeImages struct {
	submissions  []ImageRequest
	jobsByKey    map[string]SubmittedJob
	jobs         map[string]JobStatus
	submitErrors map[string]error
}

func newFakeImages() *fakeImages {
	return &fakeImages{jobsByKey: make(map[string]SubmittedJob), jobs: make(map[string]JobStatus), submitErrors: make(map[string]error)}
}

func (images *fakeImages) SubmitImage(_ context.Context, request ImageRequest) (SubmittedJob, error) {
	images.submissions = append(images.submissions, request)
	if err := images.submitErrors[request.Model]; err != nil {
		return SubmittedJob{}, err
	}
	job := SubmittedJob{Provider: "image", JobID: fmt.Sprintf("image-%d", len(images.submissions))}
	images.jobsByKey[request.SubmitKey] = job
	images.jobs[job.JobID] = JobStatus{State: JobPending}
	return job, nil
}

func (images *fakeImages) GetImage(_ context.Context, jobID string) (JobStatus, error) {
	return images.jobs[jobID], nil
}

func (images *fakeImages) FindImageBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	job, found := images.jobsByKey[key]
	return job, found, nil
}

func (images *fakeImages) succeedAll() {
	for jobID := range images.jobs {
		images.jobs[jobID] = JobStatus{State: JobSucceeded, URI: "image://" + jobID, URL: "https://example/" + jobID}
	}
}

type fakeTTS struct {
	submissions []TTSRequest
	jobsByKey   map[string]SubmittedJob
	jobs        map[string]JobStatus
	submitErr   error
	gets        int
}

func newFakeTTS() *fakeTTS {
	return &fakeTTS{jobsByKey: make(map[string]SubmittedJob), jobs: make(map[string]JobStatus)}
}

func (tts *fakeTTS) SubmitTTS(_ context.Context, request TTSRequest) (SubmittedJob, error) {
	tts.submissions = append(tts.submissions, request)
	if tts.submitErr != nil {
		return SubmittedJob{}, tts.submitErr
	}
	job := SubmittedJob{Provider: "tts", JobID: fmt.Sprintf("tts-%d", len(tts.submissions))}
	tts.jobsByKey[request.SubmitKey] = job
	tts.jobs[job.JobID] = JobStatus{State: JobPending}
	return job, nil
}

func (tts *fakeTTS) GetTTS(_ context.Context, jobID string) (JobStatus, error) {
	tts.gets++
	return tts.jobs[jobID], nil
}

func (tts *fakeTTS) FindTTSBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	job, found := tts.jobsByKey[key]
	return job, found, nil
}

func (tts *fakeTTS) succeedAll() {
	for jobID := range tts.jobs {
		tts.jobs[jobID] = JobStatus{State: JobSucceeded, ExampleURI: "example://speaker-1"}
	}
}

type fakeVideos struct {
	jobsByKey map[string]SubmittedJob
	jobs      map[string]JobStatus
}

func newFakeVideos() *fakeVideos {
	return &fakeVideos{jobsByKey: make(map[string]SubmittedJob), jobs: make(map[string]JobStatus)}
}

func (videos *fakeVideos) SubmitPreview(_ context.Context, request VideoRequest) (SubmittedJob, error) {
	return videos.submit("preview", request.SubmitKey)
}

func (videos *fakeVideos) GetPreview(_ context.Context, jobID string) (JobStatus, error) {
	return videos.jobs[jobID], nil
}

func (videos *fakeVideos) FindPreviewBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	job, found := videos.jobsByKey[key]
	return job, found, nil
}

func (videos *fakeVideos) SubmitFinalVideo(_ context.Context, request VideoRequest) (SubmittedJob, error) {
	return videos.submit("finalvideo", request.SubmitKey)
}

func (videos *fakeVideos) GetFinalVideo(_ context.Context, jobID string) (JobStatus, error) {
	return videos.jobs[jobID], nil
}

func (videos *fakeVideos) FindFinalVideoBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	job, found := videos.jobsByKey[key]
	return job, found, nil
}

func (videos *fakeVideos) submit(kind, key string) (SubmittedJob, error) {
	job := SubmittedJob{Provider: "video", JobID: fmt.Sprintf("%s-%d", kind, len(videos.jobs)+1)}
	videos.jobsByKey[key] = job
	videos.jobs[job.JobID] = JobStatus{State: JobPending}
	return job, nil
}

func (videos *fakeVideos) succeedAll() {
	for jobID := range videos.jobs {
		videos.jobs[jobID] = JobStatus{State: JobSucceeded, URI: "video://" + jobID, URL: "https://example/" + jobID}
	}
}

type allowAudit struct{}

func (allowAudit) CheckImage(context.Context, string) error { return nil }

type allowShield struct{}

func (allowShield) CheckPrompt(context.Context, string) error { return nil }
