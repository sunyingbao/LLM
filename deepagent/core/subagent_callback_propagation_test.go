package deepagents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/core/middleware/subagent"
	"eino-cli/deepagent/mock/mock_model"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	cbutils "github.com/cloudwego/eino/utils/callbacks"
	"go.uber.org/mock/gomock"
)

const (
	subAgentCallbackTaskMarker = "subagent-callback-task-marker"
	subAgentObservedToolName   = "subagent_observed_tool"
	subAgentModelErrMarker     = "subagent-model-boom"
)

type subAgentCallbackScenario struct {
	childUsesTool bool
	childFails    bool
	withCallbacks bool
}

func TestDeepAgent_SubAgentCallbacks_PropagateChildModelEvents(t *testing.T) {
	events, msg, err := runSubAgentCallbackScenario(t, subAgentCallbackScenario{
		withCallbacks: true,
	})
	logCallbackEvents(t, events)
	if err != nil {
		t.Fatalf("runSubAgentCallbackScenario() error = %v", err)
	}
	if msg == nil || msg.Content != "main-done" {
		t.Fatalf("unexpected final message: %+v", msg)
	}
	if !hasCallbackEvent(events, func(e callbackEvent) bool {
		return e.scope == "model" &&
			e.event == "start" &&
			strings.Contains(e.payload, subAgentCallbackTaskMarker)
	}) {
		t.Fatalf("expected child model start callback carrying sub-agent task marker, got %+v", events)
	}
}

func TestDeepAgent_SubAgentCallbacks_PropagateChildToolEvents(t *testing.T) {
	events, msg, err := runSubAgentCallbackScenario(t, subAgentCallbackScenario{
		childUsesTool: true,
		withCallbacks: true,
	})
	logCallbackEvents(t, events)
	if err != nil {
		t.Fatalf("runSubAgentCallbackScenario() error = %v", err)
	}
	if msg == nil || msg.Content != "main-done" {
		t.Fatalf("unexpected final message: %+v", msg)
	}
	if !hasCallbackEvent(events, func(e callbackEvent) bool {
		return e.scope == "tool" && e.event == "start" && e.name == subAgentObservedToolName
	}) {
		t.Fatalf("expected child tool start callback for %q, got %+v", subAgentObservedToolName, events)
	}
	if !hasCallbackEvent(events, func(e callbackEvent) bool {
		return e.scope == "tool" && e.event == "end" && e.name == subAgentObservedToolName
	}) {
		t.Fatalf("expected child tool end callback for %q, got %+v", subAgentObservedToolName, events)
	}
}

func TestDeepAgent_SubAgentCallbacks_PropagateChildModelErrors(t *testing.T) {
	events, _, err := runSubAgentCallbackScenario(t, subAgentCallbackScenario{
		childFails:    true,
		withCallbacks: true,
	})
	logCallbackEvents(t, events)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), subAgentModelErrMarker) {
		t.Fatalf("expected error containing %q, got %v", subAgentModelErrMarker, err)
	}
	if !hasCallbackEvent(events, func(e callbackEvent) bool {
		return e.scope == "model" &&
			e.event == "error" &&
			strings.Contains(e.payload, subAgentModelErrMarker)
	}) {
		t.Fatalf("expected child model error callback containing %q, got %+v", subAgentModelErrMarker, events)
	}
}

func TestDeepAgent_SubAgentCallbacks_NoRecorderEventsWithoutWithCallbacks(t *testing.T) {
	events, msg, err := runSubAgentCallbackScenario(t, subAgentCallbackScenario{
		withCallbacks: false,
	})
	logCallbackEvents(t, events)
	if err != nil {
		t.Fatalf("runSubAgentCallbackScenario() error = %v", err)
	}
	if msg == nil || msg.Content != "main-done" {
		t.Fatalf("unexpected final message: %+v", msg)
	}
	if len(events) != 0 {
		t.Fatalf("expected no recorder events without WithCallbacks, got %+v", events)
	}
}

