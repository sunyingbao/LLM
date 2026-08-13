//go:build !windows

package thread

import (
	"encoding/json"
	"strings"
	"testing"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/middleware/planmode"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/worker"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func eventPersistSet(event *agentworker.Event) bool {
	return event != nil && event.PersistToEventLog != nil
}

func eventPersistValue(event *agentworker.Event) bool {
	return eventPersistSet(event) && *event.PersistToEventLog
}

func eventFanoutSet(event *agentworker.Event) bool {
	return event != nil && event.FanoutToSession != nil
}

func eventFanoutValue(event *agentworker.Event) bool {
	return eventFanoutSet(event) && *event.FanoutToSession
}

func decodeOutputPayload[T any](t *testing.T, event *agentworker.Event) T {
	t.Helper()
	var payload T
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}
	return payload
}

func decodeConsumedInputsMeta(t *testing.T, event *agentworker.Event) []map[string]string {
	t.Helper()
	var payload struct {
		ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}
	return payload.ConsumedInputsMeta
}

func decodeConsumedMessageIDs(t *testing.T, event *agentworker.Event) []string {
	t.Helper()
	var payload struct {
		ConsumedMessageIDs []string `json:"consumed_message_ids"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}
	return payload.ConsumedMessageIDs
}

func outputEvent(t *testing.T, ev agentthread.Event) *agentworker.Event {
	t.Helper()
	if ev.TurnID == "" {
		ev.TurnID = "turn_1001"
	}
	item, err := threadOutputItem("", "0", ev, nil, OutputConfig{})
	if err != nil {
		t.Fatalf("threadOutputItem() error = %v", err)
	}
	if item == nil || item.Event == nil {
		t.Fatalf("threadOutputItem() = %+v", item)
	}
	return item.Event
}

type outputPayloadCase struct {
	name string
	ev   agentthread.Event
}

func outputPayloadCases() []outputPayloadCase {
	return []outputPayloadCase{
		{
			name: "turn start",
			ev: agentthread.Event{
				Type:    agentthread.EventTurnStart,
				Payload: agentthread.TurnStartPayload{Input: schema.UserMessage("hello")},
			},
		},
		{
			name: "assistant delta",
			ev: agentthread.Event{
				Type:    agentthread.EventLLMToken,
				Payload: agentthread.LLMTokenChunk{Text: "hello"},
			},
		},
		{
			name: "assistant message",
			ev: agentthread.Event{
				Type:    agentthread.EventLLMEnd,
				Payload: agentthread.LLMEnd{CallbackOutput: model.CallbackOutput{Message: schema.AssistantMessage("done", nil)}},
			},
		},
		{
			name: "tool started",
			ev: agentthread.Event{
				Type:    agentthread.EventToolStart,
				Payload: agentthread.ToolStartPayload{Name: "bash", CallID: "call-1", Args: "{}"},
			},
		},
		{
			name: "tool output delta",
			ev: agentthread.Event{
				Type:    agentthread.EventToolCallOutputChunk,
				Payload: agentthread.ToolCallOutputChunkPayload{Name: "bash", CallID: "call-1", Chunk: "out"},
			},
		},
		{
			name: "tool finished",
			ev: agentthread.Event{
				Type:    agentthread.EventToolEnd,
				Payload: agentthread.ToolEndPayload{Name: "bash", CallID: "call-1", Result: "{}"},
			},
		},
		{
			name: "approval required",
			ev: agentthread.Event{
				Type:    agentthread.EventApproveRequested,
				Payload: agentthread.ApprovalRequiredPayload{InterruptID: "interrupt-1", CheckpointID: "checkpoint-1"},
			},
		},
		{
			name: "interrupt required",
			ev: agentthread.Event{
				Type:    agentthread.EventFollowUpRequested,
				Payload: agentthread.FollowUpRequestedPayload{InterruptID: "interrupt-1", CheckpointID: "checkpoint-1"},
			},
		},
		{
			name: "plan updated",
			ev: agentthread.Event{
				Type:    agentthread.EventPlanUpdated,
				Payload: agentthread.PlanUpdatedPayload{Plan: []agentthread.PlanStep{{Step: "step", Status: agentthread.PlanStepStatusInProgress}}},
			},
		},
		{
			name: "plan input required",
			ev: agentthread.Event{
				Type: agentthread.EventInterrupted,
				Payload: agentthread.InterruptedPayload{
					Source:       "runtime",
					InterruptID:  "interrupt-plan",
					CheckpointID: "checkpoint-plan",
					Info: &planmode.RequestUserInputInfo{Questions: []planmode.Question{{
						ID:       "q1",
						Header:   "Scope",
						Question: "What should I build?",
					}}},
				},
			},
		},
		{
			name: "context compact started",
			ev: agentthread.Event{
				Type:    agentthread.EventContextCompactStarted,
				Payload: agentthread.ContextCompactStartedPayload{},
			},
		},
		{
			name: "context compacted",
			ev: agentthread.Event{
				Type:    agentthread.EventContextCompacted,
				Payload: agentthread.ContextCompactedPayload{},
			},
		},
		{
			name: "turn interrupted",
			ev: agentthread.Event{
				Type:    agentthread.EventInterrupted,
				Payload: agentthread.InterruptedPayload{Source: "external", Metadata: map[string]string{"reason": "cancelled"}},
			},
		},
		{
			name: "error",
			ev: agentthread.Event{
				Type:    agentthread.EventError,
				Payload: agentthread.ErrorPayload{Message: "failed"},
			},
		},
		{
			name: "compact interrupted",
			ev: agentthread.Event{
				Type:    agentEventContextCompactInterrupted,
				Payload: contextCompactInterruptedPayload{Kind: "cancel_input", Reason: "cancelled"},
			},
		},
		{
			name: "turn finished",
			ev: agentthread.Event{
				Type:    agentthread.EventTurnEnd,
				Payload: agentthread.TurnEndPayload{},
			},
		},
	}
}

func TestThreadOutputItemAttachesConsumedMessageIDsToAllTurnPayloads(t *testing.T) {
	consumed := schema.UserMessage("trigger message")
	attachAttribute(consumed, MessageAttribute{MessageID: "message-1"})

	for _, tc := range outputPayloadCases() {
		t.Run(tc.name, func(t *testing.T) {
			tc.ev.ConsumedInputs = []*schema.Message{consumed}
			event := outputEvent(t, tc.ev)
			got := decodeConsumedMessageIDs(t, event)
			if len(got) != 1 || got[0] != "message-1" {
				t.Fatalf("consumed_message_ids = %+v", got)
			}
		})
	}
}

func TestThreadOutputItemAttachesConsumedInputsMetaToAllPayloads(t *testing.T) {
	meta := []any{map[string]string{"trace_id": "trace-1"}}

	for _, tc := range outputPayloadCases() {
		t.Run(tc.name, func(t *testing.T) {
			tc.ev.ConsumedInputsMeta = meta
			event := outputEvent(t, tc.ev)
			got := decodeConsumedInputsMeta(t, event)
			if len(got) != 1 || got[0]["trace_id"] != "trace-1" {
				t.Fatalf("consumed_inputs_meta = %+v", got)
			}
		})
	}
}

func TestThreadOutputItemRejectsNonCloudAgentConsumedInputsMeta(t *testing.T) {
	_, err := threadOutputItem("", "0", agentthread.Event{
		TurnID:             "turn_1001",
		Type:               agentthread.EventLLMToken,
		Payload:            agentthread.LLMTokenChunk{Text: "hello"},
		ConsumedInputsMeta: []any{map[string]any{"trace_id": "trace-1"}},
	}, nil, OutputConfig{})
	if err == nil || !strings.Contains(err.Error(), "want map[string]string") {
		t.Fatalf("threadOutputItem() error = %v, want metadata type error", err)
	}
}

func TestThreadOutputItemLLMTokenOnlyFanout(t *testing.T) {
	event := outputEvent(t, agentthread.Event{
		ID:      "evt-token",
		Type:    agentthread.EventLLMToken,
		Payload: agentthread.LLMTokenChunk{Text: "hello", LLMResponseID: "llm-response-1"},
	})

	if event.Type != agentworker.EventType(protoevent.EventTypeAssistantDelta.String()) {
		t.Fatalf("event type = %q, want assistant delta", event.Type)
	}
	if !eventPersistSet(event) || eventPersistValue(event) {
		t.Fatalf("persist_to_event_log = set:%t value:%t, want set false",
			eventPersistSet(event), eventPersistValue(event))
	}
	if !eventFanoutSet(event) || !eventFanoutValue(event) {
		t.Fatalf("fanout_to_session = set:%t value:%t, want set true",
			eventFanoutSet(event), eventFanoutValue(event))
	}
	payload := decodeOutputPayload[protoevent.AssistantDeltaEventPayload](t, event)
	if payload.Delta != "hello" {
		t.Fatalf("token payload = %+v", payload)
	}
	if payload.ThinkingContentDelta != "" {
		t.Fatalf("thinking delta = %q, want empty", payload.ThinkingContentDelta)
	}
	if payload.LLMResponseID != "llm-response-1" {
		t.Fatalf("llm response id = %q", payload.LLMResponseID)
	}
}

func TestThreadOutputItemEventLogOptionPersistsToolCallStarted(t *testing.T) {
	item, err := threadOutputItem("", "0", agentthread.Event{
		ID:      "evt-tool-start",
		TurnID:  "turn_1001",
		Type:    agentthread.EventToolStart,
		Payload: agentthread.ToolStartPayload{Name: "bash", CallID: "call-1", Args: "{}"},
	}, nil, OutputConfig{
		EventLogOptions: map[string]EventLogOption{
			protoevent.EventTypeToolCallStarted.String(): {Persist: true},
		},
	})
	if err != nil {
		t.Fatalf("threadOutputItem() error = %v", err)
	}
	if item == nil || item.Event == nil {
		t.Fatalf("item = %+v", item)
	}
	if item.Event.Type != agentworker.EventType(protoevent.EventTypeToolCallStarted.String()) {
		t.Fatalf("event type = %q", item.Event.Type)
	}
	if !eventPersistSet(item.Event) || !eventPersistValue(item.Event) {
		t.Fatalf("persist_to_event_log = set:%t value:%t, want set true",
			eventPersistSet(item.Event), eventPersistValue(item.Event))
	}
	if !eventFanoutSet(item.Event) || !eventFanoutValue(item.Event) {
		t.Fatalf("fanout_to_session = set:%t value:%t, want set true",
			eventFanoutSet(item.Event), eventFanoutValue(item.Event))
	}
}

func TestThreadOutputItemEventLogOptionUnknownKeyKeepsDefault(t *testing.T) {
	item, err := threadOutputItem("", "0", agentthread.Event{
		ID:      "evt-tool-start",
		TurnID:  "turn_1001",
		Type:    agentthread.EventToolStart,
		Payload: agentthread.ToolStartPayload{Name: "bash", CallID: "call-1", Args: "{}"},
	}, nil, OutputConfig{
		EventLogOptions: map[string]EventLogOption{
			"TOOL_CALL_START": {Persist: true},
		},
	})
	if err != nil {
		t.Fatalf("threadOutputItem() error = %v", err)
	}
	if item == nil || item.Event == nil {
		t.Fatalf("item = %+v", item)
	}
	if !eventPersistSet(item.Event) || eventPersistValue(item.Event) {
		t.Fatalf("persist_to_event_log = set:%t value:%t, want default set false",
			eventPersistSet(item.Event), eventPersistValue(item.Event))
	}
}

func TestThreadOutputItemEventLogOptionCanDisableDefaultPersist(t *testing.T) {
	item, err := threadOutputItem("", "0", agentthread.Event{
		ID:      "evt-tool-end",
		TurnID:  "turn_1001",
		Type:    agentthread.EventToolEnd,
		Payload: agentthread.ToolEndPayload{Name: "bash", CallID: "call-1", Result: "{}"},
	}, nil, OutputConfig{
		EventLogOptions: map[string]EventLogOption{
			protoevent.EventTypeToolCallFinished.String(): {Persist: false},
		},
	})
	if err != nil {
		t.Fatalf("threadOutputItem() error = %v", err)
	}
	if item == nil || item.Event == nil {
		t.Fatalf("item = %+v", item)
	}
	if item.Event.Type != agentworker.EventType(protoevent.EventTypeToolCallFinished.String()) {
		t.Fatalf("event type = %q", item.Event.Type)
	}
	if !eventPersistSet(item.Event) || eventPersistValue(item.Event) {
		t.Fatalf("persist_to_event_log = set:%t value:%t, want set false",
			eventPersistSet(item.Event), eventPersistValue(item.Event))
	}
	if eventFanoutSet(item.Event) {
		t.Fatalf("fanout_to_session = set:%t value:%t, want default unset",
			eventFanoutSet(item.Event), eventFanoutValue(item.Event))
	}
}

func TestThreadOutputItemLLMTokenIncludesThinkingDelta(t *testing.T) {
	event := outputEvent(t, agentthread.Event{
		ID:      "evt-token-thinking",
		Type:    agentthread.EventLLMToken,
		Payload: agentthread.LLMTokenChunk{ReasoningText: "think"},
	})

	payload := decodeOutputPayload[protoevent.AssistantDeltaEventPayload](t, event)
	if payload.Delta != "" {
		t.Fatalf("delta = %q, want empty", payload.Delta)
	}
	if payload.ThinkingContentDelta != "think" {
		t.Fatalf("thinking delta = %q", payload.ThinkingContentDelta)
	}
}

func TestThreadOutputItemLLMEndIncludesThinkingContent(t *testing.T) {
	msg := schema.AssistantMessage("hello", nil)
	msg.ReasoningContent = "think-done"
	event := outputEvent(t, agentthread.Event{
		ID:   "evt-end-thinking",
		Type: agentthread.EventLLMEnd,
		Payload: agentthread.LLMEnd{
			CallbackOutput: model.CallbackOutput{Message: msg},
			LLMResponseID:  "llm-response-2",
		},
	})

	payload := decodeOutputPayload[protoevent.MessageEventPayload](t, event)
	if payload.ThinkingContent != "think-done" {
		t.Fatalf("thinking content = %q", payload.ThinkingContent)
	}
	if payload.LLMResponseID != "llm-response-2" {
		t.Fatalf("llm response id = %q", payload.LLMResponseID)
	}
}

func TestThreadOutputItemAttachesContextUsage(t *testing.T) {
	usage := agentthread.ContextUsageSnapshot{
		CurrentTotal:              700,
		ContextWindow:             1000,
		LastModelPromptTokens:     500,
		LastModelCompletionTokens: 200,
	}
	item, err := threadOutputItem("", "0", agentthread.Event{
		ID:      "evt-end",
		TurnID:  "turn_1001",
		Type:    agentthread.EventLLMEnd,
		Payload: agentthread.LLMEnd{CallbackOutput: model.CallbackOutput{Message: schema.AssistantMessage("hello", nil)}},
	}, &usage, OutputConfig{})
	if err != nil {
		t.Fatalf("threadOutputItem() error = %v", err)
	}
	payload := decodeOutputPayload[protoevent.MessageEventPayload](t, item.Event)
	if payload.ContextUsage == nil {
		t.Fatal("context usage is nil")
	}
	if payload.ContextUsage.UsedTokens != 700 || payload.ContextUsage.MaxTokens == nil || *payload.ContextUsage.MaxTokens != 1000 {
		t.Fatalf("context usage = %+v", payload.ContextUsage)
	}
	if payload.ContextUsage.Ratio == nil || *payload.ContextUsage.Ratio != 0.7 {
		t.Fatalf("ratio = %+v", payload.ContextUsage.Ratio)
	}
}

func TestThreadOutputItemTurnStartIncludesIncomingMessage(t *testing.T) {
	msg := schema.UserMessage("hello")
	attachAttribute(msg, MessageAttribute{MessageID: "1001", SenderID: "u1", SenderType: "USER"})

	event := outputEvent(t, agentthread.Event{
		ID:             "evt-start",
		Type:           agentthread.EventTurnStart,
		Payload:        agentthread.TurnStartPayload{Input: msg},
		ConsumedInputs: []*schema.Message{msg},
	})

	if event.Type != agentworker.EventType(protoevent.EventTypeTurnStarted.String()) {
		t.Fatalf("event type = %v", event.Type)
	}
	payload := decodeOutputPayload[protoevent.MessageEventPayload](t, event)
	if len(payload.Parts) != 1 || payload.Parts[0].Text != "hello" || payload.MessageID == nil || *payload.MessageID != "1001" {
		t.Fatalf("message = %+v", payload)
	}
	if len(payload.ConsumedMessageIDs) != 1 || payload.ConsumedMessageIDs[0] != "1001" {
		t.Fatalf("consumed_message_ids = %+v", payload.ConsumedMessageIDs)
	}
}

func TestThreadOutputItemTurnStartIncludesUserInputMultiContentParts(t *testing.T) {
	msg := &schema.Message{
		Role: schema.User,
		UserInputMultiContent: []schema.MessageInputPart{
			{
				Type:  schema.ChatMessagePartTypeText,
				Text:  "describe this",
				Extra: map[string]any{"adapter": map[string]any{"part_id": "text_1"}},
			},
			{
				Type: schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{
						URL:   stringPtrIfNotEmpty("https://example.com/a.png"),
						Extra: map[string]any{"adapter": map[string]any{"resource_id": "res_1"}},
					},
				},
			},
		},
	}
	attachAttribute(msg, MessageAttribute{MessageID: "1002", SenderID: "u1", SenderType: "USER"})

	event := outputEvent(t, agentthread.Event{
		ID:             "evt-start-multimodal",
		Type:           agentthread.EventTurnStart,
		Payload:        agentthread.TurnStartPayload{Input: msg},
		ConsumedInputs: []*schema.Message{msg},
	})

	payload := decodeOutputPayload[protoevent.MessageEventPayload](t, event)
	if len(payload.Parts) != 2 || payload.Parts[0].Text != "describe this" {
		t.Fatalf("message = %+v", payload)
	}
	if payload.Parts[1].Type != protoevent.MessagePartTypeImage || payload.Parts[1].URL != "https://example.com/a.png" {
		t.Fatalf("image part = %+v", payload.Parts[1])
	}
	if got := payload.Parts[0].Extra["adapter"]; string(got) != `{"part_id":"text_1"}` {
		t.Fatalf("text part extra = %s", got)
	}
	if got := payload.Parts[1].Extra["adapter"]; string(got) != `{"resource_id":"res_1"}` {
		t.Fatalf("image part extra = %s", got)
	}
	if payload.MessageID == nil || *payload.MessageID != "1002" {
		t.Fatalf("message_id = %+v", payload.MessageID)
	}
}

func TestThreadOutputItemTurnStartPreservesOriginalCloudInputTextParts(t *testing.T) {
	msg, err := userMessageToEinoMessage(protoinput.UserMessage{
		Parts: []protoinput.MessagePart{
			{Type: protoinput.MessagePartTypeText, Text: "first"},
			{Type: protoinput.MessagePartTypeText, Text: "second"},
		},
	})
	if err != nil {
		t.Fatalf("userMessageToEinoMessage() error=%v", err)
	}
	attachAttribute(msg, MessageAttribute{MessageID: "1003", SenderID: "u1", SenderType: "USER"})

	event := outputEvent(t, agentthread.Event{
		ID:             "evt-start-text-parts",
		Type:           agentthread.EventTurnStart,
		Payload:        agentthread.TurnStartPayload{Input: msg},
		ConsumedInputs: []*schema.Message{msg},
	})

	payload := decodeOutputPayload[protoevent.MessageEventPayload](t, event)
	if len(payload.Parts) != 2 || payload.Parts[0].Text != "first" || payload.Parts[1].Text != "second" {
		t.Fatalf("message = %+v", payload)
	}
}

func TestThreadOutputItemTurnStartPreservesOriginalCloudInputPartExtra(t *testing.T) {
	msg, err := userMessageToEinoMessage(protoinput.UserMessage{
		Parts: []protoinput.MessagePart{
			{
				Type: protoinput.MessagePartTypeText,
				Text: "first",
				Extra: map[string]json.RawMessage{
					"adapter": json.RawMessage(`{"part_id":"text_1"}`),
				},
			},
			{
				Type: protoinput.MessagePartTypeImage,
				URL:  "https://example.com/a.png",
				Extra: map[string]json.RawMessage{
					"adapter": json.RawMessage(`{"resource_id":"res_1"}`),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("userMessageToEinoMessage() error=%v", err)
	}
	attachAttribute(msg, MessageAttribute{MessageID: "1004", SenderID: "u1", SenderType: "USER"})

	event := outputEvent(t, agentthread.Event{
		ID:             "evt-start-cloud-extra",
		Type:           agentthread.EventTurnStart,
		Payload:        agentthread.TurnStartPayload{Input: msg},
		ConsumedInputs: []*schema.Message{msg},
	})

	payload := decodeOutputPayload[protoevent.MessageEventPayload](t, event)
	if len(payload.Parts) != 2 {
		t.Fatalf("message = %+v", payload)
	}
	if got := payload.Parts[0].Extra["adapter"]; string(got) != `{"part_id":"text_1"}` {
		t.Fatalf("text part extra = %s", got)
	}
	if got := payload.Parts[1].Extra["adapter"]; string(got) != `{"resource_id":"res_1"}` {
		t.Fatalf("image part extra = %s", got)
	}
}

func TestThreadOutputItemApprovalCombinesEventAndBlockYield(t *testing.T) {
	item, err := threadOutputItem("", "0", agentthread.Event{
		ID:     "evt-approve",
		TurnID: "turn_approve",
		Type:   agentthread.EventApproveRequested,
		Payload: agentthread.ApprovalRequiredPayload{
			InterruptID:  "interrupt_1",
			CheckpointID: "checkpoint_1",
			ApprovalInfo: &deeptools.ApprovalInfo{
				ToolName:        "exec_command",
				ArgumentsInJSON: `{"cmd":"go test ./..."}`,
			},
		},
	}, nil, OutputConfig{})
	if err != nil {
		t.Fatalf("threadOutputItem() error = %v", err)
	}
	if item.Event == nil || item.Yield == nil || item.Yield.Block == nil {
		t.Fatalf("item = %+v", item)
	}
	payload := decodeOutputPayload[protoevent.ApprovalRequiredEventPayload](t, item.Event)
	if payload.InterruptID != "interrupt_1" {
		t.Fatalf("approval = %+v", payload)
	}
	if item.Yield.Block.Kind != "approval" || item.Yield.Block.InterruptID != "interrupt_1" {
		t.Fatalf("yield block = %+v", item.Yield.Block)
	}
}

func TestThreadOutputItemFollowUpCombinesEventAndBlockYield(t *testing.T) {
	item, err := threadOutputItem("", "0", agentthread.Event{
		ID:     "evt-follow-up",
		TurnID: "turn_follow",
		Type:   agentthread.EventFollowUpRequested,
		Payload: agentthread.FollowUpRequestedPayload{
			InterruptID:  "interrupt_follow",
			CheckpointID: "checkpoint_follow",
			Info:         &deeptools.FollowUpInfo{Questions: []string{"Which account should I use?"}},
		},
	}, nil, OutputConfig{})
	if err != nil {
		t.Fatalf("threadOutputItem() error = %v", err)
	}
	if item.Event == nil || item.Yield == nil || item.Yield.Block == nil {
		t.Fatalf("item = %+v", item)
	}
	if item.Event.Type != agentworker.EventType(protoevent.EventTypeInterruptRequired.String()) {
		t.Fatalf("event type = %v", item.Event.Type)
	}
	payload := decodeOutputPayload[protoevent.InterruptRequiredEventPayload](t, item.Event)
	if payload.Kind != "follow_up" || payload.InterruptID != "interrupt_follow" || payload.CheckpointID != "checkpoint_follow" {
		t.Fatalf("payload = %+v", payload)
	}
	var info struct {
		Questions []string `json:"questions"`
	}
	if err := json.Unmarshal(payload.Info, &info); err != nil {
		t.Fatalf("unmarshal followup info: %v", err)
	}
	if len(info.Questions) != 1 || info.Questions[0] != "Which account should I use?" {
		t.Fatalf("followup info = %+v", info)
	}
	if item.Yield.Block.Kind != "follow_up" || item.Yield.Block.InterruptID != "interrupt_follow" {
		t.Fatalf("yield block = %+v", item.Yield.Block)
	}
}

func TestThreadOutputItemPlanInputInterruptedBlocks(t *testing.T) {
	item, err := threadOutputItem("", "0", agentthread.Event{
		ID:     "evt-plan-input",
		TurnID: "turn_plan",
		Type:   agentthread.EventInterrupted,
		Payload: agentthread.InterruptedPayload{
			Source:       "runtime",
			InterruptID:  "interrupt_plan",
			CheckpointID: "checkpoint_plan",
			Info: &planmode.RequestUserInputInfo{
				Questions: []planmode.Question{{
					ID:       "q1",
					Header:   "Scope",
					Question: "What should I build?",
				}},
			},
		},
	}, nil, OutputConfig{})
	if err != nil {
		t.Fatalf("threadOutputItem() error = %v", err)
	}
	if item.Event == nil || item.Yield == nil || item.Yield.Block == nil {
		t.Fatalf("item = %+v", item)
	}
	if item.Event.Type != agentworker.EventType(protoevent.EventTypePlanInputRequired.String()) {
		t.Fatalf("event type = %v", item.Event.Type)
	}
	payload := decodeOutputPayload[protoevent.PlanInputRequiredEventPayload](t, item.Event)
	if payload.InterruptID != "interrupt_plan" {
		t.Fatalf("payload = %+v", payload)
	}
	if item.Yield.Block.Kind != "plan_input" {
		t.Fatalf("yield block = %+v", item.Yield.Block)
	}
}

func TestThreadOutputItemCustomInterruptedBlocks(t *testing.T) {
	type customInfo struct {
		Field string `json:"field"`
	}
	item, err := threadOutputItem("", "0", agentthread.Event{
		ID:     "evt-custom-input",
		TurnID: "turn_custom",
		Type:   agentthread.EventInterrupted,
		Payload: agentthread.InterruptedPayload{
			Source:       "custom",
			InterruptID:  "interrupt_custom",
			CheckpointID: "checkpoint_custom",
			InfoType:     "*example.CustomInfo",
			Info:         &customInfo{Field: "value"},
		},
	}, nil, OutputConfig{})
	if err != nil {
		t.Fatalf("threadOutputItem() error = %v", err)
	}
	if item.Event == nil || item.Yield == nil || item.Yield.Block == nil {
		t.Fatalf("item = %+v", item)
	}
	if item.Event.Type != agentworker.EventType(protoevent.EventTypeInterruptRequired.String()) {
		t.Fatalf("event type = %v", item.Event.Type)
	}
	payload := decodeOutputPayload[protoevent.InterruptRequiredEventPayload](t, item.Event)
	if payload.Kind != "custom" || payload.InfoType != "*example.CustomInfo" {
		t.Fatalf("payload = %+v", payload)
	}
	if item.Yield.Block.Kind != "custom" || item.Yield.Block.InterruptID != "interrupt_custom" {
		t.Fatalf("yield block = %+v", item.Yield.Block)
	}
}

func TestThreadOutputItemExternalInterruptedDoesNotBlock(t *testing.T) {
	item, err := threadOutputItem("", "0", agentthread.Event{
		ID:      "evt-interrupt",
		TurnID:  "turn_1001",
		Type:    agentthread.EventInterrupted,
		Payload: agentthread.InterruptedPayload{Source: "external"},
	}, nil, OutputConfig{})
	if err != nil {
		t.Fatalf("threadOutputItem() error = %v", err)
	}
	if item.Event == nil {
		t.Fatalf("event is nil")
	}
	if item.Yield != nil {
		t.Fatalf("yield = %+v, want nil", item.Yield)
	}
}

func TestThreadOutputItemWorkerShutdownTimeoutInterruptYields(t *testing.T) {
	item, err := threadOutputItem("", "0", agentthread.Event{
		ID:     "evt-timeout",
		TurnID: "turn_1001",
		Type:   agentthread.EventInterrupted,
		Payload: agentthread.InterruptedPayload{
			Source: "external",
			Metadata: map[string]string{
				"kind":   string(agentworker.ThreadInterruptKindWorkerShutdownTimeout),
				"reason": "worker shutdown drain timeout",
			},
		},
	}, nil, OutputConfig{})
	if err != nil {
		t.Fatalf("threadOutputItem() error = %v", err)
	}
	if item.Event == nil || item.Yield == nil || item.Yield.Block != nil {
		t.Fatalf("item = %+v", item)
	}
	if item.Yield.Reason != "worker shutdown drain timeout" {
		t.Fatalf("yield reason = %q", item.Yield.Reason)
	}
}

func TestThreadOutputItemTurnEndIncludesConsumedMessageIDs(t *testing.T) {
	msg := schema.UserMessage("finish")
	attachAttribute(msg, MessageAttribute{MessageID: "1001"})
	event := outputEvent(t, agentthread.Event{
		ID:             "evt-turn-end",
		Type:           agentthread.EventTurnEnd,
		Payload:        agentthread.TurnEndPayload{},
		ConsumedInputs: []*schema.Message{msg},
	})
	payload := decodeOutputPayload[protoevent.TurnFinishedEventPayload](t, event)
	if len(payload.ConsumedMessageIDs) != 1 || payload.ConsumedMessageIDs[0] != "1001" {
		t.Fatalf("consumed_message_ids = %+v", payload.ConsumedMessageIDs)
	}
}
