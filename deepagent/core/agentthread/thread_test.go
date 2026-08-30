package agentthread

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"eino-cli/deepagent/core"
	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/subagent"
	deeptools "eino-cli/deepagent/core/tools"
	"github.com/cloudwego/eino/callbacks"
	modelcomp "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type threadScriptedModel struct {
	mu       sync.Mutex
	calls    [][]*schema.Message
	tools    [][]string
	handlers []func(context.Context, []*schema.Message) (*schema.Message, error)
}

func (m *threadScriptedModel) Generate(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.Message, error) {
	_ = opts
	call, handler := m.recordCall(input)
	_ = call
	if handler == nil {
		return schema.AssistantMessage("done", nil), nil
	}
	return handler(ctx, input)
}

func (m *threadScriptedModel) Stream(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.StreamReader[*schema.Message], error) {
	_ = opts
	call, handler := m.recordCall(input)
	reader, writer := schema.Pipe[*schema.Message](8)
	go func() {
		defer writer.Close()
		var (
			msg *schema.Message
			err error
		)
		if handler == nil {
			msg = schema.AssistantMessage("done", nil)
		} else {
			msg, err = handler(ctx, input)
		}
		if err != nil {
			writer.Send(nil, err)
			return
		}
		if msg == nil {
			msg = schema.AssistantMessage("done", nil)
		}
		msg.Content = msg.Content + call
		writer.Send(msg, nil)
	}()
	return reader, nil
}

func (m *threadScriptedModel) WithTools(tools []*schema.ToolInfo) (modelcomp.ToolCallingChatModel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(tools))
	for _, info := range tools {
		if info == nil {
			continue
		}
		names = append(names, info.Name)
	}
	m.tools = append(m.tools, names)
	return m, nil
}

func (m *threadScriptedModel) recordCall(input []*schema.Message) (string, func(context.Context, []*schema.Message) (*schema.Message, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]*schema.Message, len(input))
	copy(copied, input)
	m.calls = append(m.calls, copied)
	idx := len(m.calls) - 1
	if idx >= len(m.handlers) {
		return "", nil
	}
	return "", m.handlers[idx]
}

func (m *threadScriptedModel) snapshotCalls() [][]*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]*schema.Message, len(m.calls))
	for i := range m.calls {
		out[i] = append([]*schema.Message(nil), m.calls[i]...)
	}
	return out
}

func (m *threadScriptedModel) snapshotToolBindings() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]string, len(m.tools))
	for i := range m.tools {
		out[i] = append([]string(nil), m.tools[i]...)
	}
	return out
}

type threadBlockingStreamModel struct {
	mu       sync.Mutex
	calls    [][]*schema.Message
	tools    [][]string
	handlers []func(context.Context, []*schema.Message) (*schema.Message, error)
}

func (m *threadBlockingStreamModel) Generate(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.Message, error) {
	_ = opts
	_, handler := m.recordCall(input)
	if handler == nil {
		return schema.AssistantMessage("done", nil), nil
	}
	return handler(ctx, input)
}

func (m *threadBlockingStreamModel) Stream(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	if msg == nil {
		msg = schema.AssistantMessage("done", nil)
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *threadBlockingStreamModel) WithTools(tools []*schema.ToolInfo) (modelcomp.ToolCallingChatModel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(tools))
	for _, info := range tools {
		if info == nil {
			continue
		}
		names = append(names, info.Name)
	}
	m.tools = append(m.tools, names)
	return m, nil
}

func (m *threadBlockingStreamModel) recordCall(input []*schema.Message) (string, func(context.Context, []*schema.Message) (*schema.Message, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]*schema.Message, len(input))
	copy(copied, input)
	m.calls = append(m.calls, copied)
	idx := len(m.calls) - 1
	if idx >= len(m.handlers) {
		return "", nil
	}
	return "", m.handlers[idx]
}

