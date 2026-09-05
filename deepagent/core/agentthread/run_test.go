package agentthread

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/core/constant"
	skillmw "eino-cli/deepagent/core/middleware/skill"
	"eino-cli/deepagent/core/middleware/subagent"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/mock/mock_model"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/mock/gomock"
)

type customInterruptInfo struct {
	Label string
}

type customInterruptState struct {
	Step int
}

func init() {
	schema.Register[*customInterruptInfo]()
	schema.Register[*customInterruptState]()
}

type testContextManager struct {
	mu         sync.Mutex
	messages   []*schema.Message
	tokenUsage int64
}

func (m *testContextManager) ReloadHistory(ctx context.Context) error {
	_ = ctx
	return nil
}

func (m *testContextManager) AddHistory(ctx context.Context, turnID string, msg ...*schema.Message) error {
	_ = ctx
	_ = turnID
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg...)
	return nil
}

func (m *testContextManager) History(ctx context.Context) []*schema.Message {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*schema.Message, len(m.messages))
	copy(out, m.messages)
	return out
}

func (m *testContextManager) ContextUsage() ContextUsageSnapshot {
	return ContextUsageSnapshot{CurrentTotal: m.tokenUsage}
}

func (m *testContextManager) RecordModelUsage(ctx context.Context, usage *model.TokenUsage) {
	_ = ctx
	if usage != nil {
		m.tokenUsage = int64(usage.TotalTokens)
	}
}

func (m *testContextManager) Compact(ctx context.Context, turnID string) (*ContextCompactedPayload, error) {
	_, _ = ctx, turnID
	return nil, nil
}

func (m *testContextManager) CompactNeeded(ctx context.Context) bool {
	_ = ctx
	return false
}

func NewSimpleTestContextManager() ContextManager {
	return &testContextManager{}
}

func NewNoopTestContextManager() ContextManager {
	return &testContextManager{}
}

func newTestRun(
	t *testing.T,
	cfg *TurnConfig,
	threadID string,
	turnID string,
	contextManager ContextManager,
	eventBus chan Event,
) (current *run) {
	t.Helper()
	thread := &DeepAgentThread{ThreadID: threadID, cm: contextManager}
	current, err := thread.buildRun(context.Background(), TurnStartRequest{TurnID: turnID}, cfg, eventBus)
	if err != nil {
		t.Fatalf("DeepAgentThread.buildRun() error = %v", err)
	}
	return current
}

func executeTestRun(ctx context.Context, current *run, input *Message, opts *ResumeTurnOptions) (err error) {
	current.input = input
	current.resume = copyResumeTurnOptions(opts)
	return current.execute(ctx)
}

// 简单 drain：在 run 返回后，等待一个短暂窗口收集事件
func drainEvents(ch <-chan Event, quiet time.Duration) []Event {
	deadline := time.NewTimer(quiet)
	defer deadline.Stop()
	out := make([]Event, 0, 16)
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
			if !deadline.Stop() {
				<-deadline.C
			}
			deadline.Reset(quiet)
		case <-deadline.C:
			return out
		}
	}
}

func waitEventType(t *testing.T, ch <-chan Event, want EventType) Event {
	t.Helper()
	timeout := time.After(time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == want {
				return ev
			}
		case <-timeout:
			t.Fatalf("timed out waiting for event %s", want)
		}
	}
}

func eventIndex(events []Event, want EventType) int {
	for i, ev := range events {
		if ev.Type == want {
			return i
		}
	}
	return -1
}

func eventTypeList(events []Event) []EventType {
	out := make([]EventType, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

func assertEventLoc(t *testing.T, ev Event, wantName string, wantDepth int) {
	t.Helper()
	if ev.Loc.AgentName != wantName || ev.Loc.AgentDepth != wantDepth {
		t.Fatalf("unexpected event loc for %s: got=%+v want={AgentName:%q AgentDepth:%d}", ev.Type, ev.Loc, wantName, wantDepth)
	}
}

func TestRunDropsLateEventAfterEventBusClosed(t *testing.T) {
	events := newTurnEventRecorder(&TurnConfig{}, "thread-drop", "turn-drop", NewSimpleTestContextManager(), make(chan Event))
	events.close()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("emitEvent panicked for late event after event bus closed: %v", recovered)
		}
	}()
	events.emit(context.Background(), EventToolEnd, ToolEndPayload{Name: "late_tool"})
}

func TestNewRunReturnsBuiltAgent(t *testing.T) {
	chatModel := mock_model.NewMockToolCallingChatModel(gomock.NewController(t))
	runner := newTestRun(
		t,
		&TurnConfig{Agent: deepagents.Config{
			Model:           chatModel,
			CheckpointStore: checkpointer.NewInMemoryStore(),
		}},
		"thread-built",
		"turn-built",
		NewSimpleTestContextManager(),
		make(chan Event, 8),
	)
	if runner.agent == nil {
		t.Fatal("buildRun() returned without a built agent")
	}
}

func TestRunRunsBuiltAgent(t *testing.T) {
	ctx := context.Background()
	chatModel := mock_model.NewMockToolCallingChatModel(gomock.NewController(t))
	chatModel.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			stream, writer := schema.Pipe[*schema.Message](1)
			writer.Send(schema.AssistantMessage("done", nil), nil)
			writer.Close()
			return stream, nil
		},
	)
	runner := newTestRun(
		t,
		&TurnConfig{
			Agent: deepagents.Config{
				Model: chatModel, CheckpointStore: checkpointer.NewInMemoryStore(),
			},
		},
		"thread-ready",
		"turn-ready",
		NewSimpleTestContextManager(),
		make(chan Event, 16),
	)

	err := executeTestRun(ctx, runner, schema.UserMessage("hello"), nil)
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
}

// 不使用自定义历史存储：Run 的 MemoryContextManager 在 rs=nil 下会跳过持久化

// 简单 invokable 工具
type fakeCounterTool struct{}
type fakeNamedCounterTool struct {
	name  string
	mu    sync.Mutex
	count int
}
type fakeCustomInterruptTool struct {
	name  string
	label string
	step  int
}

func (t *fakeCounterTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "counter",
		Desc: "increase counter",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"delta": {Type: schema.Integer, Desc: "value"},
		}),
	}, nil
}

func (t *fakeCounterTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return `{"ok":true}`, nil
}

func (t *fakeNamedCounterTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name,
		Desc: "named counter",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"delta": {Type: schema.Integer, Desc: "value"},
		}),
	}, nil
}

func (t *fakeNamedCounterTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	t.mu.Lock()
	t.count++
	t.mu.Unlock()
	return `{"ok":true}`, nil
}

func (t *fakeNamedCounterTool) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.count
}

func (t *fakeCustomInterruptTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name,
		Desc: "custom interrupt tool",
	}, nil
}

func (t *fakeCustomInterruptTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	return "", tool.StatefulInterrupt(ctx, &customInterruptInfo{Label: t.label}, &customInterruptState{Step: t.step})
}

