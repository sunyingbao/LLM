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
	if !strings.Contains(response.Body.String(), `id="project-name"`) || !strings.Contains(response.Body.String(), "product-images") {
		t.Fatalf("GET / does not contain Canvas page")
	}
	if !strings.Contains(response.Body.String(), `id="runtime-mode"`) {
		t.Fatalf("GET / does not expose the runtime mode")
	}
	for _, layoutClass := range []string{`class="left-rail"`, `class="assistant-header"`, `class="run-inspector"`} {
		if !strings.Contains(response.Body.String(), layoutClass) {
			t.Fatalf("GET / does not contain product canvas layout %s", layoutClass)
		}
	}
	for _, removedControl := range []string{`data-tool="workbench"`, `data-tool="variables"`, `data-tool="settings"`, `class="run-chevron"`, `class="assistant-actions"`} {
		if strings.Contains(response.Body.String(), removedControl) {
			t.Fatalf("GET / still exposes unavailable control %s", removedControl)
		}
	}
	for _, interactionID := range []string{`id="task-input"`, `id="run-primary-label"`, `id="artifacts-panel"`, `id="artifact-scope"`} {
		if !strings.Contains(response.Body.String(), interactionID) {
			t.Fatalf("GET / does not expose task-first interaction %s", interactionID)
		}
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET / Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
	if !strings.Contains(response.Body.String(), `/styles.css?v=`) || !strings.Contains(response.Body.String(), `/app.js?v=`) {
		t.Fatalf("GET / does not version static assets")
	}
	if strings.Contains(response.Body.String(), "artifact-edges") {
		t.Fatalf("GET / contains a duplicate artifact relationship layer")
	}
	styleRequest := httptest.NewRequest(http.MethodGet, "/styles.css", nil)
	styleResponse := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(styleResponse, styleRequest)
	if styleResponse.Code != http.StatusOK || !strings.Contains(styleResponse.Body.String(), ".legend-line.lineage") || !strings.Contains(styleResponse.Body.String(), "stroke-dasharray") {
		t.Fatalf("GET /styles.css does not expose the artifact lineage legend")
	}
	if !strings.Contains(styleResponse.Body.String(), "background: rgba(255, 255, 255, .98)") {
		t.Fatalf("GET /styles.css does not keep workflow nodes opaque")
	}
	if !strings.Contains(styleResponse.Body.String(), ".app-shell { height: 100vh;") {
		t.Fatalf("GET /styles.css does not keep the run inspector inside the viewport")
	}
	if !strings.Contains(styleResponse.Body.String(), ".edges path.dimmed") || !strings.Contains(styleResponse.Body.String(), ".canvas.connect-mode") {
		t.Fatalf("GET /styles.css does not reduce edge and port noise outside editing")
	}
	if !strings.Contains(styleResponse.Body.String(), ".canvas-legend { position: absolute;") || strings.Contains(styleResponse.Body.String(), ".node.selected .node-port span") {
		t.Fatalf("GET /styles.css allows canvas chrome to overlap run or node content")
	}
	if styleResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET /styles.css Cache-Control = %q, want no-store", styleResponse.Header().Get("Cache-Control"))
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
	if !strings.Contains(scriptResponse.Body.String(), `const projectID = new URLSearchParams(window.location.search).get("project_id")`) || !strings.Contains(scriptResponse.Body.String(), "projectURL") || strings.Contains(scriptResponse.Body.String(), "/projects/demo") {
		t.Fatalf("GET /app.js does not select the Canvas project from the URL")
	}
	if !strings.Contains(scriptResponse.Body.String(), `localStorage.setItem(storageKey("run")`) || !strings.Contains(scriptResponse.Body.String(), "restoreRun") || !strings.Contains(scriptResponse.Body.String(), "restoreConversation") || !strings.Contains(scriptResponse.Body.String(), "restoreSession") || !strings.Contains(scriptResponse.Body.String(), "/operations/${part.operation_id}") {
		t.Fatalf("GET /app.js does not restore persisted run and conversation state")
	}
	if !strings.Contains(scriptResponse.Body.String(), `localStorage.removeItem(storageKey("conversation"))`) || !strings.Contains(scriptResponse.Body.String(), `headers: { "Idempotency-Key": operationKey("chat") }`) {
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
	if !strings.Contains(scriptResponse.Body.String(), `async function loadRuntime()`) || !strings.Contains(scriptResponse.Body.String(), `fetchJSON("/runtime")`) {
		t.Fatalf("GET /app.js does not load runtime capability status")
	}
	if !strings.Contains(scriptResponse.Body.String(), `label.textContent = "模型需求分析"`) || !strings.Contains(scriptResponse.Body.String(), `element.classList.add("has-requirement-result")`) {
		t.Fatalf("GET /app.js does not expose the requirement model response inside the requirement node")
	}
	if !strings.Contains(styleResponse.Body.String(), `.node.has-requirement-result .node-results`) || !strings.Contains(styleResponse.Body.String(), `.artifact-content.compact.requirement-result .artifact-text`) {
		t.Fatalf("GET /styles.css truncates the requirement model response inside the requirement node")
	}
	if strings.Contains(scriptResponse.Body.String(), `className = "artifact-node"`) || strings.Contains(response.Body.String(), `id="artifact-nodes"`) {
		t.Fatalf("Canvas renders artifacts as duplicate workflow nodes")
	}
	if !strings.Contains(scriptResponse.Body.String(), "function artifactLineagePairs()") || !strings.Contains(scriptResponse.Body.String(), `classList.toggle("lineage"`) {
		t.Fatalf("GET /app.js does not highlight artifact lineage on workflow edges")
	}
	if strings.Contains(scriptResponse.Body.String(), "Math.max(30, (x2 - x1) / 2)") || !strings.Contains(scriptResponse.Body.String(), "Math.min(80, Math.abs(x2 - x1) / 2)") {
		t.Fatalf("GET /app.js allows short edges to bend past their endpoints")
	}
	if !strings.Contains(scriptResponse.Body.String(), `confirmOperation(operation, card, element)`) || !strings.Contains(scriptResponse.Body.String(), `"待继续操作"`) || !strings.Contains(scriptResponse.Body.String(), `"确认并运行"`) {
		t.Fatalf("GET /app.js does not allow a confirmed but incomplete operation to resume")
	}
	if !strings.Contains(scriptResponse.Body.String(), "function renderPort(node, port, direction)") || !strings.Contains(scriptResponse.Body.String(), "function renderRunPanel(run)") {
		t.Fatalf("GET /app.js does not expose port-based nodes and the run inspector")
	}
	if !strings.Contains(scriptResponse.Body.String(), "function findCompatibleInput(") || strings.Contains(scriptResponse.Body.String(), "state.definitions[node.kind]?.inputs?.[0]") {
		t.Fatalf("GET /app.js does not select a compatible input when connecting nodes")
	}
	if !strings.Contains(scriptResponse.Body.String(), `internalArtifactKinds = new Set(["clipscript_annotation"])`) || !strings.Contains(scriptResponse.Body.String(), "node-result-count") {
		t.Fatalf("GET /app.js does not hide internal artifacts or summarize multi-result nodes")
	}
	if !strings.Contains(scriptResponse.Body.String(), `node.state === "failed" && !node.submission_unknown`) {
		t.Fatalf("GET /app.js allows unsafe retry after an unknown provider submission")
	}
	for _, behavior := range []string{"function setPrimaryAction(", "function setRunStatus(", "function showNodeArtifacts(", "function focusNode(", `setRunStatus(state.run)`, `element.textContent = "已返回修改"`, `item.textContent = nodeLabels[kind] || kind`, `card.scrollIntoView(`} {
		if !strings.Contains(scriptResponse.Body.String(), behavior) {
			t.Fatalf("GET /app.js does not implement task-first behavior %q", behavior)
		}
	}
}

func TestHTTPReturnsLocalRuntimeStatus(t *testing.T) {
	application, err := NewLocalApplication(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalApplication() error = %v", err)
	}
	defer application.Close()

	response := httptest.NewRecorder()
	NewHTTPHandler(application).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/runtime", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /runtime status = %d, want %d", response.Code, http.StatusOK)
	}
	var status RuntimeStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatalf("decode runtime status: %v", err)
	}
	if status.Mode != "local" || !status.Image || !status.TTS || !status.Preview || !status.FinalVideo {
		t.Fatalf("GET /runtime = %+v, want fully configured local runtime", status)
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
