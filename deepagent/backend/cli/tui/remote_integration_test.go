package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"eino-cli/deepagent/backend/config"
	clientruntime "eino-cli/deepagent/host/runtime"
	sdkruntime "eino-cli/deepagent/runtime"
	tea "github.com/charmbracelet/bubbletea"
)

type remoteStubRuntime struct {
	stubRuntime
	startEntered chan struct{}
	startRelease chan struct{}
}

func (runtime *remoteStubRuntime) RuntimeKind() sdkruntime.RuntimeKind {
	return sdkruntime.RuntimeRemote
}

func (runtime *remoteStubRuntime) Name() string {
	return "remote:test"
}

func (runtime *remoteStubRuntime) StartTurn(ctx context.Context, prompt string) (stream *clientruntime.TurnStream, err error) {
	if runtime.startEntered != nil {
		close(runtime.startEntered)
	}
	if runtime.startRelease != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-runtime.startRelease:
		}
	}
	return nil, errors.New("fixture stopped")
}

func TestNewRemoteModelSkipsLocalRunStoreAndSkillDiscovery(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "backend", "skills", "local-only")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: local-only\ndescription: local\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := config.SetRootDirForTest(root)
	t.Cleanup(restore)

	model, err := New(&remoteStubRuntime{}, "")
	if err != nil {
		t.Fatalf("New(remote) error = %v", err)
	}
	if model.runs != nil {
		t.Fatal("remote model created a local run/snapshot manager")
	}
	if _, ok := getCommand(model.commands, "local-only"); ok {
		t.Fatal("remote model discovered a local skill command")
	}
	if _, err = os.Stat(filepath.Join(root, ".eino-cli")); !os.IsNotExist(err) {
		t.Fatalf("remote model created local state: %v", err)
	}
}

func TestRemoteSubmitStartsNetworkWithoutBlockingUpdate(t *testing.T) {
	runtime := &remoteStubRuntime{startEntered: make(chan struct{}), startRelease: make(chan struct{})}
	t.Cleanup(func() { close(runtime.startRelease) })
	model, err := New(runtime, "")
	if err != nil {
		t.Fatal(err)
	}
	returned := make(chan struct{})
	var command func() any
	go func() {
		_, cmd := submit(model, "hello")
		if cmd != nil {
			command = func() any { return cmd() }
		}
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("submit blocked the Bubble Tea update loop on StartTurn")
	}
	if command == nil {
		t.Fatal("submit did not return an asynchronous command")
	}
	started := make(chan struct{})
	go func() {
		message := command()
		if batch, ok := message.(tea.BatchMsg); ok {
			for _, cmd := range batch {
				go cmd()
			}
		}
		close(started)
	}()
	select {
	case <-runtime.startEntered:
	case <-time.After(time.Second):
		t.Fatal("asynchronous command did not start the remote turn")
	}
}