type fakeStreamCounterTool struct{}
type fakeMultiChunkStreamTool struct{}

type fakeSkillLoader struct {
	skills []*skillmw.SkillMetadata
}

func (l *fakeSkillLoader) ListSkills(ctx context.Context) ([]*skillmw.SkillMetadata, error) {
	_ = ctx
	out := make([]*skillmw.SkillMetadata, 0, len(l.skills))
	for _, sk := range l.skills {
		if sk == nil {
			continue
		}
		copied := *sk
		out = append(out, &copied)
	}
	return out, nil
}

func (t *fakeStreamCounterTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "stream_counter",
		Desc: "stream counter",
	}, nil
}

func (t *fakeStreamCounterTool) StreamableRun(_ context.Context, _ string, _ ...tool.Option) (*schema.StreamReader[string], error) {
	sr, sw := schema.Pipe[string](2)
	go func() {
		defer sw.Close()
		sw.Send("part1-", nil)
		sw.Send("part2", nil)
	}()
	return sr, nil
}

func (t *fakeMultiChunkStreamTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "multi_chunk_stream",
		Desc: "emit four chunks",
	}, nil
}

func (t *fakeMultiChunkStreamTool) StreamableRun(_ context.Context, _ string, _ ...tool.Option) (*schema.StreamReader[string], error) {
	sr, sw := schema.Pipe[string](4)
	go func() {
		defer sw.Close()
		for i := 1; i <= 4; i++ {
			sw.Send(fmt.Sprintf("chunk-%d", i), nil)
			time.Sleep(2 * time.Millisecond)
		}
	}()
	return sr, nil
}

// 构建一个单轮执行器与其事件通道；withApproval 控制是否对 counter 需要审批
func newCounterRun(
	t *testing.T,
	ctrl *gomock.Controller,
	withApproval bool,
	cpStore *checkpointer.InMemoryStore,
	turnCompleted ...func(context.Context, string, string, model.ToolCallingChatModel, []*schema.Message),
) (*run, chan Event) {
	t.Helper()

	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	// 根据历史决定输出：若还未看到工具结果，则给出 tool_calls；否则给出 done
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()

			seenToolResult := false
			for i := len(msgs) - 1; i >= 0; i-- {
				m := msgs[i]
				if m != nil && m.Role == schema.Tool {
					seenToolResult = true
					break
				}
			}
			if !seenToolResult {
				tc := schema.ToolCall{ID: "tc-1", Type: "function", Function: schema.FunctionCall{Name: "counter", Arguments: `{"delta":1}`}}
				sw.Send(schema.AssistantMessage("call tool", []schema.ToolCall{tc}), nil)
			} else {
				sw.Send(schema.AssistantMessage("done", nil), nil)
			}
			return sr, nil
		},
	).AnyTimes()

	// 工具 & HITL
	tools := []tool.BaseTool{&fakeCounterTool{}}
	var hitl *deepagents.HITLConfig
	if withApproval {
		hitl = &deepagents.HITLConfig{ToolPolicyGates: map[string]deeptools.ToolPolicyGate{
			"counter": deeptools.ApprovalGate(func(context.Context, *deeptools.ApprovalInfo) bool { return true }),
		}}
	}

	threadID, turnID := "thread-agt", "r1"
	// 使用最小测试 context manager，避免依赖旧的多轮上下文接口
	cmw := NewSimpleTestContextManager()
	evBus := make(chan Event, 256)

	cfg := &TurnConfig{
		Agent: deepagents.Config{
			Model:           cm,
			Tools:           tools,
			CheckpointStore: cpStore,
			HITLConfig:      hitl,
		},
	}
	if len(turnCompleted) > 0 {
		cfg.TurnCompleted = turnCompleted[0]
	}

	ag := newTestRun(t, cfg, threadID, turnID, cmw, evBus)
	return ag, evBus
}

func buildCustomInterruptAgent(t *testing.T, ctrl *gomock.Controller, cpStore *checkpointer.InMemoryStore) (*run, chan Event) {
	t.Helper()

	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()

			seenToolResult := false
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i] != nil && msgs[i].Role == schema.Tool {
					seenToolResult = true
					break
				}
			}
			if !seenToolResult {
				tc := schema.ToolCall{
					ID:       "custom-call-1",
					Type:     "function",
					Function: schema.FunctionCall{Name: "custom_interrupt", Arguments: `{"step":1}`},
				}
				sw.Send(schema.AssistantMessage("call custom tool", []schema.ToolCall{tc}), nil)
			} else {
				sw.Send(schema.AssistantMessage("done", nil), nil)
			}
			return sr, nil
		},
	).AnyTimes()

	evBus := make(chan Event, 64)
	ag := newTestRun(t, &TurnConfig{
		Agent: deepagents.Config{
			Model:           cm,
			Tools:           []tool.BaseTool{&fakeCustomInterruptTool{name: "custom_interrupt", label: "need custom input", step: 1}},
			CheckpointStore: cpStore,
		},
	}, "thread-custom", "r1", NewNoopTestContextManager(), evBus)
	return ag, evBus
}

func buildMixedInterruptAgent(t *testing.T, ctrl *gomock.Controller, cpStore *checkpointer.InMemoryStore) (*run, chan Event) {
	t.Helper()

	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()

			seenToolResult := false
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i] != nil && msgs[i].Role == schema.Tool {
					seenToolResult = true
					break
				}
			}
			if !seenToolResult {
				callIdx0 := 0
				callIdx1 := 1
				sw.Send(&schema.Message{
					Role: schema.Assistant,
					ToolCalls: []schema.ToolCall{
						{
							ID:    "approve-call-1",
							Index: &callIdx0,
							Type:  "function",
							Function: schema.FunctionCall{
								Name:      "counter",
								Arguments: `{"delta":1}`,
							},
						},
						{
							ID:    "custom-call-1",
							Index: &callIdx1,
							Type:  "function",
							Function: schema.FunctionCall{
								Name:      "custom_interrupt",
								Arguments: `{"step":2}`,
							},
						},
					},
				}, nil)
			} else {
				sw.Send(schema.AssistantMessage("done", nil), nil)
			}
			return sr, nil
		},
	).AnyTimes()

	evBus := make(chan Event, 64)
	ag := newTestRun(t, &TurnConfig{
		Agent: deepagents.Config{
			Model:                cm,
			Tools:                []tool.BaseTool{&fakeCounterTool{}, &fakeCustomInterruptTool{name: "custom_interrupt", label: "need custom input", step: 2}},
			CheckpointStore:      cpStore,
			EnableStreamToolCall: true,
			HITLConfig: &deepagents.HITLConfig{ToolPolicyGates: map[string]deeptools.ToolPolicyGate{
				"counter": deeptools.ApprovalGate(func(context.Context, *deeptools.ApprovalInfo) bool { return true }),
			}},
		},
	}, "thread-batch", "r1", NewNoopTestContextManager(), evBus)
	return ag, evBus
}

