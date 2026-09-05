package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAppConfigOneShotPrompt(t *testing.T) {
	t.Run("from prompt flag", func(t *testing.T) {
		cfg := CLIOptions{Prompt: "hello"}
		got, err := cfg.oneShotPrompt(nil, strings.NewReader(""))
		if err != nil {
			t.Fatalf("oneShotPrompt() error = %v", err)
		}
		if got != "hello" {
			t.Fatalf("oneShotPrompt() = %q, want %q", got, "hello")
		}
	})

	t.Run("from stdin", func(t *testing.T) {
		cfg := CLIOptions{ReadFromStdin: true}
		got, err := cfg.oneShotPrompt(nil, strings.NewReader("hello from stdin\n"))
		if err != nil {
			t.Fatalf("oneShotPrompt() error = %v", err)
		}
		if got != "hello from stdin" {
			t.Fatalf("oneShotPrompt() = %q, want %q", got, "hello from stdin")
		}
	})

	t.Run("from args", func(t *testing.T) {
		cfg := CLIOptions{}
		got, err := cfg.oneShotPrompt([]string{"hello", "world"}, strings.NewReader(""))
		if err != nil {
			t.Fatalf("oneShotPrompt() error = %v", err)
		}
		if got != "hello world" {
			t.Fatalf("oneShotPrompt() = %q, want %q", got, "hello world")
		}
	})

	t.Run("conflicting sources", func(t *testing.T) {
		cfg := CLIOptions{Prompt: "hello", ReadFromStdin: true}
		if _, err := cfg.oneShotPrompt([]string{"world"}, strings.NewReader("stdin")); err == nil {
			t.Fatalf("expected conflict error")
		}
	})

	t.Run("empty stdin", func(t *testing.T) {
		cfg := CLIOptions{ReadFromStdin: true}
		if _, err := cfg.oneShotPrompt(nil, strings.NewReader("\n")); err == nil {
			t.Fatalf("expected empty stdin error")
		}
	})
}

func TestRepositoryRootFrom(t *testing.T) {
	base, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("flag wins", func(t *testing.T) {
		got, err := repositoryRootFrom(".")
		if err != nil {
			t.Fatal(err)
		}
		want, _ := filepath.Abs(".")
		if got != want {
			t.Fatalf("repositoryRootFrom() = %q, want %q", got, want)
		}
	})
	t.Run("environment fallback", func(t *testing.T) {
		t.Setenv("DEEPAGENT_ROOT", base)
		got, err := repositoryRootFrom("")
		if err != nil {
			t.Fatal(err)
		}
		if got != base {
			t.Fatalf("repositoryRootFrom() = %q, want %q", got, base)
		}
	})
	t.Run("cwd fallback", func(t *testing.T) {
		t.Setenv("DEEPAGENT_ROOT", "")
		got, err := repositoryRootFrom("")
		if err != nil {
			t.Fatal(err)
		}
		want := findRepositoryRoot(base)
		if got != want {
			t.Fatalf("repositoryRootFrom() = %q, want %q", got, want)
		}
	})
}

func TestFindRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "yaml", "config.yaml"), []byte("default_model: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := filepath.Join(root, "deepagent", "cmd", "deepagent")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findRepositoryRoot(start); got != root {
		t.Fatalf("findRepositoryRoot() = %q, want %q", got, root)
	}
}

func TestRunCLIRemoteSkipsLocalConfigWorkdirAndSessionState(t *testing.T) {
	submitted := make(chan struct{})
	var submitOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Bytedance-User") != "opaque-user-token" {
			t.Errorf("X-Bytedance-User = %q", request.Header.Get("X-Bytedance-User"))
		}
		switch request.URL.Path {
		case "/ad/deep_agent_sdk/create_session":
			writeCLIHTTPJSON(t, writer, map[string]any{
				"session_view": map[string]any{"session": map[string]any{
					"session_id": "1788607944320815478", "uid": "42", "project_name": "remote-project", "project_path": "/srv/worktree", "main_thread_id": "0", "status": 1,
				}},
				"BaseResp": map[string]any{"StatusCode": 0},
			})
		case "/ad/deep_agent_sdk/subscribe_timeline":
			writer.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := writer.(http.Flusher)
			if !ok {
				t.Error("response writer does not flush SSE")
				return
			}
			fmt.Fprint(writer, "event: queue\ndata: {\"queue_id\":\"queue-1\",\"BaseResp\":{\"StatusCode\":0}}\n\n")
			flusher.Flush()
			select {
			case <-submitted:
			case <-request.Context().Done():
				return
			}
			fmt.Fprint(writer, "event: event\ndata: {\"event\":{\"event_id\":\"1\",\"event_type\":\"TURN_STARTED\",\"session_id\":\"1788607944320815478\",\"thread_id\":\"1788607944320815489\",\"turn_id\":\"turn-1\",\"payload\":{\"consumed_message_ids\":[\"message-1\"]}},\"BaseResp\":{\"StatusCode\":0}}\n\n")
			fmt.Fprint(writer, "event: event\ndata: {\"event\":{\"event_id\":\"2\",\"event_type\":\"ASSISTANT_DELTA\",\"session_id\":\"1788607944320815478\",\"thread_id\":\"1788607944320815489\",\"turn_id\":\"turn-1\",\"payload\":{\"delta\":\"remote ok\"}},\"BaseResp\":{\"StatusCode\":0}}\n\n")
			fmt.Fprint(writer, "event: event\ndata: {\"event\":{\"event_id\":\"3\",\"event_type\":\"TURN_FINISHED\",\"session_id\":\"1788607944320815478\",\"thread_id\":\"1788607944320815489\",\"turn_id\":\"turn-1\",\"payload\":{}},\"BaseResp\":{\"StatusCode\":0}}\n\n")
			flusher.Flush()
			<-request.Context().Done()
		case "/ad/deep_agent_sdk/submit_input":
			submitOnce.Do(func() { close(submitted) })
			writeCLIHTTPJSON(t, writer, map[string]any{
				"message": map[string]any{"thread_id": "1788607944320815489", "message_id": "message-1"},
				"session_view": map[string]any{"session": map[string]any{
					"session_id": "1788607944320815478", "uid": "42", "project_name": "remote-project", "project_path": "/srv/worktree", "main_thread_id": "1788607944320815489", "status": 1,
				}},
				"BaseResp": map[string]any{"StatusCode": 0},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	t.Setenv("DEEPAGENT_RUNTIME", "remote")
	t.Setenv("DEEPAGENT_SERVER_URL", server.URL)
	t.Setenv("DEEPAGENT_PROJECT", "remote-project")
	t.Setenv("DEEPAGENT_USER_TOKEN", "opaque-user-token")
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	var output strings.Builder
	err = runCLI(ctx, nil, CLIOptions{WorkDir: filepath.Join(t.TempDir(), "missing"), Prompt: "hello"}, nil, strings.NewReader(""), &output)
	if err != nil {
		t.Fatalf("runCLI(remote) error = %v", err)
	}
	after, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("remote startup changed cwd from %q to %q", before, after)
	}
	if !strings.Contains(output.String(), "remote ok") {
		t.Fatalf("output = %q", output.String())
	}
}

func writeCLIHTTPJSON(t *testing.T, writer http.ResponseWriter, payload any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		t.Errorf("encode fixture response: %v", err)
	}
}
