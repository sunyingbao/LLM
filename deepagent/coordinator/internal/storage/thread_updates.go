package storage

import (
	"context"
	"eino-cli/deepagent/coordinator/internal/model"
	"errors"
	"time"

	"gorm.io/gorm"
)

func ScanReadyLikeThreads(ctx context.Context, db *gorm.DB, namespace string, env string, cursorTime time.Time, cursorThreadID int64, limit int32, now time.Time) (threads []*model.TThread, err error) {
	threads = make([]*model.TThread, 0, limit)
	err = db.WithContext(ctx).
		Where("namespace = ? AND env = ? AND (status = ? OR (status = ? AND (lease_token = '' OR lease_deadline_at IS NULL OR lease_deadline_at < ?)))",
			namespace, env, model.ThreadStatusReady, model.ThreadStatusClosing, now).
		Where("ready_at <= ?", now).
		Where("(ready_at > ? OR (ready_at = ? AND thread_id > ?))", cursorTime, cursorTime, cursorThreadID).
		Order("ready_at ASC, thread_id ASC").
		Limit(int(limit)).
		Find(&threads).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return threads, nil
}

func ScanExpiredRunningThreads(ctx context.Context, db *gorm.DB, namespace string, env string, cursorTime time.Time, cursorThreadID int64, limit int32, now time.Time) (threads []*model.TThread, err error) {
	threads = make([]*model.TThread, 0, limit)
	err = db.WithContext(ctx).
		Where("namespace = ? AND env = ? AND status = ? AND lease_deadline_at IS NOT NULL AND lease_deadline_at < ?",
			namespace, env, model.ThreadStatusRunning, now).
		Where("(lease_deadline_at > ? OR (lease_deadline_at = ? AND thread_id > ?))", cursorTime, cursorTime, cursorThreadID).
		Order("lease_deadline_at ASC, thread_id ASC").
		Limit(int(limit)).
		Find(&threads).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return threads, nil
}

func FindSessionEnvironmentMismatch(ctx context.Context, db *gorm.DB, namespace string, sessionID string, env string) (thread *model.TThread, err error) {
	thread = new(model.TThread)
	result := db.WithContext(ctx).
		Where("namespace = ? and session_id = ? and env <> ?", namespace, sessionID, env).
		Limit(1).
		Find(thread)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return thread, nil
}