func TestRun_InitAppliesToolMask(t *testing.T) {
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).DoAndReturn(
		func(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
			var names []string
			for _, info := range infos {
				if info == nil {
					continue
				}
				names = append(names, info.Name)
				if info.Name == "counter" {
					t.Fatalf("expected counter to be masked in turn runner init, got %+v", infos)
				}
			}
			if len(names) == 0 {
				t.Fatalf("expected builtin tools to remain after masking")
			}
			return cm, nil
		},
	).Times(1)

	newTestRun(t, &TurnConfig{
		EnablePlan: true,
		Agent: deepagents.Config{
			Model:           cm,
			Tools:           []tool.BaseTool{&fakeCounterTool{}},
			CheckpointStore: checkpointer.NewInMemoryStore(),
			ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
				return info.Name != "counter"
			},
		},
	}, "thread-mask", "turn-mask", NewSimpleTestContextManager(), make(chan Event, 8))
}

func TestRun_PlanMiddlewareEmitsPlanUpdated(t *testing.T) {
	ctx := context.Background()
	controller := gomock.NewController(t)
	chatModel := mock_model.NewMockToolCallingChatModel(controller)
	chatModel.EXPECT().WithTools(gomock.Any()).Return(chatModel, nil)
	modelCalls := 0
	chatModel.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			modelCalls++
			stream, writer := schema.Pipe[*schema.Message](1)
			defer writer.Close()
			if modelCalls == 1 {
				writer.Send(schema.AssistantMessage("update plan", []schema.ToolCall{{
					ID:   "plan-call",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      constant.ToolUpdatePlan,
						Arguments: `{"explanation":"sync progress","plan":[{"step":"Inspect","status":"completed"},{"step":"Implement","status":"in_progress"}]}`,
					},
				}}), nil)
			} else {
				writer.Send(schema.AssistantMessage("done", nil), nil)
			}
			return stream, nil
		},
	).Times(2)

	bus := make(chan Event, 32)
	ag := newTestRun(t, &TurnConfig{
		Agent: deepagents.Config{
			Model:           chatModel,
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
		EnablePlan: true,
	}, "thread-plan-v2", "turn-plan-v2", NewSimpleTestContextManager(), bus)
	if err := executeTestRun(ctx, ag, schema.UserMessage("plan this"), nil); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	event := waitEventType(t, bus, EventPlanUpdated)
	if event.ThreadID != "thread-plan-v2" || event.TurnID != "turn-plan-v2" {
		t.Fatalf("unexpected event ids: thread=%s turn=%s", event.ThreadID, event.TurnID)
	}
	payload, ok := event.Payload.(PlanUpdatedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want PlanUpdatedPayload", event.Payload)
	}
	if payload.Explanation != "sync progress" {
		t.Fatalf("explanation = %q", payload.Explanation)
	}
	if len(payload.Plan) != 2 || payload.Plan[1].Status != PlanStepStatusInProgress {
		t.Fatalf("unexpected plan payload: %+v", payload.Plan)
	}
}

func TestRun_InitInjectsPlanTool(t *testing.T) {
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).DoAndReturn(
		func(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
			for _, info := range infos {
				if info != nil && info.Name == constant.ToolUpdatePlan {
					return cm, nil
				}
			}
			t.Fatalf("expected update_plan tool, got %+v", infos)
			return cm, nil
		},
	).Times(1)

	newTestRun(t, &TurnConfig{
		Agent: deepagents.Config{
			Model: cm,

			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
		EnablePlan: true,
	}, "thread-plan-v2-init", "turn-plan-v2-init", NewSimpleTestContextManager(), make(chan Event, 8))
}