func TestTurnRunnerConfigCloneCopiesContainers(t *testing.T) {
	baseMiddleware := &middleware.BaseMiddleware{}
	extraMiddleware := &middleware.BaseMiddleware{}
	base := &TurnRunnerConfig{
		Tools:               []tool.BaseTool{&fakeCounterTool{}},
		ToolMask:            func(_ context.Context, info *schema.ToolInfo) bool { return info.Name != "base_hidden" },
		EnablePlan:          true,
		MaxSteps:            7,
		FilesystemConfig:    &deepagents.FilesystemConfig{WorkDir: "/repo"},
		HITLConfig:          &deepagents.HITLConfig{ToolPolicyGates: map[string]deeptools.ToolPolicyGate{"exec": {}}},
		SubAgents:           []*subagent.SubAgent{{Name: "base"}},
		InterruptAfterNodes: []string{"tools"},
		Middlewares:         []middleware.Middleware{baseMiddleware},
		Callbacks:           []callbacks.Handler{callbacks.NewHandlerBuilder().Build()},
	}

	clone := base.Clone()
	if clone == base {
		t.Fatalf("Clone() returned the same pointer")
	}
	if clone.MaxSteps != 7 {
		t.Fatalf("Clone() lost max steps: %d", clone.MaxSteps)
	}
	clone.Tools = append(clone.Tools, &fakeCounterTool{})
	clone.FilesystemConfig.WorkDir = "/other"
	clone.HITLConfig.ToolPolicyGates["other"] = deeptools.ToolPolicyGate{}
	clone.SubAgents = append(clone.SubAgents, &subagent.SubAgent{Name: "other"})
	clone.InterruptAfterNodes[0] = "executor"
	clone.Middlewares = append(clone.Middlewares, extraMiddleware)
	clone.Callbacks = append(clone.Callbacks, callbacks.NewHandlerBuilder().Build())

	if len(base.Tools) != 1 {
		t.Fatalf("base tools were mutated: %d", len(base.Tools))
	}
	if base.FilesystemConfig.WorkDir != "/repo" {
		t.Fatalf("base filesystem config was mutated: %+v", base.FilesystemConfig)
	}
	if len(base.HITLConfig.ToolPolicyGates) != 1 {
		t.Fatalf("base HITL gates were mutated: %+v", base.HITLConfig.ToolPolicyGates)
	}
	if len(base.SubAgents) != 1 || base.SubAgents[0].Name != "base" {
		t.Fatalf("base subagents were mutated: %+v", base.SubAgents)
	}
	if base.InterruptAfterNodes[0] != "tools" {
		t.Fatalf("base interrupt nodes were mutated: %+v", base.InterruptAfterNodes)
	}
	if len(base.Middlewares) != 1 {
		t.Fatalf("base middlewares were mutated: %d", len(base.Middlewares))
	}
	if len(base.Callbacks) != 1 {
		t.Fatalf("base callbacks were mutated: %d", len(base.Callbacks))
	}
}

func TestDeepAgentThread_ServerToolExecutes(t *testing.T) {
	ctx := context.Background()
	serverTool := &fakeNamedCounterTool{name: "server_form"}
	model := &threadScriptedModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(context.Context, []*schema.Message) (*schema.Message, error) {
				return &schema.Message{
					Role: schema.Assistant,
					ToolCalls: []schema.ToolCall{{
						ID: "call_server_form",
						Function: schema.FunctionCall{
							Name:      "server_form",
							Arguments: `{"delta":1}`,
						},
					}},
				}, nil
			},
			func(context.Context, []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("done", nil), nil
			},
		},
	}
	th := New("thread-server-tool", &TurnRunnerConfig{
		ChatModel:       model,
		Tools:           []tool.BaseTool{serverTool},
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, NewSimpleTestContextManager(), make(chan Event, 16))

	result, err := th.SubmitInput(ctx, schema.UserMessage("call server tool"))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := result.TurnHandle.Wait(waitCtx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	if got := serverTool.Count(); got != 1 {
		t.Fatalf("server tool executed %d times, want 1", got)
	}
}

func TestDeepAgentThread_CompactRunsImmediatelyWhenIdle(t *testing.T) {
	ctx := context.Background()
	cm := &recordingContextManager{}
	th := New("thread-compact-idle", &TurnRunnerConfig{}, cm, make(chan Event, 8))

	payload, err := th.Compact(ctx)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if payload == nil {
		t.Fatalf("Compact() payload is nil")
	}
	if len(cm.forceCompactCalls) != 1 || cm.forceCompactCalls[0] != 0 {
		t.Fatalf("compact calls = %v, want [0]", cm.forceCompactCalls)
	}
	if len(cm.forceCompactTurn) != 1 || cm.forceCompactTurn[0] == "" {
		t.Fatalf("compact turn ids = %v, want one non-empty turn id", cm.forceCompactTurn)
	}
}

func TestDeepAgentThread_CompactUsesTurnIDProviderFallback(t *testing.T) {
	ctx := context.Background()
	cm := &recordingContextManager{}
	var providerInput *Message
	th := New("thread-compact-provider", &TurnRunnerConfig{}, cm, make(chan Event, 8),
		WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
			_, _ = ctx, threadID
			providerInput = input
			return ""
		}),
	)

	if _, err := th.Compact(ctx); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if providerInput != nil {
		t.Fatalf("compact turn id provider input = %#v, want nil", providerInput)
	}
	if len(cm.forceCompactTurn) != 1 || cm.forceCompactTurn[0] == "" {
		t.Fatalf("compact turn ids = %v, want fallback non-empty turn id", cm.forceCompactTurn)
	}
}

