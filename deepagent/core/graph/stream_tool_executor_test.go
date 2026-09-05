package graph

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	agenttools "eino-cli/deepagent/core/tools"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	cbutils "github.com/cloudwego/eino/utils/callbacks"
)

type testInvokableTool struct {
	name    string
	runFunc func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error)
}

type testStreamableTool struct {
	name    string
	runFunc func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (*schema.StreamReader[string], error)
}

func (t *testInvokableTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "test tool"}, nil
}

func (t *testInvokableTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	return t.runFunc(ctx, argumentsInJSON, opts...)
}

func (t *testStreamableTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "test streamable tool"}, nil
}

func (t *testStreamableTool) StreamableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (*schema.StreamReader[string], error) {
	return t.runFunc(ctx, argumentsInJSON, opts...)
}

func TestStreamToolExecutor_StartsToolBeforeModelEOF(t *testing.T) {
	ctx := context.Background()
	toolStarted := make(chan struct{}, 1)

	executor, err := NewStreamToolExecutor(ctx, &compose.ToolsNodeConfig{
		Tools: []tool.BaseTool{
			&testInvokableTool{
				name: "echo",
				runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
					select {
					case toolStarted <- struct{}{}:
					default:
					}
					return argumentsInJSON, nil
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewStreamToolExecutor err: %v", err)
	}

	inputReader, inputWriter := schema.Pipe[*schema.Message](4)
	defer inputReader.Close()

	type execResult struct {
		reader *schema.StreamReader[[]*schema.Message]
		err    error
	}
	resultCh := make(chan execResult, 1)
	go func() {
		reader, execErr := executor.Execute(ctx, inputReader)
		resultCh <- execResult{reader: reader, err: execErr}
	}()

	idx := 0
	inputWriter.Send(&schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:    "call_1",
			Index: &idx,
			Function: schema.FunctionCall{
				Name:      "echo",
				Arguments: `{"value":"hel`,
			},
		}},
	}, nil)
	inputWriter.Send(&schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:    "call_1",
			Index: &idx,
			Function: schema.FunctionCall{
				Arguments: `lo"}`,
			},
		}},
	}, nil)

	select {
	case <-toolStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("tool was not started before model stream EOF")
	}

	inputWriter.Close()

	result := <-resultCh
	if result.err != nil {
		t.Fatalf("Execute err: %v", result.err)
	}

	msgs, err := collectOutputMessages(result.reader)
	if err != nil {
		t.Fatalf("collectOutputMessages err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 tool message, got %d", len(msgs))
	}
	if msgs[0].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool call id: %s", msgs[0].ToolCallID)
	}
	if msgs[0].Content != `{"value":"hello"}` {
		t.Fatalf("unexpected tool output: %s", msgs[0].Content)
	}
}

