package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"sync"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/middleware/planmode"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

type localThreadRuntime struct {
	state            *inprocess.ThreadState
	thread           *agentthread.DeepAgentThread
	baseRunnerConfig *agentthread.TurnRunnerConfig
	planRunnerConfig func() *agentthread.TurnRunnerConfig
	agentEvCh        chan agentthread.Event
	items            chan agentworker.ThreadOutputItem

	mu     sync.Mutex
	closed bool
	cancel context.CancelFunc
	active *agentworker.ActiveTurn
}

func (r *localThreadRuntime) Init(ctx context.Context) (*agentworker.ThreadOutput, error) {
	if err := r.thread.Init(ctx); err != nil {
		return nil, err
	}
	bridgeCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()
	go r.forwardAgentEvents(bridgeCtx)
	return &agentworker.ThreadOutput{Items: r.items}, nil
}

func (r *localThreadRuntime) PostMessage(ctx context.Context, msg *agentworker.Message) (*agentworker.PostMessageResult, error) {
	if msg == nil {
		return nil, inprocess.ErrInvalidMessage
	}
	if msg.Type != agentworker.MessageTypeText {
		return nil, fmt.Errorf("unsupported message type: %s", msg.Type)
	}
	if msg.Metadata != nil && msg.Metadata["message_type"] == localMessageTypeResume {
		return r.resumeTurn(ctx, msg)
	}
	content := string(msg.Payload)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("message content is required")
	}
	modelContent := content
	if msg.Sender != nil && msg.Sender.Type == agentworker.SenderTypeAgent {
		ref := msg.Metadata["from_thread_ref"]
		if strings.TrimSpace(ref) == "" {
			ref = msg.Sender.ID
		}
		modelContent = fmt.Sprintf("任务线程 %s 发来消息：\n\n%s", ref, content)
	}
	userInput := schema.UserMessage(modelContent)
	attachIncomingMessage(userInput, localIncomingMessage{
		MessageID: msg.ID,
		Content:   content,
		Sender:    senderFromInprocess(msg.Sender),
		Metadata:  maps.Clone(msg.Metadata),
	})
	var submitOpts []agentthread.SubmitInputOption
	submitOpts = append(submitOpts, agentthread.WithTurnRunnerConfigResolver(r.turnRunnerConfigResolver(msg)))
	submitOpts = append(submitOpts, agentthread.WithSubmitInputAcceptedHook(func(result agentthread.SubmitInputResult) {
		r.setActiveTurn(result.TurnID, msg.ID)
	}))
	result, err := r.thread.SubmitInput(ctx, userInput, submitOpts...)
	if err != nil {
		return nil, err
	}
	return &agentworker.PostMessageResult{TurnID: result.TurnID}, nil
}

func (r *localThreadRuntime) Interrupt(ctx context.Context, req agentworker.ThreadInterruptRequest) error {
	_ = ctx
	if r.thread.Interrupt(agentthread.InterruptOptions{Metadata: map[string]string{
		"kind":               string(req.Kind),
		"reason":             req.Reason,
		"control_message_id": req.ControlMessageID,
		"cutoff_message_id":  req.CutoffMessageID,
	}, Timeout: req.Timeout}) {
		return nil
	}
	if r.ActiveTurn() == nil {
		return nil
	}
	return fmt.Errorf("interrupt active turn failed: kind=%s control_message_id=%s", req.Kind, req.ControlMessageID)
}

