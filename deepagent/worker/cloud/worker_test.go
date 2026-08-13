//go:build !windows

package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code.byted.org/gopkg/ctxvalues"
	"code.byted.org/gopkg/metainfo"
	"code.byted.org/kite/kitex/client/callopt"
	"code.byted.org/kite/kitutil"
	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	acsvc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator/agentcoordinatorservice"
	"code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/base"
	"eino-cli/deepagent/worker"
)

type testAgentThreadRuntime struct {
	InitFunc              func(ctx context.Context) (*agentworker.ThreadOutput, error)
	PostMessageFunc       func(ctx context.Context, input *agentworker.Message) error
	PostMessageResultFunc func(ctx context.Context, input *agentworker.Message) (*agentworker.PostMessageResult, error)
	InterruptFunc         func(ctx context.Context, req agentworker.ThreadInterruptRequest) error
	ActiveTurnFunc        func() *agentworker.ActiveTurn
	CloseFunc             func(ctx context.Context) error
}

func mustParseTestID(t *testing.T, value string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("parse id %q: %v", value, err)
	}
	return id
}

func (r *testAgentThreadRuntime) Init(ctx context.Context) (*agentworker.ThreadOutput, error) {
	if r != nil && r.InitFunc != nil {
		return r.InitFunc(ctx)
	}
	items := make(chan agentworker.ThreadOutputItem, 1)
	items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
	close(items)
	return &agentworker.ThreadOutput{Items: items}, nil
}

func (r *testAgentThreadRuntime) PostMessage(ctx context.Context, input *agentworker.Message) (*agentworker.PostMessageResult, error) {
	if r != nil && r.PostMessageResultFunc != nil {
		return r.PostMessageResultFunc(ctx, input)
	}
	if r != nil && r.PostMessageFunc != nil {
		return nil, r.PostMessageFunc(ctx, input)
	}
	return nil, nil
}

func (r *testAgentThreadRuntime) Interrupt(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
	if r != nil && r.InterruptFunc != nil {
		return r.InterruptFunc(ctx, req)
	}
	return nil
}

func (r *testAgentThreadRuntime) ActiveTurn() *agentworker.ActiveTurn {
	if r != nil && r.ActiveTurnFunc != nil {
		return r.ActiveTurnFunc()
	}
	return nil
}

func (r *testAgentThreadRuntime) Close(ctx context.Context) error {
	if r != nil && r.CloseFunc != nil {
		return r.CloseFunc(ctx)
	}
	return nil
}

func TestRunClaimAppendsAcksAndReleases(t *testing.T) {
	pulls := 0
	items := make(chan agentworker.ThreadOutputItem, 4)
	client := &fakeClient{
		pullPendingMessagesFunc: func(req *ac.PullPendingMessagesRequest) []*ac.Message {
			pulls++
			if req.GetLeaseToken() != "lease-token" {
				t.Fatalf("pull lease token=%q, want lease-token", req.GetLeaseToken())
			}
			if req.GetLimit() == 0 {
				t.Fatalf("pull limit should be set")
			}
			if pulls == 1 {
				return []*ac.Message{{MessageId: 1002, ThreadId: 42, Status: ac.MessageStatus_PENDING}}
			}
			return nil
		},
	}
	handled := make([]int64, 0, 2)
	var initCalls int32
	var closeCalls int32
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		IdleTimeout:   time.Second,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			if threadInfo.GetThreadId() != 42 {
				t.Fatalf("factory thread_id=%d, want 42", threadInfo.GetThreadId())
			}
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					atomic.AddInt32(&initCalls, 1)
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					if input == nil {
						t.Fatalf("message is nil")
					}
					messageID := mustParseTestID(t, input.ID)
					handled = append(handled, messageID)
					items <- agentworker.ThreadOutputItem{Event: &agentworker.Event{TurnID: TurnIDFromMessageID(messageID), Type: "agent_result", Payload: []byte("ok")}}
					if messageID == 1002 {
						items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "test complete"}}
					}
					return nil
				},
				CloseFunc: func(ctx context.Context) error {
					atomic.AddInt32(&closeCalls, 1)
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}

	if len(handled) != 2 || handled[0] != 1001 || handled[1] != 1002 {
		t.Fatalf("handled messages=%v, want [1001 1002]", handled)
	}
	if got := atomic.LoadInt32(&initCalls); got != 1 {
		t.Fatalf("init calls=%d, want 1", got)
	}
	if got := atomic.LoadInt32(&closeCalls); got != 1 {
		t.Fatalf("close calls=%d, want 1", got)
	}

	if len(client.appendEventsRequests) != 2 {
		t.Fatalf("append requests=%d, want 2", len(client.appendEventsRequests))
	}
	appended := client.appendEventsRequests[0]
	if appended.GetNamespace() != "test_ns" || appended.GetThreadId() != 42 {
		t.Fatalf("append request ownership mismatch: namespace=%q thread_id=%d", appended.GetNamespace(), appended.GetThreadId())
	}
	if len(appended.GetEvents()) != 1 || appended.GetEvents()[0].GetEventType() != "agent_result" {
		t.Fatalf("append events=%v, want agent_result", appended.GetEvents())
	}
	if appended.GetEvents()[0].GetThreadId() != 42 {
		t.Fatalf("event thread_id=%d, want worker to fill 42", appended.GetEvents()[0].GetThreadId())
	}
	if appended.GetEvents()[0].GetTurnId() != "turn_1001" || client.appendEventsRequests[1].GetEvents()[0].GetTurnId() != "turn_1002" {
		t.Fatalf("event turn ids=%s,%s want turn_1001,turn_1002", appended.GetEvents()[0].GetTurnId(), client.appendEventsRequests[1].GetEvents()[0].GetTurnId())
	}

	if len(client.ackRequests) != 2 {
		t.Fatalf("ack requests=%d, want 2", len(client.ackRequests))
	}
	ack := client.ackRequests[0]
	if ack.GetNamespace() != "test_ns" || ack.GetThreadId() != 42 || ack.GetLeaseToken() != "lease-token" {
		t.Fatalf("ack request mismatch: namespace=%q thread_id=%d lease=%q", ack.GetNamespace(), ack.GetThreadId(), ack.GetLeaseToken())
	}
	if got := ack.GetMessageIds(); len(got) != 1 || got[0] != 1001 {
		t.Fatalf("ack message ids=%v, want [1001]", got)
	}
	if got := client.ackRequests[1].GetMessageIds(); len(got) != 1 || got[0] != 1002 {
		t.Fatalf("second ack message ids=%v, want [1002]", got)
	}

	if len(client.releaseRequests) != 1 {
		t.Fatalf("release requests=%d, want 1", len(client.releaseRequests))
	}
	release := client.releaseRequests[0]
	if release.GetNamespace() != "test_ns" || release.GetThreadId() != 42 || release.GetLeaseToken() != "lease-token" {
		t.Fatalf("release request mismatch: namespace=%q thread_id=%d lease=%q", release.GetNamespace(), release.GetThreadId(), release.GetLeaseToken())
	}
	if release.GetReason() != "test complete" {
		t.Fatalf("release reason=%q, want test complete", release.GetReason())
	}
}

