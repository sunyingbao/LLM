//go:build !windows

package api

import (
	"context"
	"testing"

	"code.byted.org/gopkg/thrift"
	"code.byted.org/kite/kitex/client/callopt"
	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	acsvc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator/agentcoordinatorservice"
	acbase "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/base"
)

func TestCoordinatorAdapterListEventsUsesStablePageCursor(t *testing.T) {
	client := &recordingACClient{}
	coord := CoordinatorAdapter{Namespace: "ns", Client: client}

	got, err := coord.ListEvents(context.Background(), ListEventsRequest{
		ThreadID: "42",
		Cursor:   "page:prev",
		Limit:    7,
		Backward: true,
	})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if got.NextCursor != "page:next" || !got.HasMore {
		t.Fatalf("page = has_more:%v next_cursor:%q", got.HasMore, got.NextCursor)
	}
	req := client.lastListEvents
	if req == nil {
		t.Fatalf("ListEvents request not recorded")
	}
	if req.GetNamespace() != "ns" || req.GetThreadId() != 42 || req.GetLimit() != 7 {
		t.Fatalf("request identity = %+v", req)
	}
	if req.IsSetCursor() {
		t.Fatalf("legacy cursor should be unset, got %d", req.GetCursor())
	}
	if req.GetPageCursor() != "page:prev" {
		t.Fatalf("page_cursor = %q", req.GetPageCursor())
	}
	if req.GetOrderBy() != ac.EventListOrder_CREATED_AT_IN_THREAD_SEQ {
		t.Fatalf("order_by = %v", req.GetOrderBy())
	}
	if req.GetDirection() != ac.EventListDirection_BACKWARD {
		t.Fatalf("direction = %v", req.GetDirection())
	}
}

func TestCoordinatorAdapterListTurnEventsUsesStablePageCursor(t *testing.T) {
	client := &recordingACClient{}
	coord := CoordinatorAdapter{Namespace: "ns", Client: client}

	got, err := coord.ListTurnEvents(context.Background(), ListTurnEventsRequest{
		ThreadID: "42",
		TurnID:   "turn-1",
		Cursor:   "turn-page:prev",
		Limit:    3,
	})
	if err != nil {
		t.Fatalf("ListTurnEvents() error = %v", err)
	}
	if got.NextCursor != "turn-page:next" || !got.HasMore {
		t.Fatalf("page = has_more:%v next_cursor:%q", got.HasMore, got.NextCursor)
	}
	req := client.lastListTurnEvents
	if req == nil {
		t.Fatalf("ListTurnEvents request not recorded")
	}
	if req.GetNamespace() != "ns" || req.GetThreadId() != 42 || req.GetTurnId() != "turn-1" || req.GetLimit() != 3 {
		t.Fatalf("request identity = %+v", req)
	}
	if req.IsSetCursor() {
		t.Fatalf("legacy cursor should be unset, got %d", req.GetCursor())
	}
	if req.GetPageCursor() != "turn-page:prev" {
		t.Fatalf("page_cursor = %q", req.GetPageCursor())
	}
	if req.GetOrderBy() != ac.EventListOrder_CREATED_AT_IN_THREAD_SEQ {
		t.Fatalf("order_by = %v", req.GetOrderBy())
	}
	if req.GetDirection() != ac.EventListDirection_FORWARD {
		t.Fatalf("direction = %v", req.GetDirection())
	}
}

type recordingACClient struct {
	acsvc.Client

	lastListEvents     *ac.ListEventsRequest
	lastListTurnEvents *ac.ListTurnEventsRequest
}

func (c *recordingACClient) ListEvents(ctx context.Context, req *ac.ListEventsRequest, callOptions ...callopt.Option) (*ac.ListEventsResponse, error) {
	c.lastListEvents = req
	return &ac.ListEventsResponse{
		BaseResp:       okACBaseResp(),
		Events:         []*ac.Event{{EventId: thrift.Int64Ptr(101)}},
		NextPageCursor: thrift.StringPtr("page:next"),
		HasMore:        thrift.BoolPtr(true),
	}, nil
}

func (c *recordingACClient) ListTurnEvents(ctx context.Context, req *ac.ListTurnEventsRequest, callOptions ...callopt.Option) (*ac.ListTurnEventsResponse, error) {
	c.lastListTurnEvents = req
	return &ac.ListTurnEventsResponse{
		BaseResp:       okACBaseResp(),
		Events:         []*ac.Event{{EventId: thrift.Int64Ptr(102)}},
		NextPageCursor: thrift.StringPtr("turn-page:next"),
		HasMore:        thrift.BoolPtr(true),
	}, nil
}

func okACBaseResp() *acbase.BaseResp {
	return &acbase.BaseResp{StatusCode: 0, StatusMessage: "OK"}
}
