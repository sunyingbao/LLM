package coordinator

import (
	"context"
	"eino-cli/deepagent/coordinator/internal/model"
	"errors"
	"testing"
	"time"
)

func TestIsFailureReleaseReason(t *testing.T) {
	svc := newTestCoordinator(nil, nil, nil, nil)
	failures := []string{
		"agent thread failed",
		"agent thread build failed",
		"agent thread init failed",
		"agent thread post message failed",
		"agent thread ack failed",
		"agent thread control input failed",
		"agent thread interrupt failed",
		"agent thread not buildable",
		"mailbox redis load failed",
		"  Agent Thread Build Failed  ", // 精确白名单，但忽略首尾空白与大小写
	}
	for _, reason := range failures {
		if !svc.isFailureReleaseReason(reason) {
			t.Fatalf("isFailureReleaseReason(%q) = false, want true", reason)
		}
	}
	nonFailures := []string{
		"agent thread completed",
		"worker graceful exit",
		"worker graceful exit timeout",
		"agent thread interrupt timeout",
		"agent thread closed",
		"user_close",
		"user_cancel",
		"",
		// 白名单外的自由文本即使以 failed 结尾也不触发退避（不再做后缀匹配）。
		"cleanup of failed",
		"business custom reason failed",
	}
	for _, reason := range nonFailures {
		if svc.isFailureReleaseReason(reason) {
			t.Fatalf("isFailureReleaseReason(%q) = true, want false", reason)
		}
	}
}

func TestWithFailureReleaseReasonsOverridesWhitelist(t *testing.T) {
	svc := newTestCoordinator(nil, nil, nil, nil, WithFailureReleaseReasons([]string{"my custom failure"}))
	if !svc.isFailureReleaseReason("my custom failure") {
		t.Fatal("custom reason should be recognized as failure")
	}
	if svc.isFailureReleaseReason("agent thread build failed") {
		t.Fatal("default whitelist should be replaced by the override")
	}
}

