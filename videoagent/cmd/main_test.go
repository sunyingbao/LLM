package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eino-cli/videoagent/backend/media"
	"eino-cli/videoagent/backend/model"
	"eino-cli/videoagent/backend/planning"
)

func TestExampleConfigsDecode(t *testing.T) {
	root := filepath.Join("..", "..", "configs", "videoagent")
	if _, err := readJSON[media.RemoteConfig](filepath.Join(root, "remote.example.json")); err != nil {
		t.Fatalf("decode remote config: %v", err)
	}
	if _, err := readJSON[model.ChatModelConfig](filepath.Join(root, "model.example.json")); err != nil {
		t.Fatalf("decode model config: %v", err)
	}
	if _, err := readJSON[planning.PromptRuntimeConfig](filepath.Join(root, "prompt.example.json")); err != nil {
		t.Fatalf("decode prompt config: %v", err)
	}
}

func TestRemoteApplicationRequiresModelAndPromptConfig(t *testing.T) {
	_, err := newRemoteApplication(context.Background(), runOptions{
		dataDir:          t.TempDir(),
		remoteConfigPath: "unused",
		promptConfigPath: "unused",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "model config is required") {
		t.Fatalf("newRemoteApplication() error = %v, want missing model config", err)
	}
	_, err = newRemoteApplication(context.Background(), runOptions{
		dataDir:          t.TempDir(),
		remoteConfigPath: "unused",
		modelConfigPath:  "unused",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "prompt config is required") {
		t.Fatalf("newRemoteApplication() error = %v, want missing prompt config", err)
	}
}

func TestRemoteApplicationRejectsIncompleteMediaConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := newRemoteApplication(context.Background(), runOptions{
		dataDir:          t.TempDir(),
		remoteConfigPath: path,
		modelConfigPath:  "model",
		promptConfigPath: "prompt",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "remote canvas config is incomplete") {
		t.Fatalf("newRemoteApplication() error = %v, want incomplete remote config", err)
	}
}
