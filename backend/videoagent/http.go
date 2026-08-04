package videoagent

import (
	"encoding/json"
	"net/http"
)

// NewHTTPHandler exposes the local video workflow for manual end-to-end checks.
func NewHTTPHandler(application *LocalApplication) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /workflow/node-definitions", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, defaultNodeCatalog())
	})
	mux.HandleFunc("POST /runs", func(writer http.ResponseWriter, request *http.Request) {
		input := struct {
			ProjectID string `json:"project_id"`
			RunInput
			Workflow *Workflow `json:"workflow,omitempty"`
		}{}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		workflow := VideoWorkflow()
		if input.Workflow != nil {
			workflow = *input.Workflow
		}
		run, err := application.Runner.StartWorkflow(request.Context(), input.ProjectID, workflow, input.RunInput)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		writeJSON(writer, http.StatusCreated, run)
	})
	mux.HandleFunc("GET /runs/{runID}", func(writer http.ResponseWriter, request *http.Request) {
		run, err := application.Store.Get(request.Context(), request.PathValue("runID"))
		if err != nil {
			writeError(writer, http.StatusNotFound, err)
			return
		}
		writeJSON(writer, http.StatusOK, run)
	})
	mux.HandleFunc("POST /runs/{runID}/poll", func(writer http.ResponseWriter, request *http.Request) {
		runID := request.PathValue("runID")
		if err := application.Runner.Poll(request.Context(), runID); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		run, err := application.Store.Get(request.Context(), runID)
		if err != nil {
			writeError(writer, http.StatusNotFound, err)
			return
		}
		writeJSON(writer, http.StatusOK, run)
	})
	return mux
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, err error) {
	writeJSON(writer, status, map[string]string{"error": err.Error()})
}
