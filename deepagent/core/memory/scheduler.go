package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"code.byted.org/gopkg/logs/v2"
)

type MemoryJobLoop struct {
	cfg    MemoryJobLoopConfig
	wakeup chan struct{}
}

func NewMemoryJobLoop(cfg MemoryJobLoopConfig) *MemoryJobLoop {
	if cfg.ScanInterval <= 0 {
		cfg.ScanInterval = time.Minute
	}
	if cfg.WakeupDebounce <= 0 {
		cfg.WakeupDebounce = 10 * time.Second
	}
	if cfg.Stage1LeaseTTL <= 0 {
		cfg.Stage1LeaseTTL = 10 * time.Minute
	}
	if cfg.Stage2LeaseTTL <= 0 {
		cfg.Stage2LeaseTTL = time.Hour
	}
	if cfg.Stage2SuccessCooldown <= 0 {
		cfg.Stage2SuccessCooldown = 6 * time.Hour
	}
	if cfg.Stage1MaxClaimedPerScan <= 0 {
		cfg.Stage1MaxClaimedPerScan = 2
	}
	if cfg.Stage2ScanInterval <= 0 {
		cfg.Stage2ScanInterval = 5 * time.Minute
	}
	if cfg.Stage2MaxUsersPerScan <= 0 {
		cfg.Stage2MaxUsersPerScan = 2
	}
	if cfg.Stage2OutputLimitPerUser <= 0 {
		cfg.Stage2OutputLimitPerUser = 100
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &MemoryJobLoop{
		cfg:    cfg,
		wakeup: make(chan struct{}, 1),
	}
}

func (l *MemoryJobLoop) Wake() {
	if l == nil {
		return
	}
	select {
	case l.wakeup <- struct{}{}:
	default:
	}
}

func (l *MemoryJobLoop) Run(ctx context.Context) error {
	if err := l.validate(); err != nil {
		return err
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			_, _ = l.RunOnce(ctx)
			timer.Reset(l.cfg.ScanInterval)
		case <-l.wakeup:
			debounce := time.NewTimer(l.cfg.WakeupDebounce)
			select {
			case <-ctx.Done():
				debounce.Stop()
				return ctx.Err()
			case <-debounce.C:
				_, _ = l.RunOnce(ctx)
			}
			timer.Reset(l.cfg.ScanInterval)
		}
	}
}

