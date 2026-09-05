//go:build !windows

package worker

import (
	"context"
	"testing"

	"eino-cli/deepagent/coordinator"
	"eino-cli/deepagent/worker/tasktool"
)

func TestCollabTaskToolUsesResolvedThreadProfileForSpawnDefaults(t *testing.T) {
	b := &threadBuilder{
		cfg: Config{Host: HostConfig{Namespace: "ns", Concurrency: 2}},
		deps: Deps{
			MessageWaitObserver: func(events []*tasktool.Event, messageID string) tasktool.MessageWaitResult {
				return tasktool.MessageWaitResult{}
			},
		},
	}
	threadInfo := &coordinator.Thread{
		ThreadID:  42,
		UserID:    1001,
		SessionID: "session-1",
		Metadata: map[string]string{
			MetadataProjectName: "wire-project",
		},
	}

	toolset := b.collabTaskTool(context.Background(), threadInfo, ResolvedThreadProfile{
		RoleID:  DefaultRoleID,
		WorkDir: "/resolved/workspace",
		Project: "resolved-project",
	})

	if toolset == nil {
		t.Fatal("collabTaskTool() returned nil")
	}
	if toolset.SpawnProfile.Cwd != "/resolved/workspace" {
		t.Fatalf("spawn cwd=%q, want resolved workdir", toolset.SpawnProfile.Cwd)
	}
	if toolset.Metadata[MetadataProjectName] != "resolved-project" {
		t.Fatalf("project metadata=%q, want resolved project", toolset.Metadata[MetadataProjectName])
	}
}