func TestRun_Callbacks_ToolStartWaitsForLLMEndWhenStreamToolCallDisabled(t *testing.T) {
	ctx := context.Background()
	bus := make(chan Event, 32)
	events := newTurnEventRecorder(&TurnConfig{}, "thread-order", "turn-order", NewSimpleTestContextManager(), bus)
	handler := events.callbackHandler()
	modelInfo := &callbacks.RunInfo{Name: "executor", Component: components.ComponentOfChatModel}
	toolInfo := &callbacks.RunInfo{Name: "glob", Component: components.ComponentOfTool}

	handler.OnStart(ctx, modelInfo, &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hi")}})
	stream, writer := schema.Pipe[callbacks.CallbackOutput](2)
	handler.OnEndWithStreamOutput(ctx, modelInfo, stream)
	writer.Send(&model.CallbackOutput{
		Message: &schema.Message{Role: schema.Assistant, ReasoningContent: "thinking"},
	}, nil)

	seen := []Event{
		waitEventType(t, bus, EventLLMRequesting),
		waitEventType(t, bus, EventLLMToken),
	}

	toolStarted := make(chan struct{})
	go func() {
		handler.OnStart(ctx, toolInfo, &tool.CallbackInput{ArgumentsInJSON: `{"path":"/tmp","pattern":"*.mp4"}`})
		close(toolStarted)
	}()

	select {
	case <-toolStarted:
		writer.Close()
		t.Fatalf("tool start callback completed before llm_end entered the event queue")
	case <-time.After(20 * time.Millisecond):
	}

	tc := schema.ToolCall{ID: "call-glob", Type: "function", Function: schema.FunctionCall{Name: "glob", Arguments: `{"path":"/tmp","pattern":"*.mp4"}`}}
	writer.Send(&model.CallbackOutput{
		Message: schema.AssistantMessage("call glob", []schema.ToolCall{tc}),
	}, nil)
	writer.Close()

	select {
	case <-toolStarted:
	case <-time.After(time.Second):
		t.Fatalf("tool start callback did not resume after llm_end")
	}
	seen = append(seen, waitEventType(t, bus, EventLLMEnd))
	seen = append(seen, waitEventType(t, bus, EventToolStart))

	llmEndIdx := eventIndex(seen, EventLLMEnd)
	toolStartIdx := eventIndex(seen, EventToolStart)
	if llmEndIdx == -1 || toolStartIdx == -1 || llmEndIdx > toolStartIdx {
		t.Fatalf("llm_end should precede tool_start, got events=%v", eventTypeList(seen))
	}
}

func TestRun_Callbacks_StreamToolCallDoesNotWaitForLLMEnd(t *testing.T) {
	ctx := context.Background()
	bus := make(chan Event, 32)
	events := newTurnEventRecorder(&TurnConfig{
		Agent: deepagents.Config{
			EnableStreamToolCall: true,
		},
	}, "thread-stream-order", "turn-stream-order", NewSimpleTestContextManager(), bus)
	handler := events.callbackHandler()
	modelInfo := &callbacks.RunInfo{Name: "executor", Component: components.ComponentOfChatModel}
	toolInfo := &callbacks.RunInfo{Name: "glob", Component: components.ComponentOfTool}

	handler.OnStart(ctx, modelInfo, &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hi")}})
	stream, writer := schema.Pipe[callbacks.CallbackOutput](2)
	handler.OnEndWithStreamOutput(ctx, modelInfo, stream)
	writer.Send(&model.CallbackOutput{
		Message: &schema.Message{Role: schema.Assistant, ReasoningContent: "thinking"},
	}, nil)

	_ = waitEventType(t, bus, EventLLMRequesting)
	_ = waitEventType(t, bus, EventLLMToken)

	toolStarted := make(chan struct{})
	go func() {
		handler.OnStart(ctx, toolInfo, &tool.CallbackInput{ArgumentsInJSON: `{"path":"/tmp","pattern":"*.mp4"}`})
		close(toolStarted)
	}()

	select {
	case <-toolStarted:
	case <-time.After(time.Second):
		writer.Close()
		t.Fatalf("stream tool call mode should not wait for llm_end before tool_start")
	}
	_ = waitEventType(t, bus, EventToolStart)

	writer.Close()
	_ = waitEventType(t, bus, EventLLMEnd)
}

// 用例一：正常（工具调用→完成）
func TestAgentRunner_NormalToolThenDone(t *testing.T) {
	ctrl := gomock.NewController(t)
	cp := checkpointer.NewInMemoryStore()
	ag, bus := newCounterRun(t, ctrl, false, cp)

	ctx := context.Background()
	input := schema.UserMessage("hi")
	opts := &ResumeTurnOptions{CheckpointID: "thread-agt:r1"}
	if err := executeTestRun(ctx, ag, input, opts); err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}

	// 收集事件序列（在 RunTurn 返回后短暂等待）
	got := drainEvents(bus, 20*time.Millisecond)
	if len(got) == 0 {
		t.Fatalf("未捕获到任何事件")
	}
	has := func(tp EventType) bool {
		for _, ev := range got {
			if ev.Type == tp {
				return true
			}
		}
		return false
	}
	// 顺序与关键节点校验
	if got[0].Type != EventTurnStart {
		t.Fatalf("首事件应为 turn_start，got=%v", got[0].Type)
	}
	turnStart, ok := got[0].Payload.(TurnStartPayload)
	if !ok {
		t.Fatalf("turn_start payload type mismatch: %T", got[0].Payload)
	}
	if turnStart.Input == nil || turnStart.Input.Content != "hi" {
		t.Fatalf("turn_start payload missing user input: %+v", turnStart)
	}
	// 对 llm_response 不做强依赖（不同实现下可能只发 token 与 turn_end）
	if !(has(EventTurnStart) && has(EventLLMRequesting) && has(EventToolStart) && has(EventToolEnd) && has(EventTurnEnd)) {
		t.Fatalf("事件缺失，got=%v", got)
	}
	requestIdx, tokenIdx, endIdx := -1, -1, -1
	for i, ev := range got {
		switch ev.Type {
		case EventLLMRequesting:
			if requestIdx == -1 {
				requestIdx = i
			}
			p, ok := ev.Payload.(*model.CallbackInput)
			if !ok {
				t.Fatalf("llm requesting payload type mismatch: %T", ev.Payload)
			}
			if p == nil || !callbackInputHasUserContent(p, "hi") {
				t.Fatalf("llm requesting payload missing input messages: %+v", p)
			}
		case EventLLMToken:
			if tokenIdx == -1 {
				tokenIdx = i
			}
		case EventLLMEnd:
			if endIdx == -1 {
				endIdx = i
			}
		}
	}
	if requestIdx == -1 {
		t.Fatalf("missing llm requesting event: %+v", got)
	}
	if tokenIdx != -1 && requestIdx > tokenIdx {
		t.Fatalf("llm requesting should happen before token: request=%d token=%d", requestIdx, tokenIdx)
	}
	if endIdx != -1 && requestIdx > endIdx {
		t.Fatalf("llm requesting should happen before end: request=%d end=%d", requestIdx, endIdx)
	}
	for _, ev := range got {
		assertEventLoc(t, ev, constant.GraphName, 0)
	}
}

func TestRunCallsTurnCompletedWithPersistedHistory(t *testing.T) {
	ctrl := gomock.NewController(t)
	called := make(chan []*schema.Message, 1)
	runner, _ := newCounterRun(t, ctrl, false, checkpointer.NewInMemoryStore(), func(ctx context.Context, threadID, turnID string, chatModel model.ToolCallingChatModel, history []*schema.Message) {
		if threadID != "thread-agt" || turnID != "r1" || chatModel == nil {
			t.Errorf("TurnCompleted identity thread=%q turn=%q model=%v", threadID, turnID, chatModel)
		}
		called <- history
	})
	if err := executeTestRun(context.Background(), runner, schema.UserMessage("remember this"), &ResumeTurnOptions{CheckpointID: "thread-agt:r1"}); err != nil {
		t.Fatalf("RunTurn() error=%v", err)
	}
	select {
	case history := <-called:
		if len(history) < 3 || history[0].Content != "remember this" || history[len(history)-1].Content != "done" {
			t.Fatalf("TurnCompleted history=%+v", history)
		}
	case <-time.After(time.Second):
		t.Fatal("TurnCompleted was not called")
	}
}

func TestAgentRunner_UnknownToolCallReturnsToolResult(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()
	var sawUnknownToolResult bool
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()

			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i] != nil && msgs[i].Role == schema.Tool {
					if msgs[i].ToolCallID == "missing-1" && strings.Contains(msgs[i].Content, `Tool "missing_tool" is not available`) {
						sawUnknownToolResult = true
					}
					sw.Send(schema.AssistantMessage("recovered", nil), nil)
					return sr, nil
				}
			}
			tc := schema.ToolCall{ID: "missing-1", Type: "function", Function: schema.FunctionCall{Name: "missing_tool", Arguments: `{}`}}
			sw.Send(schema.AssistantMessage("call missing", []schema.ToolCall{tc}), nil)
			return sr, nil
		},
	).AnyTimes()

	bus := make(chan Event, 64)
	ag := newTestRun(t, &TurnConfig{
		Agent: deepagents.Config{
			Model:           cm,
			Tools:           []tool.BaseTool{&fakeCounterTool{}},
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}, "thread-unknown-tool", "turn-unknown-tool", NewSimpleTestContextManager(), bus)
	if err := executeTestRun(ctx, ag, schema.UserMessage("hi"), &ResumeTurnOptions{CheckpointID: "thread-unknown-tool:turn-unknown-tool"}); err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}

	if !sawUnknownToolResult {
		t.Fatalf("model did not receive unknown tool result")
	}
}