func TestReleaseWithFailureReasonAppliesBackoffAndHidesFromScan(t *testing.T) {
	db := newTestDB(t)
	current := time.Date(2026, 4, 13, 19, 0, 0, 0, time.UTC)
	svc := newTestCoordinator(
		db,
		db,
		nil,
		&stubIDGen{ids: []int64{9001}},
		WithClock(func() time.Time { return current }),
		WithLeaseTokenGenerator(func() string { return "lease-backoff" }),
	)
	if err := db.WithContext(context.Background()).Create(&model.TThread{
		ThreadId:  8001,
		Namespace: "dreamina",
		Env:       "prod",
		Status:    model.ThreadStatusReady,
		ReadyAt:   current,
		CreatedAt: current,
		UpdatedAt: current,
	}).Error; err != nil {
		t.Fatalf("insert ready thread: %v", err)
	}

	// 第一次失败 release：上一轮 status_reason 不是失败 reason，视为瞬时抖动，
	// 立即可重试，不退避。
	if _, _, _, err := svc.claimThreadLease(context.Background(), "dreamina", 8001, 60_000, "worker"); err != nil {
		t.Fatalf("claim thread: %v", err)
	}
	released, err := svc.releaseThreadToStatus(context.Background(), "dreamina", 8001, "lease-backoff", "agent thread build failed", model.ThreadStatusReady, true)
	if err != nil {
		t.Fatalf("first release: %v", err)
	}
	if released.Status != model.ThreadStatusReady {
		t.Fatalf("released status = %s, want ready", released.Status)
	}
	if !released.ReadyAt.Equal(current) {
		t.Fatalf("first failure ready_at = %v, want %v (immediate retry)", released.ReadyAt, current)
	}
	threads, _, _, err := svc.scanRunnableThreadRows(context.Background(), "dreamina", "prod", "", 10)
	if err != nil {
		t.Fatalf("scan after first failure: %v", err)
	}
	if len(threads) != 1 || threads[0].ThreadId != 8001 {
		t.Fatalf("scan after first failure = %+v, want thread 8001 immediately runnable", threads)
	}

	// 第二次失败 release：上一轮 status_reason 已是失败 reason，判定为持续失败，
	// 退避 defaultFailureReleaseBackoff。
	if _, _, _, err := svc.claimThreadLease(context.Background(), "dreamina", 8001, 60_000, "worker"); err != nil {
		t.Fatalf("second claim: %v", err)
	}
	released, err = svc.releaseThreadToStatus(context.Background(), "dreamina", 8001, "lease-backoff", "agent thread build failed", model.ThreadStatusReady, true)
	if err != nil {
		t.Fatalf("second release: %v", err)
	}
	wantReadyAt := current.Add(defaultFailureReleaseBackoff)
	if !released.ReadyAt.Equal(wantReadyAt) {
		t.Fatalf("consecutive failure ready_at = %v, want %v (now + backoff)", released.ReadyAt, wantReadyAt)
	}

	threads, _, _, err = svc.scanRunnableThreadRows(context.Background(), "dreamina", "prod", "", 10)
	if err != nil {
		t.Fatalf("scan during backoff: %v", err)
	}
	if len(threads) != 0 {
		t.Fatalf("scan during backoff returned %d threads, want 0", len(threads))
	}

	current = current.Add(defaultFailureReleaseBackoff + time.Second)
	threads, _, _, err = svc.scanRunnableThreadRows(context.Background(), "dreamina", "prod", "", 10)
	if err != nil {
		t.Fatalf("scan after backoff: %v", err)
	}
	if len(threads) != 1 || threads[0].ThreadId != 8001 {
		t.Fatalf("scan after backoff = %+v, want thread 8001", threads)
	}

	// 失败后成功一轮，escalation 归零：下一次失败重新按"首次失败"立即可重试。
	if _, _, _, err := svc.claimThreadLease(context.Background(), "dreamina", 8001, 60_000, "worker"); err != nil {
		t.Fatalf("third claim: %v", err)
	}
	if _, err = svc.releaseThreadToStatus(context.Background(), "dreamina", 8001, "lease-backoff", "agent thread completed", model.ThreadStatusReady, true); err != nil {
		t.Fatalf("success release: %v", err)
	}
	if _, _, _, err := svc.claimThreadLease(context.Background(), "dreamina", 8001, 60_000, "worker"); err != nil {
		t.Fatalf("fourth claim: %v", err)
	}
	released, err = svc.releaseThreadToStatus(context.Background(), "dreamina", 8001, "lease-backoff", "agent thread build failed", model.ThreadStatusReady, true)
	if err != nil {
		t.Fatalf("failure release after success: %v", err)
	}
	if !released.ReadyAt.Equal(current) {
		t.Fatalf("failure after success ready_at = %v, want %v (escalation reset)", released.ReadyAt, current)
	}
}

func TestReleaseWithNonFailureReasonIsImmediatelyScannable(t *testing.T) {
	db := newTestDB(t)
	current := time.Date(2026, 4, 13, 19, 0, 0, 0, time.UTC)
	svc := newTestCoordinator(
		db,
		db,
		nil,
		&stubIDGen{ids: []int64{9001}},
		WithClock(func() time.Time { return current }),
		WithLeaseTokenGenerator(func() string { return "lease-ok" }),
	)
	if err := db.WithContext(context.Background()).Create(&model.TThread{
		ThreadId:  8002,
		Namespace: "dreamina",
		Env:       "prod",
		Status:    model.ThreadStatusReady,
		ReadyAt:   current,
		CreatedAt: current,
		UpdatedAt: current,
	}).Error; err != nil {
		t.Fatalf("insert ready thread: %v", err)
	}

	if _, _, _, err := svc.claimThreadLease(context.Background(), "dreamina", 8002, 60_000, "worker"); err != nil {
		t.Fatalf("claim thread: %v", err)
	}
	released, err := svc.releaseThreadToStatus(context.Background(), "dreamina", 8002, "lease-ok", "agent thread completed", model.ThreadStatusReady, true)
	if err != nil {
		t.Fatalf("release thread: %v", err)
	}
	if !released.ReadyAt.Equal(current) {
		t.Fatalf("released ready_at = %v, want %v (immediate)", released.ReadyAt, current)
	}
	threads, _, _, err := svc.scanRunnableThreadRows(context.Background(), "dreamina", "prod", "", 10)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(threads) != 1 || threads[0].ThreadId != 8002 {
		t.Fatalf("scan = %+v, want thread 8002", threads)
	}
}

