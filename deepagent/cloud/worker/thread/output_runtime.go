//go:build !windows

package thread

import (
	"context"
	"encoding/json"

	"code.byted.org/gopkg/logs/v2"
	protoevent "eino-cli/deepagent/cloud/protocol/event"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/worker"
)

func (t *Runtime) forwardAgentEvent(ctx context.Context, ev agentthread.Event) bool {
	usage := t.thread.ContextManager().ContextUsage()
	item, err := threadOutputItem(t.sessionID, t.threadID, ev, &usage, t.outputConfig)
	if err != nil {
		logs.CtxError(ctx, "[cloudagent] map agent event failed: turn_id=%s event_id=%s event_type=%s err=%v", ev.TurnID, ev.ID, ev.Type, err)
		return true
	}
	if item == nil {
		return true
	}
	t.logOutputItem(ctx, ev, *item)
	if ev.Type == agentthread.EventTurnEnd && t.turnFinishedObserver != nil {
		t.turnFinishedObserver(ctx, ev)
	}
	return t.outputBridge.deliver(ctx, t, *item)
}

func (t *Runtime) logOutputItem(ctx context.Context, ev agentthread.Event, item agentworker.ThreadOutputItem) {
	if item.Event != nil {
		switch item.Event.Type {
		case agentworker.EventType(protoevent.EventTypeInterruptRequired.String()):
			var payload protoevent.InterruptRequiredEventPayload
			if err := json.Unmarshal(item.Event.Payload, &payload); err == nil {
				logs.CtxInfo(ctx,
					"[cloudagent] interrupt required output: 对话流ID=%s thread_id=%s turn_id=%s event_id=%s source_event_type=%s interrupt_id=%s checkpoint_id=%s kind=%s info_type=%s",
					t.sessionID, t.threadID, item.Event.TurnID, item.Event.ID, ev.Type, payload.InterruptID, payload.CheckpointID, payload.Kind, payload.InfoType,
				)
			}
		case agentworker.EventType(protoevent.EventTypeTurnInterrupted.String()):
			if payload, ok := ev.Payload.(agentthread.InterruptedPayload); ok && isExternalInterrupt(payload) {
				logs.CtxInfo(ctx,
					"[cloudagent] external interrupt output: 对话流ID=%s thread_id=%s turn_id=%s event_id=%s source=%s reason=%s kind=%s",
					t.sessionID, t.threadID, item.Event.TurnID, item.Event.ID, payload.Source, interruptedMessage(payload), payload.Metadata["kind"],
				)
			}
		}
	}
	if item.Yield != nil && item.Yield.Block != nil {
		logs.CtxInfo(ctx,
			"[cloudagent] block yield output: 对话流ID=%s thread_id=%s turn_id=%s interrupt_id=%s checkpoint_id=%s kind=%s reason=%s",
			t.sessionID, t.threadID, item.Yield.Block.TurnID, item.Yield.Block.InterruptID, item.Yield.Block.CheckpointID, item.Yield.Block.Kind, item.Yield.Reason,
		)
	}
}

func (t *Runtime) emitAgentEvent(ctx context.Context, ev agentthread.Event) {
	t.mu.Lock()
	bridge := t.outputBridge
	t.mu.Unlock()
	if bridge == nil {
		return
	}
	usage := t.thread.ContextManager().ContextUsage()
	item, err := threadOutputItem(t.sessionID, t.threadID, ev, &usage, t.outputConfig)
	if err != nil {
		logs.CtxError(ctx, "[cloudagent] map runtime event failed: turn_id=%s event_id=%s event_type=%s err=%v", ev.TurnID, ev.ID, ev.Type, err)
		return
	}
	if item == nil {
		return
	}
	t.logOutputItem(ctx, ev, *item)
	bridge.send(ctx, *item)
}
