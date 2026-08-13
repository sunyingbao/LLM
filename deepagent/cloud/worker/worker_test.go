//go:build !windows

package worker

import (
	"errors"
	"reflect"
	"testing"

	"eino-cli/deepagent/worker/cloud"
)

func TestParseHostPorts(t *testing.T) {
	got := ParseHostPorts(" 127.0.0.1:8888, ,127.0.0.1:9999 ")
	want := []string{"127.0.0.1:8888", "127.0.0.1:9999"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseHostPorts()=%v, want %v", got, want)
	}
}

func TestNewWorkerValidatesRequiredDeps(t *testing.T) {
	_, err := newWorker(HostConfig{Namespace: "ns"}, nil, cloud.AgentThreadFactory(nil))
	if !errors.Is(err, cloud.ErrMissingClient) {
		t.Fatalf("newWorker() error=%v, want %v", err, cloud.ErrMissingClient)
	}
}
