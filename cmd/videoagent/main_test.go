package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eino-cli/backend/videoagent"
)

func TestExampleConfigsDecode(t *testing.T) {
	root := filepath.Join("..", "..", "configs", "videoagent")
	if _, err := readJSON[videoagent.RemoteConfig](filepath.Join(root, "remote.example.json")); err != nil {
		t.Fatalf("decode remote config: %v", err)
	}
	if _, err := readJSON[videoagent.ChatModelConfig](filepath.Join(root, "model.example.json")); err != nil {
		t.Fatalf("decode model config: %v", err)
	}
	if _, err := readJSON[videoagent.PromptRuntimeConfig](filepath.Join(root, "prompt.example.json")); err != nil {
		t.Fatalf("decode prompt config: %v", err)
	}
}

func TestRemoteApplicationRequiresModelAndPromptConfig(t *testing.T) {
	_, err := newRemoteApplication(context.Background(), t.TempDir(), "unused", "", "unused", "", "", "", "main", nil)
	if err == nil || !strings.Contains(err.Error(), "model config is required") {
		t.Fatalf("newRemoteApplication() error = %v, want missing model config", err)
	}
	_, err = newRemoteApplication(context.Background(), t.TempDir(), "unused", "unused", "", "", "", "", "main", nil)
	if err == nil || !strings.Contains(err.Error(), "prompt config is required") {
		t.Fatalf("newRemoteApplication() error = %v, want missing prompt config", err)
	}
}

func TestRemoteApplicationRejectsIncompleteMediaConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := newRemoteApplication(context.Background(), t.TempDir(), path, "model", "prompt", "", "", "", "main", nil)
	if err == nil || !strings.Contains(err.Error(), "remote canvas config is incomplete") {
		t.Fatalf("newRemoteApplication() error = %v, want incomplete remote config", err)
	}
}
