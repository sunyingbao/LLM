//go:build !windows

package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"code.byted.org/gopkg/ctxvalues"
	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/kite/kitutil"
	"code.byted.org/kite/kitutil/logid"
	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
)

const (
	logMessagePayloadPreviewBytes = 2048
	logThreadSummaryLimit         = 20
)

func (w *Worker) newScanLogContext(ctx context.Context) context.Context {
	scanLogID := logid.GetNginxID()
	ctx = contextWithLogID(ctx, scanLogID)
	return logs.CtxAddKVs(ctx,
		"log_context", "scan",
		"logid_source", "generated",
		"namespace", w.Namespace,
		"worker_env", w.Env,
		"scan_limit", w.ScanLimit,
	)
}

func (w *Worker) newClaimLogContext(ctx context.Context, thread *ac.Thread) context.Context {
	var threadID int64
	if thread != nil {
		threadID = thread.GetThreadId()
	}
	ctx = ensureContextLogID(ctx)
	scanLogID := currentContextLogID(ctx)
	return logs.CtxAddKVs(ctx,
		"log_context", "claim",
		"logid_source", "scan",
		"scan_logid", scanLogID,
		"namespace", w.Namespace,
		"thread_id", threadID,
		"thread_logid", threadProducerLogID(thread),
	)
}

func (w *Worker) newThreadLogContext(ctx context.Context, thread *ac.Thread) context.Context {
	var threadID int64
	if thread != nil {
		threadID = thread.GetThreadId()
	}
	threadLogID := strings.TrimSpace(threadProducerLogID(thread))
	logIDSource := "thread"
	if threadLogID == "" {
		threadLogID = logid.GetNginxID()
		logIDSource = "generated"
	}

	ctx = contextWithLogID(ctx, threadLogID)
	ctx = contextWithThreadLogMeta(ctx, thread)
	return logs.CtxAddKVs(ctx,
		"log_context", "thread",
		"logid_source", logIDSource,
		"namespace", w.Namespace,
		"thread_id", threadID,
	)
}

func (w *Worker) newRunLogContext(ctx context.Context, claim *claimResult) context.Context {
	var thread *ac.Thread
	var lease *ac.Lease
	var pendingMessages []*ac.Message
	if claim != nil {
		thread = claim.thread
		lease = claim.lease
		pendingMessages = claim.pendingMessages
	}
	var threadID int64
	if thread != nil {
		threadID = thread.GetThreadId()
	}
	parentLogID := currentContextLogID(ctx)
	logID := strings.TrimSpace(threadProducerLogID(thread))
	logIDSource := "thread"
	if logID == "" {
		logID = logid.GetNginxID()
		logIDSource = "generated"
	}

	ctx = contextWithLogID(ctx, logID)
	ctx = contextWithThreadLogMeta(ctx, thread)
	return logs.CtxAddKVs(ctx,
		"log_context", "run",
		"logid_source", logIDSource,
		"parent_logid", parentLogID,
		"activation_logid", logID,
		"namespace", w.Namespace,
		"thread_id", threadID,
		"thread_logid", threadProducerLogID(thread),
		"lease_deadline_at_ms", lease.GetLeaseDeadlineAtMs(),
		"pending_message_count", len(pendingMessages),
		"first_pending_message_id", firstMessageID(pendingMessages),
		"first_pending_message_logid", firstMessageLogID(pendingMessages),
	)
}

func (w *Worker) newPullLogContext(ctx context.Context, lease *ac.Lease) context.Context {
	ctx = ensureContextLogID(ctx)
	runLogID := currentContextLogID(ctx)
	return logs.CtxAddKVs(ctx,
		"log_context", "pull_message",
		"logid_source", "inherited",
		"run_logid", runLogID,
		"namespace", w.Namespace,
		"thread_id", lease.GetThreadId(),
		"pull_limit", w.MessageLimit,
	)
}