func TestRunClaimAcksMessageWithRuntimeTurnID(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	client := &fakeClient{}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		IdleTimeout:   time.Second,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageResultFunc: func(ctx context.Context, input *agentworker.Message) (*agentworker.PostMessageResult, error) {
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
					return &agentworker.PostMessageResult{TurnID: "turn_1001"}, nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(client.ackRequests) != 1 {
		t.Fatalf("ack requests=%d, want 1", len(client.ackRequests))
	}
	if got := client.ackRequests[0].GetTriggerTurnId(); got != "turn_1001" {
		t.Fatalf("ack trigger_turn_id=%q, want turn_1001", got)
	}
}

func TestRunClaimDrainsOutputWhilePostMessageWaits(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem)
	postStarted := make(chan struct{})
	eventAppended := make(chan struct{})
	var eventAppendedClosed atomic.Bool
	var active atomic.Bool
	active.Store(true)

	client := &fakeClient{
		appendEventsFunc: func(ctx context.Context, req *ac.AppendEventsRequest) error {
			if eventAppendedClosed.CompareAndSwap(false, true) {
				close(eventAppended)
			}
			return nil
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		IdleTimeout:   time.Second,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					close(postStarted)
					select {
					case <-eventAppended:
						active.Store(false)
						go func() {
							items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
						}()
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					if !active.Load() {
						return nil
					}
					return &agentworker.ActiveTurn{TurnID: "turn_1000", ConsumedMessageIDs: []string{"1000"}}
				},
			}, nil
		}),
	}

	go func() {
		<-postStarted
		items <- agentworker.ThreadOutputItem{Event: &agentworker.Event{TurnID: "turn_1000", Type: "agent_result", Payload: []byte("previous turn done")}}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := worker.runClaim(ctx, context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(client.appendEventsRequests) != 1 {
		t.Fatalf("append requests=%d, want previous turn event appended", len(client.appendEventsRequests))
	}
	if len(client.ackRequests) != 1 || client.ackRequests[0].GetMessageIds()[0] != 1001 {
		t.Fatalf("ack requests=%v, want message 1001 acked", client.ackRequests)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != "done" {
		t.Fatalf("release requests=%v, want done release", client.releaseRequests)
	}
}

func TestRunClaimAcksCurrentMessageBeforeReleaseOnRuntimeYield(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	acked := make(chan struct{})
	var ackedClosed atomic.Bool
	block := &agentworker.PendingBlock{
		TurnID:       "turn_1001",
		InterruptID:  "interrupt_1",
		CheckpointID: "checkpoint_1",
		Kind:         "approval",
	}
	client := &fakeClient{
		ackThreadMessagesFunc: func(ctx context.Context, req *ac.AckThreadMessagesRequest) error {
			if ackedClosed.CompareAndSwap(false, true) {
				close(acked)
			}
			return nil
		},
		releaseThreadFunc: func(ctx context.Context, req *ac.ReleaseThreadRequest) error {
			select {
			case <-acked:
			default:
				t.Fatalf("release happened before current message ack")
			}
			return nil
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		IdleTimeout:   time.Second,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{
						Reason: "waiting for approval",
						Block:  block,
					}}
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(client.ackRequests) != 1 || client.ackRequests[0].GetMessageIds()[0] != 1001 {
		t.Fatalf("ack requests=%v, want message 1001 acked", client.ackRequests)
	}
	if len(client.releaseRequests) != 1 {
		t.Fatalf("release requests=%d, want 1", len(client.releaseRequests))
	}
	if client.releaseRequests[0].ReleaseToStatus == nil || *client.releaseRequests[0].ReleaseToStatus != ac.ThreadStatus_BLOCKED {
		t.Fatalf("release_to_status=%v, want BLOCKED", client.releaseRequests[0].ReleaseToStatus)
	}
}

func TestRunClaimDrainsReadyOutputAfterRuntimeYield(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 2)
	client := &fakeClient{}
	block := &agentworker.PendingBlock{
		TurnID:       "turn_1001",
		InterruptID:  "interrupt_1",
		CheckpointID: "checkpoint_1",
		Kind:         "approval",
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		IdleTimeout:   time.Second,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{
						Reason: "waiting for approval",
						Block:  block,
					}}
					items <- agentworker.ThreadOutputItem{Event: &agentworker.Event{
						TurnID:  "turn_1001",
						Type:    "approval_visible_after_yield",
						Payload: []byte("tail event"),
					}}
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(client.appendEventsRequests) != 1 {
		t.Fatalf("append requests=%d, want tail event appended", len(client.appendEventsRequests))
	}
	if got := client.appendEventsRequests[0].GetEvents()[0].GetEventType(); got != "approval_visible_after_yield" {
		t.Fatalf("appended event type=%q, want approval_visible_after_yield", got)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].ReleaseToStatus == nil || *client.releaseRequests[0].ReleaseToStatus != ac.ThreadStatus_BLOCKED {
		t.Fatalf("release requests=%v, want BLOCKED release", client.releaseRequests)
	}
}

func TestRunClaimAckFailureOverridesRuntimeYield(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	ackErr := errors.New("ack failed")
	block := &agentworker.PendingBlock{
		TurnID:       "turn_1001",
		InterruptID:  "interrupt_1",
		CheckpointID: "checkpoint_1",
		Kind:         "approval",
	}
	client := &fakeClient{
		ackThreadMessagesFunc: func(ctx context.Context, req *ac.AckThreadMessagesRequest) error {
			return ackErr
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		IdleTimeout:   time.Second,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{
						Reason: "waiting for approval",
						Block:  block,
					}}
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if !errors.Is(err, ackErr) {
		t.Fatalf("runClaim error=%v, want ack error", err)
	}
	if len(client.releaseRequests) != 1 {
		t.Fatalf("release requests=%d, want 1", len(client.releaseRequests))
	}
	if got := client.releaseRequests[0].GetReason(); got != ackMessageFailedReason {
		t.Fatalf("release reason=%q, want %q", got, ackMessageFailedReason)
	}
	if client.releaseRequests[0].ReleaseToStatus != nil {
		t.Fatalf("release_to_status=%v, want nil on input failure", client.releaseRequests[0].ReleaseToStatus)
	}
}

func TestRunClaimPullFailureDoesNotOverrideRuntimeBlock(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	pullAttempted := make(chan struct{})
	var pullAttemptedClosed atomic.Bool
	var active atomic.Bool
	active.Store(true)
	pullErr := errors.New("pull failed")
	block := &agentworker.PendingBlock{
		TurnID:       "turn_1001",
		InterruptID:  "interrupt_1",
		CheckpointID: "checkpoint_1",
		Kind:         "approval",
	}
	client := &fakeClient{
		pullPendingMessagesResultFunc: func(req *ac.PullPendingMessagesRequest) ([]*ac.Message, error) {
			if pullAttemptedClosed.CompareAndSwap(false, true) {
				close(pullAttempted)
				return nil, pullErr
			}
			return nil, nil
		},
	}
	worker := &Worker{
		Namespace:           "test_ns",
		Client:              client,
		RenewInterval:       time.Hour,
		MessagePollInterval: time.Millisecond,
		IdleTimeout:         time.Second,
		MessageLimit:        10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					go func() {
						select {
						case <-pullAttempted:
							active.Store(false)
							items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{
								Reason: "waiting for approval",
								Block:  block,
							}}
						case <-ctx.Done():
						}
					}()
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					if !active.Load() {
						return nil
					}
					return &agentworker.ActiveTurn{TurnID: "turn_1001", ConsumedMessageIDs: []string{"1001"}}
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(client.releaseRequests) != 1 {
		t.Fatalf("release requests=%d, want 1", len(client.releaseRequests))
	}
	release := client.releaseRequests[0]
	if got := release.GetReason(); got != "waiting for approval" {
		t.Fatalf("release reason=%q, want waiting for approval", got)
	}
	if release.ReleaseToStatus == nil || *release.ReleaseToStatus != ac.ThreadStatus_BLOCKED {
		t.Fatalf("release_to_status=%v, want BLOCKED", release.ReleaseToStatus)
	}
	if len(client.ackRequests) != 1 || client.ackRequests[0].GetMessageIds()[0] != 1001 {
		t.Fatalf("ack requests=%v, want message 1001 acked", client.ackRequests)
	}
}

func TestRunClaimPullFailureRetriesAndProcessesLaterMessage(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	var pullCalls atomic.Int32
	var active atomic.Bool
	active.Store(true)
	client := &fakeClient{
		pullPendingMessagesResultFunc: func(req *ac.PullPendingMessagesRequest) ([]*ac.Message, error) {
			call := pullCalls.Add(1)
			if call == 1 {
				return nil, errors.New("temporary pull failure")
			}
			if call == 2 {
				return []*ac.Message{{MessageId: 1002, ThreadId: 42, Status: ac.MessageStatus_PENDING}}, nil
			}
			return nil, nil
		},
	}
	handled := make([]int64, 0, 2)
	worker := &Worker{
		Namespace:           "test_ns",
		Client:              client,
		RenewInterval:       time.Hour,
		MessagePollInterval: time.Millisecond,
		IdleTimeout:         time.Second,
		MessageLimit:        10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					messageID := mustParseTestID(t, input.ID)
					handled = append(handled, messageID)
					if messageID == 1002 {
						active.Store(false)
						items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
					}
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					if !active.Load() {
						return nil
					}
					return &agentworker.ActiveTurn{TurnID: "turn_active", ConsumedMessageIDs: []string{"1001"}}
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(handled) != 2 || handled[0] != 1001 || handled[1] != 1002 {
		t.Fatalf("handled messages=%v, want [1001 1002]", handled)
	}
	if got := len(client.ackRequests); got != 2 {
		t.Fatalf("ack requests=%d, want 2", got)
	}
	if got := pullCalls.Load(); got < 2 {
		t.Fatalf("pull calls=%d, want at least 2", got)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != "done" {
		t.Fatalf("release requests=%v, want done release", client.releaseRequests)
	}
}

func TestResolveClaimActionSemanticPriority(t *testing.T) {
	coordinator := &claimCoordinator{}
	block := &agentworker.PendingBlock{TurnID: "turn_1", InterruptID: "interrupt_1", CheckpointID: "checkpoint_1", Kind: "approval"}

	closeAction := coordinator.resolveClaimAction(
		newReleaseAction(defaultGracefulReleaseReason, nil),
		&inputStopReason{kind: inputStopCloseHandled, reason: defaultCloseThreadReason, closeMessageID: 1001},
		&runtimeYield{reason: "waiting for approval", block: block},
	)
	if closeAction.kind != claimActionCompleteClose || closeAction.controlMessageID != 1001 {
		t.Fatalf("close action=%+v, want complete close", closeAction)
	}

	failedAction := coordinator.resolveClaimAction(
		newReleaseAction(defaultGracefulReleaseReason, nil),
		&inputStopReason{kind: inputStopFailed, reason: defaultErrorReleaseReason, err: errors.New("ack failed")},
		&runtimeYield{reason: "waiting for approval", block: block},
	)
	if failedAction.kind != claimActionRelease || failedAction.block != nil || failedAction.reason != defaultErrorReleaseReason {
		t.Fatalf("failed action=%+v, want failed release without block", failedAction)
	}

	yieldAction := coordinator.resolveClaimAction(
		newReleaseAction(defaultGracefulReleaseReason, nil),
		nil,
		&runtimeYield{reason: "waiting for approval", block: block},
	)
	if yieldAction.kind != claimActionRelease || yieldAction.block == nil || yieldAction.reason != "waiting for approval" {
		t.Fatalf("yield action=%+v, want runtime block release", yieldAction)
	}
}

func TestRunClaimDoesNotAckWhenEnqueueFails(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	var releaseCtxCanceled atomic.Bool
	client := &fakeClient{
		releaseThreadFunc: func(ctx context.Context, req *ac.ReleaseThreadRequest) error {
			if err := ctx.Err(); err != nil {
				releaseCtxCanceled.Store(true)
			}
			return nil
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		IdleTimeout:   time.Second,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					return agentworker.ErrThreadBackpressure
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err == nil || !strings.Contains(err.Error(), agentworker.ErrThreadBackpressure.Error()) {
		t.Fatalf("runClaim error = %v, want backpressure", err)
	}
	if len(client.ackRequests) != 0 {
		t.Fatalf("ack requests=%d, want 0", len(client.ackRequests))
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != postMessageFailedReason {
		t.Fatalf("release requests=%v, want error release", client.releaseRequests)
	}
	if releaseCtxCanceled.Load() {
		t.Fatalf("release was called with canceled context")
	}
}

func TestRunClaimReleasesWithBuildFailureReason(t *testing.T) {
	buildErr := errors.New("build failed")
	client := &fakeClient{}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return nil, buildErr
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING, Metadata: map[string]string{"logid": "message-logid"}},
		},
	})
	if !errors.Is(err, buildErr) {
		t.Fatalf("runClaim error=%v, want build error", err)
	}
	if len(client.ackRequests) != 0 {
		t.Fatalf("ack requests=%d, want 0", len(client.ackRequests))
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != buildThreadFailedReason {
		t.Fatalf("release requests=%v, want build failure release", client.releaseRequests)
	}
}

func TestRunClaimReleasesWithInitFailureReason(t *testing.T) {
	initErr := errors.New("init failed")
	client := &fakeClient{}
	var closeCalled atomic.Bool
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return nil, initErr
				},
				CloseFunc: func(ctx context.Context) error {
					closeCalled.Store(true)
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING, Metadata: map[string]string{"logid": "message-logid"}},
		},
	})
	if !errors.Is(err, initErr) {
		t.Fatalf("runClaim error=%v, want init error", err)
	}
	if !closeCalled.Load() {
		t.Fatalf("runtime Close was not called")
	}
	if len(client.ackRequests) != 0 {
		t.Fatalf("ack requests=%d, want 0", len(client.ackRequests))
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != initThreadFailedReason {
		t.Fatalf("release requests=%v, want init failure release", client.releaseRequests)
	}
}

func TestRunClaimLeavesClosedThreadMessagePendingAndReleases(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	client := &fakeClient{}
	var posted atomic.Int32
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		IdleTimeout:   time.Second,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					posted.Add(1)
					return agentworker.ErrThreadClosed
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
			{MessageId: 1002, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error = %v", err)
	}
	if got := posted.Load(); got != 1 {
		t.Fatalf("posted messages=%d, want 1", got)
	}
	if len(client.ackRequests) != 0 {
		t.Fatalf("ack requests=%v, want none", client.ackRequests)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != defaultThreadClosedReason {
		t.Fatalf("release requests=%v, want thread closed release", client.releaseRequests)
	}
	if len(client.pullRequests) != 0 {
		t.Fatalf("pull requests=%d, want 0 after closed runtime", len(client.pullRequests))
	}
}

func TestRunClaimDoesNotCompleteCloseControlBehindClosedMessage(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	closePayload, _ := json.Marshal(CloseThreadControlPayload{Reason: "done"})
	client := &fakeClient{}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					return agentworker.ErrThreadClosed
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_CLOSING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, MessageType: "text", Status: ac.MessageStatus_PENDING},
			{MessageId: 9002, ThreadId: 42, MessageType: MessageTypeControlCloseThread, Payload: closePayload, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error = %v", err)
	}
	if len(client.ackRequests) != 0 {
		t.Fatalf("ack requests=%v, want none", client.ackRequests)
	}
	if len(client.completeCloseRequests) != 0 {
		t.Fatalf("complete close requests=%v, want none", client.completeCloseRequests)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != defaultThreadClosedReason {
		t.Fatalf("release requests=%v, want thread closed release", client.releaseRequests)
	}
}

func TestRunClaimReturnsReleaseError(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	releaseErr := errors.New("release failed")
	client := &fakeClient{
		releaseThreadFunc: func(ctx context.Context, req *ac.ReleaseThreadRequest) error {
			return releaseErr
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		IdleTimeout:   time.Second,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					return agentworker.ErrThreadBackpressure
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if !errors.Is(err, releaseErr) || !errors.Is(err, agentworker.ErrThreadBackpressure) {
		t.Fatalf("runClaim error = %v, want post and release errors", err)
	}
}

func TestRunClaimDropsInvalidRuntimeEventAndContinues(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 2)
	items <- agentworker.ThreadOutputItem{Event: &agentworker.Event{Type: "agent_result", Payload: []byte("ok")}}
	items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
	client := &fakeClient{}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		IdleTimeout:   time.Second,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(client.ackRequests) != 1 {
		t.Fatalf("ack requests=%d, want enqueue ack", len(client.ackRequests))
	}
	if len(client.appendEventsRequests) != 0 {
		t.Fatalf("append requests=%d, want invalid event dropped before append", len(client.appendEventsRequests))
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != "done" {
		t.Fatalf("release requests=%v, want done release", client.releaseRequests)
	}
}

func TestRunClaimDropsEventAfterAppendRetriesAndContinues(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 2)
	items <- agentworker.ThreadOutputItem{Event: &agentworker.Event{TurnID: "turn_1001", Type: "agent_result", Payload: []byte("ok")}}
	items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
	client := &fakeClient{
		appendEventsFunc: func(ctx context.Context, req *ac.AppendEventsRequest) error {
			return errors.New("temporary append failure")
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		IdleTimeout:   time.Second,
		MessageLimit:  10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(client.appendEventsRequests) != defaultAppendEventAttempts {
		t.Fatalf("append attempts=%d, want %d", len(client.appendEventsRequests), defaultAppendEventAttempts)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != "done" {
		t.Fatalf("release requests=%v, want done release", client.releaseRequests)
	}
}

func TestRunClaimDoesNotIdleReleaseWhileThreadActive(t *testing.T) {
	var active atomic.Bool
	items := make(chan agentworker.ThreadOutputItem, 1)
	client := &fakeClient{}
	worker := &Worker{
		Namespace:           "test_ns",
		Client:              client,
		RenewInterval:       time.Hour,
		IdleTimeout:         10 * time.Millisecond,
		MessagePollInterval: 5 * time.Millisecond,
		MessageLimit:        10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					active.Store(true)
					go func() {
						time.Sleep(50 * time.Millisecond)
						active.Store(false)
						items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "run done"}}
					}()
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					if !active.Load() {
						return nil
					}
					return &agentworker.ActiveTurn{TurnID: "turn_1001", ConsumedMessageIDs: []string{"1001"}}
				},
			}, nil
		}),
	}

	start := time.Now()
	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 45*time.Millisecond {
		t.Fatalf("claim released before active run completed: elapsed=%s", elapsed)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != "run done" {
		t.Fatalf("release requests=%v, want run done", client.releaseRequests)
	}
}

func TestRunClaimIdleTimeoutStartsAfterThreadBecomesInactive(t *testing.T) {
	var active atomic.Bool
	items := make(chan agentworker.ThreadOutputItem)
	client := &fakeClient{}
	worker := &Worker{
		Namespace:           "test_ns",
		Client:              client,
		RenewInterval:       time.Hour,
		IdleTimeout:         20 * time.Millisecond,
		MessagePollInterval: 5 * time.Millisecond,
		MessageLimit:        10,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					active.Store(true)
					go func() {
						time.Sleep(30 * time.Millisecond)
						active.Store(false)
						items <- agentworker.ThreadOutputItem{Event: &agentworker.Event{TurnID: "turn_1001", Type: "agent_result", Payload: []byte("ok")}}
					}()
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					if !active.Load() {
						return nil
					}
					return &agentworker.ActiveTurn{TurnID: "turn_1001", ConsumedMessageIDs: []string{"1001"}}
				},
			}, nil
		}),
	}

	start := time.Now()
	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 45*time.Millisecond {
		t.Fatalf("idle timeout started before runtime became inactive: elapsed=%s", elapsed)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != defaultReleaseReason {
		t.Fatalf("release requests=%v, want idle release", client.releaseRequests)
	}
}