func TestStreamToolExecutorDoesNotReuseRepairedCallsAcrossStreams(t *testing.T) {
	ctx := context.Background()
	calls := make(chan string, 8)
	executor, err := NewStreamToolExecutor(ctx, &compose.ToolsNodeConfig{Tools: []tool.BaseTool{
		&testInvokableTool{name: "echo", runFunc: func(_ context.Context, args string, _ ...tool.Option) (output string, err error) {
			calls <- args
			return args, nil
		}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []schema.ToolCall{
		{ID: "first", Function: schema.FunctionCall{Name: "echo", Arguments: `{"value":"one"`}},
		{ID: "second", Function: schema.FunctionCall{Name: "echo", Arguments: `{"value":"two"}`}},
	} {
		input := schema.StreamReaderFromArray([]*schema.Message{{Role: schema.Assistant, ToolCalls: []schema.ToolCall{call}}})
		output, err := executor.Execute(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		for {
			_, err := output.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		output.Close()
		input.Close()
	}
	if len(calls) != 2 {
		t.Fatalf("executed %d calls, want one per stream", len(calls))
	}
}

func TestStreamToolExecutor_UsesToolMiddleware(t *testing.T) {
	ctx := context.Background()

	executor, err := NewStreamToolExecutor(ctx, &compose.ToolsNodeConfig{
		Tools: []tool.BaseTool{
			&testInvokableTool{
				name: "echo",
				runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
					var payload map[string]string
					if err := json.Unmarshal([]byte(argumentsInJSON), &payload); err != nil {
						return "", err
					}
					return payload["value"], nil
				},
			},
		},
		ToolCallMiddlewares: []compose.ToolMiddleware{{
			Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
				return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
					input.Arguments = `{"value":"middleware"}`
					return next(ctx, input)
				}
			},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("NewStreamToolExecutor err: %v", err)
	}

	input := schema.StreamReaderFromArray([]*schema.Message{
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID: "call_2",
				Function: schema.FunctionCall{
					Name:      "echo",
					Arguments: `{"value":"raw"}`,
				},
			}},
		},
	})
	defer input.Close()

	output, err := executor.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute err: %v", err)
	}

	msgs, err := collectOutputMessages(output)
	if err != nil {
		t.Fatalf("collectOutputMessages err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 tool message, got %d", len(msgs))
	}
	if msgs[0].Content != "middleware" {
		t.Fatalf("unexpected middleware output: %s", msgs[0].Content)
	}
}

func TestStreamToolExecutor_StreamableSchemaErrorReturnsToolMessage(t *testing.T) {
	ctx := context.Background()

	type streamArgs struct {
		WaitForDone bool `json:"wait_for_done"`
	}

	streamTool, err := toolutils.InferStreamTool[streamArgs, string](
		"task",
		"test task",
		func(ctx context.Context, input streamArgs) (*schema.StreamReader[string], error) {
			return schema.StreamReaderFromArray([]string{"unexpected"}), nil
		},
	)
	if err != nil {
		t.Fatalf("InferStreamTool err: %v", err)
	}

	executor, err := NewStreamToolExecutor(ctx, &compose.ToolsNodeConfig{
		Tools: []tool.BaseTool{streamTool},
	}, nil)
	if err != nil {
		t.Fatalf("NewStreamToolExecutor err: %v", err)
	}

	input := schema.StreamReaderFromArray([]*schema.Message{
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID: "call_schema_err",
				Function: schema.FunctionCall{
					Name:      "task",
					Arguments: `{"wait_for_done":"true"}`,
				},
			}},
		},
	})
	defer input.Close()

	output, err := executor.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute err: %v", err)
	}

	msgs, err := collectOutputMessages(output)
	if err != nil {
		t.Fatalf("collectOutputMessages err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 tool message, got %d", len(msgs))
	}
	if msgs[0].ToolCallID != "call_schema_err" {
		t.Fatalf("unexpected tool call id: %s", msgs[0].ToolCallID)
	}
	if msgs[0].Role != schema.Tool {
		t.Fatalf("unexpected role: %s", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "[Error] Tool invocation failed: [LocalStreamFunc] failed to unmarshal arguments in json") {
		t.Fatalf("unexpected content: %s", msgs[0].Content)
	}
}

func TestStreamToolExecutor_WrappedStreamableToolCallbacksNotDuplicated(t *testing.T) {
	ctx := context.Background()

	type streamArgs struct {
		Value string `json:"value"`
	}

	streamTool, err := toolutils.InferStreamTool[streamArgs, string](
		"task",
		"test task",
		func(ctx context.Context, input streamArgs) (*schema.StreamReader[string], error) {
			return schema.StreamReaderFromArray([]string{input.Value}), nil
		},
	)
	if err != nil {
		t.Fatalf("InferStreamTool err: %v", err)
	}

	wrappedTools := agenttools.WrapToolsWithConfig([]tool.BaseTool{streamTool}, &agenttools.WrapToolsConfig{})
	executor, err := NewStreamToolExecutor(ctx, &compose.ToolsNodeConfig{
		Tools: wrappedTools,
	}, nil)
	if err != nil {
		t.Fatalf("NewStreamToolExecutor err: %v", err)
	}

	var starts int
	var streamEnds int
	var callbackContent string
	handler := cbutils.NewHandlerHelper().
		Tool(&cbutils.ToolCallbackHandler{
			OnStart: func(ctx context.Context, info *callbacks.RunInfo, input *tool.CallbackInput) context.Context {
				starts++
				return ctx
			},
			OnEndWithStreamOutput: func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[*tool.CallbackOutput]) context.Context {
				streamEnds++
				for {
					chunk, err := output.Recv()
					if err != nil {
						if !errors.Is(err, io.EOF) {
							t.Fatalf("callback stream Recv() error = %v", err)
						}
						return ctx
					}
					callbackContent += chunk.Response
				}
			},
		}).Handler()
	ctx = callbacks.InitCallbacks(ctx, &callbacks.RunInfo{
		Name:      "task",
		Component: components.ComponentOfTool,
	}, handler)

	input := schema.StreamReaderFromArray([]*schema.Message{{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID: "call_wrapped_stream",
			Function: schema.FunctionCall{
				Name:      "task",
				Arguments: `{"value":"ok"}`,
			},
		}},
	}})
	defer input.Close()

	output, err := executor.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute err: %v", err)
	}
	msgs, err := collectOutputMessages(output)
	if err != nil {
		t.Fatalf("collectOutputMessages err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 tool message, got %d", len(msgs))
	}
	if msgs[0].Content != "ok" {
		t.Fatalf("tool content = %q", msgs[0].Content)
	}
	if starts != 1 {
		t.Fatalf("tool start callbacks = %d, want 1", starts)
	}
	if streamEnds != 1 {
		t.Fatalf("tool stream end callbacks = %d, want 1", streamEnds)
	}
	if callbackContent != "ok" {
		t.Fatalf("callback content = %q, want ok", callbackContent)
	}
}

