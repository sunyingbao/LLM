package tui

import (
	"context"
	"errors"
	"testing"

	"eino-cli/deepagent/cloud/protocol/timeline"
	clientruntime "eino-cli/deepagent/host/runtime"
)

type sessionCommandRuntime struct {
	remoteStubRuntime
	opened        string
	followContext context.Context
	followErr     error
	active        *clientruntime.TurnStream
}

func (runtime *sessionCommandRuntime) OpenSession(_ context.Context, id string) (err error) {
	runtime.opened = id
	return nil
}

func (runtime *sessionCommandRuntime) ListSessions(_ context.Context) (sessions []clientruntime.SessionInfo, err error) {
	return []clientruntime.SessionInfo{{ID: "session-1", Title: "Existing task"}}, nil
}

func (runtime *sessionCommandRuntime) Session() (session clientruntime.SessionInfo) {
	return clientruntime.SessionInfo{ID: runtime.opened, ProjectName: "test", ProjectPath: "/srv/project"}
}

func (runtime *sessionCommandRuntime) FollowSession(ctx context.Context) (history []timeline.Event, stream *clientruntime.TurnStream, err error) {
	runtime.followContext = ctx
	return []timeline.Event{{EventID: "event-1"}}, runtime.active, runtime.followErr
}

func TestRemoteSessionCommands(t *testing.T) {
	runtime := &sessionCommandRuntime{}
	listed := listRemoteSessionsCmd(runtime)().(remoteSessionsMsg)
	if listed.err != nil || len(listed.sessions) != 1 || listed.sessions[0].ID != "session-1" {
		t.Fatalf("list result = %+v", listed)
	}
	loaded := followRemoteSessionCmd(runtime, "session-1")().(remoteSessionLoadedMsg)
	defer loaded.cancel()
	if loaded.err != nil || runtime.opened != "session-1" || loaded.session.ProjectPath != "/srv/project" || len(loaded.history) != 1 {
		t.Fatalf("follow result = %+v", loaded)
	}
}

func TestRemoteSessionFollowLifetime(t *testing.T) {
	for _, active := range []bool{false, true} {
		runtime := &sessionCommandRuntime{}
		if active {
			runtime.active = &clientruntime.TurnStream{TurnID: "turn-1"}
		}
		loaded := followRemoteSessionCmd(runtime, "session-1")().(remoteSessionLoadedMsg)
		if active && runtime.followContext.Err() != nil {
			t.Fatal("active follow context canceled before it could be observed")
		}
		loaded.cancel()
		if runtime.followContext.Err() == nil {
			t.Fatal("detach did not cancel follow context")
		}
	}
	runtime := &sessionCommandRuntime{followErr: errors.New("offline")}
	loaded := followRemoteSessionCmd(runtime, "session-1")().(remoteSessionLoadedMsg)
	if loaded.err == nil || runtime.followContext.Err() == nil {
		t.Fatal("failed follow should report its error and release its context")
	}
}
