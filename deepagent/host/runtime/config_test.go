package runtime

import (
	"context"
	"errors"
	"testing"

	sdkruntime "eino-cli/deepagent/runtime"
)

func TestRuntimeKindFromEnvDefaultsLocalAndAcceptsRemote(t *testing.T) {
	t.Setenv(RuntimeEnvironmentVariable, "")
	kind, err := RuntimeKindFromEnv()
	if err != nil || kind != sdkruntime.RuntimeLocal {
		t.Fatalf("RuntimeKindFromEnv(default) kind=%q error=%v", kind, err)
	}
	t.Setenv(RuntimeEnvironmentVariable, "remote")
	kind, err = RuntimeKindFromEnv()
	if err != nil || kind != sdkruntime.RuntimeRemote {
		t.Fatalf("RuntimeKindFromEnv(remote) kind=%q error=%v", kind, err)
	}
}

func TestNewInteractiveRuntimeRejectsUnconfiguredRemoteBeforeConstruction(t *testing.T) {
	t.Setenv(RuntimeEnvironmentVariable, string(sdkruntime.RuntimeRemote))
	_, err := NewInteractiveRuntime(context.Background(), nil, "session")
	if !errors.Is(err, sdkruntime.ErrCapabilityUnavailable) {
		t.Fatalf("NewInteractiveRuntime(remote) error=%v, want capability unavailable", err)
	}
	var runtimeErr *sdkruntime.Error
	if !errors.As(err, &runtimeErr) || runtimeErr.Runtime != sdkruntime.RuntimeRemote {
		t.Fatalf("NewInteractiveRuntime(remote) typed error=%#v", runtimeErr)
	}
}

func TestNewInteractiveRuntimeBuildsHTTPRemoteWithoutLocalConfig(t *testing.T) {
	t.Setenv(RuntimeEnvironmentVariable, string(sdkruntime.RuntimeRemote))
	t.Setenv("DEEPAGENT_SERVER_URL", "https://agent.example.test")
	t.Setenv("DEEPAGENT_PROJECT", "remote-project")
	t.Setenv("DEEPAGENT_USER_TOKEN", "opaque-token")

	runtime, err := NewInteractiveRuntime(context.Background(), nil, "")
	if err != nil {
		t.Fatalf("NewInteractiveRuntime(remote) error = %v", err)
	}
	if runtime.RuntimeKind() != sdkruntime.RuntimeRemote {
		t.Fatalf("RuntimeKind() = %q, want %q", runtime.RuntimeKind(), sdkruntime.RuntimeRemote)
	}
	if runtime.Name() != "remote:agent.example.test" {
		t.Fatalf("Name() = %q, want remote backend name", runtime.Name())
	}
}

func TestParseRuntimeKindRejectsAutomaticFallback(t *testing.T) {
	t.Parallel()

	if _, err := parseRuntimeKind("auto"); err == nil {
		t.Fatal("parseRuntimeKind(auto) error = nil")
	}
}