func TestStreamToolExecutor_NoCompleteToolCalls_EmitsSingleEmptyChunk(t *testing.T) {
	ctx := context.Background()

	executor, err := NewStreamToolExecutor(ctx, &compose.ToolsNodeConfig{
		Tools: []tool.BaseTool{
			&testInvokableTool{
				name: "echo",
				runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
					return argumentsInJSON, nil
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewStreamToolExecutor err: %v", err)
	}

	idx := 0
	input := schema.StreamReaderFromArray([]*schema.Message{{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:    "call_1",
			Index: &idx,
			Function: schema.FunctionCall{
				Arguments: `{"value":`,
			},
		}},
	}})
	defer input.Close()

	output, err := executor.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute err: %v", err)
	}
	defer output.Close()

	chunk, err := output.Recv()
	if err != nil {
		t.Fatalf("first Recv err: %v", err)
	}
	if len(chunk) != 0 {
		t.Fatalf("expected empty chunk, got len=%d", len(chunk))
	}

	_, err = output.Recv()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after empty chunk, got %v", err)
	}
}

// TestStreamToolExecutor_ExecuteInterruptsWhenLaunchTaskReturnsInterrupt
// 主要验证:
// 1. Execute(...) 里 `if len(interruptErrs) > 0` 这条分支处理的是“某个已完整收集的 tool call 在 launchTask 阶段就返回 interrupt/error”。
// 2. 一旦出现这种情况，Execute 不会直接返回 merged stream，而是把已完成工具和被中断工具整理成 rerun info/state，再返回 interrupt signal。
//
// 验证思路:
// - 构造两个完整 tool call，其中 tool1 正常完成，tool2 首次执行时直接触发 interrupt；
// - 直接调用 executor.Execute(...)，而不是 resume(...)；
// - 断言返回的是 interrupt-rerun error，并检查附带的 streamToolExecutorInterruptInfo：
//   - CompletedCallIDs 只有 call_1；
//   - InterruptedCallIDs 只有 call_2，说明它是在 launchTask 阶段就被记进 interruptErrs 的。
//
// 说明:
// - 这里拿不到 graph 层 ExtractInterruptInfo(...) 那种包装，因为当前是在 graph 节点内部直接测 Execute；
// - 这条路径返回的是更底层的 interrupt signal，需用 compose.IsInterruptRerunError(...) 取回我们传进 CompositeInterrupt 的 info。
func TestStreamToolExecutor_ExecuteInterruptsWhenLaunchTaskReturnsInterrupt(t *testing.T) {
	ctx := context.Background()

	tool1 := &countingTool{name: "tool1"}
	tool2 := &interruptOnceTool{name: "tool2"}

	executor, err := NewStreamToolExecutor(ctx, &compose.ToolsNodeConfig{
		Tools: []tool.BaseTool{tool1, tool2},
	}, nil)
	if err != nil {
		t.Fatalf("NewStreamToolExecutor err: %v", err)
	}

	input := schema.StreamReaderFromArray([]*schema.Message{{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{ID: "call_1", Function: schema.FunctionCall{Name: "tool1", Arguments: `{"n":1}`}},
			{ID: "call_2", Function: schema.FunctionCall{Name: "tool2", Arguments: `{"n":2}`}},
		},
	}})
	defer input.Close()

	out, err := executor.Execute(ctx, input)
	if err == nil {
		if out != nil {
			out.Close()
		}
		t.Fatal("expected interrupt error, got nil")
	}
	if out != nil {
		out.Close()
		t.Fatal("expected nil output stream on interrupt")
	}

	rawInfo, ok := compose.IsInterruptRerunError(err)
	if !ok {
		t.Fatalf("expected interrupt-rerun error, got err=%v", err)
	}
	info, ok := rawInfo.(*streamToolExecutorInterruptInfo)
	if !ok || info == nil {
		t.Fatalf("expected streamToolExecutorInterruptInfo, got %#v", rawInfo)
	}
	if len(info.CompletedCallIDs) != 1 || info.CompletedCallIDs[0] != "call_1" {
		t.Fatalf("expected only call_1 completed, got %+v", info.CompletedCallIDs)
	}
	if len(info.InterruptedCallIDs) != 1 || info.InterruptedCallIDs[0] != "call_2" {
		t.Fatalf("expected only call_2 interrupted, got %+v", info.InterruptedCallIDs)
	}

	if tool1.RunCount() != 1 {
		t.Fatalf("tool1 should execute once, got %d", tool1.RunCount())
	}
	if tool2.RunCount() != 1 {
		t.Fatalf("tool2 should execute once before interrupt, got %d", tool2.RunCount())
	}
}

