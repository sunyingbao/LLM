package videoagent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLocalHTTPWorkflowCompletesFromRequirementToFinalVideo(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	server := httptest.NewServer(NewHTTPHandler(application))
	defer server.Close()
	payload := []byte(`{"project_id":"demo","product_name":"soft sole shoe","brief":"15 second product video"}`)
	response, err := http.Post(server.URL+"/runs", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /runs error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /runs status = %d, want %d", response.StatusCode, http.StatusCreated)
	}

	var started Run
	if err := json.NewDecoder(response.Body).Decode(&started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	run := waitForFinalVideo(t, application, started.ID)
	preview := artifactByKind(t, run, "preview_video")
	final := artifactByKind(t, run, "finalvideo")
	if !contains(preview.ParentIDs, "clipscript") || len(preview.ParentIDs) < 2 {
		t.Fatalf("preview parents = %#v, want clipscript and generated resources", preview.ParentIDs)
	}
	if !contains(final.ParentIDs, preview.ID) {
		t.Fatalf("final parents = %#v, want preview %s", final.ParentIDs, preview.ID)
	}

	tts := artifactByKind(t, run, "voice_preview")
	var audio map[string]string
	if err := json.Unmarshal(tts.Data, &audio); err != nil {
		t.Fatalf("decode tts artifact: %v", err)
	}
	if audio["preview_audio_uri"] != audio["example_audio_uri"] {
		t.Fatalf("preview audio = %q, want example audio %q", audio["preview_audio_uri"], audio["example_audio_uri"])
	}
}

func TestHTTPReturnsNodeDefinitions(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	server := httptest.NewServer(NewHTTPHandler(application))
	defer server.Close()
	response, err := http.Get(server.URL + "/workflow/node-definitions")
	if err != nil {
		t.Fatalf("GET node definitions error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("node definitions status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var definitions map[NodeKind]NodeDefinition
	if err := json.NewDecoder(response.Body).Decode(&definitions); err != nil {
		t.Fatalf("decode node definitions: %v", err)
	}
	if _, exists := definitions[ClipScriptNode]; !exists {
		t.Fatalf("clipscript definition is missing: %#v", definitions)
	}
}

func waitForFinalVideo(t *testing.T, application *LocalApplication, runID string) Run {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, err := application.Store.Get(context.Background(), runID)
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
