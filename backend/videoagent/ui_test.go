package videoagent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPServesCanvas(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "广告视频工作台") || !strings.Contains(response.Body.String(), "product-images") {
		t.Fatalf("GET / does not contain Canvas page")
	}
	if !strings.Contains(response.Body.String(), "artifact-edges") {
		t.Fatalf("GET / does not contain artifact relationship layer")
	}
	scriptRequest := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	scriptResponse := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(scriptResponse, scriptRequest)
	if scriptResponse.Code != http.StatusOK || !strings.Contains(scriptResponse.Body.String(), "node-palette") || !strings.Contains(scriptResponse.Body.String(), "text/node-kind") || !strings.Contains(scriptResponse.Body.String(), "selectedEdge") || !strings.Contains(scriptResponse.Body.String(), "/runs/${state.run.run_id}") {
		t.Fatalf("GET /app.js does not contain draggable node palette")
	}
	if !strings.Contains(scriptResponse.Body.String(), `["tts", "voice_preview", "finalvideo", "resources"]`) {
		t.Fatalf("GET /app.js default workflow does not connect narration to finalvideo")
	}
	if !strings.Contains(scriptResponse.Body.String(), `localStorage.setItem("video-agent-run"`) || !strings.Contains(scriptResponse.Body.String(), "restoreRun") || !strings.Contains(scriptResponse.Body.String(), "restoreConversation") || !strings.Contains(scriptResponse.Body.String(), "restoreSession") || !strings.Contains(scriptResponse.Body.String(), "/operations/${part.operation_id}") {
		t.Fatalf("GET /app.js does not restore persisted run and conversation state")
	}
	if !strings.Contains(scriptResponse.Body.String(), `localStorage.removeItem("video-agent-conversation")`) || !strings.Contains(scriptResponse.Body.String(), `headers: { "Idempotency-Key": operationKey("chat") }`) {
		t.Fatalf("GET /app.js does not clear stale sessions or send idempotent chat requests")
	}
	for _, media := range []string{`"img"`, `"audio"`, `"video"`} {
		if !strings.Contains(scriptResponse.Body.String(), `appendArtifactMedia(content, `+media) {
			t.Fatalf("GET /app.js does not render %s artifacts inside workflow nodes", media)
		}
	}
	if !strings.Contains(scriptResponse.Body.String(), `artifact.kind === "requirement"`) || !strings.Contains(scriptResponse.Body.String(), `artifact.kind === "clipscript"`) {
		t.Fatalf("GET /app.js does not render requirement and clipscript text artifacts")
	}
	if !strings.Contains(scriptResponse.Body.String(), `confirmed ? "继续" : "确认"`) || !strings.Contains(scriptResponse.Body.String(), `"待继续操作"`) {
		t.Fatalf("GET /app.js does not allow a confirmed but incomplete operation to resume")
	}
	if !strings.Contains(scriptResponse.Body.String(), `node.state === "failed" && !node.submission_unknown`) {
		t.Fatalf("GET /app.js allows unsafe retry after an unknown provider submission")
	}
}

func TestHTTPServesMetrics(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want %d", response.Code, http.StatusOK)
	}
	var metrics map[string]int64
	if err := json.NewDecoder(response.Body).Decode(&metrics); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if metrics == nil {
		t.Fatal("GET /metrics returned nil metrics")
	}
}

func TestHTTPDoesNotExposeDirectRunPolling(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	request := httptest.NewRequest(http.MethodPost, "/runs/run-1/poll", nil)
	response := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /runs/run-1/poll status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestHTTPMutationsRequireConfirmedOperations(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()
	handler := NewHTTPHandler(application)
	for _, target := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPut, path: "/projects/demo/workflow"},
		{method: http.MethodPost, path: "/runs/run-1/retry"},
		{method: http.MethodPost, path: "/runs/run-1/cancel"},
	} {
		request := httptest.NewRequest(target.method, target.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s status = %d, want %d", target.method, target.path, response.Code, http.StatusMethodNotAllowed)
		}
	}
}

func TestHTTPRejectsUnknownOperationType(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	request := httptest.NewRequest(
		http.MethodPost,
		"/projects/demo/operations",
		strings.NewReader(`{"type":"delete_everything"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("POST unknown operation status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