func runSubAgentCallbackScenario(t *testing.T, scenario subAgentCallbackScenario) ([]callbackEvent, *schema.Message, error) {
	t.Helper()
	t.Logf("starting subagent callback scenario: withCallbacks=%v childUsesTool=%v childFails=%v",
		scenario.withCallbacks, scenario.childUsesTool, scenario.childFails)

	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mainCM := mock_model.NewMockToolCallingChatModel(ctrl)
	subCM := mock_model.NewMockToolCallingChatModel(ctrl)
	mainCM.EXPECT().WithTools(gomock.Any()).Return(mainCM, nil).AnyTimes()
	subCM.EXPECT().WithTools(gomock.Any()).Return(subCM, nil).AnyTimes()

	mainCM.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			t.Logf("main model input:\n%s", formatMessagesForLog(input))
			if !containsToolResponse(input) {
				t.Log("main model delegates to subagent via task tool")
				return schema.AssistantMessage("delegate-to-subagent", []schema.ToolCall{
					{
						ID:   "task_call_id",
						Type: "function",
						Function: schema.FunctionCall{
							Name:      "task",
							Arguments: `{"subagent":"test_sub","task":"` + subAgentCallbackTaskMarker + `","wait_for_done":true}`,
						},
					},
				}), nil
			}
			t.Log("main model observed tool response and finishes")
			return schema.AssistantMessage("main-done", nil), nil
		},
	).AnyTimes()

	childCalls := 0
	subCM.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			t.Logf("sub model input (call=%d):\n%s", childCalls+1, formatMessagesForLog(input))
			if scenario.childFails {
				t.Logf("sub model fails with marker %q", subAgentModelErrMarker)
				return nil, errors.New(subAgentModelErrMarker)
			}
			if scenario.childUsesTool && !containsToolResponse(input) && childCalls == 0 {
				childCalls++
				t.Logf("sub model requests tool %q", subAgentObservedToolName)
				return schema.AssistantMessage("child-tool-step", []schema.ToolCall{
					{
						ID:   "subagent_tool_call_id",
						Type: "function",
						Function: schema.FunctionCall{
							Name:      subAgentObservedToolName,
							Arguments: `{"kind":"observe"}`,
						},
					},
				}), nil
			}
			childCalls++
			t.Log("sub model returns final completion message")
			return schema.AssistantMessage("subagent-done", nil), nil
		},
	).AnyTimes()

	var subTools []tool.BaseTool
	if scenario.childUsesTool {
		subTools = append(subTools, &namedInvokableTool{
			name:   subAgentObservedToolName,
			result: `{"status":"ok"}`,
		})
	}

	agent, err := New(ctx,
		WithModel(mainCM),
		WithSubAgents(&subagent.SubAgent{
			Name:         "test_sub",
			Description:  "test subagent",
			SystemPrompt: "you are a test subagent",
			Tools:        subTools,
			Model:        subCM,
			MaxSteps:     4,
		}),
		WithContextManager(&noopContextManager{}),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
	)
	if err != nil {
		return nil, nil, err
	}
	t.Log("created main agent with subagent middleware")

	recorder := &callbackRecorder{}
	runOpts := []RunOptionFunc{}
	if scenario.withCallbacks {
		t.Log("installing callback recorder via WithCallbacks")
		runOpts = append(runOpts, WithCallbacks(newSubAgentCallbackRecorder(recorder)))
	}

	msg, err := agent.Run(ctx, []*schema.Message{schema.UserMessage("please delegate")}, runOpts...)
	events := recorder.snapshot()
	if err != nil {
		t.Logf("agent.Run returned error: %v", err)
	} else {
		t.Logf("agent.Run returned message: role=%s content=%q", msg.Role, msg.Content)
	}
	t.Logf("recorded %d callback events", len(events))
	return events, msg, err
}