func (w *Worker) newMessageLogContext(ctx context.Context, thread *ac.Thread, message *ac.Message) context.Context {
	parentLogID := currentContextLogID(ctx)
	messageLogID := messageProducerLogID(message)
	logIDSource := "producer"
	if messageLogID == "" {
		messageLogID = logid.GetNginxID()
		logIDSource = "generated"
	}

	ctx = contextWithLogID(ctx, messageLogID)
	ctx = contextWithMessageLogMeta(ctx, message)
	senderType, senderID := messageSender(message)
	return logs.CtxAddKVs(ctx,
		"log_context", "message",
		"logid_source", logIDSource,
		"parent_logid", parentLogID,
		"namespace", w.Namespace,
		"thread_id", thread.GetThreadId(),
		"input_message_id", message.GetMessageId(),
		"message_type", message.GetMessageType(),
		"sender_type", senderType,
		"sender_id", senderID,
	)
}

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

func messageProducerLogID(message *ac.Message) string {
	if message == nil {
		return ""
	}
	return message.GetMetadata()["logid"]
}

func threadProducerLogID(thread *ac.Thread) string {
	if thread == nil {
		return ""
	}
	return thread.GetMetadata()["logid"]
}

func messageSender(message *ac.Message) (string, string) {
	sender := message.GetSender()
	if sender == nil {
		return "", ""
	}
	return fmt.Sprint(sender.GetSenderType()), sender.GetSenderId()
}

func messageIDs(messages []*ac.Message) []int64 {
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		ids = append(ids, message.GetMessageId())
	}
	return ids
}

func firstMessage(messages []*ac.Message) *ac.Message {
	for _, message := range messages {
		if message != nil {
			return message
		}
	}
	return nil
}

func firstMessageID(messages []*ac.Message) int64 {
	message := firstMessage(messages)
	if message == nil {
		return 0
	}
	return message.GetMessageId()
}

func firstMessageLogID(messages []*ac.Message) string {
	return messageProducerLogID(firstMessage(messages))
}

func messageLogSummary(message *ac.Message) string {
	if message == nil {
		return "{}"
	}
	summary := map[string]interface{}{
		"message_id":    message.GetMessageId(),
		"thread_id":     message.GetThreadId(),
		"message_type":  message.GetMessageType(),
		"status":        fmt.Sprint(message.GetStatus()),
		"logid":         messageProducerLogID(message),
		"k_env":         message.GetMetadata()[metadataKeyKEnv],
		"payload_bytes": len(message.GetPayload()),
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return fmt.Sprintf("message_id=%d thread_id=%d summary_error=%v", message.GetMessageId(), message.GetThreadId(), err)
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

func threadSummaries(threads []*ac.Thread) []map[string]interface{} {
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
			"thread_id":            thread.GetThreadId(),
			"status":               fmt.Sprint(thread.GetStatus()),
			"session_id":           thread.GetSessionId(),
			"title":                thread.GetTitle(),
			"lease_owner_hint":     thread.GetLeaseOwnerHint(),
			"lease_deadline_at_ms": thread.GetLeaseDeadlineAtMs(),
			"env":                  thread.GetEnv(),
		})
	}
	return summaries
}

func messagePreview(message *ac.Message) string {
	senderType, senderID := messageSender(message)
	payload := message.GetPayload()
	payloadPreview := payload
	truncated := false
	if len(payloadPreview) > logMessagePayloadPreviewBytes {
		payloadPreview = payloadPreview[:logMessagePayloadPreviewBytes]
		truncated = true
	}

	preview := map[string]interface{}{
		"message_id":        message.GetMessageId(),
		"thread_id":         message.GetThreadId(),
		"message_type":      message.GetMessageType(),
		"status":            fmt.Sprint(message.GetStatus()),
		"sender_type":       senderType,
		"sender_id":         senderID,
		"metadata":          message.GetMetadata(),
		"payload_preview":   string(payloadPreview),
		"payload_bytes":     len(payload),
		"payload_truncated": truncated,
	}
	data, err := json.Marshal(preview)
	if err != nil {
		return fmt.Sprintf("message_id=%d thread_id=%d payload_bytes=%d preview_error=%v", message.GetMessageId(), message.GetThreadId(), len(payload), err)
	}
	return string(data)
}