// 用例二：工具需要审批 → 首次中断 → 恢复继续到结束
func TestAgentRunner_ApproveInterruptAndResume(t *testing.T) {
	ctrl := gomock.NewController(t)
	cp := checkpointer.NewInMemoryStore()
	ag, bus := newCounterRun(t, ctrl, true, cp)

	ctx := context.Background()
	// 首次运行，预期触发审批中断
	err := executeTestRun(ctx, ag, schema.UserMessage("start"), &ResumeTurnOptions{CheckpointID: "thread-agt:r1"})
	if err != nil {
		t.Fatalf("预期首次运行通过事件表面化中断，但返回 err=%v", err)
	}

	// 收集事件，捕获审批请求
	seq1 := drainEvents(bus, 20*time.Millisecond)
	var p ApprovalRequiredPayload
	found := false
	for _, ev := range seq1 {
		if ev.Type == EventLLMRequesting {
			if _, ok := ev.Payload.(*model.CallbackInput); !ok {
				t.Fatalf("llm requesting payload type mismatch: %T", ev.Payload)
			}
		}
		if ev.Type == EventApproveRequested {
			p, _ = ev.Payload.(ApprovalRequiredPayload)
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("未收到 approve_requested 事件, seq=%v", seq1)
	}
	if p.InterruptID == "" || p.CheckpointID == "" {
		t.Fatalf("审批事件缺少必要字段: %+v", p)
	}

	// 重新构建 Run 并恢复运行
	ag2, bus2 := newCounterRun(t, ctrl, true, cp)

	resume := map[string]any{p.InterruptID: &deeptools.ApprovalResult{Approved: true}}
	if err := executeTestRun(ctx, ag2, nil, &ResumeTurnOptions{CheckpointID: p.CheckpointID, ResumeData: resume, ResumeInterruptIDs: []string{p.InterruptID}}); err != nil {
		t.Fatalf("恢复运行失败: %v", err)
	}

	// 恢复阶段事件
	got := drainEvents(bus2, 20*time.Millisecond)
	has := func(tp EventType) bool {
		for _, ev := range got {
			if ev.Type == tp {
				return true
			}
		}
		return false
	}
	// 对 llm_response 不做强依赖
	if !(has(EventLLMRequesting) && has(EventToolEnd) && has(EventTurnEnd)) {
		t.Fatalf("恢复阶段关键事件缺失，got=%v", got)
	}
}

func TestAgentRunner_SingleCustomInterruptUsesInterruptedPayloadInfo(t *testing.T) {
	ctrl := gomock.NewController(t)
	cp := checkpointer.NewInMemoryStore()
	ag, evBus := buildCustomInterruptAgent(t, ctrl, cp)

	if err := executeTestRun(context.Background(), ag, schema.UserMessage("start"), &ResumeTurnOptions{CheckpointID: "thread-custom:r1"}); err != nil {
		t.Fatalf("expected eventized custom interrupt, got err=%v", err)
	}

	events := drainEvents(evBus, 20*time.Millisecond)
	var payload InterruptedPayload
	found := false
	for _, ev := range events {
		if ev.Type == EventInterrupted {
			payload, _ = ev.Payload.(InterruptedPayload)
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected interrupted event, got %v", events)
	}
	if payload.Source != "custom" {
		t.Fatalf("unexpected source: %+v", payload)
	}
	if payload.CheckpointID != "thread-custom:r1" || payload.InterruptID == "" {
		t.Fatalf("missing interrupt identity: %+v", payload)
	}
	gotInfo, ok := payload.Info.(*customInterruptInfo)
	if !ok {
		t.Fatalf("expected raw custom info, got %T", payload.Info)
	}
	if gotInfo.Label != "need custom input" {
		t.Fatalf("unexpected custom info: %+v", gotInfo)
	}
	if payload.InfoType != "*agentthread.customInterruptInfo" {
		t.Fatalf("unexpected info type: %s", payload.InfoType)
	}
}

func TestAgentRunner_MixedInterruptsEmitInterruptBatchRequested(t *testing.T) {
	ctrl := gomock.NewController(t)
	cp := checkpointer.NewInMemoryStore()
	ag, evBus := buildMixedInterruptAgent(t, ctrl, cp)

	if err := executeTestRun(context.Background(), ag, schema.UserMessage("start"), &ResumeTurnOptions{CheckpointID: "thread-batch:r1"}); err != nil {
		t.Fatalf("expected eventized interrupt batch, got err=%v", err)
	}

	events := drainEvents(evBus, 20*time.Millisecond)
	var payload InterruptBatchPayload
	found := false
	for _, ev := range events {
		if ev.Type == EventInterruptBatchRequested {
			payload, _ = ev.Payload.(InterruptBatchPayload)
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected interrupt batch event, got %v", events)
	}
	if payload.CheckpointID != "thread-batch:r1" {
		t.Fatalf("unexpected checkpoint id: %+v", payload)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("expected 2 batch items, got %+v", payload.Items)
	}

	var (
		sawApprove bool
		sawCustom  bool
	)
	for _, item := range payload.Items {
		switch item.Kind {
		case InterruptItemApprove:
			sawApprove = true
			if item.InterruptID == "" || item.ApprovalInfo == nil {
				t.Fatalf("approve item missing fields: %+v", item)
			}
			if item.ApprovalInfo.ToolName != "counter" {
				t.Fatalf("unexpected approval item: %+v", item)
			}
		case InterruptItemCustom:
			sawCustom = true
			gotInfo, ok := item.Info.(*customInterruptInfo)
			if !ok {
				t.Fatalf("expected custom info in batch item, got %T", item.Info)
			}
			if gotInfo.Label != "need custom input" {
				t.Fatalf("unexpected custom batch info: %+v", gotInfo)
			}
		default:
			t.Fatalf("unexpected interrupt item kind: %+v", item)
		}
	}
	if !sawApprove || !sawCustom {
		t.Fatalf("expected mixed batch items, got %+v", payload.Items)
	}
}

func TestAgentRunner_LLMRequestingCarriesRawCallbackInput(t *testing.T) {
	ctrl := gomock.NewController(t)
	cp := checkpointer.NewInMemoryStore()

	ctx := context.Background()
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()
			sw.Send(schema.AssistantMessage("done", nil), nil)
			return sr, nil
		},
	).AnyTimes()

	cmw := &testContextManager{tokenUsage: 123}
	evBus := make(chan Event, 256)
	ag := newTestRun(t, &TurnConfig{
		Agent: deepagents.Config{
			Model:           cm,
			CheckpointStore: cp,
		},
	}, "thread-req", "r1", cmw, evBus)
	if err := executeTestRun(ctx, ag, schema.UserMessage("hello"), &ResumeTurnOptions{CheckpointID: "thread-req:r1"}); err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}

	got := drainEvents(evBus, 20*time.Millisecond)
	var payload *model.CallbackInput
	var llmToken LLMTokenChunk
	var llmEnd LLMEnd
	requestIdx, tokenIdx, endIdx := -1, -1, -1
	for i, ev := range got {
		switch ev.Type {
		case EventLLMRequesting:
			if requestIdx == -1 {
				requestIdx = i
			}
			p, ok := ev.Payload.(*model.CallbackInput)
			if !ok {
				t.Fatalf("payload type mismatch: %T", ev.Payload)
			}
			payload = p
		case EventLLMToken:
			if tokenIdx == -1 {
				tokenIdx = i
			}
			p, ok := ev.Payload.(LLMTokenChunk)
			if !ok {
				t.Fatalf("llm token payload type mismatch: %T", ev.Payload)
			}
			llmToken = p
		case EventLLMEnd:
			if endIdx == -1 {
				endIdx = i
			}
			llmEnd, _ = ev.Payload.(LLMEnd)
		}
	}
	if payload == nil {
		t.Fatalf("missing llm requesting payload: %+v", got)
	}
	if llmEnd.Message == nil || llmEnd.Message.Content != "done" {
		t.Fatalf("unexpected llm end message: %+v", llmEnd)
	}
	if llmEnd.LLMResponseID == "" {
		t.Fatalf("missing llm response id in llm end: %+v", llmEnd)
	}
	if llmToken.LLMResponseID != "" && llmToken.LLMResponseID != llmEnd.LLMResponseID {
		t.Fatalf("llm response id mismatch: token=%q end=%q", llmToken.LLMResponseID, llmEnd.LLMResponseID)
	}
	if !callbackInputHasUserContent(payload, "hello") {
		t.Fatalf("unexpected callback messages: %+v", payload.Messages)
	}
	if payload.Config != nil && payload.Config.Model == "" {
		t.Fatalf("unexpected empty model config: %+v", payload.Config)
	}
	if requestIdx == -1 {
		t.Fatalf("missing llm requesting event")
	}
	if tokenIdx != -1 && requestIdx > tokenIdx {
		t.Fatalf("llm requesting should happen before token")
	}
	if endIdx != -1 && requestIdx > endIdx {
		t.Fatalf("llm requesting should happen before end")
	}
}