func (r *localThreadRuntime) resumeTurn(ctx context.Context, msg *agentworker.Message) (*agentworker.PostMessageResult, error) {
	var payload localResumePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal resume payload: %w", err)
	}
	if payload.TurnID == "" || payload.CheckpointID == "" || payload.InterruptID == "" {
		return nil, fmt.Errorf("resume payload requires turn_id, checkpoint_id and interrupt_id")
	}
	resumeData := map[string]any{}
	switch {
	case payload.Approval != nil:
		resumeData[payload.InterruptID] = &deeptools.ApprovalResult{
			Approved:         payload.Approval.Approved,
			DisapproveReason: optionalReason(payload.Approval),
		}
	case payload.RequestUserInput != nil:
		resumeData[payload.InterruptID] = payload.RequestUserInput
	default:
		return nil, fmt.Errorf("resume payload requires approval or request_user_input")
	}
	if r.ActiveTurn() != nil {
		return nil, agentthread.ErrThreadRunning
	}
	resumeOpts := agentthread.ResumeTurnOptions{
		CheckpointID:       payload.CheckpointID,
		ResumeInterruptIDs: []string{payload.InterruptID},
		ResumeData:         resumeData,
		TurnRunnerConfig:   r.turnRunnerConfigResolver(msg),
	}
	r.setActiveTurn(payload.TurnID, msg.ID)
	if _, err := r.thread.ResumeTurn(ctx, payload.TurnID, resumeOpts); err != nil {
		r.clearActiveTurn(payload.TurnID)
		return nil, err
	}
	return &agentworker.PostMessageResult{TurnID: payload.TurnID}, nil
}

func (r *localThreadRuntime) turnRunnerConfigResolver(msg *agentworker.Message) agentthread.TurnRunnerConfigResolver {
	return func(context.Context, agentthread.TurnRunnerConfigRequest) (*agentthread.TurnRunnerConfig, error) {
		return r.turnRunnerConfigForMessage(msg), nil
	}
}

func (r *localThreadRuntime) turnRunnerConfigForMessage(msg *agentworker.Message) *agentthread.TurnRunnerConfig {
	if msg != nil && msg.Metadata != nil && msg.Metadata[localTurnModeKey] == localTurnModePlan {
		return r.planTurnConfig()
	}
	if r == nil || r.baseRunnerConfig == nil {
		return nil
	}
	return r.baseRunnerConfig.Clone()
}

func (r *localThreadRuntime) planTurnConfig() *agentthread.TurnRunnerConfig {
	if r != nil && r.planRunnerConfig != nil {
		return r.planRunnerConfig()
	}
	if r == nil {
		return nil
	}
	return r.baseRunnerConfig.Clone()
}

func (r *localThreadRuntime) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (r *localThreadRuntime) ActiveTurn() *agentworker.ActiveTurn {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return nil
	}
	out := *r.active
	if r.active.ConsumedMessageIDs != nil {
		out.ConsumedMessageIDs = append([]string(nil), r.active.ConsumedMessageIDs...)
	}
	return &out
}

func (r *localThreadRuntime) forwardAgentEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-r.agentEvCh:
			out, err := r.convertEvent(ev)
			if err == nil && out != nil {
				if !r.emitItem(ctx, agentworker.ThreadOutputItem{Event: out}) {
					return
				}
			}
			if ev.Type == agentthread.EventApproveRequested {
				r.clearActiveTurn(ev.TurnID)
				r.emitBlockFinish(ctx, ev)
				return
			}
			if ev.Type == agentthread.EventInterrupted {
				if r.emitPlanInputBlockFinish(ctx, ev) {
					r.clearActiveTurn(ev.TurnID)
					return
				}
			}
			if ev.Type == agentthread.EventTurnEnd {
				r.clearActiveTurn(ev.TurnID)
			}
			if ev.Type == agentthread.EventError {
				r.clearActiveTurn(ev.TurnID)
				r.emitErrorFinish(ctx, ev)
				return
			}
		}
	}
}

func (r *localThreadRuntime) setActiveTurn(turnID, messageID string) {
	if strings.TrimSpace(turnID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil || r.active.TurnID != turnID {
		r.active = &agentworker.ActiveTurn{TurnID: turnID}
	}
	if strings.TrimSpace(messageID) != "" {
		r.active.ConsumedMessageIDs = append(r.active.ConsumedMessageIDs, messageID)
	}
}

func (r *localThreadRuntime) clearActiveTurn(turnID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return
	}
	if turnID == "" || r.active.TurnID == turnID {
		r.active = nil
	}
}