func TestDeepAgentThread_CompactRejectsActiveTurn(t *testing.T) {
	ctx := context.Background()
	modelEntered := make(chan struct{})
	releaseModel := make(chan struct{})
	model := &threadScriptedModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				close(modelEntered)
				select {
				case <-releaseModel:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return schema.AssistantMessage("done", nil), nil
			},
		},
	}
	cm := &recordingContextManager{}
	th := New("thread-compact-wait", &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, cm, make(chan Event, 16))

	run, err := th.SubmitInput(ctx, schema.UserMessage("work"))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	select {
	case <-modelEntered:
	case <-time.After(time.Second):
		t.Fatalf("model was not called")
	}
	cm.mu.Lock()
	cm.forceCompactCalls = nil
	cm.forceCompactTurn = nil
	cm.mu.Unlock()

	if _, err := th.Compact(ctx); !errors.Is(err, ErrThreadRunning) {
		t.Fatalf("Compact() error = %v, want %v", err, ErrThreadRunning)
	}
	if len(cm.forceCompactCalls) != 0 {
		t.Fatalf("compact ran while active turn existed: %v", cm.forceCompactCalls)
	}

	close(releaseModel)
	if err := run.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestDeepAgentThread_ExternalInterruptWithoutTimeoutDoesNotInterruptBlockedNode(t *testing.T) {
	ctx := context.Background()
	modelEntered := make(chan struct{})
	releaseModel := make(chan struct{})
	model := &threadBlockingStreamModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				close(modelEntered)
				select {
				case <-releaseModel:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return schema.AssistantMessage("done", nil), nil
			},
		},
	}
	eventBus := make(chan Event, 32)
	collector := &eventCollector{}
	go collector.collect(eventBus)
	th := New("thread-interrupt-no-timeout", &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, NewSimpleTestContextManager(), eventBus, WithTurnIDProvider(func(context.Context, string, *Message) string {
		return "turn-no-timeout"
	}))

	result, err := th.SubmitInput(ctx, schema.UserMessage("work"))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	select {
	case <-modelEntered:
	case <-time.After(time.Second):
		t.Fatalf("model was not called")
	}

	if !th.Interrupt(InterruptOptions{Metadata: map[string]string{"reason": "test_no_timeout"}}) {
		t.Fatalf("Interrupt() = false, want true")
	}
	if _, ok := collector.waitFor(t, EventInterrupted, 80*time.Millisecond); ok {
		t.Fatalf("interrupted before the running node completed")
	}
	if !result.TurnHandle.IsActive() {
		t.Fatalf("turn became inactive before the running node completed")
	}

	close(releaseModel)
	if err := result.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.TurnHandle.IsActive() {
		t.Fatalf("turn still active after running node completed")
	}
	if hasEventType(collector.snapshot(), EventInterrupted) {
		t.Fatalf("unexpected interrupted event without timeout")
	}
	if _, ok := collector.waitFor(t, EventTurnEnd, time.Second); !ok {
		t.Fatalf("turn did not finish normally after running node completed")
	}
}