// TestStreamToolExecutor_MultiToolCalls_SlotAlignedChunks
// 主要验证:
// 1. StreamToolExecutor 对外输出的每个 chunk 都是“按 tool call 顺序对齐槽位”的定长数组。
// 2. 多个工具并发执行时，不同工具的增量结果不会串位；同一个流式工具的多段输出会始终落在同一个槽位。
//
// 验证思路:
// - 构造 3 个 tool call，分别对应槽位 0、1、2；
// - 其中槽位 1 的工具是流式工具，会连续吐出两段结果；
// - 直接读取 StreamToolExecutor.Execute(...) 的输出 chunk；
// - 逐个断言每个 chunk 的长度恒等于 3，且非 nil message 只出现在预期槽位。
func TestStreamToolExecutor_MultiToolCalls_SlotAlignedChunks(t *testing.T) {
	ctx := context.Background()

	executor, err := NewStreamToolExecutor(ctx, &compose.ToolsNodeConfig{
		Tools: []tool.BaseTool{
			&testInvokableTool{
				name: "tool_0",
				runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
					return "slot0", nil
				},
			},
			&testStreamableTool{
				name: "tool_1",
				runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (*schema.StreamReader[string], error) {
					reader, writer := schema.Pipe[string](2)
					go func() {
						defer writer.Close()
						writer.Send("slot1_part1", nil)
						writer.Send("slot1_part2", nil)
					}()
					return reader, nil
				},
			},
			&testInvokableTool{
				name: "tool_2",
				runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
					return "slot2", nil
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewStreamToolExecutor err: %v", err)
	}

	input := schema.StreamReaderFromArray([]*schema.Message{{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{
				ID: "call_0",
				Function: schema.FunctionCall{
					Name:      "tool_0",
					Arguments: `{"slot":0}`,
				},
			},
			{
				ID: "call_1",
				Function: schema.FunctionCall{
					Name:      "tool_1",
					Arguments: `{"slot":1}`,
				},
			},
			{
				ID: "call_2",
				Function: schema.FunctionCall{
					Name:      "tool_2",
					Arguments: `{"slot":2}`,
				},
			},
		},
	}})
	defer input.Close()

	output, err := executor.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute err: %v", err)
	}
	defer output.Close()

	var chunks [][]*schema.Message
	for {
		chunk, err := output.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("output.Recv err: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	for i, chunk := range chunks {
		if len(chunk) != 3 {
			t.Fatalf("chunk %d expected len 3, got %d", i, len(chunk))
		}
	}

	slotContents := make(map[int][]string)
	for _, chunk := range chunks {
		nonNilCount := 0
		for idx, msg := range chunk {
			if msg == nil {
				continue
			}
			nonNilCount++
			slotContents[idx] = append(slotContents[idx], msg.Content)
			switch idx {
			case 0:
				if msg.ToolCallID != "call_0" {
					t.Fatalf("slot 0 expected toolCallID call_0, got %s", msg.ToolCallID)
				}
			case 1:
				if msg.ToolCallID != "call_1" {
					t.Fatalf("slot 1 expected toolCallID call_1, got %s", msg.ToolCallID)
				}
			case 2:
				if msg.ToolCallID != "call_2" {
					t.Fatalf("slot 2 expected toolCallID call_2, got %s", msg.ToolCallID)
				}
			default:
				t.Fatalf("unexpected non-nil slot index %d", idx)
			}
		}
		if nonNilCount != 1 {
			t.Fatalf("each chunk should contain exactly one non-nil slot, got %d", nonNilCount)
		}
	}

	if len(slotContents[0]) != 1 || slotContents[0][0] != "slot0" {
		t.Fatalf("slot 0 expected only slot0, got %+v", slotContents[0])
	}
	if len(slotContents[1]) != 2 {
		t.Fatalf("slot 1 expected 2 chunks, got %+v", slotContents[1])
	}
	if !containsString(slotContents[1], "slot1_part1") || !containsString(slotContents[1], "slot1_part2") {
		t.Fatalf("slot 1 expected both stream chunks, got %+v", slotContents[1])
	}
	if len(slotContents[2]) != 1 || slotContents[2][0] != "slot2" {
		t.Fatalf("slot 2 expected only slot2, got %+v", slotContents[2])
	}
}

func collectOutputMessages(reader *schema.StreamReader[[]*schema.Message]) ([]*schema.Message, error) {
	defer reader.Close()

	var msgs []*schema.Message
	for {
		chunk, err := reader.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return msgs, nil
			}
			return nil, err
		}
		msgs = append(msgs, chunk...)
	}
}

