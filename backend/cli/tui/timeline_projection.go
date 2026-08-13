package tui

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	"eino-cli/deepagent/cloud/protocol/timeline"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
)

func applyTimelineEvent(model *Model, event timeline.Event) (updated tea.Model, cmd tea.Cmd) {
	switch protoevent.EventType(event.EventType) {
	case protoevent.EventTypeToolCallStarted:
		var payload protoevent.ToolCallEventPayload
		if json.Unmarshal(event.Payload, &payload) == nil {
			arguments := ""
			if payload.ArgumentsJSON != nil {
				arguments = *payload.ArgumentsJSON
			}
			model.toolBlockSeq++
			block := &toolBlock{id: model.toolBlockSeq, name: payload.ToolName, argsLine: formatArgsLine(payload.ToolName, arguments, model.toolArgsMaxChars), lines: []string{"Running…"}, collapsed: true}
			model.toolBlocks = append(model.toolBlocks, block)
			model.toolBlocksByCall[payload.ToolCallID] = block
			pushToolBlockMessage(model, toolPlaceholderPrefix+strconv.Itoa(block.id)+"]")
		}
	case protoevent.EventTypeToolCallOutputDelta:
		var payload protoevent.ToolCallEventPayload
		if json.Unmarshal(event.Payload, &payload) == nil && payload.OutputDelta != nil {
			if block := model.toolBlocksByCall[payload.ToolCallID]; block != nil {
				if len(block.lines) == 1 && block.lines[0] == "Running…" {
					block.lines = nil
				}
				block.lines = append(block.lines, splitToolLines(*payload.OutputDelta)...)
				rebuildHistory(model)
			}
		}
	case protoevent.EventTypeToolCallFinished:
		var payload protoevent.ToolCallEventPayload
		if json.Unmarshal(event.Payload, &payload) == nil {
			if block := model.toolBlocksByCall[payload.ToolCallID]; block != nil {
				content := "Completed"
				if payload.ResultJSON != nil {
					content = *payload.ResultJSON
				} else if payload.ErrorMessage != nil {
					content = "Error: " + *payload.ErrorMessage
				}
				block.lines = splitToolLines(content)
				if len(block.lines) == 0 {
					block.lines = []string{"Completed"}
				}
				delete(model.toolBlocksByCall, payload.ToolCallID)
				rebuildHistory(model)
			}
		}
	case protoevent.EventTypePlanUpdated:
		var payload protoevent.PlanUpdatedEventPayload
		if json.Unmarshal(event.Payload, &payload) == nil {
			previousHeight := getTodoPanelHeight(model)
			model.todos = make([]deep.TODO, 0, len(payload.Items))
			for _, item := range payload.Items {
				if item != nil {
					model.todos = append(model.todos, deep.TODO{Content: item.Content, ActiveForm: item.Content, Status: item.Status})
				}
			}
			if getTodoPanelHeight(model) != previousHeight {
				recomputeLayout(model)
			}
		}
	case protoevent.EventTypeTurnInterrupted, protoevent.EventTypeCompactInterrupted:
		model.interrupted = true
	case protoevent.EventTypeError:
		var payload protoevent.ErrorEventPayload
		if json.Unmarshal(event.Payload, &payload) == nil && strings.TrimSpace(payload.Message) != "" {
			model.lastErr = errors.New(payload.Message)
		}
	}
	return model, nil
}