func TestDeepAgentThread_ExternalInterruptTimeoutInterruptsBlockedNode(t *testing.T) {
	ctx := context.Background()
	modelEntered := make(chan struct{})
	releaseModel := make(chan struct{})
	model := &threadBlockingStreamModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				close(modelEntered)
				select {
				case <-releaseModel:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return schema.AssistantMessage("done", nil), nil
			},
		},
	}
	eventBus := make(chan Event, 32)
	collector := &eventCollector{}
	go collector.collect(eventBus)
	th := New("thread-interrupt-timeout", &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, NewSimpleTestContextManager(), eventBus, WithTurnIDProvider(func(context.Context, string, *Message) string {
		return "turn-timeout"
	}))

	result, err := th.SubmitInput(ctx, schema.UserMessage("work"))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	select {
	case <-modelEntered:
	case <-time.After(time.Second):
		t.Fatalf("model was not called")
	}

	timeout := 30 * time.Millisecond
	if !th.Interrupt(InterruptOptions{Timeout: &timeout, Metadata: map[string]string{"reason": "test_timeout"}}) {
		t.Fatalf("Interrupt() = false, want true")
	}
	ev, ok := collector.waitFor(t, EventInterrupted, 500*time.Millisecond)
	if !ok {
		t.Fatalf("interrupted event was not emitted after timeout")
	}
	payload, ok := ev.Payload.(InterruptedPayload)
	if !ok {
		t.Fatalf("interrupted payload type = %T", ev.Payload)
	}
	if payload.Source != "external" || payload.Metadata["reason"] != "test_timeout" || payload.TimeoutMS != timeout.Milliseconds() {
		t.Fatalf("interrupted payload = %+v", payload)
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := result.TurnHandle.Wait(waitCtx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.TurnHandle.IsActive() {
		t.Fatalf("turn still active after timeout interrupt")
	}
	close(releaseModel)
}

func TestDeepAgentThread_ExternalInterruptStopsBeforeNextToolNode(t *testing.T) {
	ctx := context.Background()
	modelEntered := make(chan struct{})
	releaseModel := make(chan struct{})
	model := &threadBlockingStreamModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				close(modelEntered)
				select {
				case <-releaseModel:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return schema.AssistantMessage("call counter", []schema.ToolCall{{
					ID:       "counter-call-1",
					Type:     "function",
					Function: schema.FunctionCall{Name: "counter", Arguments: `{"delta":1}`},
				}}), nil
			},
		},
	}
	counter := &countingToolResult{name: "counter", result: `{"ok":true}`}
	eventBus := make(chan Event, 32)
	collector := &eventCollector{}
	go collector.collect(eventBus)
	th := New("thread-interrupt-before-tools", &TurnRunnerConfig{
		ChatModel:       model,
		Tools:           []tool.BaseTool{counter},
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, NewSimpleTestContextManager(), eventBus, WithTurnIDProvider(func(context.Context, string, *Message) string {
		return "turn-before-tools"
	}))

	result, err := th.SubmitInput(ctx, schema.UserMessage("work"))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	select {
	case <-modelEntered:
	case <-time.After(time.Second):
		t.Fatalf("model was not called")
	}

	timeout := 500 * time.Millisecond
	if !th.Interrupt(InterruptOptions{Timeout: &timeout, Metadata: map[string]string{"reason": "stop_before_tools"}}) {
		t.Fatalf("Interrupt() = false, want true")
	}
	close(releaseModel)
	ev, ok := collector.waitFor(t, EventInterrupted, time.Second)
	if !ok {
		t.Fatalf("interrupted event was not emitted")
	}
	payload, ok := ev.Payload.(InterruptedPayload)
	if !ok || payload.Source != "external" {
		t.Fatalf("interrupted payload = %#v", ev.Payload)
	}
	if counter.RunCount() != 0 {
		t.Fatalf("tool ran after external interrupt: run_count=%d", counter.RunCount())
	}
	if err := result.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestDeepAgentThread_StaticInterruptAfterNodesInterruptsWithoutExternalRequest(t *testing.T) {
	ctx := context.Background()
	model := &threadBlockingStreamModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("call counter", []schema.ToolCall{{
					ID:       "counter-call-static",
					Type:     "function",
					Function: schema.FunctionCall{Name: "counter", Arguments: `{"delta":1}`},
				}}), nil
			},
		},
	}
	counter := &countingToolResult{name: "counter", result: `{"ok":true}`}
	eventBus := make(chan Event, 32)
	collector := &eventCollector{}
	go collector.collect(eventBus)
	th := New("thread-static-interrupt-after", &TurnRunnerConfig{
		ChatModel:           model,
		Tools:               []tool.BaseTool{counter},
		CheckpointStore:     checkpointer.NewInMemoryStore(),
		InterruptAfterNodes: []string{"executor"},
	}, NewSimpleTestContextManager(), eventBus, WithTurnIDProvider(func(context.Context, string, *Message) string {
		return "turn-static-after"
	}))

	result, err := th.SubmitInput(ctx, schema.UserMessage("work"))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	ev, ok := collector.waitFor(t, EventInterrupted, time.Second)
	if !ok {
		t.Fatalf("static interrupt-after did not emit interrupted event")
	}
	payload, ok := ev.Payload.(InterruptedPayload)
	if !ok {
		t.Fatalf("interrupted payload type = %T", ev.Payload)
	}
	if payload.Source == "external" {
		t.Fatalf("static interrupt-after should not be reported as external: %+v", payload)
	}
	if counter.RunCount() != 0 {
		t.Fatalf("tool ran despite static interrupt-after: run_count=%d", counter.RunCount())
	}
	if err := result.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if hasEventType(collector.snapshot(), EventTurnEnd) {
		t.Fatalf("turn ended normally despite static interrupt-after")
	}
}