func newSubAgentCallbackRecorder(recorder *callbackRecorder) callbacks.Handler {
	return cbutils.NewHandlerHelper().
		ChatModel(&cbutils.ModelCallbackHandler{
			OnStart: func(ctx context.Context, info *callbacks.RunInfo, input *model.CallbackInput) context.Context {
				agent := GetDeepAgent(ctx)
				recorder.add(callbackEvent{
					agentName:  agent.Name(),
					agentDepth: agent.Depth(),
					scope:      "model",
					event:      "start",
					component:  string(info.Component),
					name:       info.Name,
					typ:        info.Type,
					payload:    flattenCallbackMessages(input.Messages),
				})
				return ctx
			},
			OnEnd: func(ctx context.Context, info *callbacks.RunInfo, output *model.CallbackOutput) context.Context {
				payload := ""
				if output != nil && output.Message != nil {
					payload = output.Message.Content
				}
				agent := GetDeepAgent(ctx)
				recorder.add(callbackEvent{
					agentName:  agent.Name(),
					agentDepth: agent.Depth(),
					scope:      "model",
					event:      "end",
					component:  string(info.Component),
					name:       info.Name,
					typ:        info.Type,
					payload:    payload,
				})
				return ctx
			},
			OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
				agent := GetDeepAgent(ctx)
				recorder.add(callbackEvent{
					agentName:  agent.Name(),
					agentDepth: agent.Depth(),
					scope:      "model",
					event:      "error",
					component:  string(info.Component),
					name:       info.Name,
					typ:        info.Type,
					payload:    err.Error(),
				})
				return ctx
			},
		}).
		Tool(&cbutils.ToolCallbackHandler{
			OnStart: func(ctx context.Context, info *callbacks.RunInfo, input *tool.CallbackInput) context.Context {
				agent := GetDeepAgent(ctx)
				recorder.add(callbackEvent{
					agentName:  agent.Name(),
					agentDepth: agent.Depth(),
					scope:      "tool",
					event:      "start",
					component:  string(info.Component),
					name:       info.Name,
					typ:        info.Type,
					payload:    input.ArgumentsInJSON,
				})
				return ctx
			},
			OnEnd: func(ctx context.Context, info *callbacks.RunInfo, output *tool.CallbackOutput) context.Context {
				payload := ""
				if output != nil {
					payload = output.Response
				}
				agent := GetDeepAgent(ctx)
				recorder.add(callbackEvent{
					agentName:  agent.Name(),
					agentDepth: agent.Depth(),
					scope:      "tool",
					event:      "end",
					component:  string(info.Component),
					name:       info.Name,
					typ:        info.Type,
					payload:    payload,
				})
				return ctx
			},
			OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
				agent := GetDeepAgent(ctx)
				recorder.add(callbackEvent{
					agentName:  agent.Name(),
					agentDepth: agent.Depth(),
					scope:      "tool",
					event:      "error",
					component:  string(info.Component),
					name:       info.Name,
					typ:        info.Type,
					payload:    err.Error(),
				})
				return ctx
			},
		}).
		Handler()
}

func flattenCallbackMessages(messages []*schema.Message) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		parts = append(parts, string(msg.Role)+":"+msg.Content)
	}
	return strings.Join(parts, "\n")
}

func containsToolResponse(messages []*schema.Message) bool {
	for _, msg := range messages {
		if msg != nil && msg.Role == schema.Tool {
			return true
		}
	}
	return false
}

func hasCallbackEvent(events []callbackEvent, match func(callbackEvent) bool) bool {
	for _, event := range events {
		if match(event) {
			return true
		}
	}
	return false
}

func formatMessagesForLog(messages []*schema.Message) string {
	if len(messages) == 0 {
		return "(no messages)"
	}

	lines := make([]string, 0, len(messages))
	for i, msg := range messages {
		if msg == nil {
			lines = append(lines, "[nil]")
			continue
		}
		line := fmt.Sprintf("[%d] role=%s content=%q", i, msg.Role, msg.Content)
		if len(msg.ToolCalls) > 0 {
			line += " tool_calls=" + formatToolCallsForLog(msg.ToolCalls)
		}
		if msg.ToolCallID != "" {
			line += " tool_call_id=" + msg.ToolCallID
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatToolCallsForLog(toolCalls []schema.ToolCall) string {
	parts := make([]string, 0, len(toolCalls))
	for _, tc := range toolCalls {
		parts = append(parts, tc.Function.Name+"("+tc.Function.Arguments+")")
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func logCallbackEvents(t *testing.T, events []callbackEvent) {
	t.Helper()
	if len(events) == 0 {
		t.Log("callback events: none")
		return
	}
	for i, event := range events {
		t.Logf("callback[%d] agent=%s Depth=%d scope=%s event=%s component=%s name=%s type=%s payload=%q",
			i, event.agentName, event.agentDepth, event.scope, event.event, event.component, event.name, event.typ, event.payload)
	}
}