func TestRunClaimShutdownDrainStopsPullAndPendingDelivery(t *testing.T) {
	var active atomic.Bool
	acceptCtx, stopAccept := context.WithCancel(context.Background())
	items := make(chan agentworker.ThreadOutputItem)
	client := &fakeClient{
		pullPendingMessagesFunc: func(req *ac.PullPendingMessagesRequest) []*ac.Message {
			t.Fatalf("pull should stop during shutdown drain")
			return nil
		},
	}
	handled := make([]int64, 0, 1)
	worker := &Worker{
		Namespace:            "test_ns",
		Client:               client,
		RenewInterval:        time.Hour,
		IdleTimeout:          time.Hour,
		MessagePollInterval:  5 * time.Millisecond,
		MessageLimit:         10,
		ShutdownDrainTimeout: time.Second,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					messageID := mustParseTestID(t, input.ID)
					handled = append(handled, messageID)
					active.Store(true)
					stopAccept()
					go func() {
						time.Sleep(30 * time.Millisecond)
						items <- agentworker.ThreadOutputItem{Event: &agentworker.Event{TurnID: "turn_1001", Type: "agent_result", Payload: []byte("ok")}}
						active.Store(false)
					}()
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					if !active.Load() {
						return nil
					}
					return &agentworker.ActiveTurn{TurnID: "turn_1001", ConsumedMessageIDs: []string{"1001"}}
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), acceptCtx, &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
			{MessageId: 1002, ThreadId: 42, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(handled) != 1 || handled[0] != 1001 {
		t.Fatalf("handled messages=%v, want only [1001]", handled)
	}
	if len(client.ackRequests) != 1 || client.ackRequests[0].GetMessageIds()[0] != 1001 {
		t.Fatalf("ack requests=%v, want only message 1001", client.ackRequests)
	}
	if len(client.pullRequests) != 0 {
		t.Fatalf("pull requests=%d, want 0", len(client.pullRequests))
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != defaultGracefulReleaseReason {
		t.Fatalf("release requests=%v, want graceful release", client.releaseRequests)
	}
	if got := client.releaseRequests[0].ReleaseToStatus; got != nil {
		t.Fatalf("release_to_status=%v, want nil", got)
	}
}

func TestRunClaimShutdownDrainKeepsReadyRuntimeYield(t *testing.T) {
	acceptCtx, stopAccept := context.WithCancel(context.Background())
	stopAccept()
	items := make(chan agentworker.ThreadOutputItem, 1)
	block := &agentworker.PendingBlock{
		TurnID:       "turn_1001",
		InterruptID:  "interrupt_1",
		CheckpointID: "checkpoint_1",
		Kind:         "approval",
	}
	items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{
		Reason: "waiting for approval",
		Block:  block,
	}}
	client := &fakeClient{}
	worker := &Worker{
		Namespace:            "test_ns",
		Client:               client,
		RenewInterval:        time.Hour,
		MessagePollInterval:  5 * time.Millisecond,
		ShutdownDrainTimeout: time.Second,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), acceptCtx, &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(client.releaseRequests) != 1 {
		t.Fatalf("release requests=%d, want 1", len(client.releaseRequests))
	}
	if got := client.releaseRequests[0].GetReason(); got != "waiting for approval" {
		t.Fatalf("release reason=%q, want runtime yield reason", got)
	}
	if client.releaseRequests[0].ReleaseToStatus == nil || *client.releaseRequests[0].ReleaseToStatus != ac.ThreadStatus_BLOCKED {
		t.Fatalf("release_to_status=%v, want BLOCKED", client.releaseRequests[0].ReleaseToStatus)
	}
}

func TestRunClaimShutdownDrainTimeoutInterruptsBeforeRelease(t *testing.T) {
	acceptCtx, stopAccept := context.WithCancel(context.Background())
	stopAccept()
	items := make(chan agentworker.ThreadOutputItem)
	client := &fakeClient{}
	var gotInterrupt agentworker.ThreadInterruptRequest
	worker := &Worker{
		Namespace:                     "test_ns",
		Client:                        client,
		RenewInterval:                 time.Hour,
		MessagePollInterval:           5 * time.Millisecond,
		ShutdownDrainTimeout:          20 * time.Millisecond,
		ShutdownInterruptDrainTimeout: 20 * time.Millisecond,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				InterruptFunc: func(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
					gotInterrupt = req
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					return &agentworker.ActiveTurn{TurnID: "turn_1001", ConsumedMessageIDs: []string{"1001"}}
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), acceptCtx, &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if gotInterrupt.Kind != agentworker.ThreadInterruptKindWorkerShutdownTimeout || gotInterrupt.Reason != defaultShutdownTimeoutReason {
		t.Fatalf("interrupt request=%+v", gotInterrupt)
	}
	if gotInterrupt.Timeout == nil || *gotInterrupt.Timeout != 10*time.Millisecond {
		t.Fatalf("interrupt timeout=%v, want 10ms", gotInterrupt.Timeout)
	}
	if len(client.appendEventsRequests) != 0 {
		t.Fatalf("append requests=%d, want 0; worker SDK must not emit business events", len(client.appendEventsRequests))
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != defaultShutdownTimeoutReason {
		t.Fatalf("release requests=%v, want shutdown timeout release", client.releaseRequests)
	}
	if got := client.releaseRequests[0].ReleaseToStatus; got != nil {
		t.Fatalf("release_to_status=%v, want nil", got)
	}
}

func TestRunClaimCloseControlStopDoesNotWaitFullInterruptDrain(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	closePayload, _ := json.Marshal(CloseThreadControlPayload{Reason: "user_close"})
	client := &fakeClient{}
	worker := &Worker{
		Namespace:               "test_ns",
		Client:                  client,
		RenewInterval:           time.Hour,
		MessagePollInterval:     5 * time.Millisecond,
		InterruptDrainTimeout:   time.Second,
		RuntimeInterruptTimeout: 25 * time.Millisecond,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				InterruptFunc: func(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{
						Reason: "runtime yielded while close control is draining",
						Block: &agentworker.PendingBlock{
							TurnID:       "turn_1001",
							InterruptID:  "interrupt_1",
							CheckpointID: "checkpoint_1",
							Kind:         "approval",
						},
					}}
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					return &agentworker.ActiveTurn{TurnID: "turn_1001", ConsumedMessageIDs: []string{"1001"}}
				},
			}, nil
		}),
	}

	start := time.Now()
	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_CLOSING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 9002, ThreadId: 42, MessageType: MessageTypeControlCloseThread, Payload: closePayload, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("close control waited too long after coordinator stop: elapsed=%s", elapsed)
	}
	if len(client.completeCloseRequests) != 1 || client.completeCloseRequests[0].GetControlMessageId() != 9002 {
		t.Fatalf("complete close requests=%v, want close control completion", client.completeCloseRequests)
	}
	if len(client.releaseRequests) != 0 {
		t.Fatalf("release requests=%d, want 0", len(client.releaseRequests))
	}
}