func TestDeepAgentThread_SubmitInputStartsQueuesAndWaits(t *testing.T) {
	ctx := context.Background()
	firstModelEntered := make(chan struct{})
	releaseFirstModel := make(chan struct{})
	model := &threadScriptedModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				close(firstModelEntered)
				select {
				case <-releaseFirstModel:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return schema.AssistantMessage("first", nil), nil
			},
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("second", nil), nil
			},
		},
	}
	cm := NewSimpleTestContextManager()
	eventBus := make(chan Event, 32)
	turnIDs := []string{"turn-1", "turn-2"}
	th := New("thread-v2", &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, cm, eventBus, WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
		turnID := turnIDs[0]
		turnIDs = turnIDs[1:]
		return turnID
	}))

	firstMeta := map[string]string{"trace_id": "first"}
	secondMeta := map[string]string{"trace_id": "second"}
	result, err := th.SubmitInput(ctx, schema.UserMessage("first input"), WithInputMeta(firstMeta))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	if result == nil || !result.StartNewTurn || result.QueuedToActiveTurn || result.TurnID != "turn-1" || result.TurnHandle == nil {
		t.Fatalf("first SubmitInput() result = %+v", result)
	}
	if result.TurnHandle.TurnID() != "turn-1" || !result.TurnHandle.IsActive() {
		t.Fatalf("first TurnHandle = %q active=%v, want turn-1 active", result.TurnHandle.TurnID(), result.TurnHandle.IsActive())
	}

	select {
	case <-firstModelEntered:
	case <-time.After(time.Second):
		t.Fatalf("model was not called")
	}

	queued, err := th.SubmitInput(ctx, schema.UserMessage("second input"), WithInputMeta(secondMeta))
	if err != nil {
		t.Fatalf("second SubmitInput() error = %v", err)
	}
	if queued == nil || !queued.QueuedToActiveTurn || queued.StartNewTurn || queued.TurnID != "turn-1" || queued.TurnHandle == nil {
		t.Fatalf("second SubmitInput() result = %+v", queued)
	}
	if queued.TurnHandle.TurnID() != "turn-1" {
		t.Fatalf("queued TurnHandle turn ID = %q, want turn-1", queued.TurnHandle.TurnID())
	}
	if consumed := queued.TurnHandle.ConsumedInputs(); len(consumed) != 2 || consumed[0].Content != "first input" || consumed[1].Content != "second input" {
		t.Fatalf("queued TurnHandle consumed inputs = %+v, want first and second input", consumed)
	}
	if consumedMeta := queued.TurnHandle.ConsumedInputsMeta(); len(consumedMeta) != 2 ||
		consumedMeta[0].(map[string]string)["trace_id"] != "first" ||
		consumedMeta[1].(map[string]string)["trace_id"] != "second" {
		t.Fatalf("queued TurnHandle consumed inputs meta = %+v, want first and second meta", consumedMeta)
	}
	close(releaseFirstModel)

	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := result.TurnHandle.Wait(waitCtx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.TurnHandle.IsActive() {
		t.Fatalf("first TurnHandle still active after Wait")
	}

	next, err := th.SubmitInput(ctx, schema.UserMessage("late input"))
	if err != nil {
		t.Fatalf("late SubmitInput() error = %v", err)
	}
	if next == nil || !next.StartNewTurn || next.TurnID != "turn-2" {
		t.Fatalf("late SubmitInput() result = %+v", next)
	}
	if err := next.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("late Wait() error = %v", err)
	}

	calls := model.snapshotCalls()
	if len(calls) != 3 {
		t.Fatalf("model calls = %d, want 3", len(calls))
	}
	secondCall := calls[1]
	if len(secondCall) < 3 {
		t.Fatalf("second model input too short: %+v", secondCall)
	}
	if secondCall[len(secondCall)-1].Content != "second input" {
		t.Fatalf("second input was not drained at next model boundary: %+v", secondCall)
	}

	var pendingStarted *PendingInputProcessingStartedPayload
	var turn1StartConsumed []*schema.Message
	var turn1EndConsumed []*schema.Message
	var turn1StartConsumedMeta []any
	var turn1EndConsumedMeta []any
	for len(eventBus) > 0 {
		ev := <-eventBus
		if ev.TurnID == "turn-1" && ev.Type == EventTurnStart {
			turn1StartConsumed = ev.ConsumedInputs
			turn1StartConsumedMeta = ev.ConsumedInputsMeta
		}
		if ev.TurnID == "turn-1" && ev.Type == EventTurnEnd {
			turn1EndConsumed = ev.ConsumedInputs
			turn1EndConsumedMeta = ev.ConsumedInputsMeta
		}
		if ev.Type != EventPendingInputProcessingStarted {
			continue
		}
		payload, ok := ev.Payload.(PendingInputProcessingStartedPayload)
		if !ok {
			t.Fatalf("pending input event payload type = %T", ev.Payload)
		}
		pendingStarted = &payload
	}
	if pendingStarted == nil {
		t.Fatalf("pending input processing started event was not emitted")
	}
	if len(pendingStarted.Inputs) != 1 || pendingStarted.Inputs[0].Content != "second input" {
		t.Fatalf("pending input event inputs = %+v, want second input", pendingStarted.Inputs)
	}
	if len(turn1StartConsumed) != 1 || turn1StartConsumed[0].Content != "first input" {
		t.Fatalf("turn_start consumed inputs = %+v, want first input", turn1StartConsumed)
	}
	if len(turn1StartConsumedMeta) != 1 ||
		turn1StartConsumedMeta[0].(map[string]string)["trace_id"] != "first" {
		t.Fatalf("turn_start consumed inputs meta = %+v, want first meta", turn1StartConsumedMeta)
	}
	if len(turn1EndConsumed) != 2 ||
		turn1EndConsumed[0].Content != "first input" ||
		turn1EndConsumed[1].Content != "second input" {
		t.Fatalf("turn_end consumed inputs = %+v, want first and second input", turn1EndConsumed)
	}
	if len(turn1EndConsumedMeta) != 2 ||
		turn1EndConsumedMeta[0].(map[string]string)["trace_id"] != "first" ||
		turn1EndConsumedMeta[1].(map[string]string)["trace_id"] != "second" {
		t.Fatalf("turn_end consumed inputs meta = %+v, want first and second meta", turn1EndConsumedMeta)
	}
}

