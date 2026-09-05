package storage

import (
	"context"
	"eino-cli/deepagent/coordinator/internal/model"
	"time"

	"gorm.io/gorm"
)

func FindThread(ctx context.Context, db *gorm.DB, threadID int64) (thread *model.TThread, err error) {
	thread = new(model.TThread)
	result := db.WithContext(ctx).Raw("select * from t_thread where thread_id = ?", threadID).Scan(thread)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return thread, nil
}

func ListSessionThreads(ctx context.Context, db *gorm.DB, namespace string, sessionID string, cursor int64, limit int32) (threads []*model.TThread, err error) {
	result := db.WithContext(ctx).Raw(
		"select * from t_thread where namespace = ? and session_id = ? and thread_id > ? order by thread_id asc limit ?",
		namespace,
		sessionID,
		cursor,
		limit,
	).Scan(&threads)
	if result.Error != nil {
		return nil, result.Error
	}
	if len(threads) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return threads, nil
}

func FindNamespace(ctx context.Context, db *gorm.DB, namespace string) (row *model.TAgentNamespace, err error) {
	row = new(model.TAgentNamespace)
	result := db.WithContext(ctx).Raw("select * from t_agent_namespace where namespace = ?", namespace).Scan(row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return row, nil
}

func FindMessage(ctx context.Context, db *gorm.DB, messageID int64) (message *model.TMailboxMessage, err error) {
	message = new(model.TMailboxMessage)
	result := db.WithContext(ctx).Raw("select * from t_mailbox_message where message_id = ?", messageID).Scan(message)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return message, nil
}

func UpdateInputStatus(ctx context.Context, db *gorm.DB, threadID int64, fromStatus string, toStatus string, now time.Time, triggerTurnID *string, messageIDs []int64) (rowsAffected int64, err error) {
	query := "update t_mailbox_message set status = ?, updated_at = ?, handled_at = ? where thread_id = ? and status = ? and message_id in (?)"
	args := []any{toStatus, now, now, threadID, fromStatus, messageIDs}
	if triggerTurnID != nil {
		query = "update t_mailbox_message set status = ?, updated_at = ?, handled_at = ?, trigger_turn_id = ? where thread_id = ? and status = ? and message_id in (?)"
		args = []any{toStatus, now, now, *triggerTurnID, threadID, fromStatus, messageIDs}
	}
	result := db.WithContext(ctx).Exec(query, args...)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func FindEvents(ctx context.Context, db *gorm.DB, eventIDs []int64) (events []*model.TEventLog, err error) {
	result := db.WithContext(ctx).Raw(
		"select * from t_event_log where event_id in ? order by event_id asc",
		eventIDs,
	).Scan(&events)
	if result.Error != nil {
		return nil, result.Error
	}
	if len(events) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return events, nil
}