func TestRunClaimShutdownInterruptDrainConsumesOutput(t *testing.T) {
	acceptCtx, stopAccept := context.WithCancel(context.Background())
	stopAccept()
	var active atomic.Bool
	active.Store(true)
	items := make(chan agentworker.ThreadOutputItem, 1)
	client := &fakeClient{}
	worker := &Worker{
		Namespace:                     "test_ns",
		Client:                        client,
		RenewInterval:                 time.Hour,
		MessagePollInterval:           5 * time.Millisecond,
		ShutdownDrainTimeout:          20 * time.Millisecond,
		ShutdownInterruptDrainTimeout: time.Second,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				InterruptFunc: func(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
					active.Store(false)
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "runtime interrupted by shutdown"}}
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					if !active.Load() {
						return nil
					}
					return &agentworker.ActiveTurn{TurnID: "turn_1001", ConsumedMessageIDs: []string{"1001"}}
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), acceptCtx, &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != "runtime interrupted by shutdown" {
		t.Fatalf("release requests=%v, want output yield reason", client.releaseRequests)
	}
}

func TestRunClaimShutdownInterruptDrainWaitsForDelayedRuntimeOutput(t *testing.T) {
	acceptCtx, stopAccept := context.WithCancel(context.Background())
	stopAccept()
	var active atomic.Bool
	active.Store(true)
	items := make(chan agentworker.ThreadOutputItem, 1)
	client := &fakeClient{}
	worker := &Worker{
		Namespace:                     "test_ns",
		Client:                        client,
		RenewInterval:                 time.Hour,
		MessagePollInterval:           5 * time.Millisecond,
		ShutdownDrainTimeout:          20 * time.Millisecond,
		ShutdownInterruptDrainTimeout: time.Second,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				InterruptFunc: func(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
					if req.Kind != agentworker.ThreadInterruptKindWorkerShutdownTimeout {
						t.Errorf("interrupt kind=%s, want %s", req.Kind, agentworker.ThreadInterruptKindWorkerShutdownTimeout)
					}
					go func() {
						time.Sleep(50 * time.Millisecond)
						active.Store(false)
						items <- agentworker.ThreadOutputItem{
							Event: &agentworker.Event{
								TurnID:  "turn_1001",
								Type:    agentworker.EventType("business_turn_interrupted"),
								Payload: []byte(`{"reason":"worker graceful exit timeout"}`),
							},
							Yield: &agentworker.ThreadYield{Reason: "business observed shutdown interrupt"},
						}
					}()
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					if !active.Load() {
						return nil
					}
					return &agentworker.ActiveTurn{TurnID: "turn_1001", ConsumedMessageIDs: []string{"1001"}}
				},
			}, nil
		}),
	}

	start := time.Now()
	err := worker.runClaim(context.Background(), acceptCtx, &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("runClaim returned too early: elapsed=%s", elapsed)
	}
	if len(client.appendEventsRequests) != 1 {
		t.Fatalf("append requests=%d, want 1", len(client.appendEventsRequests))
	}
	event := client.appendEventsRequests[0].GetEvents()[0]
	if event.GetTurnId() != "turn_1001" || event.GetEventType() != "business_turn_interrupted" {
		t.Fatalf("event turn=%q type=%q", event.GetTurnId(), event.GetEventType())
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != "business observed shutdown interrupt" {
		t.Fatalf("release requests=%v, want business yield reason", client.releaseRequests)
	}
}

func TestRunClaimCancelControlInterruptsAndContinuesAfterInactive(t *testing.T) {
	var active atomic.Bool
	active.Store(true)
	items := make(chan agentworker.ThreadOutputItem, 2)
	cancelPayload, _ := json.Marshal(CancelInputControlPayload{CutoffMessageID: 1001, Reason: "user_cancel"})
	client := &fakeClient{}
	ops := make([]string, 0, 3)
	client.ackThreadMessagesFunc = func(ctx context.Context, req *ac.AckThreadMessagesRequest) error {
		ids := req.GetMessageIds()
		if len(ids) > 0 {
			ops = append(ops, "ack:"+strconv.FormatInt(ids[0], 10))
		}
		return nil
	}
	handled := make([]string, 0, 1)
	var gotInterrupt agentworker.ThreadInterruptRequest
	worker := &Worker{
		Namespace:               "test_ns",
		Client:                  client,
		RenewInterval:           time.Hour,
		IdleTimeout:             time.Hour,
		MessagePollInterval:     5 * time.Millisecond,
		InterruptDrainTimeout:   time.Second,
		RuntimeInterruptTimeout: 25 * time.Millisecond,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					handled = append(handled, input.ID)
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
					return nil
				},
				InterruptFunc: func(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
					gotInterrupt = req
					ops = append(ops, "interrupt")
					active.Store(false)
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					if !active.Load() {
						return nil
					}
					return &agentworker.ActiveTurn{TurnID: "turn_1000", ConsumedMessageIDs: []string{"1000"}}
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 9001, ThreadId: 42, MessageType: MessageTypeControlCancelInput, Payload: cancelPayload, Status: ac.MessageStatus_PENDING},
			{MessageId: 1001, ThreadId: 42, MessageType: "text", Status: ac.MessageStatus_PENDING},
			{MessageId: 1002, ThreadId: 42, MessageType: "text", Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if gotInterrupt.Kind != agentworker.ThreadInterruptKindCancelInput || gotInterrupt.ControlMessageID != "9001" || gotInterrupt.CutoffMessageID != "1001" {
		t.Fatalf("interrupt request=%+v", gotInterrupt)
	}
	if gotInterrupt.Timeout == nil || *gotInterrupt.Timeout != 25*time.Millisecond {
		t.Fatalf("interrupt timeout=%v, want 25ms", gotInterrupt.Timeout)
	}
	if len(handled) != 1 || handled[0] != "1002" {
		t.Fatalf("handled=%v, want only message 1002", handled)
	}
	if len(client.ackRequests) != 2 || client.ackRequests[0].GetMessageIds()[0] != 9001 || client.ackRequests[1].GetMessageIds()[0] != 1002 {
		t.Fatalf("ack requests=%v, want control then 1002", client.ackRequests)
	}
	if strings.Join(ops, ",") != "interrupt,ack:9001,ack:1002" {
		t.Fatalf("ops=%v, want interrupt before control ack", ops)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != "done" {
		t.Fatalf("release requests=%v, want done", client.releaseRequests)
	}
}

func TestRunClaimCancelControlParseFailureAcksAndReleases(t *testing.T) {
	client := &fakeClient{}
	var postCalled atomic.Bool
	var closeCalled atomic.Bool
	worker := &Worker{
		Namespace:             "test_ns",
		Client:                client,
		RenewInterval:         time.Hour,
		InterruptDrainTimeout: 20 * time.Millisecond,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: make(chan agentworker.ThreadOutputItem)}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					postCalled.Store(true)
					return nil
				},
				CloseFunc: func(ctx context.Context) error {
					closeCalled.Store(true)
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 9001, ThreadId: 42, MessageType: MessageTypeControlCancelInput, Payload: []byte("{bad"), Status: ac.MessageStatus_PENDING},
			{MessageId: 1002, ThreadId: 42, MessageType: "text", Status: ac.MessageStatus_PENDING},
		},
	})
	if err == nil {
		t.Fatalf("runClaim error = nil, want invalid control error")
	}
	if postCalled.Load() {
		t.Fatalf("ordinary pending should not be posted after invalid cancel control")
	}
	if !closeCalled.Load() {
		t.Fatalf("runtime Close was not called")
	}
	if len(client.ackRequests) != 1 || client.ackRequests[0].GetMessageIds()[0] != 9001 {
		t.Fatalf("ack requests=%v, want only cancel control ack", client.ackRequests)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != controlInputFailedReason {
		t.Fatalf("release requests=%v, want error release", client.releaseRequests)
	}
}

func TestWorkerRuntimeInterruptTimeoutDefaultIsCappedBelowDrainTimeout(t *testing.T) {
	tests := []struct {
		name string
		w    Worker
		want time.Duration
	}{
		{
			name: "normal default",
			w:    Worker{},
			want: defaultRuntimeInterruptTimeout,
		},
		{
			name: "default capped below small drain",
			w:    Worker{InterruptDrainTimeout: time.Second},
			want: 500 * time.Millisecond,
		},
		{
			name: "explicit value",
			w:    Worker{InterruptDrainTimeout: 10 * time.Second, RuntimeInterruptTimeout: 3 * time.Second},
			want: 3 * time.Second,
		},
		{
			name: "explicit capped below drain",
			w:    Worker{InterruptDrainTimeout: time.Second, RuntimeInterruptTimeout: 3 * time.Second},
			want: 500 * time.Millisecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.w.runtimeInterruptTimeout(); got != tt.want {
				t.Fatalf("runtimeInterruptTimeout()=%s, want %s", got, tt.want)
			}
		})
	}
}

func TestRunClaimCancelControlInterruptTimeoutLeavesControlPendingAndReleases(t *testing.T) {
	cancelPayload, _ := json.Marshal(CancelInputControlPayload{CutoffMessageID: 1001, Reason: "user_cancel"})
	client := &fakeClient{}
	var gotInterrupt atomic.Bool
	var closeCalled atomic.Bool
	worker := &Worker{
		Namespace:             "test_ns",
		Client:                client,
		RenewInterval:         time.Hour,
		IdleTimeout:           time.Hour,
		MessagePollInterval:   5 * time.Millisecond,
		InterruptDrainTimeout: 20 * time.Millisecond,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: make(chan agentworker.ThreadOutputItem)}, nil
				},
				InterruptFunc: func(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
					gotInterrupt.Store(true)
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					return &agentworker.ActiveTurn{TurnID: "turn_1000", ConsumedMessageIDs: []string{"1000"}}
				},
				CloseFunc: func(ctx context.Context) error {
					closeCalled.Store(true)
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 9001, ThreadId: 42, MessageType: MessageTypeControlCancelInput, Payload: cancelPayload, Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if !gotInterrupt.Load() {
		t.Fatalf("interrupt was not requested")
	}
	if !closeCalled.Load() {
		t.Fatalf("runtime Close was not called")
	}
	if len(client.ackRequests) != 0 {
		t.Fatalf("ack requests=%v, want cancel control left pending for retry", client.ackRequests)
	}
	if len(client.releaseRequests) != 1 || client.releaseRequests[0].GetReason() != defaultInterruptTimeoutReason {
		t.Fatalf("release requests=%v, want timeout release", client.releaseRequests)
	}
}

