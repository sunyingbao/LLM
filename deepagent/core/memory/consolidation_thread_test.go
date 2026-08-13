package memory

import (
	"context"
	"testing"
	"time"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/mock/mock_model"
	"go.uber.org/mock/gomock"
)

func TestConsolidationTurnRunnerConfigIsRestricted(t *testing.T) {
	ctrl := gomock.NewController(t)
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	cfg := newConsolidationTurnRunnerConfig(ConsolidationAgentThreadConfig{
		ThreadID:  "42",
		ChatModel: mock_model.NewMockToolCallingChatModel(ctrl),
		Workspace: NewWorkspace(backend, "memory"),
	})

	if len(cfg.Tools) != 0 {
		t.Fatalf("tools=%d, want no business tools", len(cfg.Tools))
	}
	if len(cfg.Middlewares) != 0 {
		t.Fatalf("middlewares=%d, want no normal prompt/collab middleware", len(cfg.Middlewares))
	}
	if cfg.EnablePlan {
		t.Fatal("EnablePlan=true, want false")
	}
	if !cfg.EnableFilesystem || cfg.FilesystemConfig == nil {
		t.Fatalf("filesystem config missing: %+v", cfg)
	}
	if cfg.FilesystemConfig.WorkDir != "memory" || !cfg.FilesystemConfig.DisableExecute || !cfg.FilesystemConfig.DisableUploadDownload {
		t.Fatalf("filesystem config=%+v, want memory root with execute/upload disabled", cfg.FilesystemConfig)
	}
	if cfg.SkillLoader != nil {
		t.Fatal("SkillLoader set, want nil")
	}
}

func TestConsolidationAgentThreadHeartbeatsStage2Lease(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	start := time.Now()
	requireNoError(t, store.EnqueueStage2(ctx, EnqueueStage2Request{
		UserID:         "123",
		InputWatermark: "w1",
		Now:            start,
	}))
	claimed, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:      start,
		Owner:    "worker-a",
		LeaseTTL: 20 * time.Millisecond,
		Limit:    1,
	})
	requireNoError(t, err)
	if len(claimed) != 1 {
		t.Fatalf("claimed=%d, want 1", len(claimed))
	}

	ctrl := gomock.NewController(t)
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	metadata, err := BuildStage2Metadata(nil, Stage2ThreadSpec{
		UserID:         "123",
		OwnershipToken: claimed[0].OwnershipToken,
		InputWatermark: "w1",
	})
	requireNoError(t, err)
	thread, err := NewConsolidationAgentThread(ConsolidationAgentThreadConfig{
		ThreadID:     "42",
		ChatModel:    mock_model.NewMockToolCallingChatModel(ctrl),
		HistoryStore: &fakeHistoryStore{},
		Store:        store,
		Workspace:    NewWorkspace(backend, "memory"),
		Metadata:     metadata,
		LeaseTTL:     90 * time.Millisecond,
	})
	requireNoError(t, err)
	if _, err := thread.Init(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = thread.Close(ctx) }()

	time.Sleep(130 * time.Millisecond)
	deadline := time.After(time.Second)
	for {
		reclaimed, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
			Now:      time.Now(),
			Owner:    "worker-b",
			LeaseTTL: time.Minute,
			Limit:    1,
		})
		requireNoError(t, err)
		if len(reclaimed) == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("stage2 lease was reclaimed while consolidation thread heartbeat was running")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestConsolidationAgentThreadStaysActiveDuringFinalization(t *testing.T) {
	thread := &consolidationAgentThread{
		active: &consolidationActiveTurn{
			turnID:             "turn-1",
			consumedMessageIDs: []string{"msg-1"},
		},
	}

	active := thread.ActiveTurn()
	if active == nil {
		t.Fatal("ActiveTurn() nil, want active while finalization is pending")
	}
	if active.TurnID != "turn-1" || len(active.ConsumedMessageIDs) != 1 || active.ConsumedMessageIDs[0] != "msg-1" {
		t.Fatalf("ActiveTurn() = %+v", active)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
