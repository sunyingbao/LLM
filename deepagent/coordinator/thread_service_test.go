package coordinator

import (
	"context"
	"eino-cli/deepagent/coordinator/internal/model"
	"eino-cli/deepagent/coordinator/internal/util"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCreateThreadAndScanRunnable(t *testing.T) {
	db := newTestDB(t)
	svc := newTestCoordinator(db, db, nil, &stubIDGen{ids: []int64{9001, 1001, 2001}}, WithClock(func() time.Time {
		return time.Date(2026, 4, 13, 18, 0, 0, 0, time.UTC)
	}))
	_, err := svc.registerTestNamespace(context.Background(), "dreamina", "desc", "tester", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("register namespace: %v", err)
	}

	thread, message, err := svc.createThreadRow(context.Background(), "dreamina", "ppe_a", 10001, "sess-1", "hello", map[string]string{"scene": "demo"}, &model.ThreadProfile{
		Role: "main",
		Cwd:  "/tmp/dreamina",
	}, &InitialMessage{
		SenderType:  model.SenderTypeUser,
		SenderID:    "u1",
		MessageType: "input",
		Payload:     []byte("payload"),
		Metadata:    map[string]string{"trace": "1"},
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	if thread.Status != model.ThreadStatusIdle {
		t.Fatalf("thread status = %s, want idle", thread.Status)
	}
	if thread.SessionId != "sess-1" {
		t.Fatalf("thread session_id = %s, want sess-1", thread.SessionId)
	}
	if thread.Env != "ppe_a" {
		t.Fatalf("thread env = %s, want ppe_a", thread.Env)
	}
	if thread.UserId != 10001 {
		t.Fatalf("thread user_id = %d, want 10001", thread.UserId)
	}
	if thread.LastActiveAt.IsZero() {
		t.Fatalf("thread last_active_at should be set")
	}
	var readyAtNull, leaseDeadlineAtNull, closedAtNull int
	if err := db.Raw("select ready_at is null, lease_deadline_at is null, closed_at is null from t_thread where thread_id = ?", thread.ThreadId).
		Row().Scan(&readyAtNull, &leaseDeadlineAtNull, &closedAtNull); err != nil {
		t.Fatalf("query nullable times: %v", err)
	}
	if readyAtNull != 1 || leaseDeadlineAtNull != 1 || closedAtNull != 1 {
		t.Fatalf("thread nullable times should be null, ready_at_null=%d lease_deadline_at_null=%d closed_at_null=%d", readyAtNull, leaseDeadlineAtNull, closedAtNull)
	}
	if thread.Profile != `{"role":"main","cwd":"/tmp/dreamina"}` {
		t.Fatalf("thread profile = %s, want role/cwd json", thread.Profile)
	}
	if message == nil || message.ThreadId != thread.ThreadId {
		t.Fatalf("initial message not created correctly: %+v", message)
	}

	updated, err := svc.wakeIdleThread(context.Background(), "dreamina", thread.ThreadId, "")
	if err != nil {
		t.Fatalf("mark thread ready: %v", err)
	}
	if updated.Status != model.ThreadStatusReady {
		t.Fatalf("updated status = %s, want ready", updated.Status)
	}

	threads, nextCursor, hasMore, err := svc.scanRunnableThreadRows(context.Background(), "dreamina", "ppe_a", "", 10)
	if err != nil {
		t.Fatalf("scan runnable: %v", err)
	}
	if len(threads) != 1 || threads[0].ThreadId != thread.ThreadId {
		t.Fatalf("unexpected runnable threads: %+v", threads)
	}
	if hasMore || nextCursor != "" {
		t.Fatalf("unexpected pagination: hasMore=%v nextCursor=%q", hasMore, nextCursor)
	}
}

func TestCreateThreadRejectsSessionEnvMismatch(t *testing.T) {
	db := newTestDB(t)
	svc := newTestCoordinator(db, db, nil, &stubIDGen{ids: []int64{9001, 1001, 1002}})
	_, err := svc.registerTestNamespace(context.Background(), "dreamina", "desc", "tester", nil)
	if err != nil {
		t.Fatalf("register namespace: %v", err)
	}

	_, _, err = svc.createThreadRow(context.Background(), "dreamina", "ppe_a", 0, "sess-1", "first", nil, nil, nil)
	if err != nil {
		t.Fatalf("create first thread: %v", err)
	}
	_, _, err = svc.createThreadRow(context.Background(), "dreamina", "ppe_b", 0, "sess-1", "second", nil, nil, nil)
	if err != ErrSessionEnvMismatch {
		t.Fatalf("create thread err=%v, want %v", err, ErrSessionEnvMismatch)
	}
}

func TestListSessionThreadsPaginatesByThreadID(t *testing.T) {
	db := newTestDB(t)
	svc := newTestCoordinator(db, db, nil, &stubIDGen{ids: []int64{9001, 1001, 1002, 1003, 2001}})
	_, err := svc.registerTestNamespace(context.Background(), "dreamina", "desc", "tester", nil)
	if err != nil {
		t.Fatalf("register namespace: %v", err)
	}

	first, _, err := svc.createThreadRow(context.Background(), "dreamina", "ppe_a", 10001, "sess-1", "main", map[string]string{"role": "main"}, nil, nil)
	if err != nil {
		t.Fatalf("create first thread: %v", err)
	}
	second, _, err := svc.createThreadRow(context.Background(), "dreamina", "ppe_a", 10001, "sess-1", "child-a", map[string]string{"role": "subtask"}, nil, nil)
	if err != nil {
		t.Fatalf("create second thread: %v", err)
	}
	third, _, err := svc.createThreadRow(context.Background(), "dreamina", "ppe_a", 10001, "sess-1", "child-b", map[string]string{"role": "subtask"}, nil, nil)
	if err != nil {
		t.Fatalf("create third thread: %v", err)
	}
	if _, _, err := svc.createThreadRow(context.Background(), "dreamina", "ppe_a", 10001, "other-session", "other", nil, nil, nil); err != nil {
		t.Fatalf("create other session thread: %v", err)
	}

	threads, nextCursor, hasMore, err := svc.listSessionThreadRows(context.Background(), "dreamina", "sess-1", 0, 2)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if got := threadIDs(threads); !equalInt64s(got, []int64{first.ThreadId, second.ThreadId}) {
		t.Fatalf("first page ids=%v, want [%d %d]", got, first.ThreadId, second.ThreadId)
	}
	if nextCursor != second.ThreadId || !hasMore {
		t.Fatalf("first page pagination nextCursor=%d hasMore=%v", nextCursor, hasMore)
	}

	threads, nextCursor, hasMore, err = svc.listSessionThreadRows(context.Background(), "dreamina", "sess-1", nextCursor, 2)
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if got := threadIDs(threads); !equalInt64s(got, []int64{third.ThreadId}) {
		t.Fatalf("second page ids=%v, want [%d]", got, third.ThreadId)
	}
	if nextCursor != third.ThreadId || hasMore {
		t.Fatalf("second page pagination nextCursor=%d hasMore=%v", nextCursor, hasMore)
	}

	threads, nextCursor, hasMore, err = svc.listSessionThreadRows(context.Background(), "dreamina", "missing-session", 0, 10)
	if err != nil {
		t.Fatalf("list empty session: %v", err)
	}
	if len(threads) != 0 || nextCursor != 0 || hasMore {
		t.Fatalf("empty session result threads=%v nextCursor=%d hasMore=%v", threads, nextCursor, hasMore)
	}
}

func TestResumeFromBlock(t *testing.T) {
	db := newTestDB(t)
	svc := newTestCoordinator(db, db, nil, &stubIDGen{ids: []int64{1}}, WithClock(func() time.Time {
		return time.Date(2026, 4, 13, 20, 0, 0, 0, time.UTC)
	}))
	now := svc.now()
	if err := db.WithContext(context.Background()).Create(&model.TThread{
		ThreadId:        4001,
		Namespace:       "dreamina",
		Env:             "ppe_a",
		Title:           "blocked",
		Status:          model.ThreadStatusBlocked,
		LeaseToken:      "stale",
		LeaseDeadlineAt: now.Add(time.Minute),
		LeaseOwnerHint:  "worker",
		CreatedBy:       "tester",
		MetadataJson:    "{}",
		CreatedAt:       now,
		UpdatedAt:       now,
	}).Error; err != nil {
		t.Fatalf("insert blocked thread: %v", err)
	}
	resumed, err := svc.resumeBlockedThread(context.Background(), "dreamina", 4001, "resume", true, map[string]string{
		"logid":               "resume-logid",
		model.MetadataKeyKEnv: "resume_env",
	})
	if err != nil {
		t.Fatalf("resume from block: %v", err)
	}
	if resumed.Status != model.ThreadStatusReady || resumed.ReadyAt.IsZero() {
		t.Fatalf("resumed status=%s ready_at=%v, want ready with ready_at", resumed.Status, resumed.ReadyAt)
	}
	if resumed.LeaseToken != "" || !resumed.LeaseDeadlineAt.IsZero() || resumed.LeaseOwnerHint != "" {
		t.Fatalf("resume should clear lease fields: %+v", resumed)
	}
	resumeMetadata, err := util.ToStruct[map[string]string](resumed.MetadataJson)
	if err != nil {
		t.Fatalf("decode resume metadata_json=%s: %v", resumed.MetadataJson, err)
	}
	if (*resumeMetadata)["logid"] != "resume-logid" || (*resumeMetadata)[model.MetadataKeyKEnv] != "resume_env" {
		t.Fatalf("resume metadata_json=%s, want logid and K_ENV", resumed.MetadataJson)
	}
}

func TestClaimRenewReleaseThread(t *testing.T) {
	db := newTestDB(t)
	svc := newTestCoordinator(
		db,
		db,
		nil,
		&stubIDGen{ids: []int64{9001}},
		WithClock(func() time.Time {
			return time.Date(2026, 4, 13, 19, 0, 0, 0, time.UTC)
		}),
		WithLeaseTokenGenerator(func() string { return "lease-1" }),
	)
	_, err := svc.registerTestNamespace(context.Background(), "dreamina", "", "tester", nil)
	if err != nil {
		t.Fatalf("register namespace: %v", err)
	}

	now := svc.now()
	if err := db.WithContext(context.Background()).Create(&model.TThread{
		ThreadId:     3001,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		Title:        "run",
		Status:       model.ThreadStatusReady,
		ReadyAt:      now.Add(-time.Minute),
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	thread, lease, serverTimeMS, err := svc.claimThreadLease(context.Background(), "dreamina", 3001, 0, "worker-a")
	if err != nil {
		t.Fatalf("claim thread: %v", err)
	}
	if thread.Status != model.ThreadStatusRunning {
		t.Fatalf("claimed status = %s", thread.Status)
	}
	if lease.LeaseToken != "lease-1" {
		t.Fatalf("lease token = %s", lease.LeaseToken)
	}
	if serverTimeMS != now.UnixMilli() {
		t.Fatalf("server time = %d, want %d", serverTimeMS, now.UnixMilli())
	}
	renewed, err := svc.RenewThreadLease(context.Background(), RenewThreadLeaseRequest{Namespace: "dreamina", ThreadID: 3001, LeaseToken: "lease-1", LeaseMS: int64((2 * time.Minute).Milliseconds()), LeaseOwner: "worker-b"})
	if err != nil {
		t.Fatalf("renew lease: %v", err)
	}
	if renewed.LeaseDeadlineAt.Sub(now) != 2*time.Minute {
		t.Fatalf("renewed deadline diff = %v", renewed.LeaseDeadlineAt.Sub(now))
	}

	released, err := svc.releaseThreadToStatus(context.Background(), "dreamina", 3001, "lease-1", "done", model.ThreadStatusReady, true)
	if err != nil {
		t.Fatalf("release thread: %v", err)
	}
	if released.Status != model.ThreadStatusReady {
		t.Fatalf("released status = %s, want ready", released.Status)
	}
	if released.LeaseToken != "" {
		t.Fatalf("lease token should be cleared, got %s", released.LeaseToken)
	}
}

func TestClaimThreadConcurrentReadyClaimSingleWinner(t *testing.T) {
	db := newTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	now := time.Date(2026, 4, 13, 18, 30, 0, 0, time.UTC)
	var tokenMu sync.Mutex
	tokenSeq := 0
	svc := newTestCoordinator(
		db,
		db,
		nil,
		&stubIDGen{ids: []int64{1}},
		WithClock(func() time.Time { return now }),
		WithLeaseTokenGenerator(func() string {
			tokenMu.Lock()
			defer tokenMu.Unlock()
			tokenSeq++
			return fmt.Sprintf("lease-%d", tokenSeq)
		}),
	)
	if err := db.WithContext(context.Background()).Create(&model.TThread{
		ThreadId:     5101,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		Status:       model.ThreadStatusReady,
		ReadyAt:      now.Add(-time.Minute),
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("insert thread: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	successes := 0
	notRunnable := 0
	failures := make([]error, 0)
	for idx := 0; idx < workers; idx++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			_, _, _, err := svc.claimThreadLease(context.Background(), "dreamina", 5101, 60000, fmt.Sprintf("worker-%d", worker))
			resultMu.Lock()
			defer resultMu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrThreadNotRunnable):
				notRunnable++
			default:
				failures = append(failures, err)
			}
		}(idx)
	}
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("claim failures: %v", failures)
	}
	if successes != 1 {
		t.Fatalf("successes=%d, want 1", successes)
	}
	if notRunnable != workers-1 {
		t.Fatalf("notRunnable=%d, want %d", notRunnable, workers-1)
	}
	thread, err := svc.readThread(context.Background(), "dreamina", 5101)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if thread.Status != model.ThreadStatusRunning || thread.LeaseToken == "" || !thread.ReadyAt.IsZero() {
		t.Fatalf("thread after concurrent claim status=%s lease=%q ready_at=%s", thread.Status, thread.LeaseToken, thread.ReadyAt)
	}
}

func TestClaimThreadNamespaceMismatch(t *testing.T) {
	db := newTestDB(t)
	svc := newTestCoordinator(db, db, nil, &stubIDGen{ids: []int64{1}}, WithLeaseTokenGenerator(func() string { return "lease-1" }))
	now := time.Now().UTC()
	if err := db.WithContext(context.Background()).Create(&model.TThread{
		ThreadId:     1,
		Namespace:    "other",
		Status:       model.ThreadStatusReady,
		ReadyAt:      now,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("insert thread: %v", err)
	}

	_, _, _, err := svc.claimThreadLease(context.Background(), "dreamina", 1, 0, "")
	if err == nil {
		t.Fatalf("expected claim error")
	}
	if err != ErrThreadNotFound {
		t.Fatalf("claim error = %v, want %v", err, ErrThreadNotFound)
	}
}

func TestScanClaimRenewClosingThread(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 4, 13, 18, 0, 0, 0, time.UTC)
	svc := newTestCoordinator(
		db,
		db,
		nil,
		&stubIDGen{ids: []int64{1}},
		WithClock(func() time.Time { return now }),
		WithLeaseTokenGenerator(func() string { return "closing-lease" }),
	)
	if err := db.WithContext(context.Background()).Create(&model.TThread{
		ThreadId:  5001,
		Namespace: "dreamina",
		Env:       "ppe_a",
		Status:    model.ThreadStatusClosing,
		ReadyAt:   now,
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("insert closing thread: %v", err)
	}

	threads, _, _, err := svc.scanRunnableThreadRows(context.Background(), "dreamina", "ppe_a", "", 10)
	if err != nil {
		t.Fatalf("scan closing: %v", err)
	}
	if len(threads) != 1 || threads[0].ThreadId != 5001 {
		t.Fatalf("scan closing threads=%v, want 5001", threadIDs(threads))
	}

	thread, lease, _, err := svc.claimThreadLease(context.Background(), "dreamina", 5001, 60000, "worker-close")
	if err != nil {
		t.Fatalf("claim closing: %v", err)
	}
	if thread.Status != model.ThreadStatusClosing {
		t.Fatalf("claimed closing status=%s, want closing", thread.Status)
	}
	if lease.LeaseToken != "closing-lease" {
		t.Fatalf("closing lease token=%q, want closing-lease", lease.LeaseToken)
	}

	if _, err := svc.RenewThreadLease(context.Background(), RenewThreadLeaseRequest{Namespace: "dreamina", ThreadID: 5001, LeaseToken: lease.LeaseToken, LeaseMS: 120000, LeaseOwner: "worker-close-renew"}); err != nil {
		t.Fatalf("renew closing lease: %v", err)
	}

	_, _, _, err = svc.claimThreadLease(context.Background(), "dreamina", 5001, 60000, "worker-close-conflict")
	if !errors.Is(err, ErrThreadNotRunnable) {
		t.Fatalf("claim leased closing err=%v, want %v", err, ErrThreadNotRunnable)
	}

	threads, _, _, err = svc.scanRunnableThreadRows(context.Background(), "dreamina", "ppe_a", "", 10)
	if err != nil {
		t.Fatalf("scan leased closing: %v", err)
	}
	if len(threads) != 0 {
		t.Fatalf("leased closing should not scan, got %v", threadIDs(threads))
	}
}

func TestScanRunnableThreadsIncludesExpiredRunning(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 4, 13, 18, 0, 0, 0, time.Local)
	svc := newTestCoordinator(
		db,
		db,
		nil,
		&stubIDGen{ids: []int64{1}},
		WithClock(func() time.Time { return now }),
		WithLeaseTokenGenerator(func() string { return "reclaimed-lease" }),
	)
	for _, thread := range []*model.TThread{
		{
			ThreadId:  6001,
			Namespace: "dreamina",
			Env:       "ppe_a",
			Status:    model.ThreadStatusReady,
			ReadyAt:   now.Add(-3 * time.Minute),
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ThreadId:        6002,
			Namespace:       "dreamina",
			Env:             "ppe_a",
			Status:          model.ThreadStatusRunning,
			LeaseToken:      "expired",
			LeaseDeadlineAt: now.Add(-2 * time.Minute),
			LeaseOwnerHint:  "stale-worker",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ThreadId:  6003,
			Namespace: "dreamina",
			Env:       "ppe_a",
			Status:    model.ThreadStatusReady,
			ReadyAt:   now.Add(-time.Minute),
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ThreadId:        6004,
			Namespace:       "dreamina",
			Env:             "ppe_a",
			Status:          model.ThreadStatusRunning,
			LeaseToken:      "active",
			LeaseDeadlineAt: now.Add(time.Minute),
			LeaseOwnerHint:  "active-worker",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ThreadId:        6005,
			Namespace:       "dreamina",
			Env:             "ppe_b",
			Status:          model.ThreadStatusRunning,
			LeaseToken:      "expired-other-env",
			LeaseDeadlineAt: now.Add(-4 * time.Minute),
			LeaseOwnerHint:  "stale-worker",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	} {
		if err := db.WithContext(context.Background()).Create(thread).Error; err != nil {
			t.Fatalf("insert thread %d: %v", thread.ThreadId, err)
		}
	}

	threads, nextCursor, hasMore, err := svc.scanRunnableThreadRows(context.Background(), "dreamina", "ppe_a", "", 2)
	if err != nil {
		t.Fatalf("scan first page: %v", err)
	}
	if got := threadIDs(threads); !equalInt64s(got, []int64{6001, 6002}) {
		t.Fatalf("first page ids=%v, want [6001 6002]", got)
	}
	if !hasMore || nextCursor == "" {
		t.Fatalf("first page hasMore=%v nextCursor=%q, want more", hasMore, nextCursor)
	}

	threads, nextCursor, hasMore, err = svc.scanRunnableThreadRows(context.Background(), "dreamina", "ppe_a", nextCursor, 2)
	if err != nil {
		t.Fatalf("scan second page: %v", err)
	}
	if got := threadIDs(threads); !equalInt64s(got, []int64{6003}) {
		t.Fatalf("second page ids=%v, want [6003]", got)
	}
	if hasMore || nextCursor != "" {
		t.Fatalf("second page hasMore=%v nextCursor=%q, want end", hasMore, nextCursor)
	}

	_, _, _, err = svc.claimThreadLease(context.Background(), "dreamina", 6004, int64(time.Minute.Milliseconds()), "conflict-worker")
	if !errors.Is(err, ErrThreadNotRunnable) {
		t.Fatalf("claim active running err=%v, want %v", err, ErrThreadNotRunnable)
	}
	active, err := svc.readThread(context.Background(), "dreamina", 6004)
	if err != nil {
		t.Fatalf("get active running: %v", err)
	}
	if active.LeaseToken != "active" || active.LeaseOwnerHint != "active-worker" {
		t.Fatalf("active running lease changed: token=%q owner=%q", active.LeaseToken, active.LeaseOwnerHint)
	}

	claimed, lease, _, err := svc.claimThreadLease(context.Background(), "dreamina", 6002, int64(time.Minute.Milliseconds()), "new-worker")
	if err != nil {
		t.Fatalf("claim expired running: %v", err)
	}
	if claimed.Status != model.ThreadStatusRunning {
		t.Fatalf("claimed status=%s, want running", claimed.Status)
	}
	if claimed.StatusReason != claimReasonExpiredRunningLease {
		t.Fatalf("claimed status_reason=%q, want %q", claimed.StatusReason, claimReasonExpiredRunningLease)
	}
	if lease.LeaseToken != "reclaimed-lease" {
		t.Fatalf("lease token=%q, want reclaimed-lease", lease.LeaseToken)
	}
	if !lease.LeaseDeadlineAt.After(now) {
		t.Fatalf("lease deadline=%s should be after now=%s", lease.LeaseDeadlineAt, now)
	}
}

func threadIDs(threads []*model.TThread) []int64 {
	ids := make([]int64, 0, len(threads))
	for _, thread := range threads {
		ids = append(ids, thread.ThreadId)
	}
	return ids
}

func equalInt64s(a []int64, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if a[idx] != b[idx] {
			return false
		}
	}
	return true
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.TAgentNamespace{},
		&model.TThread{},
		&model.TMailboxMessage{},
		&model.TEventLog{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

type stubIDGen struct {
	ids []int64
	idx int
}

func (s *stubIDGen) NextID(context.Context) (int64, error) {
	if s.idx >= len(s.ids) {
		return 0, nil
	}
	value := s.ids[s.idx]
	s.idx++
	return value, nil
}