func TestRunClaimCloseControlCompletesCloseWithoutRelease(t *testing.T) {
	var active atomic.Bool
	active.Store(true)
	closePayload, _ := json.Marshal(CloseThreadControlPayload{Reason: "user_close"})
	client := &fakeClient{}
	var gotInterrupt agentworker.ThreadInterruptRequest
	var postCalled atomic.Bool
	var closeCalls atomic.Int32
	worker := &Worker{
		Namespace:               "test_ns",
		Client:                  client,
		RenewInterval:           time.Hour,
		MessagePollInterval:     5 * time.Millisecond,
		InterruptDrainTimeout:   time.Second,
		RuntimeInterruptTimeout: 25 * time.Millisecond,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					postCalled.Store(true)
					return nil
				},
				InterruptFunc: func(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
					gotInterrupt = req
					active.Store(false)
					return nil
				},
				ActiveTurnFunc: func() *agentworker.ActiveTurn {
					if !active.Load() {
						return nil
					}
					return &agentworker.ActiveTurn{TurnID: "turn_1001", ConsumedMessageIDs: []string{"1001"}}
				},
				CloseFunc: func(ctx context.Context) error {
					closeCalls.Add(1)
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_CLOSING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 9002, ThreadId: 42, MessageType: MessageTypeControlCloseThread, Payload: closePayload, Status: ac.MessageStatus_PENDING},
			{MessageId: 1002, ThreadId: 42, MessageType: "text", Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if postCalled.Load() {
		t.Fatalf("ordinary pending should not be posted after close control")
	}
	if gotInterrupt.Kind != agentworker.ThreadInterruptKindCloseThread || gotInterrupt.ControlMessageID != "9002" {
		t.Fatalf("interrupt request=%+v", gotInterrupt)
	}
	if gotInterrupt.Timeout == nil || *gotInterrupt.Timeout != 25*time.Millisecond {
		t.Fatalf("interrupt timeout=%v, want 25ms", gotInterrupt.Timeout)
	}
	if got := closeCalls.Load(); got != 1 {
		t.Fatalf("runtime Close calls=%d, want 1", got)
	}
	if len(client.ackRequests) != 1 || client.ackRequests[0].GetMessageIds()[0] != 9002 {
		t.Fatalf("ack requests=%v, want close control ack", client.ackRequests)
	}
	if len(client.completeCloseRequests) != 1 || client.completeCloseRequests[0].GetControlMessageId() != 9002 || client.completeCloseRequests[0].GetReason() != "user_close" {
		t.Fatalf("complete close requests=%v", client.completeCloseRequests)
	}
	if len(client.releaseRequests) != 0 {
		t.Fatalf("release requests=%d, want 0", len(client.releaseRequests))
	}
}

func TestRunClaimCloseControlParseFailureStillCompletesClose(t *testing.T) {
	client := &fakeClient{}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_CLOSING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{
			{MessageId: 9002, ThreadId: 42, MessageType: MessageTypeControlCloseThread, Payload: []byte("{bad"), Status: ac.MessageStatus_PENDING},
		},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if len(client.ackRequests) != 1 || client.ackRequests[0].GetMessageIds()[0] != 9002 {
		t.Fatalf("ack requests=%v, want close control ack", client.ackRequests)
	}
	if len(client.completeCloseRequests) != 1 || client.completeCloseRequests[0].GetReason() != defaultCloseThreadReason {
		t.Fatalf("complete close requests=%v, want default reason", client.completeCloseRequests)
	}
	if len(client.releaseRequests) != 0 {
		t.Fatalf("release requests=%d, want 0", len(client.releaseRequests))
	}
}

func TestRPCErrorReportsBaseRespWithTransportError(t *testing.T) {
	resp := &ac.ReleaseThreadResponse{BaseResp: &base.BaseResp{StatusCode: 409, StatusMessage: "lease mismatch"}}
	err := rpcError("ReleaseThread", resp, errors.New("remote error"))
	if err == nil || !strings.Contains(err.Error(), "status_code=409") || !strings.Contains(err.Error(), "lease mismatch") || !strings.Contains(err.Error(), "remote error") {
		t.Fatalf("rpcError=%v, want status code, message and transport error", err)
	}
}

func TestNewMessageLogContextUsesProducerLogID(t *testing.T) {
	worker := &Worker{Namespace: "test_ns"}
	ctx := worker.newMessageLogContext(context.Background(), &ac.Thread{ThreadId: 42}, &ac.Message{
		MessageId: 1001,
		ThreadId:  42,
		Metadata:  map[string]string{"logid": "producer-logid", metadataKeyKEnv: "message_env"},
	})

	got, ok := ctxvalues.LogID(ctx)
	if !ok || got != "producer-logid" {
		t.Fatalf("logid=%q ok=%t, want producer-logid", got, ok)
	}
	gotKitutil, ok := kitutil.GetCtxLogID(ctx)
	if !ok || gotKitutil != "producer-logid" {
		t.Fatalf("kitutil logid=%q ok=%t, want producer-logid", gotKitutil, ok)
	}
	if gotEnv, ok := kitutil.GetCtxEnv(ctx); ok {
		t.Fatalf("env=%q ok=%t, want no restored request env on log/control context", gotEnv, ok)
	}
}

func TestNewThreadLogContextUsesThreadLogID(t *testing.T) {
	worker := &Worker{Namespace: "test_ns"}
	ctx := worker.newThreadLogContext(context.Background(), &ac.Thread{
		ThreadId: 42,
		Metadata: map[string]string{
			"logid":                     "thread-logid",
			metadataKeyBytedCtxMetaInfo: `{"persist_a":"value-a"}`,
			metadataKeyKEnv:             "thread_env",
		},
	})

	got, ok := ctxvalues.LogID(ctx)
	if !ok || got != "thread-logid" {
		t.Fatalf("logid=%q ok=%t, want thread-logid", got, ok)
	}
	gotKitutil, ok := kitutil.GetCtxLogID(ctx)
	if !ok || gotKitutil != "thread-logid" {
		t.Fatalf("kitutil logid=%q ok=%t, want thread-logid", gotKitutil, ok)
	}
	gotMeta, ok := metainfo.GetPersistentValue(ctx, "persist_a")
	if !ok || gotMeta != "value-a" {
		t.Fatalf("persistent metainfo persist_a=%q ok=%t, want value-a", gotMeta, ok)
	}
	if gotEnv, ok := kitutil.GetCtxEnv(ctx); ok {
		t.Fatalf("env=%q ok=%t, want no restored request env on log/control context", gotEnv, ok)
	}
}

func TestNewThreadLogContextGeneratesLogIDWhenMissing(t *testing.T) {
	worker := &Worker{Namespace: "test_ns"}
	ctx := worker.newThreadLogContext(context.Background(), &ac.Thread{ThreadId: 42})

	got, ok := ctxvalues.LogID(ctx)
	if !ok || got == "" {
		t.Fatalf("logid=%q ok=%t, want generated logid", got, ok)
	}
	gotKitutil, ok := kitutil.GetCtxLogID(ctx)
	if !ok || gotKitutil != got {
		t.Fatalf("kitutil logid=%q ok=%t, want %q", gotKitutil, ok, got)
	}
	if gotEnv, ok := kitutil.GetCtxEnv(ctx); ok {
		t.Fatalf("env=%q ok=%t, want no env when metadata is missing", gotEnv, ok)
	}
}

func TestNewMessageLogContextGeneratesLogIDWhenMissing(t *testing.T) {
	worker := &Worker{Namespace: "test_ns"}
	ctx := worker.newMessageLogContext(context.Background(), &ac.Thread{ThreadId: 42}, &ac.Message{
		MessageId: 1001,
		ThreadId:  42,
	})

	got, ok := ctxvalues.LogID(ctx)
	if !ok || got == "" {
		t.Fatalf("logid=%q ok=%t, want generated logid", got, ok)
	}
	gotKitutil, ok := kitutil.GetCtxLogID(ctx)
	if !ok || gotKitutil != got {
		t.Fatalf("kitutil logid=%q ok=%t, want %q", gotKitutil, ok, got)
	}
	if gotEnv, ok := kitutil.GetCtxEnv(ctx); ok {
		t.Fatalf("env=%q ok=%t, want no env when metadata is missing", gotEnv, ok)
	}
}

func TestClaimLogContextInheritsScanLogID(t *testing.T) {
	worker := &Worker{Namespace: "test_ns", Env: "boe_test_lane", ScanLimit: 10}
	scanCtx := worker.newScanLogContext(context.Background())
	scanLogID, ok := ctxvalues.LogID(scanCtx)
	if !ok || scanLogID == "" {
		t.Fatalf("scan logid=%q ok=%t, want generated scan logid", scanLogID, ok)
	}

	claimCtx := worker.newClaimLogContext(scanCtx, &ac.Thread{
		ThreadId: 42,
		Metadata: map[string]string{"logid": "thread-logid", metadataKeyKEnv: "thread_env"},
	})
	if got, ok := ctxvalues.LogID(claimCtx); !ok || got != scanLogID {
		t.Fatalf("claim ctxvalues logid=%q ok=%t, want inherited scan logid %q", got, ok, scanLogID)
	}
	if got, ok := kitutil.GetCtxLogID(claimCtx); !ok || got != scanLogID {
		t.Fatalf("claim kitutil logid=%q ok=%t, want inherited scan logid %q", got, ok, scanLogID)
	}
	if gotEnv, ok := kitutil.GetCtxEnv(claimCtx); ok {
		t.Fatalf("claim env=%q ok=%t, want no restored request env on AC RPC", gotEnv, ok)
	}
}

func TestRunLogContextUsesThreadLogID(t *testing.T) {
	worker := &Worker{Namespace: "test_ns"}
	ctx := worker.newRunLogContext(context.Background(), &claimResult{
		thread: &ac.Thread{
			ThreadId: 42,
			Metadata: map[string]string{
				"logid":                     "thread-logid",
				metadataKeyBytedCtxMetaInfo: `{"persist_a":"value-a"}`,
				metadataKeyKEnv:             "thread_env",
			},
		},
		lease: &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{{
			MessageId: 1001,
			ThreadId:  42,
			Metadata:  map[string]string{"logid": "message-logid"},
		}},
	})
	if got, ok := ctxvalues.LogID(ctx); !ok || got != "thread-logid" {
		t.Fatalf("run ctxvalues logid=%q ok=%t, want thread-logid", got, ok)
	}
	if got, ok := kitutil.GetCtxLogID(ctx); !ok || got != "thread-logid" {
		t.Fatalf("run kitutil logid=%q ok=%t, want thread-logid", got, ok)
	}
	if gotMeta, ok := metainfo.GetPersistentValue(ctx, "persist_a"); !ok || gotMeta != "value-a" {
		t.Fatalf("run persistent metainfo persist_a=%q ok=%t, want value-a", gotMeta, ok)
	}
	if gotEnv, ok := kitutil.GetCtxEnv(ctx); ok {
		t.Fatalf("run env=%q ok=%t, want no restored request env on log/control context", gotEnv, ok)
	}
}

func TestRunLogContextFallsBackToThreadLogID(t *testing.T) {
	worker := &Worker{Namespace: "test_ns"}
	ctx := worker.newRunLogContext(context.Background(), &claimResult{
		thread: &ac.Thread{
			ThreadId: 42,
			Metadata: map[string]string{"logid": "thread-logid"},
		},
		lease: &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
	})
	if got, ok := ctxvalues.LogID(ctx); !ok || got != "thread-logid" {
		t.Fatalf("run ctxvalues logid=%q ok=%t, want thread-logid", got, ok)
	}
	if got, ok := kitutil.GetCtxLogID(ctx); !ok || got != "thread-logid" {
		t.Fatalf("run kitutil logid=%q ok=%t, want thread-logid", got, ok)
	}
}

func TestPullLogContextInheritsRunLogID(t *testing.T) {
	worker := &Worker{Namespace: "test_ns", MessageLimit: 20}
	runCtx := contextWithLogID(context.Background(), "run-logid")
	pullCtx := worker.newPullLogContext(runCtx, &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"})

	if got, ok := ctxvalues.LogID(pullCtx); !ok || got != "run-logid" {
		t.Fatalf("pull ctxvalues logid=%q ok=%t, want run-logid", got, ok)
	}
	if got, ok := kitutil.GetCtxLogID(pullCtx); !ok || got != "run-logid" {
		t.Fatalf("pull kitutil logid=%q ok=%t, want run-logid", got, ok)
	}
}

func TestRunClaimRestoresMessageRequestMetaInfo(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	var gotMetaValue string
	var gotMetaOK bool
	var gotEnv string
	var gotEnvOK bool
	var gotMetadata map[string]string
	client := &fakeClient{
		ackThreadMessagesFunc: func(ctx context.Context, req *ac.AckThreadMessagesRequest) error {
			if gotEnv, ok := kitutil.GetCtxEnv(ctx); ok {
				t.Fatalf("ack env=%q ok=%t, want no restored request env on AC RPC", gotEnv, ok)
			}
			return nil
		},
		releaseThreadFunc: func(ctx context.Context, req *ac.ReleaseThreadRequest) error {
			if gotEnv, ok := kitutil.GetCtxEnv(ctx); ok {
				t.Fatalf("release env=%q ok=%t, want no restored request env on AC RPC", gotEnv, ok)
			}
			return nil
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					gotMetaValue, gotMetaOK = metainfo.GetPersistentValue(ctx, "persist_a")
					gotEnv, gotEnvOK = kitutil.GetCtxEnv(ctx)
					gotMetadata = input.Metadata
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{{
			MessageId:   1001,
			ThreadId:    42,
			MessageType: "text",
			Status:      ac.MessageStatus_PENDING,
			Metadata: map[string]string{
				metadataKeyBytedCtxMetaInfo: `{"persist_a":"value-a"}`,
				metadataKeyKEnv:             "message_env",
				"biz_key":                   "biz_value",
			},
		}},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if !gotMetaOK || gotMetaValue != "value-a" {
		t.Fatalf("persistent metainfo persist_a=%q ok=%t, want value-a", gotMetaValue, gotMetaOK)
	}
	if !gotEnvOK || gotEnv != "message_env" {
		t.Fatalf("message env=%q ok=%t, want message_env", gotEnv, gotEnvOK)
	}
	if gotMetadata["biz_key"] != "biz_value" || gotMetadata[metadataKeyBytedCtxMetaInfo] == "" {
		t.Fatalf("message metadata = %+v", gotMetadata)
	}
}

func TestRunClaimInitUsesThreadRequestContext(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
	close(items)
	client := &fakeClient{}
	thread := &ac.Thread{
		ThreadId:  42,
		Namespace: "test_ns",
		Status:    ac.ThreadStatus_RUNNING,
		Metadata: map[string]string{
			"logid":                     "thread-logid",
			metadataKeyBytedCtxMetaInfo: `{"persist_a":"value-a"}`,
			metadataKeyKEnv:             "thread_env",
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			gotLogID, ok := kitutil.GetCtxLogID(ctx)
			if !ok || gotLogID != "thread-logid" {
				t.Fatalf("factory logid=%q ok=%t, want thread-logid", gotLogID, ok)
			}
			gotMeta, ok := metainfo.GetPersistentValue(ctx, "persist_a")
			if !ok || gotMeta != "value-a" {
				t.Fatalf("factory persistent metainfo persist_a=%q ok=%t, want value-a", gotMeta, ok)
			}
			gotEnv, ok := kitutil.GetCtxEnv(ctx)
			if !ok || gotEnv != "thread_env" {
				t.Fatalf("factory env=%q ok=%t, want thread_env", gotEnv, ok)
			}
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					gotLogID, ok := kitutil.GetCtxLogID(ctx)
					if !ok || gotLogID != "thread-logid" {
						t.Fatalf("init logid=%q ok=%t, want thread-logid", gotLogID, ok)
					}
					gotMeta, ok := metainfo.GetPersistentValue(ctx, "persist_a")
					if !ok || gotMeta != "value-a" {
						t.Fatalf("init persistent metainfo persist_a=%q ok=%t, want value-a", gotMeta, ok)
					}
					gotEnv, ok := kitutil.GetCtxEnv(ctx)
					if !ok || gotEnv != "thread_env" {
						t.Fatalf("init env=%q ok=%t, want thread_env", gotEnv, ok)
					}
					return &agentworker.ThreadOutput{Items: items}, nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(worker.newThreadLogContext(context.Background(), thread), context.Background(), &claimResult{
		thread: thread,
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
}

func TestRunClaimIgnoresMalformedRequestMetaInfo(t *testing.T) {
	items := make(chan agentworker.ThreadOutputItem, 1)
	client := &fakeClient{}
	var gotMetaOK bool
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		RenewInterval: time.Hour,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					_, gotMetaOK = metainfo.GetPersistentValue(ctx, "persist_a")
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
					return nil
				},
			}, nil
		}),
	}

	err := worker.runClaim(context.Background(), context.Background(), &claimResult{
		thread: &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
		lease:  &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
		pendingMessages: []*ac.Message{{
			MessageId:   1001,
			ThreadId:    42,
			MessageType: "text",
			Status:      ac.MessageStatus_PENDING,
			Metadata:    map[string]string{metadataKeyBytedCtxMetaInfo: "{bad"},
		}},
	})
	if err != nil {
		t.Fatalf("runClaim error: %v", err)
	}
	if gotMetaOK {
		t.Fatalf("malformed request metainfo should not be restored")
	}
}

func TestScanRunnableThreadsSetsEnv(t *testing.T) {
	client := &fakeClient{}
	worker := &Worker{
		Namespace: "test_ns",
		Env:       "boe_test_lane",
		Client:    client,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{}, nil
		}),
	}
	worker.normalize()

	_, err := worker.scanRunnableThreads(context.Background())
	if err != nil {
		t.Fatalf("scanRunnableThreads error: %v", err)
	}

	if len(client.scanRequests) != 1 {
		t.Fatalf("scan requests=%d, want 1", len(client.scanRequests))
	}
	if got := client.scanRequests[0].GetEnv(); got != "boe_test_lane" {
		t.Fatalf("scan env=%q, want boe_test_lane", got)
	}
}

func TestRunUsesThreadRequestContextForInit(t *testing.T) {
	thread := &ac.Thread{
		ThreadId:  42,
		Namespace: "test_ns",
		Status:    ac.ThreadStatus_READY,
		Metadata: map[string]string{
			"logid":                     "activation-logid",
			metadataKeyBytedCtxMetaInfo: `{"persist_a":"value-a"}`,
			metadataKeyKEnv:             "activation_env",
		},
	}
	released := make(chan struct{})
	var scanned atomic.Bool
	var releasedClosed atomic.Bool
	client := &fakeClient{
		scanRunnableThreadsFunc: func(req *ac.ScanRunnableThreadsRequest) []*ac.Thread {
			if scanned.Swap(true) {
				return nil
			}
			return []*ac.Thread{thread}
		},
		claimThreadFunc: func(ctx context.Context, req *ac.ClaimThreadRequest) *ac.ClaimThreadResponse {
			if gotEnv, ok := kitutil.GetCtxEnv(ctx); ok {
				t.Fatalf("claim env=%q ok=%t, want no restored request env on AC RPC", gotEnv, ok)
			}
			claimed := *thread
			claimed.Status = ac.ThreadStatus_RUNNING
			return &ac.ClaimThreadResponse{
				BaseResp: okBaseResp(),
				Thread:   &claimed,
				Lease:    &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
			}
		},
		releaseThreadFunc: func(ctx context.Context, req *ac.ReleaseThreadRequest) error {
			if gotEnv, ok := kitutil.GetCtxEnv(ctx); ok {
				t.Fatalf("release env=%q ok=%t, want no restored request env on AC RPC", gotEnv, ok)
			}
			if releasedClosed.CompareAndSwap(false, true) {
				close(released)
			}
			return nil
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		Concurrency:   1,
		ScanInterval:  time.Hour,
		RenewInterval: time.Hour,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			gotLogID, ok := kitutil.GetCtxLogID(ctx)
			if !ok || gotLogID != "activation-logid" {
				t.Fatalf("factory logid=%q ok=%t, want activation-logid", gotLogID, ok)
			}
			gotMeta, ok := metainfo.GetPersistentValue(ctx, "persist_a")
			if !ok || gotMeta != "value-a" {
				t.Fatalf("factory persistent metainfo persist_a=%q ok=%t, want value-a", gotMeta, ok)
			}
			gotEnv, ok := kitutil.GetCtxEnv(ctx)
			if !ok || gotEnv != "activation_env" {
				t.Fatalf("factory env=%q ok=%t, want activation_env", gotEnv, ok)
			}
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					gotLogID, ok := kitutil.GetCtxLogID(ctx)
					if !ok || gotLogID != "activation-logid" {
						t.Fatalf("init logid=%q ok=%t, want activation-logid", gotLogID, ok)
					}
					gotMeta, ok := metainfo.GetPersistentValue(ctx, "persist_a")
					if !ok || gotMeta != "value-a" {
						t.Fatalf("init persistent metainfo persist_a=%q ok=%t, want value-a", gotMeta, ok)
					}
					gotEnv, ok := kitutil.GetCtxEnv(ctx)
					if !ok || gotEnv != "activation_env" {
						t.Fatalf("init env=%q ok=%t, want activation_env", gotEnv, ok)
					}
					items := make(chan agentworker.ThreadOutputItem, 1)
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
					close(items)
					return &agentworker.ThreadOutput{Items: items}, nil
				},
			}, nil
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	select {
	case <-released:
		cancel()
	case <-time.After(time.Second):
		t.Fatalf("thread was not released")
	}
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("Run returned nil, want context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatalf("worker did not stop")
	}
}

func TestRunClaimsWithScanLogIDAndInitializesWithThreadLogID(t *testing.T) {
	thread := &ac.Thread{
		ThreadId:  42,
		Namespace: "test_ns",
		Status:    ac.ThreadStatus_READY,
		Metadata: map[string]string{
			"logid":                     "thread-logid",
			metadataKeyBytedCtxMetaInfo: `{"persist_a":"value-a"}`,
			metadataKeyKEnv:             "thread_env",
		},
	}
	firstPendingMessage := &ac.Message{
		MessageId:   1001,
		ThreadId:    42,
		MessageType: "text",
		Status:      ac.MessageStatus_PENDING,
		Metadata:    map[string]string{"logid": "message-logid"},
	}
	released := make(chan struct{})
	var scanned atomic.Bool
	var claimScanLogID string
	var releasedClosed atomic.Bool
	client := &fakeClient{
		scanRunnableThreadsFunc: func(req *ac.ScanRunnableThreadsRequest) []*ac.Thread {
			if scanned.Swap(true) {
				return nil
			}
			return []*ac.Thread{thread}
		},
		claimThreadFunc: func(ctx context.Context, req *ac.ClaimThreadRequest) *ac.ClaimThreadResponse {
			gotLogID, ok := kitutil.GetCtxLogID(ctx)
			if !ok || gotLogID == "" {
				t.Fatalf("claim logid=%q ok=%t, want inherited scan logid", gotLogID, ok)
			}
			if gotLogID == "thread-logid" || gotLogID == "message-logid" {
				t.Fatalf("claim logid=%q, want scan logid instead of business logid", gotLogID)
			}
			claimScanLogID = gotLogID
			if gotEnv, ok := kitutil.GetCtxEnv(ctx); ok {
				t.Fatalf("claim env=%q ok=%t, want no restored request env on AC RPC", gotEnv, ok)
			}
			claimed := *thread
			claimed.Status = ac.ThreadStatus_RUNNING
			return &ac.ClaimThreadResponse{
				BaseResp:        okBaseResp(),
				Thread:          &claimed,
				Lease:           &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
				PendingMessages: []*ac.Message{firstPendingMessage},
			}
		},
		releaseThreadFunc: func(ctx context.Context, req *ac.ReleaseThreadRequest) error {
			if gotEnv, ok := kitutil.GetCtxEnv(ctx); ok {
				t.Fatalf("release env=%q ok=%t, want no restored request env on AC RPC", gotEnv, ok)
			}
			if releasedClosed.CompareAndSwap(false, true) {
				close(released)
			}
			return nil
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		Concurrency:   1,
		ScanInterval:  time.Hour,
		RenewInterval: time.Hour,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			gotLogID, ok := kitutil.GetCtxLogID(ctx)
			if !ok || gotLogID != "thread-logid" {
				t.Fatalf("factory logid=%q ok=%t, want thread logid", gotLogID, ok)
			}
			if gotLogID == claimScanLogID {
				t.Fatalf("factory logid=%q, want thread logid instead of scan logid", gotLogID)
			}
			gotMeta, ok := metainfo.GetPersistentValue(ctx, "persist_a")
			if !ok || gotMeta != "value-a" {
				t.Fatalf("factory persistent metainfo persist_a=%q ok=%t, want value-a", gotMeta, ok)
			}
			gotEnv, ok := kitutil.GetCtxEnv(ctx)
			if !ok || gotEnv != "thread_env" {
				t.Fatalf("factory env=%q ok=%t, want thread_env", gotEnv, ok)
			}
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					gotLogID, ok := kitutil.GetCtxLogID(ctx)
					if !ok || gotLogID != "thread-logid" {
						t.Fatalf("init logid=%q ok=%t, want thread logid", gotLogID, ok)
					}
					items := make(chan agentworker.ThreadOutputItem, 1)
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
					close(items)
					return &agentworker.ThreadOutput{Items: items}, nil
				},
			}, nil
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	select {
	case <-released:
		cancel()
	case <-time.After(time.Second):
		t.Fatalf("thread was not released")
	}
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("Run returned nil, want context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatalf("worker did not stop")
	}
}

func TestRunClaimsBeforeNextScan(t *testing.T) {
	var scanCalls int32
	var claimCalls int32
	var claimStartedClosed int32
	claimStarted := make(chan struct{})
	allowClaim := make(chan struct{})
	client := &fakeClient{
		scanRunnableThreadsFunc: func(req *ac.ScanRunnableThreadsRequest) []*ac.Thread {
			atomic.AddInt32(&scanCalls, 1)
			if atomic.LoadInt32(&claimCalls) == 0 {
				return []*ac.Thread{{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_READY}}
			}
			return nil
		},
		claimThreadFunc: func(ctx context.Context, req *ac.ClaimThreadRequest) *ac.ClaimThreadResponse {
			if atomic.CompareAndSwapInt32(&claimStartedClosed, 0, 1) {
				close(claimStarted)
			}
			<-allowClaim
			atomic.AddInt32(&claimCalls, 1)
			return &ac.ClaimThreadResponse{
				BaseResp: okBaseResp(),
				Thread:   &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
				Lease:    &ac.Lease{ThreadId: 42, LeaseToken: "lease-token"},
				PendingMessages: []*ac.Message{
					{MessageId: 1001, ThreadId: 42, Status: ac.MessageStatus_PENDING},
				},
			}
		},
	}
	var factoryCalls int32
	var initCalls int32
	var enqueueCalls int32
	items := make(chan agentworker.ThreadOutputItem, 1)
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		Concurrency:   2,
		ScanInterval:  10 * time.Millisecond,
		RenewInterval: time.Hour,
		IdleTimeout:   20 * time.Millisecond,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			atomic.AddInt32(&factoryCalls, 1)
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					atomic.AddInt32(&initCalls, 1)
					return &agentworker.ThreadOutput{Items: items}, nil
				},
				PostMessageFunc: func(ctx context.Context, input *agentworker.Message) error {
					atomic.AddInt32(&enqueueCalls, 1)
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
					return nil
				},
			}, nil
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	select {
	case <-claimStarted:
	case <-time.After(time.Second):
		t.Fatalf("claim was not started")
	}

	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&scanCalls); got != 1 {
		t.Fatalf("scan calls before claim finished=%d, want 1", got)
	}

	close(allowClaim)
	deadline := time.After(time.Second)
	for atomic.LoadInt32(&enqueueCalls) == 0 {
		select {
		case <-deadline:
			t.Fatalf("agent thread did not enqueue message")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("Run returned nil, want context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatalf("worker did not stop")
	}
	if got := atomic.LoadInt32(&claimCalls); got != 1 {
		t.Fatalf("claim calls=%d, want 1", got)
	}
	if got := atomic.LoadInt32(&factoryCalls); got != 1 {
		t.Fatalf("factory calls=%d, want 1", got)
	}
	if got := atomic.LoadInt32(&initCalls); got != 1 {
		t.Fatalf("init calls=%d, want 1", got)
	}
	if got := atomic.LoadInt32(&enqueueCalls); got != 1 {
		t.Fatalf("enqueue calls=%d, want 1", got)
	}
}

func TestRunCoolsDownAfterNonEmptyScanWithFastClaimMiss(t *testing.T) {
	var scanCalls int32
	var claimCalls int32
	firstScan := make(chan struct{})
	thread := &ac.Thread{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_READY}
	client := &fakeClient{
		scanRunnableThreadsFunc: func(req *ac.ScanRunnableThreadsRequest) []*ac.Thread {
			if atomic.AddInt32(&scanCalls, 1) == 1 {
				close(firstScan)
			}
			return []*ac.Thread{thread}
		},
		claimThreadFunc: func(ctx context.Context, req *ac.ClaimThreadRequest) *ac.ClaimThreadResponse {
			atomic.AddInt32(&claimCalls, 1)
			return &ac.ClaimThreadResponse{
				BaseResp: okBaseResp(),
				Thread:   &ac.Thread{ThreadId: req.GetThreadId(), Namespace: "test_ns", Status: ac.ThreadStatus_READY},
			}
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		Concurrency:   1,
		ScanInterval:  time.Hour,
		RenewInterval: time.Hour,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{}, nil
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	select {
	case <-firstScan:
	case <-time.After(time.Second):
		t.Fatalf("first scan did not run")
	}
	deadline := time.After(time.Second)
	for atomic.LoadInt32(&claimCalls) == 0 {
		select {
		case <-deadline:
			t.Fatalf("claim did not run")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt32(&scanCalls); got != 1 {
		t.Fatalf("scan calls before ScanInterval elapsed=%d, want 1", got)
	}

	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("Run returned nil, want context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatalf("worker did not stop")
	}
}

func TestRunEmptyScanStillSleepsAndCancelStopsWorker(t *testing.T) {
	var scanCalls int32
	firstScan := make(chan struct{})
	client := &fakeClient{
		scanRunnableThreadsFunc: func(req *ac.ScanRunnableThreadsRequest) []*ac.Thread {
			if atomic.AddInt32(&scanCalls, 1) == 1 {
				close(firstScan)
			}
			return nil
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		Concurrency:   1,
		ScanInterval:  time.Hour,
		RenewInterval: time.Hour,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{}, nil
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	select {
	case <-firstScan:
	case <-time.After(time.Second):
		cancel()
		t.Fatalf("first scan did not run")
	}
	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt32(&scanCalls); got != 1 {
		cancel()
		t.Fatalf("empty scan calls before ScanInterval elapsed=%d, want 1", got)
	}

	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("Run returned nil, want context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatalf("worker did not stop after canceling ScanInterval sleep")
	}
}

func TestRunNextScanAfterCooldownDoesNotWaitForActiveClaim(t *testing.T) {
	var scanCalls int32
	var secondScanClosed int32
	secondScan := make(chan struct{})
	initStarted := make(chan struct{})
	allowInit := make(chan struct{})
	client := &fakeClient{
		scanRunnableThreadsFunc: func(req *ac.ScanRunnableThreadsRequest) []*ac.Thread {
			switch atomic.AddInt32(&scanCalls, 1) {
			case 1:
				return []*ac.Thread{{ThreadId: 42, Namespace: "test_ns", Status: ac.ThreadStatus_READY}}
			case 2:
				if atomic.CompareAndSwapInt32(&secondScanClosed, 0, 1) {
					close(secondScan)
				}
			}
			return nil
		},
		claimThreadFunc: func(ctx context.Context, req *ac.ClaimThreadRequest) *ac.ClaimThreadResponse {
			return &ac.ClaimThreadResponse{
				BaseResp: okBaseResp(),
				Thread:   &ac.Thread{ThreadId: req.GetThreadId(), Namespace: "test_ns", Status: ac.ThreadStatus_RUNNING},
				Lease:    &ac.Lease{ThreadId: req.GetThreadId(), LeaseToken: "lease-token"},
			}
		},
	}
	worker := &Worker{
		Namespace:     "test_ns",
		Client:        client,
		Concurrency:   2,
		ScanInterval:  30 * time.Millisecond,
		RenewInterval: time.Hour,
		AgentThreadFactory: AgentThreadFactory(func(ctx context.Context, threadInfo *ac.Thread) (agentworker.AgentThread, error) {
			return &testAgentThreadRuntime{
				InitFunc: func(ctx context.Context) (*agentworker.ThreadOutput, error) {
					close(initStarted)
					<-allowInit
					items := make(chan agentworker.ThreadOutputItem, 1)
					items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "done"}}
					close(items)
					return &agentworker.ThreadOutput{Items: items}, nil
				},
			}, nil
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	select {
	case <-initStarted:
	case <-time.After(time.Second):
		close(allowInit)
		t.Fatalf("claim processing did not start")
	}
	select {
	case <-secondScan:
	case <-time.After(time.Second):
		close(allowInit)
		t.Fatalf("second scan did not run while claim processing was active")
	}

	close(allowInit)
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("Run returned nil, want context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatalf("worker did not stop")
	}
}

func TestMessagePreviewTruncatesPayload(t *testing.T) {
	payload := strings.Repeat("x", logMessagePayloadPreviewBytes+10)
	preview := messagePreview(&ac.Message{
		MessageId:   1001,
		ThreadId:    42,
		MessageType: "text",
		Payload:     []byte(payload),
		Metadata:    map[string]string{"logid": "producer-logid"},
	})

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(preview), &decoded); err != nil {
		t.Fatalf("preview is not json: %v preview=%s", err, preview)
	}
	if got := decoded["payload_preview"].(string); len(got) != logMessagePayloadPreviewBytes {
		t.Fatalf("payload preview len=%d, want %d", len(got), logMessagePayloadPreviewBytes)
	}
	if got := decoded["payload_truncated"].(bool); !got {
		t.Fatalf("payload_truncated=%t, want true", got)
	}
	if got := int(decoded["payload_bytes"].(float64)); got != len(payload) {
		t.Fatalf("payload_bytes=%d, want %d", got, len(payload))
	}
}

func TestMessagePreviewHandlesNilSenderAndMetadata(t *testing.T) {
	preview := messagePreview(&ac.Message{MessageId: 1001, ThreadId: 42})

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(preview), &decoded); err != nil {
		t.Fatalf("preview is not json: %v preview=%s", err, preview)
	}
	if got := decoded["sender_id"].(string); got != "" {
		t.Fatalf("sender_id=%q, want empty", got)
	}
}

var _ acsvc.Client = (*fakeClient)(nil)

type fakeClient struct {
	appendEventsRequests          []*ac.AppendEventsRequest
	ackRequests                   []*ac.AckThreadMessagesRequest
	releaseRequests               []*ac.ReleaseThreadRequest
	completeCloseRequests         []*ac.CompleteCloseThreadRequest
	pullRequests                  []*ac.PullPendingMessagesRequest
	scanRequests                  []*ac.ScanRunnableThreadsRequest
	pullPendingMessagesFunc       func(req *ac.PullPendingMessagesRequest) []*ac.Message
	pullPendingMessagesResultFunc func(req *ac.PullPendingMessagesRequest) ([]*ac.Message, error)
	scanRunnableThreadsFunc       func(req *ac.ScanRunnableThreadsRequest) []*ac.Thread
	claimThreadFunc               func(ctx context.Context, req *ac.ClaimThreadRequest) *ac.ClaimThreadResponse
	releaseThreadFunc             func(ctx context.Context, req *ac.ReleaseThreadRequest) error
	ackThreadMessagesFunc         func(ctx context.Context, req *ac.AckThreadMessagesRequest) error
	appendEventsFunc              func(ctx context.Context, req *ac.AppendEventsRequest) error
}

func okBaseResp() *base.BaseResp {
	return &base.BaseResp{StatusCode: 0, StatusMessage: "OK"}
}

func (f *fakeClient) RegisterAgentNamespace(ctx context.Context, req *ac.RegisterAgentNamespaceRequest, callOptions ...callopt.Option) (*ac.RegisterAgentNamespaceResponse, error) {
	return &ac.RegisterAgentNamespaceResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) CreateThread(ctx context.Context, req *ac.CreateThreadRequest, callOptions ...callopt.Option) (*ac.CreateThreadResponse, error) {
	return &ac.CreateThreadResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) GetThread(ctx context.Context, req *ac.GetThreadRequest, callOptions ...callopt.Option) (*ac.GetThreadResponse, error) {
	return &ac.GetThreadResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) ListSessionThreads(ctx context.Context, req *ac.ListSessionThreadsRequest, callOptions ...callopt.Option) (*ac.ListSessionThreadsResponse, error) {
	return &ac.ListSessionThreadsResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) ListSessionEvents(ctx context.Context, req *ac.ListSessionEventsRequest, callOptions ...callopt.Option) (*ac.ListSessionEventsResponse, error) {
	return &ac.ListSessionEventsResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) ListUserThreads(ctx context.Context, req *ac.ListUserThreadsRequest, callOptions ...callopt.Option) (*ac.ListUserThreadsResponse, error) {
	return &ac.ListUserThreadsResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) ScanRunnableThreads(ctx context.Context, req *ac.ScanRunnableThreadsRequest, callOptions ...callopt.Option) (*ac.ScanRunnableThreadsResponse, error) {
	f.scanRequests = append(f.scanRequests, req)
	threads := []*ac.Thread(nil)
	if f.scanRunnableThreadsFunc != nil {
		threads = f.scanRunnableThreadsFunc(req)
	}
	return &ac.ScanRunnableThreadsResponse{BaseResp: okBaseResp(), Threads: threads}, nil
}

func (f *fakeClient) SendMessage(ctx context.Context, req *ac.SendMessageRequest, callOptions ...callopt.Option) (*ac.SendMessageResponse, error) {
	return &ac.SendMessageResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) CancelInput(ctx context.Context, req *ac.CancelInputRequest, callOptions ...callopt.Option) (*ac.CancelInputResponse, error) {
	return &ac.CancelInputResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) ClaimThread(ctx context.Context, req *ac.ClaimThreadRequest, callOptions ...callopt.Option) (*ac.ClaimThreadResponse, error) {
	if f.claimThreadFunc != nil {
		return f.claimThreadFunc(ctx, req), nil
	}
	return &ac.ClaimThreadResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) RenewThreadLease(ctx context.Context, req *ac.RenewThreadLeaseRequest, callOptions ...callopt.Option) (*ac.RenewThreadLeaseResponse, error) {
	return &ac.RenewThreadLeaseResponse{BaseResp: okBaseResp(), Lease: &ac.Lease{ThreadId: req.GetThreadId(), LeaseToken: req.GetLeaseToken()}}, nil
}

func (f *fakeClient) PullPendingMessages(ctx context.Context, req *ac.PullPendingMessagesRequest, callOptions ...callopt.Option) (*ac.PullPendingMessagesResponse, error) {
	f.pullRequests = append(f.pullRequests, req)
	messages := []*ac.Message(nil)
	if f.pullPendingMessagesResultFunc != nil {
		var err error
		messages, err = f.pullPendingMessagesResultFunc(req)
		if err != nil {
			return nil, err
		}
		return &ac.PullPendingMessagesResponse{BaseResp: okBaseResp(), PendingMessages: messages}, nil
	}
	if f.pullPendingMessagesFunc != nil {
		messages = f.pullPendingMessagesFunc(req)
	}
	return &ac.PullPendingMessagesResponse{BaseResp: okBaseResp(), PendingMessages: messages}, nil
}

func (f *fakeClient) ListThreadMessages(ctx context.Context, req *ac.ListThreadMessagesRequest, callOptions ...callopt.Option) (*ac.ListThreadMessagesResponse, error) {
	return &ac.ListThreadMessagesResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) BatchGetMessages(ctx context.Context, req *ac.BatchGetMessagesRequest, callOptions ...callopt.Option) (*ac.BatchGetMessagesResponse, error) {
	return &ac.BatchGetMessagesResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) AckThreadMessages(ctx context.Context, req *ac.AckThreadMessagesRequest, callOptions ...callopt.Option) (*ac.AckThreadMessagesResponse, error) {
	f.ackRequests = append(f.ackRequests, req)
	if f.ackThreadMessagesFunc != nil {
		if err := f.ackThreadMessagesFunc(ctx, req); err != nil {
			return nil, err
		}
	}
	return &ac.AckThreadMessagesResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) ReleaseThread(ctx context.Context, req *ac.ReleaseThreadRequest, callOptions ...callopt.Option) (*ac.ReleaseThreadResponse, error) {
	f.releaseRequests = append(f.releaseRequests, req)
	if f.releaseThreadFunc != nil {
		if err := f.releaseThreadFunc(ctx, req); err != nil {
			return nil, err
		}
	}
	return &ac.ReleaseThreadResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) ResumeFromBlock(ctx context.Context, req *ac.ResumeFromBlockRequest, callOptions ...callopt.Option) (*ac.ResumeFromBlockResponse, error) {
	return &ac.ResumeFromBlockResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) CloseThread(ctx context.Context, req *ac.CloseThreadRequest, callOptions ...callopt.Option) (*ac.CloseThreadResponse, error) {
	return &ac.CloseThreadResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) CompleteCloseThread(ctx context.Context, req *ac.CompleteCloseThreadRequest, callOptions ...callopt.Option) (*ac.CompleteCloseThreadResponse, error) {
	f.completeCloseRequests = append(f.completeCloseRequests, req)
	return &ac.CompleteCloseThreadResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) AppendEvents(ctx context.Context, req *ac.AppendEventsRequest, callOptions ...callopt.Option) (*ac.AppendEventsResponse, error) {
	f.appendEventsRequests = append(f.appendEventsRequests, req)
	if f.appendEventsFunc != nil {
		if err := f.appendEventsFunc(ctx, req); err != nil {
			return nil, err
		}
	}
	return &ac.AppendEventsResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) ListEvents(ctx context.Context, req *ac.ListEventsRequest, callOptions ...callopt.Option) (*ac.ListEventsResponse, error) {
	return &ac.ListEventsResponse{BaseResp: okBaseResp()}, nil
}

func (f *fakeClient) ListTurnEvents(ctx context.Context, req *ac.ListTurnEventsRequest, callOptions ...callopt.Option) (*ac.ListTurnEventsResponse, error) {
	return &ac.ListTurnEventsResponse{BaseResp: okBaseResp()}, nil
}
