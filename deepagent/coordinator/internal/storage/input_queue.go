package storage

import (
	"context"
	redisstore "eino-cli/deepagent/coordinator/internal/infra/store/redis"
	"eino-cli/deepagent/coordinator/internal/model"
	"errors"
	"fmt"
	"strconv"
	"time"
)

var ErrMessageNotFound = errors.New("message not found in mailbox redis")

type StoredInput struct {
	Namespace string
	Message   *model.TMailboxMessage
}

type InputQueue struct {
	client redisstore.Client
}

func NewInputQueue(client redisstore.Client) (queue *InputQueue) {
	if client == nil {
		return nil
	}
	return &InputQueue{client: client}
}

func (s *InputQueue) Enqueue(ctx context.Context, namespace string, message *model.TMailboxMessage, score float64) (resultErr error) {
	key := redisstore.MessageKey(message.MessageId)
	if err := s.client.StructSet(ctx, key, CachedMessage(namespace, message)); err != nil {
		return err
	}
	if err := s.client.ZAdd(ctx, redisstore.PendingInputKey(namespace, message.ThreadId), score, strconv.FormatInt(message.MessageId, 10)); err != nil {
		_, _ = s.client.Del(ctx, key)
		return err
	}
	return nil
}

func (s *InputQueue) List(ctx context.Context, namespace string, threadID int64, start int64, stop int64) (messages []*model.TMailboxMessage, err error) {
	members, err := s.client.ZRangePrimary(ctx, redisstore.PendingInputKey(namespace, threadID), start, stop)
	if err != nil || len(members) == 0 {
		return nil, err
	}
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		id, parseErr := strconv.ParseInt(member, 10, 64)
		if parseErr != nil {
			return nil, parseErr
		}
		ids = append(ids, id)
	}
	items, err := s.batchGet(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	messages = make([]*model.TMailboxMessage, 0, len(items))
	for idx, item := range items {
		if item == nil || item.Message == nil {
			return nil, fmt.Errorf("%w: message_id=%d", ErrMessageNotFound, ids[idx])
		}
		if item.Namespace != namespace || item.Message.ThreadId != threadID {
			return nil, ownershipMismatch(item)
		}
		messages = append(messages, item.Message)
	}
	return messages, nil
}

func (s *InputQueue) Count(ctx context.Context, namespace string, threadID int64) (count int64, err error) {
	return s.client.ZCardPrimary(ctx, redisstore.PendingInputKey(namespace, threadID))
}

func (s *InputQueue) Get(ctx context.Context, messageID int64) (input *StoredInput, resultErr error) {
	var cached redisstore.CachedMessage
	if err := s.client.StructGetPrimary(ctx, redisstore.MessageKey(messageID), &cached); err != nil {
		return nil, err
	}
	return StoredInputFromCached(&cached), nil
}

func (s *InputQueue) batchGet(ctx context.Context, messageIDs []int64) (inputs []*StoredInput, resultErr error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(messageIDs))
	for _, id := range messageIDs {
		keys = append(keys, redisstore.MessageKey(id))
	}
	var cached []*redisstore.CachedMessage
	if err := s.client.StructMGetPrimary(ctx, keys, &cached); err != nil {
		return nil, err
	}
	items := make([]*StoredInput, len(cached))
	for idx, item := range cached {
		if item != nil {
			items[idx] = StoredInputFromCached(item)
		}
	}
	return items, nil
}

func (s *InputQueue) Finalize(ctx context.Context, namespace string, threadID int64, messageIDs []int64, targetStatus string, triggerTurnID string, handledAt time.Time) (finalized []*model.TMailboxMessage, err error) {
	messages := make([]*model.TMailboxMessage, 0, len(messageIDs))
	members := make([]interface{}, 0, len(messageIDs))
	for _, id := range messageIDs {
		item, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if item.Namespace != namespace || item.Message.ThreadId != threadID {
			return nil, ownershipMismatch(item)
		}
		message := item.Message
		if message.Status == model.MessageStatusPending {
			message.Status = targetStatus
			message.UpdatedAt = handledAt
			message.HandledAt = handledAt
			if triggerTurnID != "" {
				message.TriggerTurnId = triggerTurnID
			}
			if err := s.client.StructSet(ctx, redisstore.MessageKey(id), CachedMessage(namespace, message)); err != nil {
				return nil, err
			}
		}
		messages = append(messages, message)
		members = append(members, strconv.FormatInt(id, 10))
	}
	if _, err := s.client.ZRem(ctx, redisstore.PendingInputKey(namespace, threadID), members); err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *InputQueue) Remove(ctx context.Context, namespace string, threadID int64, messageID int64) (resultErr error) {
	if _, err := s.client.ZRem(ctx, redisstore.PendingInputKey(namespace, threadID), []interface{}{strconv.FormatInt(messageID, 10)}); err != nil {
		return err
	}
	_, err := s.client.Del(ctx, redisstore.MessageKey(messageID))
	return err
}

func StoredInputFromCached(cached *redisstore.CachedMessage) (input *StoredInput) {
	if cached == nil {
		return nil
	}
	return &StoredInput{Namespace: cached.Namespace, Message: cachedMessageToModel(cached)}
}

func ownershipMismatch(item *StoredInput) (err error) {
	if item == nil || item.Message == nil {
		return ErrMessageNotFound
	}
	return fmt.Errorf("mailbox redis message ownership mismatch: message_id=%d namespace=%s thread_id=%d", item.Message.MessageId, item.Namespace, item.Message.ThreadId)
}

func CachedMessage(namespace string, message *model.TMailboxMessage) (value redisstore.CachedMessage) {
	var handledAt *time.Time
	if !message.HandledAt.IsZero() {
		handledAt = &message.HandledAt
	}
	return redisstore.CachedMessage{
		MessageID:     message.MessageId,
		Namespace:     namespace,
		ThreadID:      message.ThreadId,
		SenderType:    message.SenderType,
		SenderID:      message.SenderId,
		MessageType:   message.MessageType,
		Status:        message.Status,
		Payload:       message.Payload,
		MetadataJSON:  message.MetadataJson,
		TriggerTurnID: message.TriggerTurnId,
		CreatedAt:     message.CreatedAt,
		UpdatedAt:     message.UpdatedAt,
		HandledAt:     handledAt,
	}
}

func cachedMessageToModel(cached *redisstore.CachedMessage) (message *model.TMailboxMessage) {
	msg := &model.TMailboxMessage{
		MessageId:     cached.MessageID,
		ThreadId:      cached.ThreadID,
		SenderType:    cached.SenderType,
		SenderId:      cached.SenderID,
		MessageType:   cached.MessageType,
		Status:        cached.Status,
		Payload:       cached.Payload,
		MetadataJson:  cached.MetadataJSON,
		TriggerTurnId: cached.TriggerTurnID,
		CreatedAt:     cached.CreatedAt,
		UpdatedAt:     cached.UpdatedAt,
	}
	if cached.HandledAt != nil {
		msg.HandledAt = *cached.HandledAt
	}
	return msg
}