func TestDeepAgentThread_SubmitInputUsesTurnRunnerConfigOnlyForNewTurn(t *testing.T) {
	ctx := context.Background()
	model := &threadScriptedModel{}
	turnIDs := []string{"turn-masked", "turn-base"}
	baseCfg := &TurnRunnerConfig{
		ChatModel:       model,
		Tools:           []tool.BaseTool{&fakeCounterTool{}},
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}
	th := New("thread-patch", baseCfg, NewSimpleTestContextManager(), make(chan Event, 16), WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
		turnID := turnIDs[0]
		turnIDs = turnIDs[1:]
		return turnID
	}))

	turnCfg := baseCfg.Clone()
	turnCfg.ToolMask = func(context.Context, *schema.ToolInfo) bool { return false }
	first, err := th.SubmitInput(ctx, schema.UserMessage("masked"), WithTurnRunnerConfig(turnCfg))
	if err != nil {
		t.Fatalf("first SubmitInput() error = %v", err)
	}
	if first.RunnerConfig == nil || first.RunnerConfig.ToolMask == nil {
		t.Fatalf("first runner config snapshot = %+v, want patched config", first.RunnerConfig)
	}
	if err := first.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("first Wait() error = %v", err)
	}

	second, err := th.SubmitInput(ctx, schema.UserMessage("base"))
	if err != nil {
		t.Fatalf("second SubmitInput() error = %v", err)
	}
	if err := second.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("second Wait() error = %v", err)
	}

	toolBindings := model.snapshotToolBindings()
	if len(toolBindings) != 1 {
		t.Fatalf("tool bindings = %+v, want exactly one binding from the unpatched turn", toolBindings)
	}
	if len(toolBindings[0]) != 1 || toolBindings[0][0] != "counter" {
		t.Fatalf("unpatched turn tools = %+v, want counter", toolBindings[0])
	}
}

func TestDeepAgentThread_SubmitInputUsesTurnRunnerConfigResolverForNewTurn(t *testing.T) {
	ctx := context.Background()
	model := &threadScriptedModel{}
	baseCfg := &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}
	th := New("thread-resolver", baseCfg, NewSimpleTestContextManager(), make(chan Event, 16), WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
		return "turn-resolver"
	}))

	inputMeta := map[string]string{"trace_id": "trace-resolver"}
	resolverCalls := 0
	result, err := th.SubmitInput(ctx, schema.UserMessage("resolver input"),
		WithInputMeta(inputMeta),
		WithTurnRunnerConfigResolver(func(ctx context.Context, req TurnRunnerConfigRequest) (*TurnRunnerConfig, error) {
			resolverCalls++
			if req.ThreadID != "thread-resolver" || req.TurnID != "turn-resolver" {
				t.Fatalf("resolver ids thread=%q turn=%q", req.ThreadID, req.TurnID)
			}
			if req.Trigger != TurnRunnerConfigForSubmit {
				t.Fatalf("resolver trigger=%q, want submit", req.Trigger)
			}
			if req.Input == nil || req.Input.Content != "resolver input" {
				t.Fatalf("resolver input=%+v", req.Input)
			}
			if req.Resume != nil {
				t.Fatalf("resolver resume=%+v, want nil", req.Resume)
			}
			if req.InputMeta.(map[string]string)["trace_id"] != "trace-resolver" {
				t.Fatalf("resolver input meta=%+v", req.InputMeta)
			}
			if req.Base == nil || req.Base.ChatModel != model {
				t.Fatalf("resolver base=%+v", req.Base)
			}
			cfg := req.Base.Clone()
			cfg.MaxSteps = 12
			return cfg, nil
		}),
	)
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	if resolverCalls != 1 {
		t.Fatalf("resolver calls=%d, want 1", resolverCalls)
	}
	if result == nil || !result.StartNewTurn || result.QueuedToActiveTurn || result.RunnerConfig == nil || result.RunnerConfig.MaxSteps != 12 {
		t.Fatalf("result=%+v", result)
	}
	result.RunnerConfig.MaxSteps = 99
	if err := result.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestDeepAgentThread_RuntimeConstructorUsesBaseTurnRunnerConfig(t *testing.T) {
	ctx := context.Background()
	model := &threadScriptedModel{}
	baseCfg := &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
		MaxSteps:        9,
	}
	th := New("thread-runtime-constructor", nil, NewSimpleTestContextManager(), make(chan Event, 16),
		WithBaseTurnRunnerConfig(baseCfg),
		WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
			return "turn-runtime-constructor"
		}),
	)

	result, err := th.SubmitInput(ctx, schema.UserMessage("base config"))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	if result.RunnerConfig == nil || result.RunnerConfig.MaxSteps != 9 {
		t.Fatalf("runner config = %+v, want base MaxSteps=9", result.RunnerConfig)
	}
	if err := result.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestDeepAgentThread_SubmitInputIgnoresTurnRunnerConfigResolverForQueuedInput(t *testing.T) {
	ctx := context.Background()
	firstModelEntered := make(chan struct{})
	releaseFirstModel := make(chan struct{})
	model := &threadScriptedModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				close(firstModelEntered)
				select {
				case <-releaseFirstModel:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return schema.AssistantMessage("first", nil), nil
			},
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("queued", nil), nil
			},
		},
	}
	th := New("thread-queued-patch", &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, NewSimpleTestContextManager(), make(chan Event, 16), WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
		return "turn-queued-patch"
	}))

	first, err := th.SubmitInput(ctx, schema.UserMessage("first"))
	if err != nil {
		t.Fatalf("first SubmitInput() error = %v", err)
	}
	select {
	case <-firstModelEntered:
	case <-time.After(time.Second):
		t.Fatalf("model was not called")
	}

	resolverCalls := 0
	queued, err := th.SubmitInput(ctx, schema.UserMessage("patched queued"), WithTurnRunnerConfigResolver(func(ctx context.Context, req TurnRunnerConfigRequest) (*TurnRunnerConfig, error) {
		resolverCalls++
		return (&TurnRunnerConfig{ToolMask: func(context.Context, *schema.ToolInfo) bool { return false }}).Clone(), nil
	}))
	if err != nil {
		t.Fatalf("queued SubmitInput() error = %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver calls=%d, want 0", resolverCalls)
	}
	if queued == nil || !queued.QueuedToActiveTurn || queued.TurnID != "turn-queued-patch" {
		t.Fatalf("queued result = %+v", queued)
	}
	if queued.RunnerConfig != nil {
		t.Fatalf("queued runner config=%+v, want nil", queued.RunnerConfig)
	}

	close(releaseFirstModel)
	if err := first.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestDeepAgentThread_ActiveTurnWaitsOnlyItsTurn(t *testing.T) {
	ctx := context.Background()
	model := &threadScriptedModel{}
	var nextTurn int
	th := New("thread-handle", &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, NewSimpleTestContextManager(), make(chan Event, 16), WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
		nextTurn++
		return "turn-handle-" + string(rune('0'+nextTurn))
	}))

	firstResult, err := th.SubmitInput(ctx, schema.UserMessage("one"))
	if err != nil {
		t.Fatalf("first SubmitInput() error = %v", err)
	}
	if err := firstResult.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("first Wait() error = %v", err)
	}
	if err := firstResult.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("second wait on first TurnHandle error = %v", err)
	}

	secondResult, err := th.SubmitInput(ctx, schema.UserMessage("two"))
	if err != nil {
		t.Fatalf("second SubmitInput() error = %v", err)
	}
	if err := secondResult.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("second Wait() error = %v", err)
	}
}

