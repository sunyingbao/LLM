package ac

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"code.byted.org/gopkg/logid"
	"code.byted.org/gopkg/metainfo"
	"code.byted.org/kite/kitex/client"
	coordinator "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	acsvc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator/agentcoordinatorservice"
	acbase "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/base"
)

const listSessionThreadsLimit = int32(100)

type Client struct {
	namespace string
	rpc       acsvc.Client
}

func NewClient(psm string, cluster string, hostports []string, namespace string) (*Client, error) {
	if strings.TrimSpace(namespace) == "" {
		return nil, fmt.Errorf("ac namespace is required")
	}
	opts := make([]client.Option, 0, len(hostports))
	if cluster = strings.TrimSpace(cluster); cluster != "" {
		opts = append(opts, client.WithCluster(cluster))
	}
	if len(hostports) > 0 {
		opts = append(opts, client.WithHostPorts(hostports...))
	}
	rpc, err := acsvc.NewClient(psm, opts...)
	if err != nil {
		return nil, fmt.Errorf("create agent_coordinator client: %w", err)
	}
	return &Client{namespace: namespace, rpc: rpc}, nil
}

func (c *Client) ListSessionThreads(ctx context.Context, sessionID int64) ([]*coordinator.Thread, error) {
	if c == nil || c.rpc == nil {
		return nil, nil
	}
	if sessionID <= 0 {
		return nil, fmt.Errorf("session_id is required")
	}
	var (
		cursor  int64
		threads []*coordinator.Thread
	)
	for {
		req := &coordinator.ListSessionThreadsRequest{
			Namespace: c.namespace,
			SessionId: strconv.FormatInt(sessionID, 10),
			Limit:     int32Ptr(listSessionThreadsLimit),
			Base:      acbase.NewBase(),
		}
		if cursor > 0 {
			req.Cursor = int64Ptr(cursor)
		}
		resp, err := c.rpc.ListSessionThreads(withLogID(ctx), req)
		if err := rpcError("ListSessionThreads", resp, err); err != nil {
			return nil, err
		}
		if resp == nil {
			return nil, fmt.Errorf("ListSessionThreads returned nil response")
		}
		threads = append(threads, resp.GetThreads()...)
		if !resp.GetHasMore() || resp.GetNextCursor() == 0 {
			return threads, nil
		}
		cursor = resp.GetNextCursor()
	}
}

func (c *Client) CloseSessionThreads(ctx context.Context, sessionID int64, reason string) ([]*coordinator.Thread, error) {
	threads, err := c.ListSessionThreads(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for _, thread := range threads {
		if thread == nil || thread.GetThreadId() == 0 || thread.GetStatus() == coordinator.ThreadStatus_CLOSED {
			continue
		}
		req := &coordinator.CloseThreadRequest{
			Namespace: c.namespace,
			ThreadId:  thread.GetThreadId(),
			Base:      acbase.NewBase(),
			Metadata: map[string]string{
				"source":     "aic_agent_sdk_session",
				"session_id": strconv.FormatInt(sessionID, 10),
			},
		}
		if trimmed := strings.TrimSpace(reason); trimmed != "" {
			req.Reason = stringPtr(trimmed)
		}
		resp, err := c.rpc.CloseThread(withLogID(ctx), req)
		if err := rpcError(fmt.Sprintf("CloseThread thread_id=%d", thread.GetThreadId()), resp, err); err != nil {
			return nil, err
		}
	}
	return threads, nil
}

type baseRespGetter interface {
	GetBaseResp() *acbase.BaseResp
}

func rpcError(op string, resp baseRespGetter, err error) error {
	if resp != nil {
		if baseResp := resp.GetBaseResp(); baseResp != nil && baseResp.GetStatusCode() != 0 {
			msg := fmt.Sprintf("%s status_code=%d status_message=%q", op, baseResp.GetStatusCode(), baseResp.GetStatusMessage())
			if err != nil {
				return fmt.Errorf("%s: %w", msg, err)
			}
			return fmt.Errorf("%s", msg)
		}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

func withLogID(ctx context.Context) context.Context {
	if _, ok := metainfo.GetValue(ctx, "x-tt-logid"); ok {
		return ctx
	}
	return metainfo.WithValue(ctx, "x-tt-logid", logid.GenLogID())
}

func stringPtr(value string) *string {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}

func int32Ptr(value int32) *int32 {
	return &value
}
