package videoagent

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"time"
)

//go:embed web/*
var canvasFiles embed.FS

// NewHTTPHandler exposes the local video workflow for manual end-to-end checks.
func NewHTTPHandler(application *LocalApplication) http.Handler {
	return newHTTPHandler(application.Application)
}

// NewApplicationHTTPHandler exposes the same API for injected production dependencies.
func NewApplicationHTTPHandler(application *Application) http.Handler {
	return newHTTPHandler(application)
}

func newHTTPHandler(application *Application) http.Handler {
	if application == nil {
		panic("video agent application is nil")
	}
	mux := http.NewServeMux()
	staticFiles, _ := fs.Sub(canvasFiles, "web")
	staticHandler := http.FileServer(http.FS(staticFiles))
	mux.Handle("GET /", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		staticHandler.ServeHTTP(writer, request)
	}))
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /workflow/node-definitions", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, defaultNodeCatalog())
	})
	mux.HandleFunc("GET /metrics", func(writer http.ResponseWriter, _ *http.Request) {
		if application == nil || application.Runner == nil || application.Runner.Metrics == nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "metrics are not initialized"})
			return
		}
		writeJSON(writer, http.StatusOK, application.Runner.Metrics.Snapshot())
	})
	mux.HandleFunc("GET /projects/{projectID}", func(writer http.ResponseWriter, request *http.Request) {
		project, err := application.Store.GetProject(request.Context(), request.PathValue("projectID"))
		if err != nil {
			writeError(writer, http.StatusNotFound, err)
			return
		}
		writeJSON(writer, http.StatusOK, project)
	})
	mux.HandleFunc("GET /projects/{projectID}/session", func(writer http.ResponseWriter, request *http.Request) {
		session, err := application.Store.GetProjectSession(request.Context(), request.PathValue("projectID"))
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err)
			return
		}
		writeJSON(writer, http.StatusOK, session)
	})
	mux.HandleFunc("GET /conversations/{conversationID}/messages", func(writer http.ResponseWriter, request *http.Request) {
		messages, err := application.Store.ListMessages(request.Context(), request.PathValue("conversationID"))
		if err != nil {
			writeError(writer, http.StatusNotFound, err)
			return
		}
		writeJSON(writer, http.StatusOK, messages)
	})
	mux.HandleFunc("POST /agent/chat", func(writer http.ResponseWriter, request *http.Request) {
		if application.Agent == nil {
			writeError(writer, http.StatusNotImplemented, fmt.Errorf("agent is not configured"))
			return
		}
		input := AgentChatInput{}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		if input.IdempotencyKey == "" {
			input.IdempotencyKey = request.Header.Get("Idempotency-Key")
		}
		response, err := application.Agent.Chat(request.Context(), input)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		writeJSON(writer, http.StatusOK, response)
	})
	mux.HandleFunc("POST /projects/{projectID}/operations", func(writer http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if err := EnsureProject(request.Context(), application.Store, projectID); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		operation := CanvasOperation{}
		if err := json.NewDecoder(request.Body).Decode(&operation); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		if !validOperationType(operation.Type) {
			writeError(writer, http.StatusBadRequest, fmt.Errorf("unsupported canvas operation: %s", operation.Type))
			return
		}
		operation.ID = newID("operation")
		operation.ProjectID = projectID
		operation.IdempotencyKey = request.Header.Get("Idempotency-Key")
		operation.Status = OperationPending
		operation.CreatedAt = now()
		stored, reused, err := application.Store.CreateOrGetOperation(request.Context(), operation)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		if reused {
			operation = stored
		}
		status := http.StatusCreated
		if reused {
			status = http.StatusOK
		}
		writeJSON(writer, status, operation)
	})
	mux.HandleFunc("GET /operations/{operationID}", func(writer http.ResponseWriter, request *http.Request) {
		operation, err := application.Store.GetOperation(request.Context(), request.PathValue("operationID"))
		if err != nil {
			writeError(writer, http.StatusNotFound, err)
			return
		}
		writeJSON(writer, http.StatusOK, operation)
	})
	mux.HandleFunc("POST /operations/{operationID}/confirm", func(writer http.ResponseWriter, request *http.Request) {
		operation, run, err := application.Runner.ConfirmOperation(request.Context(), request.PathValue("operationID"))
		if err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		writeJSON(writer, http.StatusOK, struct {
			Operation CanvasOperation `json:"operation"`
			Run       *Run            `json:"run,omitempty"`
		}{Operation: operation, Run: run})
	})
	mux.HandleFunc("POST /operations/{operationID}/reject", func(writer http.ResponseWriter, request *http.Request) {
		operation, err := application.Runner.RejectOperation(request.Context(), request.PathValue("operationID"))
		if err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		writeJSON(writer, http.StatusOK, operation)
	})
	mux.HandleFunc("POST /runs", func(writer http.ResponseWriter, request *http.Request) {
		input := struct {
			ProjectID string `json:"project_id"`
			RunInput
		}{}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		if err := EnsureProject(request.Context(), application.Store, input.ProjectID); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		payload, err := encode(input.RunInput)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		operation := CanvasOperation{
			ID:             newID("operation"),
			ProjectID:      input.ProjectID,
			Type:           OperationRun,
			Payload:        payload,
			Status:         OperationPending,
			CreatedAt:      now(),
			IdempotencyKey: request.Header.Get("Idempotency-Key"),
		}
		stored, reused, err := application.Store.CreateOrGetOperation(request.Context(), operation)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		if reused {
			operation = stored
		}
		status := http.StatusAccepted
		if reused {
			status = http.StatusOK
		}
		writeJSON(writer, status, operation)
	})
	mux.HandleFunc("GET /runs/{runID}", func(writer http.ResponseWriter, request *http.Request) {
		run, err := application.Store.Get(request.Context(), request.PathValue("runID"))
		if err != nil {
			writeError(writer, http.StatusNotFound, err)
			return
		}
		writeJSON(writer, http.StatusOK, run)
	})
	mux.HandleFunc("POST /callbacks/{provider}", func(writer http.ResponseWriter, request *http.Request) {
		if application.callbackVerifier == nil {
			writeError(writer, http.StatusServiceUnavailable, fmt.Errorf("callback verifier is not configured"))
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
		if err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		if err := application.callbackVerifier.Verify(request.Context(), request.PathValue("provider"), body, request.Header); err != nil {
			writeError(writer, http.StatusUnauthorized, err)
			return
		}
		if application.callbackPublisher == nil {
			writeError(writer, http.StatusServiceUnavailable, fmt.Errorf("callback publisher is not configured"))
			return
		}
		message, err := parseCallbackMessageWithEventID(request.PathValue("provider"), body, request.Header.Get("X-Event-ID"))
		if err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		if err := application.callbackPublisher.Publish(request.Context(), message); err != nil {
			writeError(writer, http.StatusServiceUnavailable, err)
			return
		}
		writeJSON(writer, http.StatusAccepted, map[string]string{"status": "accepted"})
	})
	return mux
}

func now() time.Time { return time.Now().UTC() }

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, err error) {
	writeJSON(writer, status, map[string]string{"error": err.Error()})
}