func TestMergeModelCallbackStreamBuildsLLMEnd(t *testing.T) {
	ctx := context.Background()
	idx := 0
	stream := schema.StreamReaderFromArray([]*model.CallbackOutput{
		{
			Message: &schema.Message{
				Role:             schema.Assistant,
				Content:          "hel",
				ReasoningContent: "think-",
				ToolCalls: []schema.ToolCall{{
					ID:    "call_1",
					Index: &idx,
					Function: schema.FunctionCall{
						Name:      "echo",
						Arguments: `{"value":"wo`,
					},
				}},
			},
			Config: &model.Config{Model: "model-a", Stop: []string{"stop-a"}},
			TokenUsage: &model.TokenUsage{
				PromptTokens:     2,
				CompletionTokens: 1,
				TotalTokens:      3,
			},
			Extra: map[string]any{"source": "first"},
		},
		{
			Message: &schema.Message{
				Role:             schema.Assistant,
				Content:          "lo",
				ReasoningContent: "done",
				ToolCalls: []schema.ToolCall{{
					Index: &idx,
					Function: schema.FunctionCall{
						Arguments: `rld"}`,
					},
				}},
				ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls"},
				Extra:        map[string]any{"request_id": "req_1"},
			},
			Config: &model.Config{Model: "model-b", Stop: []string{"stop-b"}},
			TokenUsage: &model.TokenUsage{
				CompletionTokens: 4,
				TotalTokens:      4,
				CompletionTokensDetails: model.CompletionTokensDetails{
					ReasoningTokens: 2,
				},
			},
			Extra: map[string]any{"source": "second"},
		},
	})

	var tokens []string
	var reasoningTokens []string
	got, chunkCount, err := mergeModelCallbackStream(ctx, stream, "llm-response-1", func(_ context.Context, chunk *schema.Message) {
		if chunk.Content != "" {
			tokens = append(tokens, chunk.Content)
		}
		if chunk.ReasoningContent != "" {
			reasoningTokens = append(reasoningTokens, chunk.ReasoningContent)
		}
	})
	if err != nil {
		t.Fatalf("mergeModelCallbackStream() error = %v", err)
	}
	if chunkCount != 2 {
		t.Fatalf("chunk count = %d, want 2", chunkCount)
	}
	if fmt.Sprint(tokens) != "[hel lo]" {
		t.Fatalf("tokens = %+v", tokens)
	}
	if fmt.Sprint(reasoningTokens) != "[think- done]" {
		t.Fatalf("reasoning tokens = %+v", reasoningTokens)
	}
	if got.Message == nil {
		t.Fatalf("missing merged message: %+v", got)
	}
	if got.Message.Content != "hello" {
		t.Fatalf("merged content = %q", got.Message.Content)
	}
	if got.Message.ReasoningContent != "think-done" {
		t.Fatalf("merged reasoning = %q", got.Message.ReasoningContent)
	}
	if len(got.Message.ToolCalls) != 1 || got.Message.ToolCalls[0].Function.Arguments != `{"value":"world"}` {
		t.Fatalf("merged tool calls = %+v", got.Message.ToolCalls)
	}
	if got.Message.ResponseMeta == nil || got.Message.ResponseMeta.FinishReason != "tool_calls" {
		t.Fatalf("missing final response meta: %+v", got.Message.ResponseMeta)
	}
	if got.Config == nil || got.Config.Model != "model-b" || len(got.Config.Stop) != 1 || got.Config.Stop[0] != "stop-b" {
		t.Fatalf("unexpected config: %+v", got.Config)
	}
	if got.TokenUsage == nil ||
		got.TokenUsage.PromptTokens != 0 ||
		got.TokenUsage.CompletionTokens != 4 ||
		got.TokenUsage.TotalTokens != 4 ||
		got.TokenUsage.CompletionTokensDetails.ReasoningTokens != 2 {
		t.Fatalf("unexpected token usage: %+v", got.TokenUsage)
	}
	if got.Extra["source"] != "second" {
		t.Fatalf("unexpected extra: %+v", got.Extra)
	}
	if got.LLMResponseID != "llm-response-1" {
		t.Fatalf("llm response id = %q", got.LLMResponseID)
	}
}

func callbackInputHasUserContent(input *model.CallbackInput, want string) bool {
	if input == nil {
		return false
	}
	for _, msg := range input.Messages {
		if msg != nil && msg.Role == schema.User && msg.Content == want {
			return true
		}
	}
	return false
}