func (r *localThreadRuntime) convertEvent(ev agentthread.Event) (*agentworker.Event, error) {
	policy := localEventDeliveryPolicyForAgentEvent(ev.Type)
	if !policy.keep {
		return nil, nil
	}
	payload, err := encodeAgentEventPayload(ev)
	if err != nil {
		return nil, err
	}
	eventID := ev.ID
	if eventID == "" {
		eventID = newEventID()
	}
	event := &agentworker.Event{
		ID:       eventID,
		ThreadID: r.state.ID,
		TurnID:   ev.TurnID,
		Type:     agentworker.EventType(ev.Type),
		Payload:  payload,
		Metadata: map[string]string{
			"agent_name":  ev.Loc.AgentName,
			"agent_depth": fmt.Sprintf("%d", ev.Loc.AgentDepth),
		},
		TS: ev.TS,
	}
	applyLocalEventDeliveryPolicy(event, policy)
	return event, nil
}

type localEventDeliveryPolicy struct {
	keep    bool
	persist *bool
	fanout  *bool
}

func localEventDeliveryPolicyForAgentEvent(eventType agentthread.EventType) localEventDeliveryPolicy {
	switch eventType {
	case agentthread.EventLLMRequesting,
		agentthread.EventLLMToken,
		agentthread.EventToolStart,
		agentthread.EventToolCallOutputChunk,
		agentthread.EventPlanUpdated,
		agentthread.EventContextCompactStarted,
		agentthread.EventContextCompacted:
		persist := false
		fanout := true
		return localEventDeliveryPolicy{keep: true, persist: &persist, fanout: &fanout}
	default:
		return localEventDeliveryPolicy{keep: true}
	}
}

func applyLocalEventDeliveryPolicy(event *agentworker.Event, policy localEventDeliveryPolicy) {
	if event == nil {
		return
	}
	if policy.persist != nil {
		event.PersistToEventLog = policy.persist
	}
	if policy.fanout != nil {
		event.FanoutToSession = policy.fanout
	}
}

func (r *localThreadRuntime) emitBlockFinish(ctx context.Context, ev agentthread.Event) {
	payload, _ := ev.Payload.(agentthread.ApprovalRequiredPayload)
	r.emitItem(ctx, agentworker.ThreadOutputItem{
		Yield: &agentworker.ThreadYield{
			Block: &agentworker.PendingBlock{
				TurnID:       ev.TurnID,
				InterruptID:  payload.InterruptID,
				CheckpointID: payload.CheckpointID,
				Kind:         "approval",
			},
		},
	})
}

func (r *localThreadRuntime) emitPlanInputBlockFinish(ctx context.Context, ev agentthread.Event) bool {
	payload, ok := ev.Payload.(agentthread.InterruptedPayload)
	if !ok {
		return false
	}
	if _, ok := payload.Info.(*planmode.RequestUserInputInfo); !ok {
		return false
	}
	r.emitItem(ctx, agentworker.ThreadOutputItem{
		Yield: &agentworker.ThreadYield{
			Block: &agentworker.PendingBlock{
				TurnID:       ev.TurnID,
				InterruptID:  payload.InterruptID,
				CheckpointID: payload.CheckpointID,
				Kind:         "plan_input",
			},
		},
	})
	return true
}

func (r *localThreadRuntime) emitErrorFinish(ctx context.Context, ev agentthread.Event) {
	message := "deepagent turn failed"
	if payload, ok := ev.Payload.(agentthread.ErrorPayload); ok && strings.TrimSpace(payload.Message) != "" {
		message = payload.Message
	}
	r.emitItem(ctx, agentworker.ThreadOutputItem{
		Yield: &agentworker.ThreadYield{Err: fmt.Errorf("%s", message)},
	})
}

func (r *localThreadRuntime) emitItem(ctx context.Context, item agentworker.ThreadOutputItem) bool {
	select {
	case r.items <- item:
		return true
	case <-ctx.Done():
		return false
	}
}

func senderFromInprocess(sender *agentworker.Sender) *localSender {
	if sender == nil {
		return nil
	}
	return &localSender{Type: string(sender.Type), ID: sender.ID}
}

func optionalReason(decision *localApprovalDecision) *string {
	if decision == nil || decision.Approved || strings.TrimSpace(decision.Reason) == "" {
		return nil
	}
	reason := decision.Reason
	return &reason
}

func newMessageID() string {
	return "msg_" + uuid.NewString()
}
