package ac

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"eino-cli/deepagent/coordinator"
)

const listSessionThreadsLimit = int32(100)

type Client struct {
	namespace   string
	coordinator *coordinator.Coordinator
}

func NewClient(core *coordinator.Coordinator, namespace string) (client *Client, err error) {
	if core == nil {
		return nil, fmt.Errorf("coordinator is required")
	}
	if namespace = strings.TrimSpace(namespace); namespace == "" {
		return nil, fmt.Errorf("coordinator namespace is required")
	}
	return &Client{namespace: namespace, coordinator: core}, nil
}

func (c *Client) ListSessionThreads(ctx context.Context, sessionID int64) (threads []*coordinator.Thread, err error) {
	if sessionID <= 0 {
		return nil, fmt.Errorf("session_id is required")
	}
	var cursor int64
	for {
		result, listErr := c.coordinator.ListSessionThreads(ctx, coordinator.ListSessionThreadsRequest{
			Namespace: c.namespace,
			SessionID: strconv.FormatInt(sessionID, 10),
			Cursor:    cursor,
			Limit:     listSessionThreadsLimit,
		})
		if listErr != nil {
			return nil, listErr
		}
		threads = append(threads, result.Threads...)
		if !result.HasMore || result.NextCursor == 0 {
			return threads, nil
		}
		cursor = result.NextCursor
	}
}

func (c *Client) CloseSessionThreads(ctx context.Context, sessionID int64, reason string) (threads []*coordinator.Thread, err error) {
	threads, err = c.ListSessionThreads(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for _, thread := range threads {
		if thread == nil || thread.ThreadID == 0 || thread.Status == coordinator.ThreadStatusClosed {
			continue
		}
		_, err = c.coordinator.RequestThreadClose(ctx, coordinator.RequestThreadCloseRequest{
			Namespace: c.namespace,
			ThreadID:  thread.ThreadID,
			Reason:    strings.TrimSpace(reason),
			Metadata: map[string]string{
				"source":     "deep_agent_sdk_session",
				"session_id": strconv.FormatInt(sessionID, 10),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("close thread %d: %w", thread.ThreadID, err)
		}
	}
	return threads, nil
}