func TestReleaseClosingThreadKeepsClosingAndClearsLease(t *testing.T) {
	db := newTestDB(t)
	current := time.Date(2026, 4, 13, 19, 0, 0, 0, time.UTC)
	svc := newTestCoordinator(
		db,
		db,
		nil,
		&stubIDGen{ids: []int64{9001}},
		WithClock(func() time.Time { return current }),
	)
	if err := db.WithContext(context.Background()).Create(&model.TThread{
		ThreadId:  8003,
		Namespace: "dreamina",
		Env:       "prod",
		Status:    model.ThreadStatusClosing,
		ReadyAt:   current,
		// 预置失败 reason 模拟上一轮已失败，本次 release 走连续失败退避分支。
		StatusReason:    "agent thread build failed",
		LeaseToken:      "lease-closing",
		LeaseDeadlineAt: current.Add(time.Minute),
		LeaseOwnerHint:  "worker",
		CreatedAt:       current,
		UpdatedAt:       current,
	}).Error; err != nil {
		t.Fatalf("insert closing thread: %v", err)
	}

	released, err := svc.releaseThreadToStatus(context.Background(), "dreamina", 8003, "lease-closing", "agent thread build failed", model.ThreadStatusReady, true)
	if err != nil {
		t.Fatalf("release closing thread: %v", err)
	}
	if released.Status != model.ThreadStatusClosing {
		t.Fatalf("released status = %s, want closing (close still in progress)", released.Status)
	}
	if released.LeaseToken != "" || !released.LeaseDeadlineAt.IsZero() || released.LeaseOwnerHint != "" {
		t.Fatalf("release should clear lease fields: %+v", released)
	}
	wantReadyAt := current.Add(defaultFailureReleaseBackoff)
	if !released.ReadyAt.Equal(wantReadyAt) {
		t.Fatalf("released ready_at = %v, want %v (failure backoff)", released.ReadyAt, wantReadyAt)
	}

	// 退避到期后，closing 线程应重新可被扫描（lease 已清空，走 closing 扫描分支）。
	current = current.Add(defaultFailureReleaseBackoff + time.Second)
	threads, _, _, err := svc.scanRunnableThreadRows(context.Background(), "dreamina", "prod", "", 10)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(threads) != 1 || threads[0].ThreadId != 8003 {
		t.Fatalf("scan = %+v, want closing thread 8003", threads)
	}
}

func TestReleaseWithWrongLeaseStillFails(t *testing.T) {
	db := newTestDB(t)
	current := time.Date(2026, 4, 13, 19, 0, 0, 0, time.UTC)
	svc := newTestCoordinator(db, db, nil, &stubIDGen{ids: []int64{9001}}, WithClock(func() time.Time { return current }))
	if err := db.WithContext(context.Background()).Create(&model.TThread{
		ThreadId:        8004,
		Namespace:       "dreamina",
		Env:             "prod",
		Status:          model.ThreadStatusClosing,
		ReadyAt:         current,
		LeaseToken:      "lease-real",
		LeaseDeadlineAt: current.Add(time.Minute),
		CreatedAt:       current,
		UpdatedAt:       current,
	}).Error; err != nil {
		t.Fatalf("insert closing thread: %v", err)
	}
	if _, err := svc.releaseThreadToStatus(context.Background(), "dreamina", 8004, "lease-stale", "agent thread completed", model.ThreadStatusIdle, false); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("release with wrong lease = %v, want ErrLeaseMismatch", err)
	}
}
