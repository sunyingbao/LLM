package redis

import "fmt"

func PendingInputKey(namespace string, threadID int64) string {
	return fmt.Sprintf("ac:%s:thread:%d:input", namespace, threadID)
}

func MessageKey(messageID int64) string {
	return fmt.Sprintf("ac:message:%d", messageID)
}

func SessionQueueSetKey(namespace string, sessionID string) string {
	return fmt.Sprintf("ac:%s:session:%s:queues", namespace, sessionID)
}

func StreamQueueMetaKey(queueID string) string {
	return fmt.Sprintf("ac:queue:%s:meta", queueID)
}

func StreamQueuePendingKey(queueID string) string {
	return fmt.Sprintf("ac:queue:%s:pending", queueID)
}

func StreamQueueSequenceKey(queueID string) string {
	return fmt.Sprintf("ac:queue:%s:seq", queueID)
}

func StreamQueueInlineEventKey(queueID string, deliveryID string) string {
	return fmt.Sprintf("ac:queue:%s:inline:%s", queueID, deliveryID)
}

func SessionLiveSequenceKey(namespace string, sessionID string) string {
	return fmt.Sprintf("ac:%s:session:%s:live:seq", namespace, sessionID)
}

func SessionLiveEventKey(namespace string, sessionID string, seq int64) string {
	return fmt.Sprintf("ac:%s:session:%s:live:%d", namespace, sessionID, seq)
}
