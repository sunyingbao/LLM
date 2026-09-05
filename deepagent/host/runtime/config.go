package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"

	sdkruntime "eino-cli/deepagent/runtime"

	"eino-cli/deepagent/backend/config"
)

const (
	RuntimeEnvironmentVariable   = "DEEPAGENT_RUNTIME"
	ServerURLEnvironmentVariable = "DEEPAGENT_SERVER_URL"
	ProjectEnvironmentVariable   = "DEEPAGENT_PROJECT"
	UserTokenEnvironmentVariable = "DEEPAGENT_USER_TOKEN"
)

func NewInteractiveRuntime(ctx context.Context, appConfig *config.Config, sessionID string) (runtime InteractiveRuntime, err error) {
	runtimeKind, err := RuntimeKindFromEnv()
	if err != nil {
		return nil, err
	}
	switch runtimeKind {
	case sdkruntime.RuntimeLocal:
		localRuntime, localErr := NewLocalRuntime(ctx, appConfig, sessionID)
		runtime, err = localRuntime, localErr
		return runtime, err
	case sdkruntime.RuntimeRemote:
		serverURL := strings.TrimSpace(os.Getenv(ServerURLEnvironmentVariable))
		project := strings.TrimSpace(os.Getenv(ProjectEnvironmentVariable))
		if serverURL == "" || project == "" {
			return nil, &sdkruntime.Error{
				Code: sdkruntime.ErrorCodeCapabilityUnavailable, Op: "runtime.create", Runtime: sdkruntime.RuntimeRemote,
				Message: "remote runtime requires DEEPAGENT_SERVER_URL and DEEPAGENT_PROJECT",
			}
		}
		runtime, err = NewHTTPRuntime(serverURL, project, strings.TrimSpace(os.Getenv(UserTokenEnvironmentVariable)))
		return runtime, err
	default:
		return nil, fmt.Errorf("unsupported runtime %q", runtimeKind)
	}
}

func RuntimeKindFromEnv() (kind sdkruntime.RuntimeKind, err error) {
	value := strings.TrimSpace(os.Getenv(RuntimeEnvironmentVariable))
	if value == "" {
		value = string(sdkruntime.RuntimeLocal)
	}
	kind, err = parseRuntimeKind(value)
	return kind, err
}

func parseRuntimeKind(value string) (kind sdkruntime.RuntimeKind, err error) {
	kind = sdkruntime.RuntimeKind(strings.ToLower(strings.TrimSpace(value)))
	if err = kind.Validate(); err != nil {
		return "", fmt.Errorf("parse runtime %q: %w", value, err)
	}
	return kind, nil
}
