package gormstore

import (
	"context"
	"errors"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"eino-cli/deepagent/core/memory"
	"eino-cli/deepagent/core/memory/gorm_store/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGormStoreStage1Stage2Lifecycle(t *testing.T) {
	ctx := context.Background()
	store := newTestGormStore(t)
	now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)

	if err := store.TouchSource(ctx, memory.TouchSourceRequest{
		Memory:          memory.UserMemoryContext{UserID: "user-1"},
		SourceThreadID:  "thread-1",
		SourceUpdatedAt: now,
		EligibleAt:      now.Add(-time.Minute),
		Mode:            memory.SourceModeEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimStage1Sources(ctx, memory.ClaimStage1Request{
		Now:      now,
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].UserID != "user-1" || claimed[0].SourceThreadID != "thread-1" {
		t.Fatalf("claimed = %+v", claimed)
	}

	out := memory.Stage1Output{
		ID:              "stage1-a",
		UserID:          "user-1",
		SourceThreadID:  "thread-1",
		RawMemory:       "User prefers concise Chinese engineering docs.",
		RolloutSummary:  "Discussed memory design docs.",
		Status:          memory.Stage1Succeeded,
		GeneratedAt:     now.Add(time.Second),
		SourceUpdatedAt: now,
	}
	if err := store.SaveStage1Output(ctx, out); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteStage1Source(ctx, memory.CompleteStage1SourceRequest{
		UserID:                   "user-1",
		SourceThreadID:           "thread-1",
		OwnershipToken:           claimed[0].OwnershipToken,
		ProcessedSourceUpdatedAt: now,
		Stage1OutputID:           out.ID,
		CompletedAt:              now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	stage2, err := store.ClaimStage2Jobs(ctx, memory.ClaimStage2Request{
		Now:      now.Add(3 * time.Second),
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stage2) != 1 || stage2[0].UserID != "user-1" || stage2[0].ClaimedInputWatermark == "" {
		t.Fatalf("stage2 = %+v", stage2)
	}
	if err := store.BindStage2Thread(ctx, memory.BindStage2ThreadRequest{
		UserID:         "user-1",
		OwnershipToken: stage2[0].OwnershipToken,
		ThreadID:       "stage2-thread",
		UpdatedAt:      now.Add(4 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkStage2Done(ctx, memory.MarkStage2DoneRequest{
		UserID:                  "user-1",
		OwnershipToken:          stage2[0].OwnershipToken,
		CompletedInputWatermark: stage2[0].ClaimedInputWatermark,
		BaselineHash:            "hash-a",
		CompletedAt:             now.Add(5 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	baseline, ok, err := store.LoadBaseline(ctx, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || baseline.Hash != "hash-a" {
		t.Fatalf("baseline = %+v ok=%t", baseline, ok)
	}
	stage2Again, err := store.ClaimStage2Jobs(ctx, memory.ClaimStage2Request{
		Now:      now.Add(6 * time.Second),
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stage2Again) != 0 {
		t.Fatalf("stage2 claimed again = %+v", stage2Again)
	}
}

func TestGormStoreStage2RejectsStaleToken(t *testing.T) {
	ctx := context.Background()
	store := newTestGormStore(t)
	now := time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC)
	if err := store.EnqueueStage2(ctx, memory.EnqueueStage2Request{UserID: "user-1", InputWatermark: "wm-1", Now: now}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimStage2Jobs(ctx, memory.ClaimStage2Request{Now: now, Owner: "worker-a", LeaseTTL: time.Minute, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %+v", claimed)
	}
	err = store.MarkStage2Done(ctx, memory.MarkStage2DoneRequest{
		UserID:         "user-1",
		OwnershipToken: "stale-token",
		BaselineHash:   "hash-a",
		CompletedAt:    now.Add(time.Second),
	})
	if !errors.Is(err, memory.ErrStage2JobLeaseLost) {
		t.Fatalf("err = %v, want memory.ErrStage2JobLeaseLost", err)
	}
}

func TestGormStoreTouchSourceKeepsNewestWatermark(t *testing.T) {
	ctx := context.Background()
	store := newTestGormStore(t)
	older := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	newer := older.Add(10 * time.Minute)

	if err := store.TouchSource(ctx, memory.TouchSourceRequest{
		Memory:          memory.UserMemoryContext{UserID: "user-1"},
		SourceThreadID:  "thread-1",
		SourceUpdatedAt: newer,
		EligibleAt:      newer.Add(5 * time.Minute),
		Mode:            memory.SourceModeEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.TouchSource(ctx, memory.TouchSourceRequest{
		Memory:          memory.UserMemoryContext{UserID: "user-1"},
		SourceThreadID:  "thread-1",
		SourceUpdatedAt: older,
		EligibleAt:      older.Add(5 * time.Minute),
		Mode:            memory.SourceModeEnabled,
	}); err != nil {
		t.Fatal(err)
	}

	tooEarly, err := store.ClaimStage1Sources(ctx, memory.ClaimStage1Request{
		Now:      older.Add(6 * time.Minute),
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tooEarly) != 0 {
		t.Fatalf("claimed before newest idle window = %+v", tooEarly)
	}
	claimed, err := store.ClaimStage1Sources(ctx, memory.ClaimStage1Request{
		Now:      newer.Add(6 * time.Minute),
		Owner:    "worker-a",
		LeaseTTL: time.Minute,
		Limit:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || !claimed[0].SourceUpdatedAt.Equal(newer) || !claimed[0].ClaimedSourceUpdatedAt.Equal(newer) {
		t.Fatalf("claimed = %+v, want newest watermark %s", claimed, newer)
	}
}

func TestGeneratedModelsKeepNullableTimeColumns(t *testing.T) {
	timePtrType := reflect.TypeOf((*time.Time)(nil))
	cases := []struct {
		name  string
		field any
	}{
		{"source_updated_at", model.TMemorySource{}.SourceUpdatedAt},
		{"source_eligible_at", model.TMemorySource{}.EligibleAt},
		{"source_last_success", model.TMemorySource{}.LastStage1SuccessSourceUpdatedAt},
		{"source_lease_until", model.TMemorySource{}.LeaseUntil},
		{"stage1_last_used_at", model.TMemoryStage1Output{}.LastUsedAt},
		{"stage2_last_success_at", model.TMemoryStage2Job{}.LastSuccessAt},
		{"stage2_lease_until", model.TMemoryStage2Job{}.LeaseUntil},
		{"stage2_retry_at", model.TMemoryStage2Job{}.RetryAt},
		{"baseline_updated_at", model.TMemoryBaseline{}.UpdatedAt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reflect.TypeOf(tc.field); got != timePtrType {
				t.Fatalf("type=%v, want %v", got, timePtrType)
			}
		})
	}
}

func TestGormStoreStage2DonePreservesDatabaseError(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/memory.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := NewGormStore(db, GormStoreConfig{
		Tables: GormStoreTables{
			Stage2Job: "missing_stage2_job",
			Baseline:  "missing_baseline",
		},
		Generator: newTestGenerator(),
	})

	err = store.MarkStage2Done(ctx, memory.MarkStage2DoneRequest{
		UserID:         "user-1",
		OwnershipToken: "token",
		CompletedAt:    time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("err = nil, want database error")
	}
	if errors.Is(err, memory.ErrStage2JobLeaseLost) {
		t.Fatalf("err = %v, should preserve database error instead of memory.ErrStage2JobLeaseLost", err)
	}
}

func newTestGormStore(t *testing.T) *GormStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/memory.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tables := GormStoreTables{
		Source:       DefaultSourceTable,
		Stage1Output: DefaultStage1OutputTable,
		Stage2Job:    DefaultStage2JobTable,
		Baseline:     DefaultBaselineTable,
	}
	execDDLFile(t, db, "sql/t_memory_source.sql")
	execDDLFile(t, db, "sql/t_memory_stage1_output.sql")
	execDDLFile(t, db, "sql/t_memory_stage2_job.sql")
	execDDLFile(t, db, "sql/t_memory_baseline.sql")
	return NewGormStore(db, GormStoreConfig{Tables: tables, Generator: newTestGenerator()})
}

type testGenerator struct {
	next int64
}

func newTestGenerator() *testGenerator {
	return &testGenerator{}
}

func (g *testGenerator) NextID(context.Context) (int64, error) {
	g.next++
	return g.next, nil
}

func execDDLFile(t *testing.T, db *gorm.DB, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(sqliteDDL(string(raw))).Error; err != nil {
		t.Fatalf("exec %s: %v", path, err)
	}
}

func sqliteDDL(sql string) string {
	lines := strings.Split(sql, "\n")
	kept := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "UNIQUE KEY ") {
			line = regexp.MustCompile(`(?i)^\s*UNIQUE KEY\s+\S+\s+(\([^)]+\)),?\s*$`).ReplaceAllString(line, "  UNIQUE $1,")
		} else if strings.HasPrefix(trimmed, "KEY ") {
			continue
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	out = regexp.MustCompile(` COMMENT '[^']*'`).ReplaceAllString(out, "")
	out = regexp.MustCompile(`VARCHAR\([0-9]+\)`).ReplaceAllString(out, "TEXT")
	out = strings.ReplaceAll(out, "DATETIME(6)", "DATETIME")
	out = strings.ReplaceAll(out, "MEDIUMTEXT", "TEXT")
	out = regexp.MustCompile(`\)\s*ENGINE=.*;`).ReplaceAllString(out, ");")
	out = strings.ReplaceAll(out, ",\n)", "\n)")
	return out
}
