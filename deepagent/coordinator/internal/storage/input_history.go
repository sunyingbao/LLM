package storage

import (
	"context"
	"eino-cli/deepagent/coordinator/internal/model"

	"gorm.io/gorm"
)

func InsertThread(ctx context.Context, db *gorm.DB, thread *model.TThread) (err error) {
	return db.WithContext(ctx).Omit("ReadyAt", "LeaseDeadlineAt", "ClosedAt").Create(thread).Error
}

func ArchiveInput(ctx context.Context, db *gorm.DB, message *model.TMailboxMessage) (err error) {
	return db.WithContext(ctx).Omit("HandledAt").Create(message).Error
}

func LastOrdinaryInputID(ctx context.Context, db *gorm.DB, threadID int64) (messageID int64, err error) {
	cursor := int64(1<<63 - 1)
	for {
		var messages []*model.TMailboxMessage
		err := db.WithContext(ctx).Where("thread_id = ? AND message_id < ?", threadID, cursor).
			Order("message_id DESC").Limit(1000).Find(&messages).Error
		if err != nil {
			return 0, err
		}
		if len(messages) == 0 {
			return 0, nil
		}
		for _, message := range messages {
			if model.IsOrdinaryInputMessage(message.MessageType) {
				return message.MessageId, nil
			}
		}
		cursor = messages[len(messages)-1].MessageId
	}
}
