//go:build !windows

package worker

import (
	"context"
	"testing"
	"time"

	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/memory"
	"eino-cli/deepagent/mock/mock_model"
	"eino-cli/deepagent/worker"
	"go.uber.org/mock/gomock"
)

func TestBuildMemoryTurnObserverDisabledByDefault(t *testing.T) {
	b := &threadBuilder{
		cfg:  Config{},
		deps: Deps{MemoryStore: memory.NewInMemoryStore()},
	}
	if got := b.buildMemoryTurnObserver(&ac.Thread{ThreadId: 42, UserId: 123}); got != nil {
		t.Fatalf("observer=%v, want nil when memory is disabled", got)
	}
}

func TestBuildMemoryTurnObserverTouchesSourceOnly(t *testing.T) {
	ctx := context.Background()
	store := memory.NewInMemoryStore()
	b := &threadBuilder{
		cfg: Config{
			Memory: MemoryConfig{
				Enabled:          true,
				Stage1IdleWindow: 5 * time.Minute,
			},
		},
		deps: Deps{MemoryStore: store},
	}
	observer := b.buildMemoryTurnObserver(&ac.Thread{ThreadId: 42, UserId: 123})
	if observer == nil {
		t.Fatal("observer=nil, want non-nil")
	}

	observer(ctx, agentthread.Event{
		ThreadID: "42",
		TurnID:   "turn-1",
		Type:     agentthread.EventTurnEnd,
		TS:       time.Unix(100, 0),
	})

	deadline := time.After(time.Second)
	for {
		claimed, err := store.ClaimStage1Sources(ctx, memory.ClaimStage1Request{
			Now:      time.Unix(401, 0),
			Owner:    "test-worker",
			LeaseTTL: time.Minute,
			Limit:    1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(claimed) == 1 {
			if claimed[0].UserID != "123" || claimed[0].SourceThreadID != "42" {
				t.Fatalf("claimed source=%+v", claimed[0])
			}
			if !claimed[0].SourceUpdatedAt.Equal(time.Unix(100, 0)) {
				t.Fatalf("SourceUpdatedAt=%v, want unix 100", claimed[0].SourceUpdatedAt)
			}
			if !claimed[0].EligibleAt.Equal(time.Unix(400, 0)) {
				t.Fatalf("EligibleAt=%v, want unix 400", claimed[0].EligibleAt)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("source was not touched")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestBuildMemoryTurnObserverSkipsConsolidationThread(t *testing.T) {
	b := &threadBuilder{
		cfg: Config{Memory: MemoryConfig{Enabled: true}},
		deps: Deps{
			MemoryStore: memory.NewInMemoryStore(),
		},
	}
	metadata, err := memory.BuildStage2Metadata(nil, memory.Stage2ThreadSpec{
		UserID:         "123",
		OwnershipToken: "token",
		InputWatermark: "w1",
	})
	if err != nil {
		t.Fatal(err)
	}
	observer := b.buildMemoryTurnObserver(&ac.Thread{
		ThreadId: 42,
		UserId:   123,
		Metadata: metadata,
	})
	if observer != nil {
		t.Fatalf("observer=%v, want nil for memory-owned consolidation thread", observer)
	}
}

func TestNewMemoryJobLoopFromConfigDisabledByDefault(t *testing.T) {
	loop, err := newMemoryJobLoopFromConfig(context.Background(), Config{}, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if loop != nil {
		t.Fatalf("loop=%v, want nil when memory is disabled", loop)
	}
}

func TestNewMemoryJobLoopFromConfigEnabledWithStore(t *testing.T) {
	ctrl := gomock.NewController(t)
	cfg := Config{
		Host:   HostConfig{LeaseOwnerHint: "worker-a"},
		Memory: MemoryConfig{Enabled: true},
		Turn:   testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl)),
	}
	loop, err := newMemoryJobLoopFromConfig(context.Background(), cfg, Deps{
		MemoryStore: memory.NewInMemoryStore(),
		HistoryStore: func(_ context.Context, _ string) (agentthread.HistoryRolloutStore, error) {
			return fakeMemoryHistoryStore{}, nil
		},
		MemoryWorkspace: memory.NewWorkspace(backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
			RootDir:     t.TempDir(),
			VirtualMode: true,
		}), "memory"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if loop == nil {
		t.Fatal("loop=nil, want memory job loop")
	}
}

func TestNewMemoryJobLoopFromConfigBuildsDefaultWorkspace(t *testing.T) {
	ctrl := gomock.NewController(t)
	cfg := Config{
		Host: HostConfig{LeaseOwnerHint: "worker-a"},
		Memory: MemoryConfig{
			Enabled:       true,
			WorkspaceRoot: t.TempDir(),
		},
		Turn: testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl)),
	}
	loop, err := newMemoryJobLoopFromConfig(context.Background(), cfg, Deps{
		MemoryStore: memory.NewInMemoryStore(),
		HistoryStore: func(_ context.Context, _ string) (agentthread.HistoryRolloutStore, error) {
			return fakeMemoryHistoryStore{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if loop == nil {
		t.Fatal("loop=nil, want memory job loop")
	}
}

func TestResolveMemoryDepsBuildsDefaultWorkspace(t *testing.T) {
	deps := resolveMemoryDeps(MemoryConfig{
		Enabled:       true,
		WorkspaceRoot: t.TempDir(),
	}, Deps{})
	if deps.MemoryWorkspace == nil {
		t.Fatal("MemoryWorkspace=nil, want default workspace from config")
	}
	if deps.MemoryWorkspace.AgentBackend() == nil {
		t.Fatal("MemoryWorkspace.AgentBackend=nil, want sandbox backend")
	}
}

func TestNewMemoryJobLoopFromConfigRequiresWorkspace(t *testing.T) {
	ctrl := gomock.NewController(t)
	cfg := Config{
		Memory: MemoryConfig{Enabled: true},
		Turn:   testTurnConfig(mock_model.NewMockToolCallingChatModel(ctrl)),
	}
	_, err := newMemoryJobLoopFromConfig(context.Background(), cfg, Deps{
		MemoryStore: memory.NewInMemoryStore(),
		HistoryStore: func(_ context.Context, _ string) (agentthread.HistoryRolloutStore, error) {
			return fakeMemoryHistoryStore{}, nil
		},
	})
	if err == nil || err.Error() != "cloudagent: memory workspace is required" {
		t.Fatalf("error=%v, want memory workspace required", err)
	}
}

func TestBuildMemoryStage2CreateThreadRequestDoesNotUseWorkspaceRootAsCwd(t *testing.T) {
	req, err := buildMemoryStage2CreateThreadRequest(Config{
		Host: HostConfig{Namespace: "ns", Env: "boe"},
	}, memory.Stage2ThreadSpec{
		UserID:              "123",
		OwnershipToken:      "token",
		InputWatermark:      "w1",
		InputHash:           "hash",
		StartedArtifactHash: "artifact",
		StartedMemoryHash:   "memory-hash",
		StartedSummaryHash:  "summary-hash",
		WorkspaceRoot:       "memory",
		InitialPrompt:       "consolidate",
		Metadata:            map[string]string{"keep": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.GetProfile() == nil {
		t.Fatal("profile=nil")
	}
	if req.GetProfile().GetCwd() != "" {
		t.Fatalf("profile cwd=%q, want empty because memory thread uses MemoryWorkspace directly", req.GetProfile().GetCwd())
	}
	if req.GetProfile().GetRole() != DefaultRoleID {
		t.Fatalf("profile role=%q, want %q", req.GetProfile().GetRole(), DefaultRoleID)
	}
	metadata, err := memory.ParseStage2Metadata(req.GetMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if metadata.WorkspaceRoot != "memory" || req.GetMetadata()["keep"] != "yes" {
		t.Fatalf("metadata=%v", req.GetMetadata())
	}
	if metadata.StartedArtifactHash != "artifact" {
		t.Fatalf("artifact metadata=%q, want artifact", metadata.StartedArtifactHash)
	}
	if metadata.StartedMemoryHash != "memory-hash" {
		t.Fatalf("memory hash metadata=%q, want memory-hash", metadata.StartedMemoryHash)
	}
	if metadata.StartedSummaryHash != "summary-hash" {
		t.Fatalf("summary hash metadata=%q, want summary-hash", metadata.StartedSummaryHash)
	}
}

func TestNewMemoryConsolidationAgentThreadConsumesSpoofedMetadata(t *testing.T) {
	b := &threadBuilder{
		cfg: Config{
			Turn: testTurnConfig(nil),
		},
		deps: Deps{
			MemoryStore: memory.NewInMemoryStore(),
		},
	}

	metadata, err := memory.BuildStage2Metadata(nil, memory.Stage2ThreadSpec{
		UserID:         "victim-user",
		OwnershipToken: "spoofed-token",
		InputWatermark: "w1",
	})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := b.newMemoryConsolidationAgentThread(context.Background(), threadSpec{
		ThreadID: "42",
		Info:     &ac.Thread{Metadata: metadata},
	}, threadResources{}, ResolvedTurnProfile{Model: ModelProfile{}})
	if err != nil {
		t.Fatalf("newMemoryConsolidationAgentThread() error=%v", err)
	}
	output, err := thread.Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error=%v", err)
	}
	if _, err := thread.PostMessage(context.Background(), &agentworker.Message{ID: "msg-1"}); err != nil {
		t.Fatalf("PostMessage() error=%v", err)
	}
	select {
	case item := <-output.Items:
		if item.Yield == nil || item.Yield.Reason == "" {
			t.Fatalf("yield item=%+v, want stale yield", item)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stale yield")
	}
}

func TestNewAgentThreadRejectsConsolidationThreadWhenMemoryDisabled(t *testing.T) {
	b := &threadBuilder{
		cfg: Config{
			Turn: testTurnConfig(nil),
		},
	}

	metadata, err := memory.BuildStage2Metadata(nil, memory.Stage2ThreadSpec{
		UserID:         "123",
		OwnershipToken: "token",
		InputWatermark: "w1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.newAgentThread(context.Background(), &ac.Thread{
		ThreadId: 42,
		UserId:   123,
		Metadata: metadata,
	})
	if err == nil || err.Error() != "cloudagent: memory consolidation thread requires memory enabled" {
		t.Fatalf("newAgentThread() error=%v", err)
	}
}

type fakeMemoryHistoryStore struct{}

func (fakeMemoryHistoryStore) Append(context.Context, *agentthread.HistoryRecord) error {
	return nil
}

func (fakeMemoryHistoryStore) List(context.Context, agentthread.ListQuery) ([]*agentthread.HistoryRecord, error) {
	return nil, nil
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
