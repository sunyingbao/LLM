package videoagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWaitingNodeClaimCannotBeTakenTwice(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	run := Run{ID: "run-lease", NodeRuns: []NodeRun{{NodeID: "preview", Kind: PreviewNode, InstanceKey: "scene-1", State: Waiting, SubmitStarted: true, SubmitKey: "submit-1"}}}
	if err := store.Create(context.Background(), run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	command, claimed, err := store.claimWaiting(run.ID, map[string]bool{})
	if err != nil || !claimed {
		t.Fatalf("claimWaiting() = (%#v, %t, %v), want claim", command, claimed, err)
	}
	if _, claimed, err = store.claimSubmitted(run.ID); err != nil || claimed {
		t.Fatalf("claimSubmitted() while lease is active = (%t, %v), want no claim", claimed, err)
	}
	if err = store.apply(command, Result{State: Waiting}); err != nil {
		t.Fatalf("apply() error = %v", err)
	}
	if _, claimed, err = store.claimWaiting(run.ID, map[string]bool{}); err != nil || !claimed {
		t.Fatalf("claimWaiting() after release = (%t, %v), want claim", claimed, err)
	}
}

func TestExpiredNodeClaimRejectsStaleResult(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	run := Run{ID: "run-stale-lease", NodeRuns: []NodeRun{{NodeID: "preview", Kind: PreviewNode, InstanceKey: "scene-1", State: Waiting, SubmitStarted: true, SubmitKey: "submit-1"}}}
	if err := store.Create(context.Background(), run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	stale, claimed, err := store.claimWaiting(run.ID, map[string]bool{})
	if err != nil || !claimed {
		t.Fatalf("claimWaiting() = (%#v, %t, %v), want claim", stale, claimed, err)
	}
	if err = store.update(func(data *storeData) error {
		current := data.Runs[run.ID]
		expired := time.Now().Add(-nodeClaimTTL - time.Second)
		current.NodeRuns[0].ClaimedAt = &expired
		data.Runs[run.ID] = current
		return nil
	}); err != nil {
		t.Fatalf("expire claim: %v", err)
	}
	fresh, claimed, err := store.claimSubmitted(run.ID)
	if err != nil || !claimed {
		t.Fatalf("claimSubmitted() = (%#v, %t, %v), want expired claim", fresh, claimed, err)
	}
	if err = store.apply(stale, Result{State: Succeeded}); err == nil {
		t.Fatal("stale apply succeeded")
	}
	if err = store.apply(fresh, Result{State: Waiting}); err != nil {
		t.Fatalf("fresh apply() error = %v", err)
	}
}

func TestRecoverDoesNotStealActiveNodeClaim(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	run := Run{ID: "run-active-lease", NodeRuns: []NodeRun{{NodeID: "preview", Kind: PreviewNode, InstanceKey: "scene-1", State: Waiting, SubmitStarted: true, SubmitKey: "submit-1"}}}
	if err := store.Create(context.Background(), run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	command, claimed, err := store.claimWaiting(run.ID, map[string]bool{})
	if err != nil || !claimed {
		t.Fatalf("claimWaiting() = (%#v, %t, %v), want claim", command, claimed, err)
	}
	if err = store.recover(run.ID); err != nil {
		t.Fatalf("recover() error = %v", err)
	}
	persisted, err := store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	node := persisted.NodeRuns[0]
	if node.State != Running || node.ClaimToken != command.NodeRun.ClaimToken {
		t.Fatalf("recovered node = %#v, want active claim preserved", node)
	}
}

func TestSubmitKeepsKnownJobWhenSubmissionReceiptCannotBeSaved(t *testing.T) {
	backend := &flakyStateBackend{data: emptyStoreData(), failAt: map[int]bool{1: true}}
	store := &Store{backend: backend}
	tts := newFakeTTS()
	handler := nodeHandler{clients: Clients{TTS: tts}, store: store}
	command := Command{RunID: "run-1", NodeRun: NodeRun{NodeID: "tts", Kind: PromptTTSNode, InstanceKey: "scene-1", SubmitKey: "submit-1"}}

	result, err := handler.submitTTS(context.Background(), command, ResourcePlan{ID: "scene-1", Text: "hello"})
	if err != nil {
		t.Fatalf("submitTTS() error = %v", err)
	}
	if result.State != Waiting || result.JobID == "" || result.SubmissionUnknown {
		t.Fatalf("submitTTS() = %#v, want known waiting job", result)
	}
}

func TestSubmitWithoutJobIDBecomesUnknownFailure(t *testing.T) {
	handler := nodeHandler{clients: Clients{TTS: emptyJobTTS{}}}
	command := Command{RunID: "run-1", NodeRun: NodeRun{NodeID: "tts", Kind: PromptTTSNode, InstanceKey: "scene-1", SubmitKey: "submit-1"}}

	result, err := handler.submitTTS(context.Background(), command, ResourcePlan{ID: "scene-1", Text: "hello"})
	if err != nil {
		t.Fatalf("submitTTS() error = %v", err)
	}
	if result.State != Failed || !result.SubmissionUnknown {
		t.Fatalf("submitTTS() = %#v, want unknown submission failure", result)
	}
}

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
	var audio struct {
		PreviewURI string `json:"preview_audio_uri"`
	}
	if err := json.Unmarshal(ttsArtifact.Data, &audio); err != nil {
		t.Fatalf("decode tts artifact: %v", err)
	}
	if audio.PreviewURI != "example://speaker-1" {
		t.Fatalf("preview audio = %q, want callback example audio", audio.PreviewURI)
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
	metrics := runner.Metrics.Snapshot()
	if metrics[MonitorNodeStarted] == 0 || metrics[MonitorNodeCompleted] == 0 {
		t.Fatalf("execution metrics = %#v, want node start and completion events", metrics)
	}
}

func TestSynchronousTTSResultCompletesWithoutPolling(t *testing.T) {
	runner, _, _, _, _ := testRunner(t)
	runner.handler.clients.TTS = immediateTTS{}

	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	node := nodeRun(run, "tts", "speaker-1")
	if node.State != Succeeded || node.JobID != "" {
		t.Fatalf("synchronous tts node = %#v, want succeeded without job id", node)
	}
	var audio struct {
		PreviewURL string `json:"preview_audio_url"`
	}
	if err := json.Unmarshal(node.Artifacts[0].Data, &audio); err != nil {
		t.Fatalf("decode tts artifact: %v", err)
	}
	if audio.PreviewURL != "https://example/preview.wav" {
		t.Fatalf("preview audio = %q", audio.PreviewURL)
	}
}

func TestUncertainSubmissionIsRecoveredFromMQBySubmitKeyWithoutResubmitting(t *testing.T) {
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
	message := CallbackMessage{Provider: "tts", EventID: "reconcile:" + ttsNode.SubmitKey, SubmitKey: ttsNode.SubmitKey}
	if err := runner.ProcessCallback(context.Background(), message); err != nil {
		t.Fatalf("ProcessCallback() error = %v", err)
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

func TestCallbackMonitorIncludesClaimedNode(t *testing.T) {
	runner, _, _, tts, _ := testRunner(t)
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	ttsNode := nodeRun(run, "tts", "speaker-1")
	tts.jobs[ttsNode.JobID] = JobStatus{State: JobSucceeded, ExampleURI: "example://speaker-1"}

	var callback RunEvent
	runner.SetMonitor(MonitorFunc(func(_ context.Context, event RunEvent) {
		if event.Action == MonitorCallback {
			callback = event
		}
	}))
	if err := runner.ProcessCallback(context.Background(), CallbackMessage{Provider: ttsNode.Provider, EventID: "done:" + ttsNode.JobID, JobID: ttsNode.JobID}); err != nil {
		t.Fatalf("ProcessCallback() error = %v", err)
	}
	if callback.RunID != run.ID || callback.NodeID != ttsNode.NodeID || callback.Kind != ttsNode.Kind || callback.Provider != ttsNode.Provider {
		t.Fatalf("callback event = %#v, want claimed node identity", callback)
	}
}

func TestSubmitAndReconcileFailureStaysVisibleAndRecoverable(t *testing.T) {
	runner, store, _, tts, _ := testRunner(t)
	tts.submitErr = fmt.Errorf("connection reset after submit")
	tts.findErr = fmt.Errorf("reconcile unavailable")
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}

	node := nodeRun(run, "tts", "speaker-1")
	if node.State != Waiting || node.JobID != "" || !node.SubmissionUnknown {
		t.Fatalf("uncertain tts node = %#v, want waiting without job id", node)
	}
	if node.Message == "" {
		t.Fatal("uncertain tts submission lost its reconciliation error")
	}

	if err := runner.Poll(context.Background(), run.ID); err == nil {
		t.Fatal("Poll() error = nil, want reconciliation error")
	}
	stored, err := store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if node := nodeRun(stored, "tts", "speaker-1"); node.State != Waiting {
		t.Fatalf("tts state after failed reconciliation = %s, want waiting", node.State)
	}
	if got := runner.Metrics.Snapshot()[MonitorReconcileFailed]; got != 1 {
		t.Fatalf("reconcile failure events = %d, want 1", got)
	}
	if got := runner.Metrics.Snapshot()[MonitorNodeFailed]; got != 0 {
		t.Fatalf("node failure events = %d, want 0 for a recoverable reconciliation error", got)
	}
	if got := runner.Metrics.Snapshot()[MonitorSubmissionUnknown]; got != 1 {
		t.Fatalf("submission unknown events = %d, want 1", got)
	}
}

func TestRetryDoesNotDuplicateUnknownSubmission(t *testing.T) {
	runner, store, _, tts, _ := testRunner(t)
	tts.submitErr = fmt.Errorf("connection reset after submit")
	tts.findErr = ErrSubmitReconciliationUnsupported
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}

	node := nodeRun(run, "tts", "speaker-1")
	if node.State != Failed || !node.SubmissionUnknown {
		t.Fatalf("unknown submission node = %#v, want failed and protected from retry", node)
	}
	if err := runner.Retry(context.Background(), run.ID); err == nil {
		t.Fatal("Retry() error = nil, want unsafe retry rejection")
	}
	stored, err := store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if node := nodeRun(stored, "tts", "speaker-1"); node.State != Failed || !node.SubmissionUnknown {
		t.Fatalf("node after rejected retry = %#v", node)
	}
	if got := len(tts.submissions); got != 1 {
		t.Fatalf("tts submissions = %d, want 1", got)
	}
	if got := runner.Metrics.Snapshot()[MonitorSubmissionUnknown]; got != 1 {
		t.Fatalf("submission unknown events = %d, want 1", got)
	}
}

func TestRestoreRecoversSubmissionBeforeJobIDIsPersisted(t *testing.T) {
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
	tts.findErr = ErrSubmitReconciliationUnsupported

	resumedRunner, err := NewRunner(NewStore(store.path), runner.handler.clients)
	if err != nil {
		t.Fatalf("NewRunner() after restart error = %v", err)
	}
	if err := resumedRunner.Restore(context.Background(), run.ID); err != nil {
		t.Fatalf("Restore() error = %v", err)
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

func TestRestoreCancelsNodesBlockedByPersistedFailure(t *testing.T) {
	runner, store, _, _, _ := testRunner(t)
	run := Run{
		ID:       "legacy-failed-run",
		Workflow: WorkflowVersion{Workflow: VideoWorkflow()},
		NodeRuns: []NodeRun{
			{NodeID: "requirement", Kind: RequirementNode, State: Failed},
			{NodeID: "clipscript", Kind: ClipScriptNode, State: Pending},
			{NodeID: "preview", Kind: PreviewNode, State: Pending},
			{NodeID: "finalvideo", Kind: FinalVideoNode, State: Pending},
		},
	}
	if err := store.Create(context.Background(), run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := runner.Restore(context.Background(), run.ID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	restored, err := store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	for _, nodeID := range []string{"clipscript", "preview", "finalvideo"} {
		if node := nodeRun(restored, nodeID, ""); node.State != Canceled {
			t.Fatalf("%s state = %s, want canceled", nodeID, node.State)
		}
	}
}

func TestRefreshUsesPersistedSynchronousSubmission(t *testing.T) {
	store := NewStore(t.TempDir() + "/workflow.json")
	handler := nodeHandler{clients: Clients{TTS: immediateTTS{}}, store: store}
	command := Command{RunID: "run-1", NodeRun: NodeRun{
		NodeID: "tts", InstanceKey: "speaker-1", Kind: PromptTTSNode,
		State: Running, SubmitStarted: true, SubmitKey: "submit-1",
	}}
	plan := ResourcePlan{ID: "speaker-1", SceneID: "scene-1", Text: "hello"}

	result, err := handler.submitTTS(context.Background(), command, plan)
	if err != nil || result.State != Succeeded {
		t.Fatalf("submitTTS() = (%#v, %v), want succeeded", result, err)
	}
	result, err = handler.refreshTTS(context.Background(), command, plan)
	if err != nil || result.State != Succeeded {
		t.Fatalf("refreshTTS() = (%#v, %v), want persisted synchronous result", result, err)
	}
}

func TestClaimReadyDoesNotReturnStaleCommandAfterCASRetry(t *testing.T) {
	backend := &claimConflictBackend{data: emptyStoreData()}
	backend.data.Runs["run-1"] = Run{ID: "run-1", NodeRuns: []NodeRun{{NodeID: "requirement", Kind: RequirementNode, State: Pending}}}
	store := &Store{backend: backend}

	command, claimed, err := store.claimReady("run-1")
	if err != nil {
		t.Fatalf("claimReady() error = %v", err)
	}
	if claimed || command.RunID != "" {
		t.Fatalf("claimReady() = (%#v, %t), want no stale claim", command, claimed)
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

func TestCallbackRetriesAfterRefreshFailure(t *testing.T) {
	runner, _, _, tts, _ := testRunner(t)
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}

	ttsNode := nodeRun(run, "tts", "speaker-1")
	tts.getErr = fmt.Errorf("temporary status failure")
	if err := runner.OnCallback(context.Background(), "tts", "event-retry", ttsNode.JobID); err == nil {
		t.Fatal("first OnCallback() error = nil, want refresh failure")
	}
	tts.getErr = nil
	tts.jobs[ttsNode.JobID] = JobStatus{State: JobSucceeded, ExampleURI: "example://speaker-1"}
	if err := runner.OnCallback(context.Background(), "tts", "event-retry", ttsNode.JobID); err != nil {
		t.Fatalf("retried OnCallback() error = %v", err)
	}
	if tts.gets != 2 {
		t.Fatalf("tts refresh count = %d, want 2", tts.gets)
	}
}

func TestCallbackWaitsForDurableJob(t *testing.T) {
	runner, _, _, _, _ := testRunner(t)
	err := runner.OnCallback(context.Background(), "tts", "event-early", "job-not-persisted")
	if !errors.Is(err, ErrCallbackNotReady) {
		t.Fatalf("OnCallback() error = %v, want ErrCallbackNotReady", err)
	}
}

func TestCallbackRequeuesWhenRefreshResultCannotBeSaved(t *testing.T) {
	baseRunner, _, _, _, _ := testRunner(t)
	backend := &flakyStateBackend{data: emptyStoreData()}
	store := &Store{backend: backend}
	runner, err := NewRunner(store, baseRunner.handler.clients)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	tts := runner.handler.clients.TTS.(*fakeTTS)
	ttsNode := nodeRun(run, "tts", "speaker-1")
	tts.jobs[ttsNode.JobID] = JobStatus{State: JobSucceeded, ExampleURI: "example://speaker-1"}
	backend.failAt = map[int]bool{
		backend.saves + 2: true,
		backend.saves + 3: true,
	}

	if err := runner.OnCallback(context.Background(), "tts", "event-save", ttsNode.JobID); err == nil {
		t.Fatal("OnCallback() error = nil, want store failure")
	}
	stored, err := store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if node := nodeRun(stored, "tts", "speaker-1"); node.State != Running {
		t.Fatalf("tts state after apply and requeue failures = %s, want running", node.State)
	}
	if got := runner.Metrics.Snapshot()[MonitorReconcileFailed]; got != 1 {
		t.Fatalf("reconcile failure events = %d, want 1 after apply failure", got)
	}
	if err := runner.OnCallback(context.Background(), "tts", "event-save", ttsNode.JobID); err != nil {
		t.Fatalf("retried OnCallback() error = %v", err)
	}
}

func TestCancelStopsPendingNodesAndRejectsRetry(t *testing.T) {
	runner, store, _, _, _ := testRunner(t)
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	if err := runner.Cancel(context.Background(), run.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if err := runner.Advance(context.Background(), run.ID); err != nil {
		t.Fatalf("Advance() after cancel error = %v", err)
	}
	run, err = store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !run.Canceled {
		t.Fatal("run is not marked canceled")
	}
	for _, node := range run.NodeRuns {
		if !node.State.terminal() {
			t.Fatalf("node %s remains active after cancel: %s", node.NodeID, node.State)
		}
	}
	if err := runner.Retry(context.Background(), run.ID); err == nil {
		t.Fatal("Retry() after cancel succeeded, want error")
	}
}

func TestCancelRejectsCompletedRun(t *testing.T) {
	runner, store, _, _, _ := testRunner(t)
	run := Run{ID: "completed-run", NodeRuns: []NodeRun{{NodeID: "requirement", State: Succeeded}}}
	if err := store.Create(context.Background(), run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := runner.Cancel(context.Background(), run.ID); err == nil {
		t.Fatal("Cancel() completed run error = nil")
	}
	stored, err := store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Canceled || stored.CancelRequested {
		t.Fatalf("completed run changed by cancel: %#v", stored)
	}
}

func TestCancelStaysRequestedWhenProviderDoesNotSupportCancellation(t *testing.T) {
	images := newFakeImages()
	runner, err := NewRunner(NewStore(t.TempDir()+"/workflow.json"), Clients{
		Planner: testPlanner{}, Image: imageWithoutCancel{client: images}, TTS: newFakeTTS(),
		Video: newFakeVideos(), Audit: allowAudit{}, Shield: allowShield{},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	if err := runner.Cancel(context.Background(), run.ID); !errors.Is(err, ErrCancellationUnsupported) {
		t.Fatalf("Cancel() error = %v, want ErrCancellationUnsupported", err)
	}
	run, err = runner.store.Get(context.Background(), run.ID)
	if err != nil || run.Canceled || !run.CancelRequested {
		t.Fatalf("cancel-pending run = (%#v, %v)", run, err)
	}
	if got := runner.Metrics.Snapshot()[MonitorCancelFailed]; got != 1 {
		t.Fatalf("cancel failure events = %d, want 1", got)
	}
}

func TestCallbacksSettleJobsWhileCancellationIsPending(t *testing.T) {
	images := newFakeImages()
	runner, err := NewRunner(NewStore(t.TempDir()+"/workflow.json"), Clients{
		Planner: testPlanner{}, Image: imageWithoutCancel{client: images}, TTS: newFakeTTS(),
		Video: newFakeVideos(), Audit: allowAudit{}, Shield: allowShield{},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	workflow := Workflow{
		Nodes: []WorkflowNode{
			{ID: "requirement", Kind: RequirementNode},
			{ID: "clipscript", Kind: ClipScriptNode},
			{ID: "competition", Kind: CompetitionReferenceNode},
		},
		Edges: []WorkflowEdge{
			{FromNodeID: "requirement", FromPort: "requirement", ToNodeID: "clipscript", ToPort: "requirement"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "competition", ToPort: "clipscript"},
		},
	}
	run, err := runner.startWorkflow(context.Background(), "project-1", workflow, RunInput{ProductName: "shoe"}, "cancel-callback-run")
	if err != nil {
		t.Fatalf("startWorkflow() error = %v", err)
	}
	job := nodeRun(run, "competition", "competition-1")
	if err := runner.Cancel(context.Background(), run.ID); !errors.Is(err, ErrCancellationUnsupported) {
		t.Fatalf("Cancel() error = %v, want ErrCancellationUnsupported", err)
	}
	images.jobs[job.JobID] = JobStatus{State: JobSucceeded, URI: "image://completed"}
	if err := runner.ProcessCallback(context.Background(), CallbackMessage{Provider: job.Provider, EventID: "done:" + job.JobID, JobID: job.JobID}); err != nil {
		t.Fatalf("ProcessCallback() error = %v", err)
	}

	stored := mustGetRun(t, runner.store, run.ID)
	if !stored.Canceled || stored.CancelRequested {
		t.Fatalf("run after terminal callback = %#v, want completed cancellation", stored)
	}
	if node := nodeRun(stored, "competition", "competition-1"); node.State != Succeeded {
		t.Fatalf("completed remote node state = %s, want succeeded", node.State)
	}
}

func TestPollingSettlesJobsWhileCancellationIsPending(t *testing.T) {
	images := newFakeImages()
	runner, err := NewRunner(NewStore(t.TempDir()+"/workflow.json"), Clients{
		Planner: testPlanner{}, Image: imageWithoutCancel{client: images}, TTS: newFakeTTS(),
		Video: newFakeVideos(), Audit: allowAudit{}, Shield: allowShield{},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	workflow := Workflow{
		Nodes: []WorkflowNode{
			{ID: "requirement", Kind: RequirementNode},
			{ID: "clipscript", Kind: ClipScriptNode},
			{ID: "competition", Kind: CompetitionReferenceNode},
		},
		Edges: []WorkflowEdge{
			{FromNodeID: "requirement", FromPort: "requirement", ToNodeID: "clipscript", ToPort: "requirement"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "competition", ToPort: "clipscript"},
		},
	}
	run, err := runner.startWorkflow(context.Background(), "project-1", workflow, RunInput{ProductName: "shoe"}, "cancel-poll-run")
	if err != nil {
		t.Fatalf("startWorkflow() error = %v", err)
	}
	job := nodeRun(run, "competition", "competition-1")
	if err := runner.Cancel(context.Background(), run.ID); !errors.Is(err, ErrCancellationUnsupported) {
		t.Fatalf("Cancel() error = %v, want ErrCancellationUnsupported", err)
	}
	images.jobs[job.JobID] = JobStatus{State: JobSucceeded, URI: "image://completed"}
	if err := runner.Poll(context.Background(), run.ID); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}

	stored := mustGetRun(t, runner.store, run.ID)
	if !stored.Canceled || stored.CancelRequested {
		t.Fatalf("run after terminal poll = %#v, want completed cancellation", stored)
	}
	if node := nodeRun(stored, "competition", "competition-1"); node.State != Succeeded {
		t.Fatalf("completed remote node state = %s, want succeeded", node.State)
	}
}

func TestCallbackClaimFailureIsObservable(t *testing.T) {
	runner, err := NewRunner(&Store{backend: failingStateBackend{err: errors.New("store unavailable")}}, Clients{
		Planner: testPlanner{}, Image: newFakeImages(), TTS: newFakeTTS(),
		Video: newFakeVideos(), Audit: allowAudit{}, Shield: allowShield{},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	var callback RunEvent
	runner.SetMonitor(MonitorFunc(func(_ context.Context, event RunEvent) {
		if event.Action == MonitorCallback {
			callback = event
		}
	}))
	err = runner.ProcessCallback(context.Background(), CallbackMessage{Provider: "video", EventID: "done:job-1", JobID: "job-1"})
	if err == nil {
		t.Fatal("ProcessCallback() error = nil")
	}
	if callback.Action != MonitorCallback || callback.Provider != "video" || !strings.Contains(callback.Message, "claim failed") {
		t.Fatalf("callback event = %#v, want failed claim observation", callback)
	}
}

func TestCancelCompensatesJobSubmittedDuringCancellation(t *testing.T) {
	images := newFakeImages()
	blockingImages := &blockingImageClient{
		fakeImages: images,
		started:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	runner, err := NewRunner(NewStore(t.TempDir()+"/workflow.json"), Clients{
		Planner: testPlanner{}, Image: blockingImages, TTS: newFakeTTS(),
		Video: newFakeVideos(), Audit: allowAudit{}, Shield: allowShield{},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, runErr := runner.startWorkflow(context.Background(), "project-1", VideoWorkflow(), RunInput{ProductName: "shoe"}, "cancel-submit-race")
		done <- runErr
	}()
	<-blockingImages.started
	if err := runner.Cancel(context.Background(), "cancel-submit-race"); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	close(blockingImages.release)
	if err := <-done; err != nil {
		t.Fatalf("startWorkflow() error = %v", err)
	}
	if len(images.jobs) != 0 {
		t.Fatalf("remote jobs after cancel = %#v, want none", images.jobs)
	}
}

func TestLateSubmissionCancelFailureReportsRemoteJobAsWaiting(t *testing.T) {
	images := newFakeImages()
	blockingImages := &cancelFailingBlockingImageClient{blockingImageClient: &blockingImageClient{
		fakeImages: images,
		started:    make(chan struct{}),
		release:    make(chan struct{}),
	}}
	runner, err := NewRunner(NewStore(t.TempDir()+"/workflow.json"), Clients{
		Planner: testPlanner{}, Image: blockingImages, TTS: newFakeTTS(),
		Video: newFakeVideos(), Audit: allowAudit{}, Shield: allowShield{},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	var cancelFailure RunEvent
	runner.SetMonitor(MonitorFunc(func(_ context.Context, event RunEvent) {
		if event.Action == MonitorCancelFailed && event.NodeID != "" {
			cancelFailure = event
		}
	}))

	done := make(chan error, 1)
	go func() {
		_, runErr := runner.startWorkflow(context.Background(), "project-1", VideoWorkflow(), RunInput{ProductName: "shoe"}, "cancel-submit-failure")
		done <- runErr
	}()
	<-blockingImages.started
	if err := runner.Cancel(context.Background(), "cancel-submit-failure"); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	close(blockingImages.release)
	if err := <-done; err != nil {
		t.Fatalf("startWorkflow() error = %v", err)
	}
	if cancelFailure.State != Waiting || cancelFailure.Provider == "" {
		t.Fatalf("cancel failure event = %#v, want waiting remote job", cancelFailure)
	}
	stored := mustGetRun(t, runner.store, "cancel-submit-failure")
	remoteNode := nodeRun(stored, "competition", "competition-1")
	if !stored.CancelRequested || stored.Canceled || remoteNode.State != Waiting || remoteNode.JobID == "" {
		t.Fatalf("run after failed late cancel = %#v, node = %#v", stored, remoteNode)
	}
	images.jobs[remoteNode.JobID] = JobStatus{State: JobSucceeded, URI: "image://completed"}
	if err := runner.Poll(context.Background(), stored.ID); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	stored = mustGetRun(t, runner.store, stored.ID)
	if !stored.Canceled || stored.CancelRequested {
		t.Fatalf("run after late job terminal state = %#v, want canceled", stored)
	}
}

func TestRestoreRetriesFailedCancellationFinalization(t *testing.T) {
	images := newFakeImages()
	backend := &cancelFinalizeFailBackend{data: emptyStoreData()}
	store := &Store{backend: backend}
	runner, err := NewRunner(store, Clients{
		Planner: testPlanner{}, Image: imageWithoutCancel{client: images}, TTS: newFakeTTS(),
		Video: newFakeVideos(), Audit: allowAudit{}, Shield: allowShield{},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	run, err := runner.startWorkflow(context.Background(), "project-1", Workflow{
		Nodes: []WorkflowNode{
			{ID: "requirement", Kind: RequirementNode},
			{ID: "clipscript", Kind: ClipScriptNode},
			{ID: "competition", Kind: CompetitionReferenceNode},
		},
		Edges: []WorkflowEdge{
			{FromNodeID: "requirement", FromPort: "requirement", ToNodeID: "clipscript", ToPort: "requirement"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "competition", ToPort: "clipscript"},
		},
	}, RunInput{ProductName: "shoe"}, "cancel-finalize-retry")
	if err != nil {
		t.Fatalf("startWorkflow() error = %v", err)
	}
	job := nodeRun(run, "competition", "competition-1")
	if err := runner.Cancel(context.Background(), run.ID); !errors.Is(err, ErrCancellationUnsupported) {
		t.Fatalf("Cancel() error = %v, want ErrCancellationUnsupported", err)
	}
	cancelFailuresBeforePoll := runner.Metrics.Snapshot()[MonitorCancelFailed]
	images.jobs[job.JobID] = JobStatus{State: JobSucceeded, URI: "image://completed"}
	if err := runner.Poll(context.Background(), run.ID); err == nil {
		t.Fatal("Poll() error = nil, want cancellation finalization store failure")
	}
	stored := mustGetRun(t, store, run.ID)
	if !stored.CancelRequested || stored.Canceled || nodeRun(stored, "competition", "competition-1").State != Succeeded {
		t.Fatalf("run after failed cancellation finalization = %#v", stored)
	}
	if got := runner.Metrics.Snapshot()[MonitorCancelFailed]; got != cancelFailuresBeforePoll+1 {
		t.Fatalf("cancel failure events = %d, want %d", got, cancelFailuresBeforePoll+1)
	}
	if err := runner.Restore(context.Background(), run.ID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	stored = mustGetRun(t, store, run.ID)
	if !stored.Canceled || stored.CancelRequested {
		t.Fatalf("run after Restore() = %#v, want canceled", stored)
	}
}

func TestSubmissionCannotStartAfterCancellationRequest(t *testing.T) {
	store := NewStore(t.TempDir() + "/workflow.json")
	command := Command{RunID: "cancel-before-submit", NodeRun: NodeRun{
		NodeID: "competition", Kind: CompetitionReferenceNode, InstanceKey: "competition-1",
		State: Running, ClaimToken: "claim-1",
	}}
	if err := store.update(func(data *storeData) error {
		data.Runs[command.RunID] = Run{ID: command.RunID, CancelRequested: true, NodeRuns: []NodeRun{command.NodeRun}}
		return nil
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	if err := store.markSubmitStarted(command); !errors.Is(err, errRunCancelRequested) {
		t.Fatalf("markSubmitStarted() error = %v, want errRunCancelRequested", err)
	}
	stored := mustGetRun(t, store, command.RunID)
	if stored.NodeRuns[0].SubmitStarted {
		t.Fatal("submission started after cancellation request")
	}
}

func TestCompleteCancelKeepsNewSubmissionTracked(t *testing.T) {
	store := NewStore(t.TempDir() + "/workflow.json")
	node := NodeRun{
		NodeID: "competition", Kind: CompetitionReferenceNode, InstanceKey: "competition-1",
		State: Running, SubmitStarted: true, ClaimToken: "claim-1",
	}
	if err := store.update(func(data *storeData) error {
		data.Runs["cancel-during-submit"] = Run{ID: "cancel-during-submit", CancelRequested: true, NodeRuns: []NodeRun{node}}
		return nil
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	if err := store.completeCancelIfIdle("cancel-during-submit", nil); err != nil {
		t.Fatalf("completeCancelIfIdle() error = %v", err)
	}
	stored := mustGetRun(t, store, "cancel-during-submit")
	if stored.Canceled || !stored.CancelRequested || !stored.NodeRuns[0].SubmitStarted {
		t.Fatalf("run = %#v, want tracked in-flight submission", stored)
	}
}

func TestLongRunningSubmissionRenewsNodeClaim(t *testing.T) {
	images := newFakeImages()
	blockingImages := &blockingImageClient{
		fakeImages: images,
		started:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	runner, err := NewRunner(NewStore(t.TempDir()+"/workflow.json"), Clients{
		Planner: testPlanner{}, Image: blockingImages, TTS: newFakeTTS(),
		Video: newFakeVideos(), Audit: allowAudit{}, Shield: allowShield{},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	runner.claimHeartbeat = 5 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		_, runErr := runner.startWorkflow(context.Background(), "project-1", VideoWorkflow(), RunInput{ProductName: "shoe"}, "lease-heartbeat")
		done <- runErr
	}()
	<-blockingImages.started
	first := nodeRun(mustGetRun(t, runner.store, "lease-heartbeat"), "competition", "competition-1")
	if first.ClaimedAt == nil {
		t.Fatal("claimed node has no timestamp")
	}
	time.Sleep(20 * time.Millisecond)
	second := nodeRun(mustGetRun(t, runner.store, "lease-heartbeat"), "competition", "competition-1")
	if second.ClaimedAt == nil || !second.ClaimedAt.After(*first.ClaimedAt) {
		t.Fatalf("claim timestamp did not advance: first=%v second=%v", first.ClaimedAt, second.ClaimedAt)
	}
	close(blockingImages.release)
	if err := <-done; err != nil {
		t.Fatalf("startWorkflow() error = %v", err)
	}
}

func TestClaimHeartbeatFailureCancelsNodeExecution(t *testing.T) {
	metrics := NewMetrics()
	runner := &Runner{
		store:          NewStore(t.TempDir() + "/workflow.json"),
		monitor:        metrics,
		Metrics:        metrics,
		claimHeartbeat: time.Millisecond,
	}
	command := Command{RunID: "missing-run", NodeRun: NodeRun{
		NodeID: "requirement", Kind: RequirementNode, ClaimToken: "stale-claim",
	}}

	executionCanceled := make(chan struct{})
	_, err := runner.withClaimHeartbeat(context.Background(), command, func(ctx context.Context, _ Command) (Result, error) {
		<-ctx.Done()
		close(executionCanceled)
		return Result{}, context.Cause(ctx)
	})
	if !errors.Is(err, ErrClaimHeartbeat) {
		t.Fatalf("withClaimHeartbeat() error = %v, want ErrClaimHeartbeat", err)
	}
	select {
	case <-executionCanceled:
	default:
		t.Fatal("node execution was not canceled after losing its claim")
	}
	if got := metrics.Snapshot()[MonitorLeaseRenewalFailed]; got != 1 {
		t.Fatalf("lease renewal failure events = %d, want 1", got)
	}
}

func TestStartRunDoesNotTreatStorageFailureAsMissingProject(t *testing.T) {
	storageErr := errors.New("mongo unavailable")
	store := &Store{backend: failingStateBackend{err: storageErr}}
	runner, err := NewRunner(store, Clients{
		Planner: testPlanner{}, Image: newFakeImages(), TTS: newFakeTTS(),
		Video: newFakeVideos(), Audit: allowAudit{}, Shield: allowShield{},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	_, err = runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if !errors.Is(err, storageErr) {
		t.Fatalf("StartRun() error = %v, want storage error", err)
	}
}

func TestConfirmOperationIsIdempotent(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	reply, err := application.Agent.Chat(context.Background(), AgentChatInput{
		ProjectID: "demo",
		Text:      "请生成一条广告",
		RunInput:  RunInput{ProductName: "shoe", Brief: "short video"},
	})
	if err != nil {
		t.Fatalf("Agent.Chat() error = %v", err)
	}
	first, firstRun, err := application.Runner.ConfirmOperation(context.Background(), reply.Operation.ID)
	if err != nil {
		t.Fatalf("first ConfirmOperation() error = %v", err)
	}
	second, secondRun, err := application.Runner.ConfirmOperation(context.Background(), reply.Operation.ID)
	if err != nil {
		t.Fatalf("second ConfirmOperation() error = %v", err)
	}
	if first.Status != OperationApplied || second.Status != OperationApplied {
		t.Fatalf("operation statuses = %q, %q", first.Status, second.Status)
	}
	if firstRun == nil || secondRun == nil || firstRun.ID != secondRun.ID {
		t.Fatalf("run ids = %#v, %#v, want the same run", firstRun, secondRun)
	}
}

func TestInvalidRunOperationRemainsRejectable(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	workflow := VideoWorkflow()
	workflow.Edges = workflow.Edges[:len(workflow.Edges)-1]
	if _, err := application.Runner.SaveWorkflow(context.Background(), "demo", workflow); err != nil {
		t.Fatalf("SaveWorkflow() error = %v", err)
	}
	operation := CanvasOperation{
		ID:        "invalid-run",
		ProjectID: "demo",
		Type:      OperationRun,
		Payload:   mustJSON(RunInput{ProductName: "shoe"}),
		Status:    OperationPending,
	}
	if err := application.Store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatalf("CreateOperation() error = %v", err)
	}

	if _, _, err := application.Runner.ConfirmOperation(context.Background(), operation.ID); err == nil {
		t.Fatal("ConfirmOperation() succeeded for an invalid workflow")
	}
	stored, err := application.Store.GetOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatalf("GetOperation() error = %v", err)
	}
	if stored.Status != OperationPending {
		t.Fatalf("operation status = %s, want pending", stored.Status)
	}
	if _, err := application.Runner.RejectOperation(context.Background(), operation.ID); err != nil {
		t.Fatalf("RejectOperation() error = %v", err)
	}
}

func TestConfirmRetryOperationResubmitsFailedResource(t *testing.T) {
	runner, store, _, tts, _ := testRunner(t)
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	failed := nodeRun(run, "tts", "speaker-1")
	tts.jobs[failed.JobID] = JobStatus{State: JobFailed, Message: "tts failed"}
	if err := runner.Poll(context.Background(), run.ID); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if got := nodeRun(mustGetRun(t, store, run.ID), "tts", "speaker-1").State; got != Failed {
		t.Fatalf("tts state = %s, want failed", got)
	}
	operation := CanvasOperation{
		ID:        "operation-retry",
		ProjectID: "project-1",
		RunID:     run.ID,
		Type:      OperationRetry,
		Payload:   mustJSON(map[string]string{"run_id": run.ID}),
		Status:    OperationPending,
	}
	if err := store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatalf("CreateOperation() error = %v", err)
	}
	confirmed, retried, err := runner.ConfirmOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatalf("ConfirmOperation() error = %v", err)
	}
	if confirmed.Status != OperationApplied || retried == nil {
		t.Fatalf("retry result = %#v, run = %#v", confirmed, retried)
	}
	if got := len(tts.submissions); got != 2 {
		t.Fatalf("tts submissions = %d, want one resubmission; retry run = %#v", got, retried.NodeRuns)
	}
	if got := nodeRun(*retried, "tts", "speaker-1").State; got != Waiting {
		t.Fatalf("retried tts state = %s, want waiting", got)
	}
	retriedTTS := nodeRun(*retried, "tts", "speaker-1")
	if retriedTTS.Attempt != 1 || retriedTTS.SubmitKey == failed.SubmitKey {
		t.Fatalf("retried tts identity = %#v, want a new attempt submit key", retriedTTS)
	}
	if submission, found, err := store.GetSubmission(context.Background(), failed.SubmitKey); err != nil || !found || submission.JobID != failed.JobID {
		t.Fatalf("original submission = %#v, found = %v, err = %v", submission, found, err)
	}
}

func TestConfirmCancelOperationStopsRun(t *testing.T) {
	runner, store, _, _, _ := testRunner(t)
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	operation := CanvasOperation{
		ID:        "operation-cancel",
		ProjectID: "project-1",
		RunID:     run.ID,
		Type:      OperationCancel,
		Payload:   mustJSON(map[string]string{"run_id": run.ID}),
		Status:    OperationPending,
	}
	if err := store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatalf("CreateOperation() error = %v", err)
	}

	confirmed, canceled, err := runner.ConfirmOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatalf("ConfirmOperation() error = %v", err)
	}
	if confirmed.Status != OperationApplied || canceled == nil || !canceled.Canceled {
		t.Fatalf("cancel result = %#v, run = %#v", confirmed, canceled)
	}
}

func TestPreviewFailureFailsController(t *testing.T) {
	runner, store, images, tts, videos := testRunner(t)
	run, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	images.succeedAll()
	tts.succeedAll()
	if err := runner.Poll(context.Background(), run.ID); err != nil {
		t.Fatalf("Poll() before preview failure error = %v", err)
	}
	preview := nodeRun(mustGetRun(t, store, run.ID), "preview", "scene-1")
	if preview.JobID == "" {
		t.Fatalf("preview child has no job: %#v", preview)
	}
	videos.jobs[preview.JobID] = JobStatus{State: JobFailed, Message: "preview failed"}
	if err := runner.Poll(context.Background(), run.ID); err != nil {
		t.Fatalf("Poll() after preview failure error = %v", err)
	}
	if got := nodeRun(mustGetRun(t, store, run.ID), "preview", "").State; got != Failed {
		t.Fatalf("preview controller state = %s, want failed", got)
	}
	stored := mustGetRun(t, store, run.ID)
	if got := nodeRun(stored, "finalvideo", "").State; got != Canceled {
		t.Fatalf("finalvideo state = %s, want canceled after preview failure", got)
	}
	for _, node := range stored.NodeRuns {
		if !node.State.terminal() {
			t.Fatalf("node %s/%s remains active after terminal failure: %s", node.NodeID, node.InstanceKey, node.State)
		}
	}
	if err := runner.Retry(context.Background(), run.ID); err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	if got := nodeRun(mustGetRun(t, store, run.ID), "finalvideo", "").State; got != Pending {
		t.Fatalf("finalvideo state after retry = %s, want pending", got)
	}
}

func TestFinalVideoUsesVideoClientForSubmitAndReconciliation(t *testing.T) {
	videos := newFakeVideos()
	handler := nodeHandler{clients: Clients{Video: videos}}
	plan := ResourcePlan{ID: "finalvideo"}
	output, err := encode(plan)
	if err != nil {
		t.Fatalf("encode() error = %v", err)
	}
	command := Command{
		RunID: "run-1",
		NodeRun: NodeRun{
			NodeID:    "finalvideo",
			Kind:      FinalVideoNode,
			SubmitKey: "submit-1",
			Output:    output,
		},
	}
	result, err := handler.submit(context.Background(), command)
	if err != nil {
		t.Fatalf("submit() error = %v", err)
	}
	if result.State != Waiting || result.Provider != "video" || result.JobID == "" {
		t.Fatalf("submit result = %#v, want a video job", result)
	}

	videos.submitErr = fmt.Errorf("connection reset after submit")
	videos.jobsByKey["submit-2"] = SubmittedJob{Provider: "video", JobID: "video-2"}
	command.NodeRun.SubmitKey = "submit-2"
	result, err = handler.submit(context.Background(), command)
	if err != nil {
		t.Fatalf("reconcile submit() error = %v", err)
	}
	if result.Provider != "video" || result.JobID != "video-2" {
		t.Fatalf("reconciled result = %#v, want video job", result)
	}
}

func TestPlanPreviewUsesOnlyResourcesForEachScene(t *testing.T) {
	clipScript, _ := encode(ClipScript{Scenes: []Scene{{ID: "scene-1"}, {ID: "scene-2"}}})
	resourceOne, _ := encode(map[string]string{"scene_id": "scene-1", "url": "https://example/one.png"})
	resourceTwo, _ := encode(map[string]string{"scene_id": "scene-2", "url": "https://example/two.png"})
	shared, _ := encode(map[string]string{"url": "https://example/shared.png"})
	result, err := planPreview(Command{
		RunID:   "run-1",
		NodeRun: NodeRun{NodeID: "preview", Kind: PreviewNode},
		Inputs: []Artifact{
			{ID: "clipscript", Kind: "clipscript", Status: string(Succeeded), Data: clipScript},
			{ID: "image:one", Kind: "competition_reference_image", Status: string(Succeeded), Data: resourceOne},
			{ID: "image:two", Kind: "competition_reference_image", Status: string(Succeeded), Data: resourceTwo},
			{ID: "image:shared", Kind: "competition_reference_image", Status: string(Succeeded), Data: shared},
		},
	})
	if err != nil {
		t.Fatalf("planPreview() error = %v", err)
	}
	if len(result.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(result.Children))
	}
	first, err := decode[ResourcePlan](result.Children[0].Output)
	if err != nil {
		t.Fatalf("decode first plan: %v", err)
	}
	if !contains(first.ArtifactIDs, "image:one") || !contains(first.ArtifactIDs, "image:shared") || contains(first.ArtifactIDs, "image:two") {
		t.Fatalf("scene-1 artifact ids = %#v", first.ArtifactIDs)
	}
	second, err := decode[ResourcePlan](result.Children[1].Output)
	if err != nil {
		t.Fatalf("decode second plan: %v", err)
	}
	if !contains(second.ArtifactIDs, "image:two") || !contains(second.ArtifactIDs, "image:shared") || contains(second.ArtifactIDs, "image:one") {
		t.Fatalf("scene-2 artifact ids = %#v", second.ArtifactIDs)
	}
}

func TestNodeConfigChangesPreviewRequest(t *testing.T) {
	runner, _, images, tts, videos := testRunner(t)
	workflow := VideoWorkflow()
	for index := range workflow.Nodes {
		if workflow.Nodes[index].Kind == PreviewNode {
			workflow.Nodes[index].Config = json.RawMessage(`{"prompt":"configured preview","model":"seedance-x","duration":9,"aspect_ratio":"9:16"}`)
		}
	}
	run, err := runner.StartWorkflow(context.Background(), "project-config", workflow, RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartWorkflow() error = %v", err)
	}
	images.succeedAll()
	tts.succeedAll()
	if err := runner.Poll(context.Background(), run.ID); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if videos.preview.Prompt != "configured preview" || videos.preview.Model != "seedance-x" || videos.preview.Duration != 9 || videos.preview.AspectRatio != "9:16" {
		t.Fatalf("preview request = %#v, want node config overrides", videos.preview)
	}
}

func TestInputArtifactsFollowOutputPort(t *testing.T) {
	run := Run{
		Workflow: WorkflowVersion{Workflow: Workflow{Edges: []WorkflowEdge{{FromNodeID: "competition", FromPort: "competition_reference_image", ToNodeID: "preview", ToPort: "resources"}}}},
		NodeRuns: []NodeRun{
			{NodeID: "competition", State: Succeeded},
			{NodeID: "competition", InstanceKey: "one", State: Succeeded, Artifacts: []Artifact{
				{ID: "image", Kind: "competition_reference_image", Status: string(Succeeded)},
				{ID: "annotation", Kind: "clipscript_annotation", Status: string(Succeeded)},
			}},
		},
	}
	artifacts := inputArtifacts(run, NodeRun{NodeID: "preview"})
	if len(artifacts) != 1 || artifacts[0].ID != "image" {
		t.Fatalf("input artifacts = %#v, want only connected output port", artifacts)
	}
}

func mustGetRun(t *testing.T, store *Store, runID string) Run {
	t.Helper()
	run, err := store.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	return run
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

func (testPlanner) CreateClipScript(context.Context, Requirement, RunInput) (ClipScript, error) {
	return ClipScript{Title: "shoe story", Scenes: []Scene{{ID: "scene-1", Voiceover: "comfortable shoes", Visual: "shoe on a city street", DurationMS: 5000}}}, nil
}

func (testPlanner) PlanCompetition(context.Context, ClipScript, RunInput) ([]ResourcePlan, error) {
	return []ResourcePlan{{ID: "competition-1", SceneID: "scene-1", Prompt: "shoe on street", Model: "primary"}}, nil
}

func (testPlanner) PlanTTS(context.Context, ClipScript) ([]ResourcePlan, error) {
	return []ResourcePlan{{ID: "speaker-1", SceneID: "scene-1", Speaker: "narrator", Text: "comfortable shoes"}}, nil
}

func (testPlanner) PlanCharacterReferences(context.Context, ClipScript, RunInput) ([]ResourcePlan, error) {
	return []ResourcePlan{{ID: "character-1", SceneID: "scene-1", Prompt: "young woman", Model: "primary", FallbackModel: "fallback"}}, nil
}

type fakeImages struct {
	submissions  []ImageRequest
	jobsByKey    map[string]SubmittedJob
	jobs         map[string]JobStatus
	submitErrors map[string]error
}

type imageWithoutCancel struct{ client *fakeImages }

type blockingImageClient struct {
	fakeImages *fakeImages
	started    chan struct{}
	release    chan struct{}
	startOnce  sync.Once
}

type cancelFailingBlockingImageClient struct {
	*blockingImageClient
}

func (*cancelFailingBlockingImageClient) CancelImage(context.Context, string) error {
	return errors.New("remote cancel failed")
}

func (images *blockingImageClient) SubmitImage(ctx context.Context, request ImageRequest) (SubmittedJob, error) {
	images.startOnce.Do(func() { close(images.started) })
	<-images.release
	return images.fakeImages.SubmitImage(ctx, request)
}

func (images *blockingImageClient) GetImage(ctx context.Context, jobID string) (JobStatus, error) {
	return images.fakeImages.GetImage(ctx, jobID)
}

func (images *blockingImageClient) FindImageBySubmitKey(ctx context.Context, key string) (SubmittedJob, bool, error) {
	return images.fakeImages.FindImageBySubmitKey(ctx, key)
}

func (images *blockingImageClient) CancelImage(ctx context.Context, jobID string) error {
	return images.fakeImages.CancelImage(ctx, jobID)
}

func (images imageWithoutCancel) SubmitImage(ctx context.Context, request ImageRequest) (SubmittedJob, error) {
	return images.client.SubmitImage(ctx, request)
}

func (images imageWithoutCancel) GetImage(ctx context.Context, jobID string) (JobStatus, error) {
	return images.client.GetImage(ctx, jobID)
}

func (images imageWithoutCancel) FindImageBySubmitKey(ctx context.Context, key string) (SubmittedJob, bool, error) {
	return images.client.FindImageBySubmitKey(ctx, key)
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

func (images *fakeImages) CancelImage(_ context.Context, jobID string) error {
	delete(images.jobs, jobID)
	return nil
}

type fakeTTS struct {
	submissions []TTSRequest
	jobsByKey   map[string]SubmittedJob
	jobs        map[string]JobStatus
	submitErr   error
	findErr     error
	getErr      error
	gets        int
}

type immediateTTS struct{}

func (immediateTTS) SubmitTTS(context.Context, TTSRequest) (SubmittedJob, error) {
	return SubmittedJob{Provider: "tts", Status: &JobStatus{
		State:      JobSucceeded,
		URL:        "https://example/voice.wav",
		ExampleURL: "https://example/preview.wav",
	}}, nil
}

func (immediateTTS) GetTTS(context.Context, string) (JobStatus, error) {
	return JobStatus{}, fmt.Errorf("GetTTS must not be called for a synchronous result")
}

func (immediateTTS) FindTTSBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, ErrSubmitReconciliationUnsupported
}

type emptyJobTTS struct{}

func (emptyJobTTS) SubmitTTS(context.Context, TTSRequest) (SubmittedJob, error) {
	return SubmittedJob{Provider: "tts"}, nil
}

func (emptyJobTTS) GetTTS(context.Context, string) (JobStatus, error) {
	return JobStatus{}, errors.New("empty job must not be queried")
}

func (emptyJobTTS) FindTTSBySubmitKey(context.Context, string) (SubmittedJob, bool, error) {
	return SubmittedJob{}, false, ErrSubmitReconciliationUnsupported
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
	if tts.getErr != nil {
		return JobStatus{}, tts.getErr
	}
	return tts.jobs[jobID], nil
}

func (tts *fakeTTS) FindTTSBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	if tts.findErr != nil {
		return SubmittedJob{}, false, tts.findErr
	}
	job, found := tts.jobsByKey[key]
	return job, found, nil
}

func (tts *fakeTTS) succeedAll() {
	for jobID := range tts.jobs {
		tts.jobs[jobID] = JobStatus{State: JobSucceeded, ExampleURI: "example://speaker-1"}
	}
}

func (tts *fakeTTS) CancelTTS(_ context.Context, jobID string) error {
	delete(tts.jobs, jobID)
	return nil
}

type fakeVideos struct {
	jobsByKey map[string]SubmittedJob
	jobs      map[string]JobStatus
	submitErr error
	preview   VideoRequest
	final     VideoRequest
}

func newFakeVideos() *fakeVideos {
	return &fakeVideos{jobsByKey: make(map[string]SubmittedJob), jobs: make(map[string]JobStatus)}
}

func (videos *fakeVideos) SubmitPreview(_ context.Context, request VideoRequest) (SubmittedJob, error) {
	videos.preview = request
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
	videos.final = request
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
	if videos.submitErr != nil {
		return SubmittedJob{}, videos.submitErr
	}
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

func (videos *fakeVideos) CancelVideo(_ context.Context, jobID string) error {
	delete(videos.jobs, jobID)
	return nil
}

type allowAudit struct{}

func (allowAudit) CheckImage(context.Context, string) error { return nil }

type allowShield struct{}

func (allowShield) CheckPrompt(context.Context, string) error { return nil }

type flakyStateBackend struct {
	data   storeData
	saves  int
	failAt map[int]bool
}

type cancelFinalizeFailBackend struct {
	data   storeData
	failed bool
}

type failingStateBackend struct {
	err error
}

func (backend failingStateBackend) Load() (storeData, error) {
	return storeData{}, backend.err
}

func (backend failingStateBackend) Save(storeData) error {
	return backend.err
}

type claimConflictBackend struct {
	data       storeData
	conflicted bool
}

func (backend *claimConflictBackend) Load() (storeData, error) {
	payload, err := json.Marshal(backend.data)
	if err != nil {
		return storeData{}, err
	}
	data := storeData{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return storeData{}, err
	}
	return normalizeStoreData(data), nil
}

func (backend *claimConflictBackend) Save(data storeData) error {
	if !backend.conflicted {
		backend.conflicted = true
		run := backend.data.Runs["run-1"]
		run.NodeRuns[0].State = Running
		backend.data.Runs[run.ID] = run
		return fmt.Errorf("mongo workflow state was modified concurrently")
	}
	backend.data = data
	return nil
}

func (backend *flakyStateBackend) Load() (storeData, error) {
	payload, err := json.Marshal(backend.data)
	if err != nil {
		return storeData{}, err
	}
	data := storeData{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return storeData{}, err
	}
	return normalizeStoreData(data), nil
}

func (backend *flakyStateBackend) Save(data storeData) error {
	backend.saves++
	if backend.failAt[backend.saves] {
		return fmt.Errorf("temporary store failure")
	}
	backend.data = data
	return nil
}

func (backend *cancelFinalizeFailBackend) Load() (storeData, error) {
	payload, err := json.Marshal(backend.data)
	if err != nil {
		return storeData{}, err
	}
	data := storeData{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return storeData{}, err
	}
	return normalizeStoreData(data), nil
}

func (backend *cancelFinalizeFailBackend) Save(data storeData) error {
	if !backend.failed {
		for _, run := range data.Runs {
			if run.Canceled {
				backend.failed = true
				return fmt.Errorf("temporary cancel finalization failure")
			}
		}
	}
	backend.data = data
	return nil
}