// 开启 stream tool call 后，streamable tool 应该:
// 1. 继续发出 EventToolStart；
// 2. 对每个工具输出 chunk 发 EventToolCallOutputChunk；
// 3. 最终发一次 EventToolEnd，且 Result 是完整拼接结果。
func TestAgentRunner_StreamToolCall_StreamableToolEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](2)
			defer sw.Close()

			seenToolResult := false
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i] != nil && msgs[i].Role == schema.Tool {
					seenToolResult = true
					break
				}
			}
			if !seenToolResult {
				tc := schema.ToolCall{ID: "stream-call-1", Type: "function", Function: schema.FunctionCall{Name: "stream_counter", Arguments: `{"delta":1}`}}
				sw.Send(schema.AssistantMessage("call stream tool", []schema.ToolCall{tc}), nil)
			} else {
				sw.Send(schema.AssistantMessage("done", nil), nil)
			}
			return sr, nil
		},
	).AnyTimes()

	evBus := make(chan Event, 256)
	ag := newTestRun(t, &TurnConfig{
		Agent: deepagents.Config{
			Model:                cm,
			Tools:                []tool.BaseTool{&fakeStreamCounterTool{}},
			CheckpointStore:      checkpointer.NewInMemoryStore(),
			EnableStreamToolCall: true,
		},
	}, "thread-stream", "r1", NewNoopTestContextManager(), evBus)
	if err := executeTestRun(ctx, ag, schema.UserMessage("start"), &ResumeTurnOptions{CheckpointID: "thread-stream:r1"}); err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}

	got := drainEvents(evBus, 20*time.Millisecond)
	var (
		startPayload ToolStartPayload
		endPayload   ToolEndPayload
		chunks       []ToolCallOutputChunkPayload
	)
	for _, ev := range got {
		switch ev.Type {
		case EventToolStart:
			startPayload, _ = ev.Payload.(ToolStartPayload)
		case EventToolCallOutputChunk:
			if p, ok := ev.Payload.(ToolCallOutputChunkPayload); ok {
				chunks = append(chunks, p)
			}
		case EventToolEnd:
			endPayload, _ = ev.Payload.(ToolEndPayload)
		}
	}

	if startPayload.Name != "stream_counter" || startPayload.CallID != "stream-call-1" {
		t.Fatalf("unexpected tool start payload: %+v", startPayload)
	}
	if startPayload.Args != `{"delta":1}` {
		t.Fatalf("unexpected tool start args: %+v", startPayload)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 output chunks, got %+v", chunks)
	}
	if chunks[0].Chunk != "part1-" || chunks[1].Chunk != "part2" {
		t.Fatalf("unexpected output chunks: %+v", chunks)
	}
	if endPayload.Name != "stream_counter" || endPayload.CallID != "stream-call-1" {
		t.Fatalf("unexpected tool end payload: %+v", endPayload)
	}
	if endPayload.ToolStartTime.IsZero() {
		t.Fatalf("missing tool start time in tool end payload: %+v", endPayload)
	}
	if endPayload.ArgumentsInJSON != `{"delta":1}` {
		t.Fatalf("unexpected tool end arguments: %+v", endPayload)
	}
	if endPayload.Result != "part1-part2" {
		t.Fatalf("unexpected aggregated tool result: %s", endPayload.Result)
	}
	for _, ev := range got {
		assertEventLoc(t, ev, constant.GraphName, 0)
	}
}

func TestDeepAgent_Callbacks_StreamToolCallEnabled_ToolCallbacksStillFire_ManyConcurrentCalls(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](8)

			seenToolResult := false
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i] != nil && msgs[i].Role == schema.Tool {
					seenToolResult = true
					break
				}
			}
			go func() {
				defer sw.Close()
				if !seenToolResult {
					argChunks := [4]func(i int) string{
						func(i int) string { return fmt.Sprintf(`{"idx":%d,"pay`, i) },
						func(i int) string { return `load":"seg` },
						func(i int) string { return fmt.Sprintf(`-%d","step":`, i) },
						func(i int) string { return `4}` },
					}
					for chunkIdx := 0; chunkIdx < 4; chunkIdx++ {
						toolCalls := make([]schema.ToolCall, 0, 10)
						for i := 0; i < 10; i++ {
							idx := i
							fc := schema.FunctionCall{
								Arguments: argChunks[chunkIdx](i),
							}
							if chunkIdx == 0 {
								fc.Name = "multi_chunk_stream"
							}
							toolCalls = append(toolCalls, schema.ToolCall{
								ID:       fmt.Sprintf("stream-call-%d", i),
								Index:    &idx,
								Type:     "function",
								Function: fc,
							})
						}
						sw.Send(&schema.Message{
							Role:      schema.Assistant,
							Content:   fmt.Sprintf("tool-call-batch-%d", chunkIdx),
							ToolCalls: toolCalls,
						}, nil)
						time.Sleep(2 * time.Millisecond)
					}
					return
				}
				sw.Send(schema.AssistantMessage("done", nil), nil)
			}()
			return sr, nil
		},
	).AnyTimes()

	evBus := make(chan Event, 2048)
	ag := newTestRun(t, &TurnConfig{
		Agent: deepagents.Config{
			Model:                cm,
			Tools:                []tool.BaseTool{&fakeMultiChunkStreamTool{}},
			CheckpointStore:      checkpointer.NewInMemoryStore(),
			EnableStreamToolCall: true,
		},
	}, "thread-stream-many", "r1", NewNoopTestContextManager(), evBus)
	if err := executeTestRun(ctx, ag, schema.UserMessage("start"), &ResumeTurnOptions{CheckpointID: "thread-stream-many:r1"}); err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}

	got := drainEvents(evBus, 20*time.Millisecond)
	startByCallID := make(map[string]ToolStartPayload)
	endByCallID := make(map[string]ToolEndPayload)
	chunksByCallID := make(map[string][]string)
	for _, ev := range got {
		switch ev.Type {
		case EventToolStart:
			if p, ok := ev.Payload.(ToolStartPayload); ok {
				startByCallID[p.CallID] = p
			}
		case EventToolCallOutputChunk:
			if p, ok := ev.Payload.(ToolCallOutputChunkPayload); ok {
				chunksByCallID[p.CallID] = append(chunksByCallID[p.CallID], p.Chunk)
			}
		case EventToolEnd:
			if p, ok := ev.Payload.(ToolEndPayload); ok {
				endByCallID[p.CallID] = p
			}
		}
	}

	if len(startByCallID) != 10 {
		t.Fatalf("expected 10 tool starts, got %d, events=%+v", len(startByCallID), got)
	}
	if len(endByCallID) != 10 {
		t.Fatalf("expected 10 tool ends, got %d, events=%+v", len(endByCallID), got)
	}
	if len(chunksByCallID) != 10 {
		t.Fatalf("expected chunks for 10 call IDs, got %d, events=%+v", len(chunksByCallID), got)
	}

	for i := 0; i < 10; i++ {
		callID := fmt.Sprintf("stream-call-%d", i)
		startPayload, ok := startByCallID[callID]
		if !ok {
			t.Fatalf("missing tool start for %s", callID)
		}
		if startPayload.Name != "multi_chunk_stream" {
			t.Fatalf("unexpected tool start payload for %s: %+v", callID, startPayload)
		}

		chunks := chunksByCallID[callID]
		if len(chunks) != 4 {
			t.Fatalf("expected 4 chunks for %s, got %+v", callID, chunks)
		}
		for idx, chunk := range chunks {
			want := fmt.Sprintf("chunk-%d", idx+1)
			if chunk != want {
				t.Fatalf("unexpected chunk order for %s: got %+v", callID, chunks)
			}
		}

		endPayload, ok := endByCallID[callID]
		if !ok {
			t.Fatalf("missing tool end for %s", callID)
		}
		if endPayload.Name != "multi_chunk_stream" {
			t.Fatalf("unexpected tool end payload for %s: %+v", callID, endPayload)
		}
		if endPayload.Result != "chunk-1chunk-2chunk-3chunk-4" {
			t.Fatalf("unexpected aggregated result for %s: %+v", callID, endPayload)
		}
	}

	var totalChunkEvents int
	for _, chunks := range chunksByCallID {
		totalChunkEvents += len(chunks)
	}
	if totalChunkEvents != 40 {
		t.Fatalf("expected 40 output chunk events, got %d", totalChunkEvents)
	}

	for _, ev := range got {
		assertEventLoc(t, ev, constant.GraphName, 0)
	}
}