func (l *MemoryJobLoop) RunOnce(ctx context.Context) (MemoryJobLoopRunResult, error) {
	if err := l.validate(); err != nil {
		return MemoryJobLoopRunResult{}, err
	}
	start := time.Now()
	logs.CtxInfo(ctx, "[memory] job loop scan start: owner=%s", l.owner())
	result := MemoryJobLoopRunResult{}
	claimed, err := l.cfg.Store.ClaimStage1Sources(ctx, ClaimStage1Request{
		Now:      l.cfg.Now(),
		Owner:    l.owner(),
		LeaseTTL: l.cfg.Stage1LeaseTTL,
		Limit:    l.cfg.Stage1MaxClaimedPerScan,
	})
	if err != nil {
		logs.CtxError(ctx, "[memory] stage1 claim failed: owner=%s err=%v", l.owner(), err)
		return result, err
	}
	result.Stage1Claimed = len(claimed)
	logs.CtxInfo(ctx, "[memory] stage1 claimed: owner=%s count=%d", l.owner(), len(claimed))
	for _, source := range claimed {
		logs.CtxInfo(ctx, "[memory] stage1 run start: user_id=%s source_thread_id=%s token_present=%t", source.UserID, source.SourceThreadID, source.OwnershipToken != "")
		if _, err := l.cfg.Engine.RunClaimedStage1(ctx, source); err != nil {
			logs.CtxError(ctx, "[memory] stage1 run failed: user_id=%s source_thread_id=%s err=%v", source.UserID, source.SourceThreadID, err)
			result.Stage1Failed++
			continue
		}
		logs.CtxInfo(ctx, "[memory] stage1 run success: user_id=%s source_thread_id=%s", source.UserID, source.SourceThreadID)
		result.Stage1Succeeded++
	}

	claimedStage2, err := l.cfg.Store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:             l.cfg.Now(),
		Owner:           l.owner(),
		LeaseTTL:        l.cfg.Stage2LeaseTTL,
		SuccessCooldown: l.cfg.Stage2SuccessCooldown,
		Limit:           l.cfg.Stage2MaxUsersPerScan,
	})
	if err != nil {
		logs.CtxError(ctx, "[memory] stage2 claim failed: owner=%s err=%v", l.owner(), err)
		return result, err
	}
	logs.CtxInfo(ctx, "[memory] stage2 claimed: owner=%s count=%d", l.owner(), len(claimedStage2))
	for _, job := range claimedStage2 {
		result.Stage2Attempted++
		logs.CtxInfo(ctx, "[memory] stage2 prepare start: user_id=%s watermark=%s", job.UserID, job.ClaimedInputWatermark)
		prepared, err := l.cfg.Engine.PrepareStage2(ctx, job, l.cfg.Stage2OutputLimitPerUser)
		if err != nil {
			logs.CtxError(ctx, "[memory] stage2 prepare failed: user_id=%s watermark=%s err=%v", job.UserID, job.ClaimedInputWatermark, err)
			_ = l.cfg.Store.MarkStage2Error(ctx, MarkStage2ErrorRequest{
				UserID:         job.UserID,
				OwnershipToken: job.OwnershipToken,
				ErrorSummary:   err.Error(),
				RetryAt:        l.cfg.Now().Add(l.cfg.Stage2ScanInterval),
				FailedAt:       l.cfg.Now(),
			})
			result.Stage2Failed++
			continue
		}
		if prepared.Noop {
			logs.CtxInfo(ctx, "[memory] stage2 skipped: user_id=%s watermark=%s synced_hash=%s", job.UserID, job.ClaimedInputWatermark, prepared.SyncedHash)
			if err := l.cfg.Store.MarkStage2Done(ctx, MarkStage2DoneRequest{
				UserID:                  job.UserID,
				OwnershipToken:          job.OwnershipToken,
				CompletedInputWatermark: job.ClaimedInputWatermark,
				BaselineHash:            prepared.SyncedHash,
				CompletedAt:             l.cfg.Now(),
			}); err != nil {
				result.Stage2Failed++
				continue
			}
			result.Stage2Skipped++
			continue
		}
		if l.cfg.Stage2ThreadHost == nil {
			logs.CtxError(ctx, "[memory] stage2 thread host missing: user_id=%s watermark=%s", job.UserID, job.ClaimedInputWatermark)
			_ = l.cfg.Store.MarkStage2Error(ctx, MarkStage2ErrorRequest{
				UserID:         job.UserID,
				OwnershipToken: job.OwnershipToken,
				ErrorSummary:   "memory stage2 thread host is required",
				RetryAt:        l.cfg.Now().Add(l.cfg.Stage2ScanInterval),
				FailedAt:       l.cfg.Now(),
			})
			result.Stage2Failed++
			continue
		}
		logs.CtxInfo(ctx, "[memory] stage2 create thread start: user_id=%s watermark=%s output_count=%d", job.UserID, job.ClaimedInputWatermark, prepared.OutputCount)
		created, err := l.cfg.Stage2ThreadHost.CreateStage2Thread(ctx, Stage2CreateThreadRequest{Spec: prepared.Spec})
		if err != nil {
			logs.CtxError(ctx, "[memory] stage2 create thread failed: user_id=%s watermark=%s err=%v", job.UserID, job.ClaimedInputWatermark, err)
			_ = l.cfg.Store.MarkStage2Error(ctx, MarkStage2ErrorRequest{
				UserID:         job.UserID,
				OwnershipToken: job.OwnershipToken,
				ErrorSummary:   err.Error(),
				RetryAt:        l.cfg.Now().Add(l.cfg.Stage2ScanInterval),
				FailedAt:       l.cfg.Now(),
			})
			result.Stage2Failed++
			continue
		}
		if err := l.cfg.Store.BindStage2Thread(ctx, BindStage2ThreadRequest{
			UserID:         job.UserID,
			OwnershipToken: job.OwnershipToken,
			ThreadID:       created.ThreadID,
			UpdatedAt:      l.cfg.Now(),
		}); err != nil {
			logs.CtxError(ctx, "[memory] stage2 bind thread failed: user_id=%s thread_id=%s err=%v", job.UserID, created.ThreadID, err)
			_ = l.cfg.Stage2ThreadHost.CloseStage2Thread(ctx, created.ThreadID, "memory_stage2_bind_failed")
			result.Stage2Failed++
			continue
		}
		logs.CtxInfo(ctx, "[memory] stage2 thread created: user_id=%s thread_id=%s watermark=%s", job.UserID, created.ThreadID, job.ClaimedInputWatermark)
		result.Stage2ThreadCreated++
	}
	logs.CtxInfo(ctx, "[memory] job loop scan done: owner=%s duration_ms=%d stage1_claimed=%d stage1_succeeded=%d stage1_failed=%d stage2_attempted=%d stage2_created=%d stage2_skipped=%d stage2_failed=%d",
		l.owner(), time.Since(start).Milliseconds(), result.Stage1Claimed, result.Stage1Succeeded, result.Stage1Failed, result.Stage2Attempted, result.Stage2ThreadCreated, result.Stage2Skipped, result.Stage2Failed)
	return result, nil
}

func (l *MemoryJobLoop) validate() error {
	if l == nil {
		return fmt.Errorf("memory job loop is nil")
	}
	if l.cfg.Store == nil {
		return fmt.Errorf("memory job loop: store is required")
	}
	if l.cfg.Engine == nil {
		return fmt.Errorf("memory job loop: engine is required")
	}
	return nil
}

func (l *MemoryJobLoop) owner() string {
	if owner := strings.TrimSpace(l.cfg.Owner); owner != "" {
		return owner
	}
	return "memory-job-loop"
}