func TestDeepAgentThread_ResumeTurnRequiresNoActiveTurn(t *testing.T) {
	ctx := context.Background()
	firstModelEntered := make(chan struct{})
	releaseFirstModel := make(chan struct{})
	model := &threadScriptedModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				close(firstModelEntered)
				select {
				case <-releaseFirstModel:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return schema.AssistantMessage("first", nil), nil
			},
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("resumed", nil), nil
			},
		},
	}
	th := New("thread-resume", &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, NewSimpleTestContextManager(), make(chan Event, 16), WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
		return "turn-active"
	}))

	result, err := th.SubmitInput(ctx, schema.UserMessage("one"))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	select {
	case <-firstModelEntered:
	case <-time.After(time.Second):
		t.Fatalf("model was not called")
	}
	if _, err := th.ResumeTurn(ctx, "turn-active", ResumeTurnOptions{CheckpointID: "thread-resume:turn-active"}); !errors.Is(err, ErrThreadRunning) {
		t.Fatalf("ResumeTurn() while active error = %v, want ErrThreadRunning", err)
	}
	resolverCalls := 0
	_, err = th.ResumeTurn(ctx, "turn-active", ResumeTurnOptions{
		CheckpointID: "thread-resume:turn-active",
		TurnRunnerConfig: func(ctx context.Context, req TurnRunnerConfigRequest) (*TurnRunnerConfig, error) {
			resolverCalls++
			return req.Base, nil
		},
	})
	if !errors.Is(err, ErrThreadRunning) {
		t.Fatalf("ResumeTurn() with resolver while active error = %v, want ErrThreadRunning", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("resume resolver calls while active = %d, want 0", resolverCalls)
	}
	close(releaseFirstModel)
	if err := result.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	resumed, err := th.ResumeTurn(ctx, "turn-active", ResumeTurnOptions{CheckpointID: "thread-resume:turn-active"})
	if err != nil {
		t.Fatalf("ResumeTurn() error = %v", err)
	}
	if err := resumed.Wait(ctx); err != nil {
		t.Fatalf("resume Wait() error = %v", err)
	}
}