func TestAgentRunner_SubAgentEventsCarryChildLoc(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mainCM := mock_model.NewMockToolCallingChatModel(ctrl)
	subCM := mock_model.NewMockToolCallingChatModel(ctrl)
	mainCM.EXPECT().WithTools(gomock.Any()).Return(mainCM, nil).AnyTimes()
	subCM.EXPECT().WithTools(gomock.Any()).Return(subCM, nil).AnyTimes()

	mainCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()

			seenToolResult := false
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i] != nil && msgs[i].Role == schema.Tool {
					seenToolResult = true
					break
				}
			}
			if !seenToolResult {
				tc := schema.ToolCall{
					ID:   "task-call-1",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "task",
						Arguments: `{"subagent":"child_agent","task":"child-task","wait_for_done":true}`,
					},
				}
				sw.Send(schema.AssistantMessage("delegate", []schema.ToolCall{tc}), nil)
			} else {
				sw.Send(schema.AssistantMessage("main-done", nil), nil)
			}
			return sr, nil
		},
	).AnyTimes()

	subCM.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			return schema.AssistantMessage("child-done", nil), nil
		},
	).AnyTimes()

	evBus := make(chan Event, 256)
	ag := newTestRun(t, &TurnConfig{
		Agent: deepagents.Config{
			Model:           mainCM,
			CheckpointStore: checkpointer.NewInMemoryStore(),
			SubAgents: []*subagent.SubAgent{
				{
					Name:         "child_agent",
					Description:  "child test agent",
					SystemPrompt: "you are child",
					Model:        subCM,
					MaxSteps:     4,
				},
			},
		},
	}, "thread-sub", "r1", NewNoopTestContextManager(), evBus)
	if err := executeTestRun(ctx, ag, schema.UserMessage("start"), &ResumeTurnOptions{CheckpointID: "thread-sub:r1"}); err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}

	got := drainEvents(evBus, 20*time.Millisecond)
	if len(got) == 0 {
		t.Fatalf("未捕获到任何事件")
	}

	var (
		taskStart     *Event
		childLLM      *Event
		parentTurnEnd *Event
	)
	for i := range got {
		ev := &got[i]
		switch ev.Type {
		case EventToolStart:
			if payload, ok := ev.Payload.(ToolStartPayload); ok && payload.Name == "task" {
				taskStart = ev
			}
		case EventLLMEnd:
			if payload, ok := ev.Payload.(LLMEnd); ok && payload.Message != nil && payload.Message.Content == "child-done" {
				childLLM = ev
			}
		case EventTurnEnd:
			parentTurnEnd = ev
		}
	}

	if taskStart == nil {
		t.Fatalf("未收到 task tool_start 事件, got=%v", got)
	}
	assertEventLoc(t, *taskStart, constant.GraphName, 0)

	if childLLM == nil {
		t.Fatalf("未收到 child llm_end 事件, got=%v", got)
	}
	assertEventLoc(t, *childLLM, constant.GraphName, 1)

	if parentTurnEnd == nil {
		t.Fatalf("未收到 turn_end 事件, got=%v", got)
	}
	assertEventLoc(t, *parentTurnEnd, constant.GraphName, 0)
}

// TestToolEventMiddleware_ResumeNoDuplicateEvents 验证 resume 后不会重复发送已执行工具的事件。
// 场景：工具触发审批中断 → interrupt → 序列化 → 新 runner resume → 回调重放。
// 期望：resume 阶段不应出现重复的 EventToolStart（已在中断前发过）。
func TestToolEventMiddleware_ResumeNoDuplicateEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	cp := checkpointer.NewInMemoryStore()
	ag, bus := newCounterRun(t, ctrl, true, cp)

	ctx := context.Background()
	// 首次运行：工具需审批 → 中断
	err := executeTestRun(ctx, ag, schema.UserMessage("do something"), &ResumeTurnOptions{CheckpointID: "thread-dedup:r1"})
	if err != nil {
		t.Fatalf("首次 RunTurn 应通过事件表面化中断，err=%v", err)
	}

	seq1 := drainEvents(bus, 20*time.Millisecond)

	// 统计首次运行的 tool_start 次数
	toolStartCount1 := 0
	var approvePayload ApprovalRequiredPayload
	for _, ev := range seq1 {
		if ev.Type == EventToolStart {
			toolStartCount1++
		}
		if ev.Type == EventApproveRequested {
			approvePayload, _ = ev.Payload.(ApprovalRequiredPayload)
		}
	}
	if toolStartCount1 != 1 {
		t.Fatalf("首次运行应有 1 个 tool_start 事件，got=%d, events=%v", toolStartCount1, seq1)
	}
	if approvePayload.InterruptID == "" {
		t.Fatalf("未收到 approve_requested 事件, seq=%v", seq1)
	}

	// Resume：新 runner 使用相同 checkpoint store
	ag2, bus2 := newCounterRun(t, ctrl, true, cp)
	resume := map[string]any{approvePayload.InterruptID: &deeptools.ApprovalResult{Approved: true}}
	err = executeTestRun(ctx, ag2, nil, &ResumeTurnOptions{
		CheckpointID:       approvePayload.CheckpointID,
		ResumeData:         resume,
		ResumeInterruptIDs: []string{approvePayload.InterruptID},
	})
	if err != nil {
		t.Fatalf("恢复运行失败: %v", err)
	}

	seq2 := drainEvents(bus2, 20*time.Millisecond)

	// 核心断言：resume 阶段不应有重复的 tool_start
	toolStartCount2 := 0
	toolEndCount2 := 0
	for _, ev := range seq2 {
		if ev.Type == EventToolStart {
			toolStartCount2++
		}
		if ev.Type == EventToolEnd {
			toolEndCount2++
		}
	}
	if toolStartCount2 > 0 {
		t.Errorf("resume 阶段不应重复发送 tool_start 事件，got=%d, events=%v", toolStartCount2, seq2)
	}
	if toolEndCount2 != 1 {
		t.Errorf("resume 阶段应有 1 个 tool_end 事件，got=%d, events=%v", toolEndCount2, seq2)
	}
}

func TestEventLocationFromContext_NoAgentReturnsZeroValue(t *testing.T) {
	got := eventLocationFromContext(context.Background())
	if got != (EventLocation{}) {
		t.Fatalf("unexpected event location: %+v", got)
	}
}
