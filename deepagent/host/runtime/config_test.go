package runtime

import (
	"context"
	"errors"
	"testing"

	sdkruntime "eino-cli/deepagent/runtime"
)

func TestConfigFromEnvDefaultsLocalAndAcceptsRemote(t *testing.T) {
	t.Setenv(RuntimeEnvironmentVariable, "")
	t.Setenv(LegacyImportEnvironmentVariable, "")
	config, err := ConfigFromEnv()
	if err != nil || config.DefaultRuntime != sdkruntime.RuntimeLocal || config.LegacyImportPolicy != LegacyImportPrompt {
		t.Fatalf("ConfigFromEnv(default) config=%+v error=%v", config, err)
	}
	t.Setenv(RuntimeEnvironmentVariable, "remote")
	config, err = ConfigFromEnv()
	if err != nil || config.DefaultRuntime != sdkruntime.RuntimeRemote {
		t.Fatalf("ConfigFromEnv(remote) config=%+v error=%v", config, err)
	}
}

func TestParseLegacyImportPolicy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value string
		want  LegacyImportPolicy
	}{
		{value: "", want: LegacyImportPrompt},
		{value: "OFF", want: LegacyImportOff},
		{value: "auto", want: LegacyImportAuto},
	} {
		policy, err := ParseLegacyImportPolicy(test.value)
		if err != nil || policy != test.want {
			t.Fatalf("ParseLegacyImportPolicy(%q) = %q, %v; want %q", test.value, policy, err, test.want)
		}
	}
	if _, err := ParseLegacyImportPolicy("always"); err == nil {
		t.Fatal("ParseLegacyImportPolicy(always) error = nil")
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
