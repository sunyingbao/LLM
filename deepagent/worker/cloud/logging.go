//go:build !windows

package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"code.byted.org/gopkg/ctxvalues"
	"code.byted.org/kite/kitutil"
	"code.byted.org/kite/kitutil/logid"
	"eino-cli/deepagent/coordinator"
)

const (
	logMessagePayloadPreviewBytes = 2048
	logThreadSummaryLimit         = 20
)

func contextWithLogID(ctx context.Context, logID string) context.Context {
	logID = strings.TrimSpace(logID)
	if logID == "" {
		return ctx
	}
	ctx = ctxvalues.SetLogID(ctx, logID)
	return kitutil.NewCtxWithLogID(ctx, logID)
}

func ensureContextLogID(ctx context.Context) context.Context {
	if logID, ok := kitutil.GetCtxLogID(ctx); ok && strings.TrimSpace(logID) != "" {
		return contextWithLogID(ctx, logID)
	}
	if logID, ok := ctxvalues.LogID(ctx); ok && strings.TrimSpace(logID) != "" {
		return contextWithLogID(ctx, logID)
	}
	return contextWithLogID(ctx, logid.GetNginxID())
}

func currentContextLogID(ctx context.Context) string {
	if logID, ok := kitutil.GetCtxLogID(ctx); ok && strings.TrimSpace(logID) != "" {
		return strings.TrimSpace(logID)
	}
	if logID, ok := ctxvalues.LogID(ctx); ok && strings.TrimSpace(logID) != "" {
		return strings.TrimSpace(logID)
	}
	return ""
}

func messageProducerLogID(message *coordinator.Message) string {
	if message == nil {
		return ""
	}
	return message.Metadata["logid"]
}

func threadProducerLogID(thread *coordinator.Thread) string {
	if thread == nil {
		return ""
	}
	return thread.Metadata["logid"]
}

func messageSender(message *coordinator.Message) (senderType string, senderID string) {
	if message == nil || message.Sender == nil {
		return "", ""
	}
	return strings.ToUpper(string(message.Sender.Type)), message.Sender.ID
}

func messageIDs(messages []*coordinator.Message) []int64 {
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		ids = append(ids, message.MessageID)
	}
	return ids
}

func firstMessage(messages []*coordinator.Message) *coordinator.Message {
	for _, message := range messages {
		if message != nil {
			return message
		}
	}
	return nil
}

func firstMessageID(messages []*coordinator.Message) int64 {
	message := firstMessage(messages)
	if message == nil {
		return 0
	}
	return message.MessageID
}

func firstMessageLogID(messages []*coordinator.Message) string {
	return messageProducerLogID(firstMessage(messages))
}

func messageLogSummary(message *coordinator.Message) string {
	if message == nil {
		return "{}"
	}
	summary := map[string]interface{}{
		"message_id":    message.MessageID,
		"thread_id":     message.ThreadID,
		"message_type":  message.MessageType,
		"status":        strings.ToUpper(string(message.Status)),
		"logid":         messageProducerLogID(message),
		"k_env":         message.Metadata[metadataKeyKEnv],
		"payload_bytes": len(message.Payload),
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return fmt.Sprintf("message_id=%d thread_id=%d summary_error=%v", message.MessageID, message.ThreadID, err)
	}
	return string(data)
}

func metadataKeys(metadata map[string]string) []string {
	if len(metadata) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func threadSummaries(threads []*coordinator.Thread) []map[string]interface{} {
	limit := len(threads)
	if limit > logThreadSummaryLimit {
		limit = logThreadSummaryLimit
	}
	summaries := make([]map[string]interface{}, 0, limit)
	for _, thread := range threads[:limit] {
		if thread == nil {
			continue
		}
		summaries = append(summaries, map[string]interface{}{
			"thread_id":            thread.ThreadID,
			"status":               strings.ToUpper(string(thread.Status)),
			"session_id":           thread.SessionID,
			"title":                thread.Title,
			"lease_owner_hint":     thread.LeaseOwnerHint,
			"lease_deadline_at_ms": timeToMillis(thread.LeaseDeadlineAt),
			"env":                  thread.Env,
		})
	}
	return summaries
}

func messagePreview(message *coordinator.Message) string {
	senderType, senderID := messageSender(message)
	payload := message.Payload
	payloadPreview := payload
	truncated := false
	if len(payloadPreview) > logMessagePayloadPreviewBytes {
		payloadPreview = payloadPreview[:logMessagePayloadPreviewBytes]
		truncated = true
	}

	preview := map[string]interface{}{
		"message_id":        message.MessageID,
		"thread_id":         message.ThreadID,
		"message_type":      message.MessageType,
		"status":            strings.ToUpper(string(message.Status)),
		"sender_type":       senderType,
		"sender_id":         senderID,
		"metadata":          message.Metadata,
		"payload_preview":   string(payloadPreview),
		"payload_bytes":     len(payload),
		"payload_truncated": truncated,
	}
	data, err := json.Marshal(preview)
	if err != nil {
		return fmt.Sprintf("message_id=%d thread_id=%d payload_bytes=%d preview_error=%v", message.MessageID, message.ThreadID, len(payload), err)
	}
	return string(data)
}

func leaseDeadlineAtMS(lease *coordinator.Lease) (milliseconds int64) {
	if lease == nil {
		return 0
	}
	return timeToMillis(lease.LeaseDeadlineAt)
}

func timeToMillis(value time.Time) (milliseconds int64) {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}
