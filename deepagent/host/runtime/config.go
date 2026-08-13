package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"

	cloudapi "eino-cli/deepagent/cloud/api"
	sdkruntime "eino-cli/deepagent/runtime"
	remoteclient "eino-cli/deepagent/runtime/remote"

	"eino-cli/backend/config"
)

const (
	RuntimeEnvironmentVariable         = "SGADK_RUNTIME"
	LegacyImportEnvironmentVariable    = "SGADK_LEGACY_IMPORT"
	RemotePSMEnvironmentVariable       = "SGADK_REMOTE_AC_PSM"
	RemoteNamespaceEnvironmentVariable = "SGADK_REMOTE_NAMESPACE"
	RemoteClusterEnvironmentVariable   = "SGADK_REMOTE_AC_CLUSTER"
	RemoteHostPortsEnvironmentVariable = "SGADK_REMOTE_AC_HOSTPORTS"
	RemoteEnvEnvironmentVariable       = "SGADK_REMOTE_ENV"
	RemoteUserIDEnvironmentVariable    = "SGADK_REMOTE_USER_ID"
)

type LegacyImportPolicy string

const (
	LegacyImportOff    LegacyImportPolicy = "off"
	LegacyImportPrompt LegacyImportPolicy = "prompt"
	LegacyImportAuto   LegacyImportPolicy = "auto"
)

type Config struct {
	DefaultRuntime     sdkruntime.RuntimeKind
	LegacyImportPolicy LegacyImportPolicy
}

func NewInteractiveRuntime(ctx context.Context, appConfig *config.Config, sessionID string) (runtime InteractiveRuntime, err error) {
	runtimeConfig, err := ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	switch runtimeConfig.DefaultRuntime {
	case sdkruntime.RuntimeLocal:
		runtime, err = NewLocalRuntime(ctx, appConfig, sessionID)
		return runtime, err
	case sdkruntime.RuntimeRemote:
		remote, remoteErr := newRemoteClientFromEnv()
		if remoteErr != nil {
			return nil, remoteErr
		}
		runtime, err = NewRemoteRuntime(ctx, appConfig, sessionID, remote)
		return runtime, err
	default:
		return nil, fmt.Errorf("unsupported runtime %q", runtimeConfig.DefaultRuntime)
	}
}

func newRemoteClientFromEnv() (client sdkruntime.Client, err error) {
	psm := strings.TrimSpace(os.Getenv(RemotePSMEnvironmentVariable))
	namespace := strings.TrimSpace(os.Getenv(RemoteNamespaceEnvironmentVariable))
	userID := strings.TrimSpace(os.Getenv(RemoteUserIDEnvironmentVariable))
	if psm == "" || namespace == "" || userID == "" {
		return nil, &sdkruntime.Error{Code: sdkruntime.ErrorCodeCapabilityUnavailable, Op: "runtime.create", Runtime: sdkruntime.RuntimeRemote, Message: "remote runtime requires SGADK_REMOTE_AC_PSM, SGADK_REMOTE_NAMESPACE, and SGADK_REMOTE_USER_ID"}
	}
	api, err := cloudapi.NewCoordinatorAPI(cloudapi.CoordinatorClientConfig{
		PSM: psm, Namespace: namespace,
		Cluster:   strings.TrimSpace(os.Getenv(RemoteClusterEnvironmentVariable)),
		HostPorts: splitCommaSeparated(os.Getenv(RemoteHostPortsEnvironmentVariable)),
		Env:       strings.TrimSpace(os.Getenv(RemoteEnvEnvironmentVariable)),
	})
	if err != nil {
		return nil, &sdkruntime.Error{Code: sdkruntime.ErrorCodeUnavailable, Op: "runtime.create", Runtime: sdkruntime.RuntimeRemote, Cause: err}
	}
	client, err = remoteclient.New(api, userID)
	return client, err
}

func splitCommaSeparated(value string) (values []string) {
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func ConfigFromEnv() (config Config, err error) {
	value := strings.TrimSpace(os.Getenv(RuntimeEnvironmentVariable))
	if value == "" {
		value = string(sdkruntime.RuntimeLocal)
	}
	if config.DefaultRuntime, err = ParseRuntimeKind(value); err != nil {
		return Config{}, err
	}
	if config.LegacyImportPolicy, err = ParseLegacyImportPolicy(os.Getenv(LegacyImportEnvironmentVariable)); err != nil {
		return Config{}, err
	}
	return config, nil
}

func ParseLegacyImportPolicy(value string) (policy LegacyImportPolicy, err error) {
	policy = LegacyImportPolicy(strings.ToLower(strings.TrimSpace(value)))
	if policy == "" {
		policy = LegacyImportPrompt
	}
	switch policy {
	case LegacyImportOff, LegacyImportPrompt, LegacyImportAuto:
		return policy, nil
	default:
		return "", fmt.Errorf("parse legacy import policy %q: expected off, prompt, or auto", value)
	}
}

func ParseRuntimeKind(value string) (kind sdkruntime.RuntimeKind, err error) {
	kind = sdkruntime.RuntimeKind(strings.ToLower(strings.TrimSpace(value)))
	if err = kind.Validate(); err != nil {
		return "", fmt.Errorf("parse runtime %q: %w", value, err)
	}
	return kind, nil
}