func RenewActiveThreadLease(ctx context.Context, db *gorm.DB, namespace string, threadID int64, leaseToken string, leaseDeadlineAt time.Time, leaseOwnerHint string, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set lease_deadline_at = ?, lease_owner_hint = ?, updated_at = ?
		 where thread_id = ? and namespace = ? and status in (?, ?) and
		 lease_token = ? and lease_deadline_at is not null and lease_deadline_at >= ?`,
		leaseDeadlineAt,
		leaseOwnerHint,
		now,
		threadID,
		namespace,
		model.ThreadStatusRunning,
		model.ThreadStatusClosing,
		leaseToken,
		now,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func ClaimReadyThread(ctx context.Context, db *gorm.DB, namespace string, threadID int64, leaseToken string, leaseDeadlineAt time.Time, leaseOwnerHint string, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set status = ?, ready_at = null, lease_token = ?, lease_deadline_at = ?, lease_owner_hint = ?, last_active_at = ?, updated_at = ?
		 where thread_id = ? and namespace = ? and status = ?`,
		model.ThreadStatusRunning,
		leaseToken,
		leaseDeadlineAt,
		leaseOwnerHint,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusReady,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func ClaimExpiredRunningThread(ctx context.Context, db *gorm.DB, namespace string, threadID int64, statusReason string, leaseToken string, leaseDeadlineAt time.Time, leaseOwnerHint string, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set status_reason = ?, lease_token = ?, lease_deadline_at = ?, lease_owner_hint = ?, last_active_at = ?, updated_at = ?
		 where thread_id = ? and namespace = ? and status = ? and lease_deadline_at is not null and lease_deadline_at < ?`,
		statusReason,
		leaseToken,
		leaseDeadlineAt,
		leaseOwnerHint,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusRunning,
		now,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func ClaimClosingThread(ctx context.Context, db *gorm.DB, namespace string, threadID int64, leaseToken string, leaseDeadlineAt time.Time, leaseOwnerHint string, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set lease_token = ?, lease_deadline_at = ?, lease_owner_hint = ?, last_active_at = ?, updated_at = ?
		 where thread_id = ? and namespace = ? and status = ? and (lease_token = '' or lease_deadline_at is null or lease_deadline_at < ?)`,
		leaseToken,
		leaseDeadlineAt,
		leaseOwnerHint,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusClosing,
		now,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func ReleaseRunningThread(ctx context.Context, db *gorm.DB, namespace string, threadID int64, leaseToken string, targetStatus string, reason string, readyAt any, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set status = ?, status_reason = ?, ready_at = ?, lease_token = '', lease_deadline_at = null, lease_owner_hint = '', last_active_at = ?, updated_at = ?
			 where thread_id = ? and namespace = ? and status = ? and lease_token = ? and lease_deadline_at is not null and lease_deadline_at >= ?`,
		targetStatus,
		reason,
		readyAt,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusRunning,
		leaseToken,
		now,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func ReleaseClosingThread(ctx context.Context, db *gorm.DB, namespace string, threadID int64, leaseToken string, reason string, readyAt time.Time, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set status_reason = ?, ready_at = ?, lease_token = '', lease_deadline_at = null, lease_owner_hint = '', last_active_at = ?, updated_at = ?
			 where thread_id = ? and namespace = ? and status = ? and lease_token = ? and lease_deadline_at is not null and lease_deadline_at >= ?`,
		reason,
		readyAt,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusClosing,
		leaseToken,
		now,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func ResumeBlockedThread(ctx context.Context, db *gorm.DB, namespace string, threadID int64, targetStatus string, reason string, readyAt any, metadataJSON string, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set status = ?, status_reason = ?, ready_at = ?, metadata_json = ?, lease_token = '', lease_deadline_at = null, lease_owner_hint = '', last_active_at = ?, updated_at = ?
			 where thread_id = ? and namespace = ? and status = ?`,
		targetStatus,
		reason,
		readyAt,
		metadataJSON,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusBlocked,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func WakeIdleThread(ctx context.Context, db *gorm.DB, namespace string, threadID int64, reason string, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set status = ?, status_reason = ?, ready_at = ?, last_active_at = ?, updated_at = ?
			 where thread_id = ? and namespace = ? and status = ?`,
		model.ThreadStatusReady,
		reason,
		now,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusIdle,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func WakeIdleThreadWithActivation(ctx context.Context, db *gorm.DB, namespace string, threadID int64, reason string, metadataJSON string, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set status = ?, status_reason = ?, ready_at = ?, metadata_json = ?, last_active_at = ?, updated_at = ?
		 where thread_id = ? and namespace = ? and status = ?`,
		model.ThreadStatusReady,
		reason,
		now,
		metadataJSON,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusIdle,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func WakeIdleThreadForInput(ctx context.Context, db *gorm.DB, namespace string, threadID int64, metadataJSON string, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set status = ?, ready_at = ?, metadata_json = ?, last_active_at = ?, updated_at = ? where thread_id = ? and namespace = ? and status = ?`,
		model.ThreadStatusReady,
		now,
		metadataJSON,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusIdle,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func StartClosingIdleOrBlockedThread(ctx context.Context, db *gorm.DB, namespace string, threadID int64, currentStatus string, reason string, metadataJSON string, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set status = ?, status_reason = ?, ready_at = ?, metadata_json = ?, last_active_at = ?, updated_at = ?
			 where thread_id = ? and namespace = ? and status = ?`,
		model.ThreadStatusClosing,
		reason,
		now,
		metadataJSON,
		now,
		now,
		threadID,
		namespace,
		currentStatus,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func StartClosingReadyOrRunningThread(ctx context.Context, db *gorm.DB, namespace string, threadID int64, reason string, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set status = ?, status_reason = ?, ready_at = ?, last_active_at = ?, updated_at = ?
			 where thread_id = ? and namespace = ? and status in (?, ?)`,
		model.ThreadStatusClosing,
		reason,
		now,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusReady,
		model.ThreadStatusRunning,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func CompleteClosingThread(ctx context.Context, db *gorm.DB, namespace string, threadID int64, leaseToken string, reason string, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set status = ?, status_reason = ?, ready_at = null, lease_token = '', lease_deadline_at = null,
		 lease_owner_hint = '', closed_at = ?, last_active_at = ?, updated_at = ?
		 where thread_id = ? and namespace = ? and status = ? and lease_token = ? and lease_deadline_at is not null and lease_deadline_at >= ?`,
		model.ThreadStatusClosed,
		reason,
		now,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusClosing,
		leaseToken,
		now,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func PullReadyAtForward(ctx context.Context, db *gorm.DB, namespace string, threadID int64, now time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set ready_at = ?, updated_at = ? where thread_id = ? and namespace = ? and status = ? and ready_at > ?`,
		now,
		now,
		threadID,
		namespace,
		model.ThreadStatusReady,
		now,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func TouchThreadActive(ctx context.Context, db *gorm.DB, namespace string, threadID int64, activeAt time.Time) (changed bool, err error) {
	result := db.WithContext(ctx).Exec(
		`update t_thread set last_active_at = ?, updated_at = ? where thread_id = ? and namespace = ?`,
		activeAt,
		activeAt,
		threadID,
		namespace,
	)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
