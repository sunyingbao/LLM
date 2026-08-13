package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInMemoryStoreStage1OutputLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	out := Stage1Output{
		ID:             "stage1-1",
		UserID:         "user-1",
		SourceThreadID: "thread-1",
		SourceTurnID:   "turn-1",
		RawMemory:      "prefers concrete docs",
		RolloutSummary: "docs preference",
		Status:         Stage1Succeeded,
		GeneratedAt:    time.Unix(10, 0),
	}

	require.NoError(t, store.SaveStage1Output(ctx, out))

	got, err := store.ListStage1Outputs(ctx, "user-1", ListStage1Options{Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, out.RawMemory, got[0].RawMemory)

	other, err := store.ListStage1Outputs(ctx, "user-2", ListStage1Options{Limit: 10})
	require.NoError(t, err)
	require.Empty(t, other)
}

func TestInMemoryStoreTouchSourceUpsertsAndPushesEligibleAt(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	firstUpdated := time.Unix(100, 0)
	firstEligible := time.Unix(700, 0)
	secondUpdated := time.Unix(160, 0)
	secondEligible := time.Unix(760, 0)

	require.NoError(t, store.TouchSource(ctx, TouchSourceRequest{
		Memory:          UserMemoryContext{UserID: "user-1", WriteEnabled: true},
		SourceThreadID:  "thread-1",
		SourceUpdatedAt: firstUpdated,
		EligibleAt:      firstEligible,
		Mode:            SourceModeEnabled,
	}))
	require.NoError(t, store.TouchSource(ctx, TouchSourceRequest{
		Memory:          UserMemoryContext{UserID: "user-1", WriteEnabled: true},
		SourceThreadID:  "thread-1",
		SourceUpdatedAt: secondUpdated,
		EligibleAt:      secondEligible,
		Mode:            SourceModeEnabled,
	}))

	claimed, err := store.ClaimStage1Sources(ctx, ClaimStage1Request{
		Now:      time.Unix(800, 0),
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    10,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, secondUpdated, claimed[0].SourceUpdatedAt)
	require.Equal(t, secondEligible, claimed[0].EligibleAt)
	require.Equal(t, SourceStatusLeased, claimed[0].Status)
	require.NotEmpty(t, claimed[0].OwnershipToken)
}

func TestInMemoryStoreClaimStage1SourcesRespectsIdleLeaseAndSuccessVersion(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	sourceUpdated := time.Unix(100, 0)
	require.NoError(t, store.TouchSource(ctx, TouchSourceRequest{
		Memory:          UserMemoryContext{UserID: "user-1", WriteEnabled: true},
		SourceThreadID:  "thread-1",
		SourceUpdatedAt: sourceUpdated,
		EligibleAt:      time.Unix(700, 0),
		Mode:            SourceModeEnabled,
	}))

	early, err := store.ClaimStage1Sources(ctx, ClaimStage1Request{
		Now:      time.Unix(699, 0),
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    10,
	})
	require.NoError(t, err)
	require.Empty(t, early)

	claimed, err := store.ClaimStage1Sources(ctx, ClaimStage1Request{
		Now:      time.Unix(701, 0),
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    10,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	leasedAgain, err := store.ClaimStage1Sources(ctx, ClaimStage1Request{
		Now:      time.Unix(702, 0),
		Owner:    "worker-b",
		LeaseTTL: time.Minute,
		Limit:    10,
	})
	require.NoError(t, err)
	require.Empty(t, leasedAgain)

	require.NoError(t, store.CompleteStage1Source(ctx, CompleteStage1SourceRequest{
		UserID:                   "user-1",
		SourceThreadID:           "thread-1",
		OwnershipToken:           claimed[0].OwnershipToken,
		ProcessedSourceUpdatedAt: sourceUpdated,
		Stage1OutputID:           "stage1-1",
		CompletedAt:              time.Unix(710, 0),
	}))

	afterSuccess, err := store.ClaimStage1Sources(ctx, ClaimStage1Request{
		Now:      time.Unix(800, 0),
		Owner:    "worker-c",
		LeaseTTL: time.Minute,
		Limit:    10,
	})
	require.NoError(t, err)
	require.Empty(t, afterSuccess)
}

func TestInMemoryStoreRejectsStaleStage1CompletionToken(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	require.NoError(t, store.TouchSource(ctx, TouchSourceRequest{
		Memory:          UserMemoryContext{UserID: "user-1", WriteEnabled: true},
		SourceThreadID:  "thread-1",
		SourceUpdatedAt: time.Unix(100, 0),
		EligibleAt:      time.Unix(100, 0),
		Mode:            SourceModeEnabled,
	}))
	claimed, err := store.ClaimStage1Sources(ctx, ClaimStage1Request{
		Now:      time.Unix(101, 0),
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    10,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	err = store.CompleteStage1Source(ctx, CompleteStage1SourceRequest{
		UserID:                   "user-1",
		SourceThreadID:           "thread-1",
		OwnershipToken:           "stale-token",
		ProcessedSourceUpdatedAt: time.Unix(100, 0),
		Stage1OutputID:           "stage1-1",
		CompletedAt:              time.Unix(102, 0),
	})
	require.ErrorIs(t, err, ErrStage1SourceLeaseLost)
}

func TestInMemoryStoreFailStage1SourceReleasesLeaseAndChecksToken(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	require.NoError(t, store.TouchSource(ctx, TouchSourceRequest{
		Memory:          UserMemoryContext{UserID: "user-1", WriteEnabled: true},
		SourceThreadID:  "thread-1",
		SourceUpdatedAt: time.Unix(100, 0),
		EligibleAt:      time.Unix(100, 0),
		Mode:            SourceModeEnabled,
	}))
	claimed, err := store.ClaimStage1Sources(ctx, ClaimStage1Request{
		Now:      time.Unix(101, 0),
		Owner:    "worker-a",
		LeaseTTL: time.Hour,
		Limit:    10,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	err = store.FailStage1Source(ctx, FailStage1SourceRequest{
		UserID:                   "user-1",
		SourceThreadID:           "thread-1",
		OwnershipToken:           "stale-token",
		ProcessedSourceUpdatedAt: time.Unix(100, 0),
		ErrorSummary:             "model failed",
		FailedAt:                 time.Unix(102, 0),
	})
	require.ErrorIs(t, err, ErrStage1SourceLeaseLost)

	require.NoError(t, store.FailStage1Source(ctx, FailStage1SourceRequest{
		UserID:                   "user-1",
		SourceThreadID:           "thread-1",
		OwnershipToken:           claimed[0].OwnershipToken,
		ProcessedSourceUpdatedAt: time.Unix(100, 0),
		ErrorSummary:             "model failed",
		FailedAt:                 time.Unix(103, 0),
	}))

	reclaimed, err := store.ClaimStage1Sources(ctx, ClaimStage1Request{
		Now:      time.Unix(104, 0),
		Owner:    "worker-b",
		LeaseTTL: time.Hour,
		Limit:    10,
	})
	require.NoError(t, err)
	require.Len(t, reclaimed, 1)
	require.Equal(t, SourceStatusLeased, reclaimed[0].Status)
	require.Equal(t, 2, reclaimed[0].Attempts)
}

func TestInMemoryStoreStage2JobLifecycleAndCooldown(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	require.NoError(t, store.SaveStage1Output(ctx, Stage1Output{
		ID:              "stage1-1",
		UserID:          "user-1",
		RawMemory:       "prefers concrete implementation plans",
		Status:          Stage1Succeeded,
		SourceUpdatedAt: time.Unix(100, 0),
		GeneratedAt:     time.Unix(101, 0),
	}))

	claimed, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:             time.Unix(110, 0),
		Owner:           "worker-a",
		LeaseTTL:        time.Minute,
		SuccessCooldown: 6 * time.Hour,
		Limit:           10,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, "user-1", claimed[0].UserID)
	require.NotEmpty(t, claimed[0].OwnershipToken)

	claimedAgain, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:      time.Unix(111, 0),
		Owner:    "worker-b",
		LeaseTTL: time.Minute,
		Limit:    10,
	})
	require.NoError(t, err)
	require.Empty(t, claimedAgain)

	require.NoError(t, store.MarkStage2Done(ctx, MarkStage2DoneRequest{
		UserID:                  "user-1",
		OwnershipToken:          claimed[0].OwnershipToken,
		CompletedInputWatermark: claimed[0].ClaimedInputWatermark,
		BaselineHash:            "hash-1",
		CompletedAt:             time.Unix(120, 0),
	}))
	baseline, ok, err := store.LoadBaseline(ctx, "user-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "hash-1", baseline.Hash)

	require.NoError(t, store.EnqueueStage2(ctx, EnqueueStage2Request{
		UserID:         "user-1",
		InputWatermark: "z-new",
		Now:            time.Unix(130, 0),
	}))
	cooldown, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:             time.Unix(130, 0),
		Owner:           "worker-c",
		LeaseTTL:        time.Minute,
		SuccessCooldown: 6 * time.Hour,
		Limit:           10,
	})
	require.NoError(t, err)
	require.Empty(t, cooldown)

	afterCooldown, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:             time.Unix(120, 0).Add(6*time.Hour + time.Second),
		Owner:           "worker-c",
		LeaseTTL:        time.Minute,
		SuccessCooldown: 6 * time.Hour,
		Limit:           10,
	})
	require.NoError(t, err)
	require.Len(t, afterCooldown, 1)
	require.Equal(t, "z-new", afterCooldown[0].ClaimedInputWatermark)
}

func TestInMemoryStoreStage2RejectsStaleToken(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	require.NoError(t, store.EnqueueStage2(ctx, EnqueueStage2Request{
		UserID:         "user-1",
		InputWatermark: "w1",
		Now:            time.Unix(100, 0),
	}))
	claimed, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:      time.Unix(101, 0),
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    1,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	err = store.MarkStage2Done(ctx, MarkStage2DoneRequest{
		UserID:                  "user-1",
		OwnershipToken:          "stale",
		CompletedInputWatermark: "w1",
		CompletedAt:             time.Unix(102, 0),
	})
	require.ErrorIs(t, err, ErrStage2JobLeaseLost)

	err = store.BindStage2Thread(ctx, BindStage2ThreadRequest{
		UserID:         "user-1",
		OwnershipToken: "stale",
		ThreadID:       "42",
		UpdatedAt:      time.Unix(102, 0),
	})
	require.ErrorIs(t, err, ErrStage2JobLeaseLost)
}

func TestInMemoryStoreValidateStage2ThreadBindsUnboundThread(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	require.NoError(t, store.EnqueueStage2(ctx, EnqueueStage2Request{
		UserID:         "user-1",
		InputWatermark: "w1",
		Now:            time.Unix(100, 0),
	}))
	claimed, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:      time.Unix(101, 0),
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    1,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	require.NoError(t, store.ValidateStage2Thread(ctx, ValidateStage2ThreadRequest{
		UserID:         "user-1",
		OwnershipToken: claimed[0].OwnershipToken,
		ThreadID:       "42",
		ValidatedAt:    time.Unix(102, 0),
	}))

	require.NoError(t, store.BindStage2Thread(ctx, BindStage2ThreadRequest{
		UserID:         "user-1",
		OwnershipToken: claimed[0].OwnershipToken,
		ThreadID:       "42",
		UpdatedAt:      time.Unix(102, 0),
	}))
	err = store.BindStage2Thread(ctx, BindStage2ThreadRequest{
		UserID:         "user-1",
		OwnershipToken: claimed[0].OwnershipToken,
		ThreadID:       "other",
		UpdatedAt:      time.Unix(102, 0),
	})
	require.ErrorIs(t, err, ErrStage2JobLeaseLost)
	require.NoError(t, store.ValidateStage2Thread(ctx, ValidateStage2ThreadRequest{
		UserID:         "user-1",
		OwnershipToken: claimed[0].OwnershipToken,
		ThreadID:       "42",
		ValidatedAt:    time.Unix(103, 0),
	}))

	err = store.ValidateStage2Thread(ctx, ValidateStage2ThreadRequest{
		UserID:         "user-1",
		OwnershipToken: claimed[0].OwnershipToken,
		ThreadID:       "spoofed",
		ValidatedAt:    time.Unix(103, 0),
	})
	require.ErrorIs(t, err, ErrStage2JobLeaseLost)
}

func TestInMemoryStoreStage2HeartbeatExtendsLeaseAndExpiredOwnerCannotComplete(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	require.NoError(t, store.EnqueueStage2(ctx, EnqueueStage2Request{
		UserID:         "user-1",
		InputWatermark: "w1",
		Now:            time.Unix(100, 0),
	}))
	claimed, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:      time.Unix(101, 0),
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    1,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	require.NoError(t, store.HeartbeatStage2(ctx, HeartbeatStage2Request{
		UserID:         "user-1",
		OwnershipToken: claimed[0].OwnershipToken,
		LeaseTTL:       2 * time.Minute,
		HeartbeatAt:    time.Unix(150, 0),
	}))
	noReclaim, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:      time.Unix(250, 0),
		Owner:    "worker-b",
		LeaseTTL: time.Minute,
		Limit:    1,
	})
	require.NoError(t, err)
	require.Empty(t, noReclaim)

	err = store.MarkStage2Done(ctx, MarkStage2DoneRequest{
		UserID:                  "user-1",
		OwnershipToken:          claimed[0].OwnershipToken,
		CompletedInputWatermark: "w1",
		CompletedAt:             time.Unix(300, 0),
	})
	require.ErrorIs(t, err, ErrStage2JobLeaseLost)

	reclaimed, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:      time.Unix(301, 0),
		Owner:    "worker-b",
		LeaseTTL: time.Minute,
		Limit:    1,
	})
	require.NoError(t, err)
	require.Len(t, reclaimed, 1)
	require.NotEqual(t, claimed[0].OwnershipToken, reclaimed[0].OwnershipToken)
}