func collectNonNilOutputMessages(reader *schema.StreamReader[[]*schema.Message]) ([]*schema.Message, error) {
	defer reader.Close()

	var msgs []*schema.Message
	for {
		chunk, err := reader.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return msgs, nil
			}
			return nil, err
		}
		for _, msg := range chunk {
			if msg != nil {
				msgs = append(msgs, msg)
			}
		}
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

type interruptOnceTool struct {
	name       string
	mu         sync.Mutex
	runCount   int
	interrupts int
}

func (t *interruptOnceTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "interrupt once tool"}, nil
}

func (t *interruptOnceTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.runCount++
	if t.interrupts == 0 {
		t.interrupts++
		return "", tool.Interrupt(ctx, t.name+" rerun")
	}

	return t.name + ":" + argumentsInJSON, nil
}

func (t *interruptOnceTool) RunCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runCount
}

type countingTool struct {
	name     string
	mu       sync.Mutex
	runCount int
}

func (t *countingTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "counting tool"}, nil
}

func (t *countingTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.runCount++
	return t.name + ":" + argumentsInJSON, nil
}

func (t *countingTool) RunCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runCount
}

// TestStreamToolExecutor_InterruptResume_RerunsOnlyInterruptedCalls
// 主要验证:
// 1. 当中断 state 已经保存了 4 个 tool call 且其中 3 个工具结果已完成时，resume 只会重跑被中断的那个工具。
// 2. 已完成的工具结果会被原样复用，并按原 tool call 顺序与 rerun 结果一起输出。
//
// 验证思路:
// - 直接构造 interrupt state，模拟“4 个 call 都已收集，其中 1/2/3 已完成，4 被中断”；
// - 调用 executor.resume(...)；
// - 检查最终输出包含 4 条 tool message，且 1/2/3 不会再执行，只有 4 会执行一次。
func TestStreamToolExecutor_InterruptResume_RerunsOnlyInterruptedCalls(t *testing.T) {
	ctx := context.Background()
	tool1 := &countingTool{name: "tool1"}
	tool2 := &countingTool{name: "tool2"}
	tool3 := &countingTool{name: "tool3"}
	tool4 := &interruptOnceTool{name: "tool4"}
	tool1.runCount = 1
	tool2.runCount = 1
	tool3.runCount = 1
	tool4.runCount = 1
	tool4.interrupts = 1

	executor, err := NewStreamToolExecutor(ctx, &compose.ToolsNodeConfig{
		Tools: []tool.BaseTool{tool1, tool2, tool3, tool4},
	}, nil)
	if err != nil {
		t.Fatalf("NewStreamToolExecutor err: %v", err)
	}

	state := &streamToolExecutorInterruptState{
		ToolCalls: []schema.ToolCall{
			{ID: "1", Function: schema.FunctionCall{Name: "tool1", Arguments: `{"n":1}`}},
			{ID: "2", Function: schema.FunctionCall{Name: "tool2", Arguments: `{"n":2}`}},
			{ID: "3", Function: schema.FunctionCall{Name: "tool3", Arguments: `{"n":3}`}},
			{ID: "4", Function: schema.FunctionCall{Name: "tool4", Arguments: `{"n":4}`}},
		},
		CompletedCalls: map[string][]*schema.Message{
			"1": {schema.ToolMessage(`tool1:{"n":1}`, "1", schema.WithToolName("tool1"))},
			"2": {schema.ToolMessage(`tool2:{"n":2}`, "2", schema.WithToolName("tool2"))},
			"3": {schema.ToolMessage(`tool3:{"n":3}`, "3", schema.WithToolName("tool3"))},
		},
	}

	out, err := executor.resume(ctx, state)
	if err != nil {
		t.Fatalf("resume err: %v", err)
	}

	msgs, err := collectNonNilOutputMessages(out)
	if err != nil {
		t.Fatalf("collectOutputMessages err: %v", err)
	}
	sortMessagesByCallID(msgs)
	if len(msgs) != 4 {
		t.Fatalf("expected 4 output messages, got %d", len(msgs))
	}
	if got := joinMessageContents(msgs); got != `tool1:{"n":1}|tool2:{"n":2}|tool3:{"n":3}|tool4:{"n":4}` {
		t.Fatalf("unexpected final output: %s", got)
	}
	if tool1.RunCount() != 1 || tool2.RunCount() != 1 || tool3.RunCount() != 1 {
		t.Fatalf("completed tools should not rerun, got counts: %d %d %d", tool1.RunCount(), tool2.RunCount(), tool3.RunCount())
	}
	if tool4.RunCount() != 2 {
		t.Fatalf("interrupted tool should run twice, got %d", tool4.RunCount())
	}
}

