package runtime

import (
	"context"
	"errors"
	"testing"

	sdkruntime "eino-cli/deepagent/runtime"
)

func TestConfigFromEnvDefaultsLocalAndAcceptsRemote(t *testing.T) {
	t.Setenv(RuntimeEnvironmentVariable, "")
	config, err := ConfigFromEnv()
	if err != nil || config.DefaultRuntime != sdkruntime.RuntimeLocal {
		t.Fatalf("ConfigFromEnv(default) config=%+v error=%v", config, err)
	}
	t.Setenv(RuntimeEnvironmentVariable, "remote")
	config, err = ConfigFromEnv()
	if err != nil || config.DefaultRuntime != sdkruntime.RuntimeRemote {
		t.Fatalf("ConfigFromEnv(remote) config=%+v error=%v", config, err)
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

func TestParseRuntimeKindRejectsAutomaticFallback(t *testing.T) {
	t.Parallel()

	if _, err := ParseRuntimeKind("auto"); err == nil {
		t.Fatal("ParseRuntimeKind(auto) error = nil")
	}
}
