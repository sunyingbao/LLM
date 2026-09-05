//go:build !windows

package worker

import (
	"errors"
	"testing"

	"eino-cli/deepagent/worker/cloud"
)

func TestNewWorkerValidatesRequiredDeps(t *testing.T) {
	_, err := newWorker(HostConfig{Namespace: "ns"}, nil, cloud.AgentThreadFactory(nil))
	if !errors.Is(err, cloud.ErrMissingClient) {
		t.Fatalf("newWorker() error=%v, want %v", err, cloud.ErrMissingClient)
	}
}