// TestStreamToolExecutor_InterruptBeforeAllToolCallsCollected_DropsIncompleteFutureCalls
// 主要验证:
// 1. 如果恢复 state 里只记录了第 1 个完整 tool call，那么 resume 时只会执行这一个 call。
// 2. 第 2 个未完整 call 以及还未出现的后续 call 不会在 resume 阶段凭空出现。
//
// 验证思路:
// - 直接构造一个只含 call_1 的 interrupt state，模拟“中断前只收集到了第 1 个完整 tool call”；
// - 调用 executor.resume(...)；
// - 检查最终输出只包含 call_1，且 tool2/tool3/tool4 都没有执行。
func TestStreamToolExecutor_InterruptBeforeAllToolCallsCollected_DropsIncompleteFutureCalls(t *testing.T) {
	ctx := context.Background()
	tool1 := &interruptOnceTool{name: "tool1"}
	tool2 := &countingTool{name: "tool2"}
	tool3 := &countingTool{name: "tool3"}
	tool4 := &countingTool{name: "tool4"}
	tool1.runCount = 1
	tool1.interrupts = 1

	executor, err := NewStreamToolExecutor(ctx, &compose.ToolsNodeConfig{
		Tools: []tool.BaseTool{tool1, tool2, tool3, tool4},
	}, nil)
	if err != nil {
		t.Fatalf("NewStreamToolExecutor err: %v", err)
	}

	idx1 := 0
	state := &streamToolExecutorInterruptState{
		ToolCalls: []schema.ToolCall{{
			ID:    "1",
			Index: &idx1,
			Function: schema.FunctionCall{
				Name:      "tool1",
				Arguments: `{"n":1}`,
			},
		}},
		CompletedCalls: map[string][]*schema.Message{},
	}

	out, err := executor.resume(ctx, state)
	if err != nil {
		t.Fatalf("resume err: %v", err)
	}

	msgs, err := collectNonNilOutputMessages(out)
	if err != nil {
		t.Fatalf("collectOutputMessages err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 output message, got %d", len(msgs))
	}
	if msgs[0].Content != `tool1:{"n":1}` {
		t.Fatalf("unexpected final output: %s", msgs[0].Content)
	}
	if tool1.RunCount() != 2 {
		t.Fatalf("tool1 should run twice, got %d", tool1.RunCount())
	}
	if tool2.RunCount() != 0 || tool3.RunCount() != 0 || tool4.RunCount() != 0 {
		t.Fatalf("future or incomplete calls should not run, got counts: %d %d %d", tool2.RunCount(), tool3.RunCount(), tool4.RunCount())
	}
}

func sortMessagesByCallID(msgs []*schema.Message) {
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].ToolCallID < msgs[j].ToolCallID
	})
}

func joinMessageContents(msgs []*schema.Message) string {
	parts := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		parts = append(parts, msg.Content)
	}
	return strings.Join(parts, "|")
}
