package memory

import (
	"context"
	"testing"
	"time"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func TestMemoryJobLoopRunOnceClaimsStage1(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	history := &fakeHistoryStore{}
	require.NoError(t, history.Append(ctx, &agentthread.HistoryRecord{
		Type:      agentthread.HistoryRecordMessage,
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		MessageID: 1,
		Message:   schema.UserMessage("我偏好具体可执行的计划。"),
		CreateAt:  100,
	}))
	require.NoError(t, store.TouchSource(ctx, TouchSourceRequest{
		Memory:          UserMemoryContext{UserID: "user-1", WriteEnabled: true, WorkspaceRoot: "memory"},
		SourceThreadID:  "thread-1",
		SourceUpdatedAt: time.Unix(100, 0),
		EligibleAt:      time.Unix(100, 0),
		Mode:            SourceModeEnabled,
	}))

	engine, err := NewEngine(Config{
		Store:   store,
		History: history,
		Extract: func(context.Context, Stage1ExtractInput) (Stage1ExtractResult, error) {
			return Stage1ExtractResult{
				RawMemory:      "Preference signals:\n- user likes concrete executable plans.",
				RolloutSummary: "# Plan preference\n\nOutcome: useful",
				RolloutSlug:    "plan-preference",
			}, nil
		},
		Now: func() time.Time { return time.Unix(110, 0) },
	})
	require.NoError(t, err)

	loop := NewMemoryJobLoop(MemoryJobLoopConfig{
		Store:                   store,
		Engine:                  engine,
		Owner:                   "worker-a",
		Stage1LeaseTTL:          time.Minute,
		Stage1MaxClaimedPerScan: 2,
		Now:                     func() time.Time { return time.Unix(110, 0) },
	})

	result, err := loop.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Stage1Claimed)
	require.Equal(t, 1, result.Stage1Succeeded)
	require.Zero(t, result.Stage1Failed)

	outputs, err := store.ListStage1Outputs(ctx, "user-1", ListStage1Options{Limit: 10})
	require.NoError(t, err)
	require.Len(t, outputs, 1)
}

func TestMemoryJobLoopRunOnceSkipsWhenNoEligibleSource(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	engine, err := NewEngine(Config{
		Store: store,
		Now:   func() time.Time { return time.Unix(100, 0) },
	})
	require.NoError(t, err)
	loop := NewMemoryJobLoop(MemoryJobLoopConfig{
		Store:                   store,
		Engine:                  engine,
		Owner:                   "worker-a",
		Stage1LeaseTTL:          time.Minute,
		Stage1MaxClaimedPerScan: 2,
		Now:                     func() time.Time { return time.Unix(100, 0) },
	})

	result, err := loop.RunOnce(ctx)
	require.NoError(t, err)
	require.Zero(t, result.Stage1Claimed)
	require.Zero(t, result.Stage1Succeeded)
	require.Zero(t, result.Stage2Attempted)
}

func TestMemoryJobLoopRunOnceCreatesStage2SystemThread(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	require.NoError(t, store.SaveStage1Output(ctx, Stage1Output{
		ID:              "stage1-1",
		UserID:          "user-1",
		SourceThreadID:  "thread-1",
		RawMemory:       "Preference signals:\n- user wants concise implementation docs.",
		RolloutSummary:  "# Summary\n\nUseful signal.",
		Status:          Stage1Succeeded,
		SourceUpdatedAt: time.Unix(100, 0),
		GeneratedAt:     time.Unix(101, 0),
	}))
	workspace := NewWorkspace(backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	}), "memory")
	engine, err := NewEngine(Config{
		Store:     store,
		Workspace: workspace,
		Now:       func() time.Time { return time.Unix(110, 0) },
	})
	require.NoError(t, err)
	host := &fakeStage2ThreadHost{threadID: "stage2-thread-1"}
	loop := NewMemoryJobLoop(MemoryJobLoopConfig{
		Store:                    store,
		Engine:                   engine,
		Stage2ThreadHost:         host,
		Owner:                    "worker-a",
		Stage2LeaseTTL:           time.Minute,
		Stage2SuccessCooldown:    6 * time.Hour,
		Stage2MaxUsersPerScan:    1,
		Stage2OutputLimitPerUser: 10,
		Now:                      func() time.Time { return time.Unix(110, 0) },
	})

	result, err := loop.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Stage2Attempted)
	require.Equal(t, 1, result.Stage2ThreadCreated)
	require.Equal(t, "user-1", host.got.Spec.UserID)
	require.Equal(t, workspace.ForUser("user-1").Root(), host.got.Spec.WorkspaceRoot)
	metadata, err := ParseStage2Metadata(host.got.Spec.Metadata)
	require.NoError(t, err)
	require.Equal(t, "user-1", metadata.UserID)
	require.NotEmpty(t, host.got.Spec.InitialPrompt)

	claimedAgain, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:      time.Unix(111, 0),
		Owner:    "worker-b",
		LeaseTTL: time.Minute,
		Limit:    1,
	})
	require.NoError(t, err)
	require.Empty(t, claimedAgain)
}

type fakeStage2ThreadHost struct {
	threadID string
	got      Stage2CreateThreadRequest
}

func (h *fakeStage2ThreadHost) CreateStage2Thread(_ context.Context, req Stage2CreateThreadRequest) (Stage2CreatedThread, error) {
	h.got = req
	return Stage2CreatedThread{ThreadID: h.threadID}, nil
}

func (h *fakeStage2ThreadHost) CloseStage2Thread(context.Context, string, string) error {
	return nil
}
