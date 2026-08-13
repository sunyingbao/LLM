//go:build !windows

package worker

import (
	"testing"

	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
)

func TestThreadInfoFromCoordinatorCopiesStableView(t *testing.T) {
	sessionID := "session-1"
	env := "boe"
	thread := &ac.Thread{
		ThreadId:  42,
		Namespace: "demo",
		SessionId: &sessionID,
		Status:    ac.ThreadStatus_RUNNING,
		Env:       &env,
		UserId:    1001,
		Metadata:  map[string]string{"project_name": "demo-project"},
		Profile:   &ac.ThreadProfile{Role: "main", Cwd: "/workspace"},
	}

	info := threadInfoFromCoordinator(thread)
	if info.ThreadID != 42 || info.SessionID != sessionID || info.UserID != 1001 {
		t.Fatalf("thread identity = %+v", info)
	}
	if info.Status != ThreadStatusRunning || info.Env != env {
		t.Fatalf("thread lifecycle = %+v", info)
	}
	if info.Role != "main" || info.CWD != "/workspace" {
		t.Fatalf("thread profile = %+v", info)
	}

	info.Metadata["project_name"] = "changed"
	if thread.Metadata["project_name"] != "demo-project" {
		t.Fatalf("metadata aliases Coordinator object: %+v", thread.Metadata)
	}
}