func TestDeepAgentThread_ResumeTurnUsesTurnRunnerConfigResolver(t *testing.T) {
	ctx := context.Background()
	model := &threadScriptedModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("first", nil), nil
			},
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("resumed", nil), nil
			},
		},
	}
	th := New("thread-resume-resolver", nil, NewSimpleTestContextManager(), make(chan Event, 16),
		WithBaseTurnRunnerConfig(&TurnRunnerConfig{
			ChatModel:       model,
			CheckpointStore: checkpointer.NewInMemoryStore(),
		}),
		WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
			return "turn-resume-resolver"
		}),
	)

	first, err := th.SubmitInput(ctx, schema.UserMessage("first"))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	if err := first.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("first Wait() error = %v", err)
	}

	resumeData := map[string]any{"interrupt-1": "approved"}
	resolverCalls := 0
	resumed, err := th.ResumeTurn(ctx, "turn-resume-resolver", ResumeTurnOptions{
		CheckpointID:        "thread-resume-resolver:turn-resume-resolver",
		WriteToCheckpointID: "thread-resume-resolver:turn-resume-resolver:next",
		ForceNewRun:         true,
		ResumeInterruptIDs:  []string{"interrupt-1"},
		ResumeData:          resumeData,
		ConfigMeta:          map[string]string{"mode": "plan"},
		TurnRunnerConfig: func(ctx context.Context, req TurnRunnerConfigRequest) (*TurnRunnerConfig, error) {
			resolverCalls++
			if req.ThreadID != "thread-resume-resolver" || req.TurnID != "turn-resume-resolver" {
				t.Fatalf("resolver ids thread=%q turn=%q", req.ThreadID, req.TurnID)
			}
			if req.Trigger != TurnRunnerConfigForResume {
				t.Fatalf("resolver trigger=%q, want resume", req.Trigger)
			}
			if req.Input != nil || req.InputMeta != nil {
				t.Fatalf("resolver input=%+v inputMeta=%+v, want nil", req.Input, req.InputMeta)
			}
			if req.Resume == nil {
				t.Fatal("resolver resume=nil")
			}
			if req.Resume.CheckpointID != "thread-resume-resolver:turn-resume-resolver" ||
				req.Resume.WriteToCheckpointID != "thread-resume-resolver:turn-resume-resolver:next" ||
				!req.Resume.ForceNewRun {
				t.Fatalf("resolver resume options=%+v", req.Resume)
			}
			if len(req.Resume.ResumeInterruptIDs) != 1 || req.Resume.ResumeInterruptIDs[0] != "interrupt-1" {
				t.Fatalf("resolver resume interrupts=%v", req.Resume.ResumeInterruptIDs)
			}
			if req.Resume.ResumeData["interrupt-1"] != "approved" {
				t.Fatalf("resolver resume data=%v", req.Resume.ResumeData)
			}
			if req.Resume.ConfigMeta.(map[string]string)["mode"] != "plan" {
				t.Fatalf("resolver config meta=%v", req.Resume.ConfigMeta)
			}
			if req.Base == nil || req.Base.ChatModel != model {
				t.Fatalf("resolver base=%+v", req.Base)
			}
			cfg := req.Base.Clone()
			cfg.MaxSteps = 14
			return cfg, nil
		},
	})
	if err != nil {
		t.Fatalf("ResumeTurn() error = %v", err)
	}
	if resolverCalls != 1 {
		t.Fatalf("resolver calls=%d, want 1", resolverCalls)
	}
	if err := resumed.Wait(ctx); err != nil {
		t.Fatalf("resume Wait() error = %v", err)
	}
}

func TestDeepAgentThread_ResumeTurnRunnerConfigOverridesResolver(t *testing.T) {
	ctx := context.Background()
	model := &threadScriptedModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("first", nil), nil
			},
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("resumed", nil), nil
			},
		},
	}
	base := &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
		MaxSteps:        2,
	}
	th := New("thread-resume-explicit", base, NewSimpleTestContextManager(), make(chan Event, 16), WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
		return "turn-resume-explicit"
	}))

	first, err := th.SubmitInput(ctx, schema.UserMessage("first"))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	if err := first.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("first Wait() error = %v", err)
	}

	resolverCalls := 0
	explicit := base.Clone()
	explicit.MaxSteps = 11
	resumed, err := th.ResumeTurn(ctx, "turn-resume-explicit", ResumeTurnOptions{
		CheckpointID: "thread-resume-explicit:turn-resume-explicit",
		RunnerConfig: explicit,
		TurnRunnerConfig: func(ctx context.Context, req TurnRunnerConfigRequest) (*TurnRunnerConfig, error) {
			resolverCalls++
			return req.Base, nil
		},
	})
	if err != nil {
		t.Fatalf("ResumeTurn() error = %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver calls=%d, want 0", resolverCalls)
	}
	if err := resumed.Wait(ctx); err != nil {
		t.Fatalf("resume Wait() error = %v", err)
	}
}

func TestThreadRunCommitEndRejectsLaterInput(t *testing.T) {
	run := newTurn(
		"run-race",
		nil,
		nil,
		nil,
	)
	if !run.commitEndIfNoPending() {
		t.Fatalf("CommitEndIfNoPending() = false, want true")
	}
	err := run.enqueueInput(schema.UserMessage("late"), nil)
	if !errors.Is(err, ErrRunInputClosed) {
		t.Fatalf("enqueue after commit error = %v, want ErrRunInputClosed", err)
	}
}
