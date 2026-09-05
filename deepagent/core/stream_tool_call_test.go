package deepagents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/core/constant"
	mw "eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/subagent"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/core/types"
	"eino-cli/deepagent/mock/mock_model"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	cbutils "github.com/cloudwego/eino/utils/callbacks"
	"github.com/tidwall/gjson"
	"go.uber.org/mock/gomock"
)

type observingInvokableTool struct {
	total     int64
	startedCh chan<- time.Time
}

func (t *observingInvokableTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "counter_observing",
		Desc: "observe invokable tool execution",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"delta": {
				Type:     schema.Integer,
				Required: true,
			},
		}),
	}, nil
}

func (t *observingInvokableTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t.startedCh != nil {
		select {
		case t.startedCh <- time.Now():
		default:
		}
	}
	delta := gjson.Get(argumentsInJSON, "delta").Int()
	total := atomic.AddInt64(&t.total, delta)
	return fmt.Sprintf(`{"total":%d}`, total), nil
}

type observingStreamableTool struct {
	total     int64
	startedCh chan<- time.Time
	delay     time.Duration
}

type chunkedStreamableTool struct{}

type namedInvokableTool struct {
	name   string
	result string
}

type countingResultTool struct {
	name     string
	result   string
	runCount int64
}

type capcutInvokableOnlyToolInterceptor struct {
	mw.BaseMiddleware
	invokableHits int32
}

func (m *capcutInvokableOnlyToolInterceptor) Name() string {
	return "test.capcut_invokable_only_tool_interceptor"
}

func (m *capcutInvokableOnlyToolInterceptor) WrapToolCall() compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if input != nil && input.Name == constant.ToolTask {
					atomic.AddInt32(&m.invokableHits, 1)
					input.Arguments = `{}`
				}
				return next(ctx, input)
			}
		},
	}
}

type replacingStreamableToolMiddleware struct {
	mw.BaseMiddleware
	replacement     string
	observedContent string
	streamableHits  int32
}

func (m *replacingStreamableToolMiddleware) Name() string {
	return "test.replacing_streamable_tool_middleware"
}

func (m *replacingStreamableToolMiddleware) WrapToolCall() compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Streamable: func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
				output, err := next(ctx, input)
				if err != nil || input == nil || input.Name != constant.ToolTask || output == nil || output.Result == nil {
					return output, err
				}
				atomic.AddInt32(&m.streamableHits, 1)
				var observed string
				for {
					chunk, recvErr := output.Result.Recv()
					if errors.Is(recvErr, io.EOF) {
						break
					}
					if recvErr != nil {
						return nil, recvErr
					}
					observed += chunk
				}
				output.Result.Close()
				m.observedContent = observed
				output.Result = schema.StreamReaderFromArray([]string{m.replacement})
				return output, nil
			}
		},
	}
}

func (t *chunkedStreamableTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "chunked_stream_tool",
		Desc: "streamable tool that emits multiple chunks",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"label": {
				Type:     schema.String,
				Required: true,
			},
		}),
	}, nil
}

func (t *chunkedStreamableTool) StreamableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (*schema.StreamReader[string], error) {
	label := gjson.Get(argumentsInJSON, "label").String()
	reader, writer := schema.Pipe[string](2)
	go func() {
		defer writer.Close()
		writer.Send(label+"_part1", nil)
		writer.Send(label+"_part2", nil)
	}()
	return reader, nil
}

func (t *namedInvokableTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name,
		Desc: "invokable tool with fixed output",
	}, nil
}

func (t *namedInvokableTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	return t.result, nil
}

func (t *countingResultTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name,
		Desc: "counting invokable tool with fixed output",
	}, nil
}

func (t *countingResultTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	atomic.AddInt64(&t.runCount, 1)
	return t.result, nil
}

func (t *countingResultTool) RunCount() int64 {
	return atomic.LoadInt64(&t.runCount)
}

func (t *observingStreamableTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "counter_streaming",
		Desc: "observe streamable tool execution",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"delta": {
				Type:     schema.Integer,
				Required: true,
			},
		}),
	}, nil
}

func (t *observingStreamableTool) StreamableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (*schema.StreamReader[string], error) {
	if t.startedCh != nil {
		select {
		case t.startedCh <- time.Now():
		default:
		}
	}

	delta := gjson.Get(argumentsInJSON, "delta").Int()
	total := atomic.AddInt64(&t.total, delta)
	reader, writer := schema.Pipe[string](1)
	go func() {
		defer writer.Close()
		time.Sleep(t.delay)
		writer.Send(fmt.Sprintf(`{"total":%d}`, total), nil)
	}()
	return reader, nil
}

type toolArgRewriteMiddleware struct {
	mw.BaseMiddleware
	hits int64
}

type callbackEvent struct {
	agentName  string
	agentDepth int
	scope      string
	event      string
	component  string
	name       string
	typ        string
	payload    string
}

type callbackRecorder struct {
	mu     sync.Mutex
	events []callbackEvent
}

func (r *callbackRecorder) add(event callbackEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *callbackRecorder) snapshot() []callbackEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]callbackEvent, len(r.events))
	copy(out, r.events)
	return out
}

type noopContextManager struct {
	mw.BaseMiddleware
}

func (m *noopContextManager) Name() string {
	return "noop_context_manager"
}

func (m *noopContextManager) ModifyModelRequest(ctx context.Context, initialContext []*schema.Message, userInputOrToolRes []*schema.Message, state *types.GraphState) ([]*schema.Message, error) {
	return userInputOrToolRes, nil
}

func (m *toolArgRewriteMiddleware) Name() string {
	return "tool_arg_rewrite"
}

func (m *toolArgRewriteMiddleware) WrapToolCall() compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				atomic.AddInt64(&m.hits, 1)
				input.Arguments = `{"delta":2}`
				return next(ctx, input)
			}
		},
	}
}

// TestDeepAgent_StreamToolCallEnabled_EarlyExecutionAndMiddleware
// 主要验证:
//  1. DeepAgent 开启 stream tool call 后，会在单个 tool call 参数完整时立即触发工具执行，
//     而不是等待整个 model stream EOF。
//  2. 这条新路径下，tool middleware 仍然会命中并生效。
//
// 验证思路:
// - mock chatModel 的首轮 Stream 分两段吐出同一个 tool call，第二段补齐 JSON；
// - 补齐后故意延迟关闭 model stream，并记录 model EOF 时刻；
// - fake tool 在执行入口记录启动时刻；
// - 最后断言 tool 启动时间早于 model EOF，且 middleware 改写参数后的结果真正反映到工具执行结果上。
func TestDeepAgent_StreamToolCallEnabled_EarlyExecutionAndMiddleware(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	toolStartedCh := make(chan time.Time, 1)
	modelEOFCh := make(chan time.Time, 1)
	testTool := &observingInvokableTool{startedCh: toolStartedCh}
	rewriteMW := &toolArgRewriteMiddleware{}
	toolInfo, _ := testTool.Info(ctx)

	var streamCall int32
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&streamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					idx := 0
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "call_enabled",
							Index: &idx,
							Function: schema.FunctionCall{
								Name:      toolInfo.Name,
								Arguments: `{"delta":`,
							},
						}},
					}, nil)
					time.Sleep(80 * time.Millisecond)
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "call_enabled",
							Index: &idx,
							Function: schema.FunctionCall{
								Arguments: `1}`,
							},
						}},
					}, nil)
					time.Sleep(150 * time.Millisecond)
					modelEOFCh <- time.Now()
				case 2:
					writer.Send(schema.AssistantMessage("done-enabled", nil), nil)
				default:
					writer.Send(schema.AssistantMessage("unexpected", nil), nil)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	agent, err := New(ctx,
		WithModel(cm),
		WithTools(testTool),
		WithMiddleware(rewriteMW),
		WithStreamToolCall(),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(&noopContextManager{}),
	)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("run")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}

	toolStartedAt := <-toolStartedCh

	msgs, err := collectAgentMessages(out)
	if err != nil {
		t.Fatalf("collectAgentMessages err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 final assistant message, got %d", len(msgs))
	}
	if msgs[0].Content != "done-enabled" {
		t.Fatalf("unexpected final content: %s", msgs[0].Content)
	}
	if atomic.LoadInt64(&rewriteMW.hits) != 1 {
		t.Fatalf("middleware should be hit once, got %d", atomic.LoadInt64(&rewriteMW.hits))
	}
	if atomic.LoadInt64(&testTool.total) != 2 {
		t.Fatalf("middleware should rewrite delta to 2, got total=%d", atomic.LoadInt64(&testTool.total))
	}

	eofAt := <-modelEOFCh
	if !toolStartedAt.Before(eofAt) {
		t.Fatalf("tool should start before model eof: tool=%v eof=%v", toolStartedAt, eofAt)
	}
}

// TestDeepAgent_StreamToolCallDisabled_DefaultPath
// 主要验证:
// 1. 不开启 stream tool call 时，DeepAgent 仍保持原有默认行为；
// 2. 新增公开开关不会破坏旧路径。
//
// 验证思路:
// - 使用与开启场景相同的分块 tool call mock 输出；
// - 不传 WithStreamToolCall()，让 agent 走默认 tools 路径；
// - 记录 model EOF 和工具启动时刻；
// - 断言最终结果正确，并确认不会出现“在 model EOF 之前就提前执行工具”的行为。
func TestDeepAgent_StreamToolCallDisabled_DefaultPath(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	toolStartedCh := make(chan time.Time, 1)
	modelEOFCh := make(chan time.Time, 1)
	testTool := &observingInvokableTool{startedCh: toolStartedCh}
	toolInfo, _ := testTool.Info(ctx)

	var streamCall int32
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&streamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					idx := 0
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "call_disabled",
							Index: &idx,
							Function: schema.FunctionCall{
								Name:      toolInfo.Name,
								Arguments: `{"delta":`,
							},
						}},
					}, nil)
					time.Sleep(80 * time.Millisecond)
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "call_disabled",
							Index: &idx,
							Function: schema.FunctionCall{
								Arguments: `1}`,
							},
						}},
					}, nil)
					time.Sleep(150 * time.Millisecond)
					modelEOFCh <- time.Now()
				case 2:
					writer.Send(schema.AssistantMessage("done-disabled", nil), nil)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	agent, err := New(ctx,
		WithModel(cm),
		WithTools(testTool),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(&noopContextManager{}),
	)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("run")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}

	eofAt := <-modelEOFCh
	select {
	case startedAt := <-toolStartedCh:
		if startedAt.Before(eofAt) {
			t.Fatalf("tool should not start before model eof when stream tool call is disabled: tool=%v eof=%v", startedAt, eofAt)
		}
	default:
	}

	msgs, err := collectAgentMessages(out)
	if err != nil {
		t.Fatalf("collectAgentMessages err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 final assistant message, got %d", len(msgs))
	}
	if msgs[0].Content != "done-disabled" {
		t.Fatalf("unexpected final content: %s", msgs[0].Content)
	}
	if atomic.LoadInt64(&testTool.total) != 1 {
		t.Fatalf("unexpected total=%d", atomic.LoadInt64(&testTool.total))
	}
}

// TestDeepAgent_SubAgentTaskStreamingWithoutStreamToolCall_ReachesParentContext
// 主要验证:
//  1. 不开启 WithStreamToolCall() 时，父 agent 走默认 compose.ToolsNode 路径；
//  2. task 工具仍然可以是 StreamableTool；
//  3. 子 agent 流式输出的最终 assistant content 会被收敛成一条 ToolMessage，
//     并作为下一轮主模型的上下文输入。
func TestDeepAgent_SubAgentTaskStreamingWithoutStreamToolCall_ReachesParentContext(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	mainCM := mock_model.NewMockToolCallingChatModel(ctrl)
	subCM := mock_model.NewMockToolCallingChatModel(ctrl)
	mainCM.EXPECT().WithTools(gomock.Any()).Return(mainCM, nil).AnyTimes()
	subCM.EXPECT().WithTools(gomock.Any()).Return(subCM, nil).AnyTimes()

	var mainStreamCall int32
	mainCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&mainStreamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					writer.Send(schema.AssistantMessage("delegate", []schema.ToolCall{{
						ID:   "task_call",
						Type: "function",
						Function: schema.FunctionCall{
							Name:      constant.ToolTask,
							Arguments: `{"subagent":"stream_sub","task":"produce streamed result","wait_for_done":true}`,
						},
					}}), nil)
				case 2:
					if len(input) != 1 {
						t.Errorf("expected exactly one tool message in second parent model input, got %d: %+v", len(input), input)
						return
					}
					toolMsg := input[0]
					if toolMsg == nil {
						t.Errorf("expected non-nil tool message")
						return
					}
					if toolMsg.Role != schema.Tool {
						t.Errorf("expected tool role, got %s", toolMsg.Role)
						return
					}
					if toolMsg.ToolCallID != "task_call" {
						t.Errorf("expected task_call tool call id, got %q", toolMsg.ToolCallID)
						return
					}
					if toolMsg.Content != "sub-result-part1sub-result-part2" {
						t.Errorf("expected concatenated subagent result, got %q", toolMsg.Content)
						return
					}
					writer.Send(schema.AssistantMessage("parent-saw-subagent-result", nil), nil)
				default:
					t.Errorf("unexpected parent model stream call %d", callIndex)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	subCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](2)
			go func() {
				defer writer.Close()
				writer.Send(schema.AssistantMessage("sub-result-part1", nil), nil)
				writer.Send(schema.AssistantMessage("sub-result-part2", nil), nil)
			}()
			return reader, nil
		},
	).AnyTimes()

	agent, err := New(ctx,
		WithModel(mainCM),
		WithSubAgents(&subagent.SubAgent{
			Name:        "stream_sub",
			Description: "subagent that streams its final answer",
			Model:       subCM,
			MaxSteps:    2,
		}),
		WithSubAgentTaskStreaming(),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(&noopContextManager{}),
	)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("delegate to stream subagent")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	msgs, err := collectAgentMessages(out)
	if err != nil {
		t.Fatalf("collectAgentMessages err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 final assistant message, got %d", len(msgs))
	}
	if msgs[0].Content != "parent-saw-subagent-result" {
		t.Fatalf("unexpected final content: %s", msgs[0].Content)
	}
	if got := atomic.LoadInt32(&mainStreamCall); got != 2 {
		t.Fatalf("expected parent model to be called twice, got %d", got)
	}
}

func TestDeepAgent_SubAgentTaskStreaming_ToolNodePostHandlerCanClearContent(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	mainCM := mock_model.NewMockToolCallingChatModel(ctrl)
	subCM := mock_model.NewMockToolCallingChatModel(ctrl)
	mainCM.EXPECT().WithTools(gomock.Any()).Return(mainCM, nil).AnyTimes()
	subCM.EXPECT().WithTools(gomock.Any()).Return(subCM, nil).AnyTimes()

	const originalResult = "sub-result-part1sub-result-part2"
	const rewrittenResult = "rewritten-subagent-result"

	var postHandlerSawOriginal int32
	postHandler := func(ctx context.Context, output []*schema.Message) ([]*schema.Message, error) {
		for _, msg := range output {
			if msg == nil || msg.Role != schema.Tool || msg.ToolCallID != "task_call" {
				continue
			}
			if msg.Content != originalResult {
				t.Errorf("post handler expected original content %q, got %q", originalResult, msg.Content)
				continue
			}
			atomic.StoreInt32(&postHandlerSawOriginal, 1)
			msg.Content = ""
			msg.MultiContent = append(msg.MultiContent, schema.ChatMessagePart{
				Type: schema.ChatMessagePartTypeText,
				Text: rewrittenResult,
			})
		}
		return output, nil
	}

	var mainStreamCall int32
	mainCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&mainStreamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					writer.Send(schema.AssistantMessage("delegate", []schema.ToolCall{{
						ID:   "task_call",
						Type: "function",
						Function: schema.FunctionCall{
							Name:      constant.ToolTask,
							Arguments: `{"subagent":"stream_sub","task":"produce streamed result","wait_for_done":true}`,
						},
					}}), nil)
				case 2:
					if len(input) != 1 {
						t.Errorf("expected exactly one tool message in second parent model input, got %d: %+v", len(input), input)
						return
					}
					toolMsg := input[0]
					if toolMsg == nil {
						t.Errorf("expected non-nil tool message")
						return
					}
					if toolMsg.Role != schema.Tool {
						t.Errorf("expected tool role, got %s", toolMsg.Role)
						return
					}
					if toolMsg.Content != "" {
						t.Errorf("expected post handler to clear tool content, got %q", toolMsg.Content)
						return
					}
					if len(toolMsg.MultiContent) != 1 || toolMsg.MultiContent[0].Text != rewrittenResult {
						t.Errorf("expected rewritten result in MultiContent, got %+v", toolMsg.MultiContent)
						return
					}
					writer.Send(schema.AssistantMessage("parent-saw-cleared-tool-content", nil), nil)
				default:
					t.Errorf("unexpected parent model stream call %d", callIndex)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	subCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](2)
			go func() {
				defer writer.Close()
				writer.Send(schema.AssistantMessage("sub-result-part1", nil), nil)
				writer.Send(schema.AssistantMessage("sub-result-part2", nil), nil)
			}()
			return reader, nil
		},
	).AnyTimes()

	agent, err := New(ctx,
		WithModel(mainCM),
		WithSubAgents(&subagent.SubAgent{
			Name:        "stream_sub",
			Description: "subagent that streams its final answer",
			Model:       subCM,
			MaxSteps:    2,
		}),
		WithSubAgentTaskStreaming(),
		WithToolNodePostHandler(postHandler),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(&noopContextManager{}),
	)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("delegate to stream subagent")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	msgs, err := collectAgentMessages(out)
	if err != nil {
		t.Fatalf("collectAgentMessages err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 final assistant message, got %d", len(msgs))
	}
	if msgs[0].Content != "parent-saw-cleared-tool-content" {
		t.Fatalf("unexpected final content: %s", msgs[0].Content)
	}
	if atomic.LoadInt32(&postHandlerSawOriginal) != 1 {
		t.Fatalf("expected post handler to observe original non-empty task result")
	}
}

func TestDeepAgent_SubAgentTaskStreaming_CapcutInvokableOnlyToolInterceptorDoesNotRewriteStreamTask(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	mainCM := mock_model.NewMockToolCallingChatModel(ctrl)
	subCM := mock_model.NewMockToolCallingChatModel(ctrl)
	mainCM.EXPECT().WithTools(gomock.Any()).Return(mainCM, nil).AnyTimes()
	subCM.EXPECT().WithTools(gomock.Any()).Return(subCM, nil).AnyTimes()

	const originalResult = "subagent-long-result"
	interceptor := &capcutInvokableOnlyToolInterceptor{}

	var mainStreamCall int32
	mainCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&mainStreamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					writer.Send(schema.AssistantMessage("delegate", []schema.ToolCall{{
						ID:   "task_call",
						Type: "function",
						Function: schema.FunctionCall{
							Name:      constant.ToolTask,
							Arguments: `{"subagent":"stream_sub","task":"produce streamed result","wait_for_done":true}`,
						},
					}}), nil)
				case 2:
					if len(input) != 1 {
						t.Errorf("expected exactly one tool message in second parent model input, got %d: %+v", len(input), input)
						return
					}
					toolMsg := input[0]
					if toolMsg == nil || toolMsg.Role != schema.Tool || toolMsg.ToolName != constant.ToolTask {
						t.Errorf("expected task tool message, got %+v", toolMsg)
						return
					}
					if toolMsg.Content != originalResult {
						t.Errorf("expected invokable-only middleware not to rewrite stream task result, got %q", toolMsg.Content)
						return
					}
					writer.Send(schema.AssistantMessage("parent-saw-original-result", nil), nil)
				default:
					t.Errorf("unexpected parent model stream call %d", callIndex)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	subCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage(originalResult, nil)}), nil
		},
	).AnyTimes()

	agent, err := New(ctx,
		WithModel(mainCM),
		WithMiddleware(interceptor),
		WithSubAgents(&subagent.SubAgent{
			Name:        "stream_sub",
			Description: "subagent that streams its final answer",
			Model:       subCM,
			MaxSteps:    2,
		}),
		WithSubAgentTaskStreaming(),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(&noopContextManager{}),
	)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("delegate to stream subagent")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	msgs, err := collectAgentMessages(out)
	if err != nil {
		t.Fatalf("collectAgentMessages err: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "parent-saw-original-result" {
		t.Fatalf("unexpected final messages: %+v", msgs)
	}
	if got := atomic.LoadInt32(&interceptor.invokableHits); got != 0 {
		t.Fatalf("invokable-only interceptor hit task %d times, want 0", got)
	}
}

func TestDeepAgent_SubAgentTaskStreaming_StreamableMiddlewareCanRewriteTaskResult(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	mainCM := mock_model.NewMockToolCallingChatModel(ctrl)
	subCM := mock_model.NewMockToolCallingChatModel(ctrl)
	mainCM.EXPECT().WithTools(gomock.Any()).Return(mainCM, nil).AnyTimes()
	subCM.EXPECT().WithTools(gomock.Any()).Return(subCM, nil).AnyTimes()

	const originalResult = "subagent-long-result"
	const rewrittenResult = `{}`
	rewriter := &replacingStreamableToolMiddleware{replacement: rewrittenResult}

	var mainStreamCall int32
	mainCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&mainStreamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					writer.Send(schema.AssistantMessage("delegate", []schema.ToolCall{{
						ID:   "task_call",
						Type: "function",
						Function: schema.FunctionCall{
							Name:      constant.ToolTask,
							Arguments: `{"subagent":"stream_sub","task":"produce streamed result","wait_for_done":true}`,
						},
					}}), nil)
				case 2:
					if len(input) != 1 {
						t.Errorf("expected exactly one tool message in second parent model input, got %d: %+v", len(input), input)
						return
					}
					toolMsg := input[0]
					if toolMsg == nil || toolMsg.Role != schema.Tool || toolMsg.ToolName != constant.ToolTask {
						t.Errorf("expected task tool message, got %+v", toolMsg)
						return
					}
					if toolMsg.Content != rewrittenResult {
						t.Errorf("expected streamable middleware to rewrite task result, got %q", toolMsg.Content)
						return
					}
					writer.Send(schema.AssistantMessage("parent-saw-rewritten-result", nil), nil)
				default:
					t.Errorf("unexpected parent model stream call %d", callIndex)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	subCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage(originalResult, nil)}), nil
		},
	).AnyTimes()

	agent, err := New(ctx,
		WithModel(mainCM),
		WithMiddleware(rewriter),
		WithSubAgents(&subagent.SubAgent{
			Name:        "stream_sub",
			Description: "subagent that streams its final answer",
			Model:       subCM,
			MaxSteps:    2,
		}),
		WithSubAgentTaskStreaming(),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(&noopContextManager{}),
	)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("delegate to stream subagent")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	msgs, err := collectAgentMessages(out)
	if err != nil {
		t.Fatalf("collectAgentMessages err: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "parent-saw-rewritten-result" {
		t.Fatalf("unexpected final messages: %+v", msgs)
	}
	if got := atomic.LoadInt32(&rewriter.streamableHits); got != 1 {
		t.Fatalf("streamable middleware hits = %d, want 1", got)
	}
	if rewriter.observedContent != originalResult {
		t.Fatalf("streamable middleware observed %q, want %q", rewriter.observedContent, originalResult)
	}
}

func TestDeepAgent_SubAgentTaskStreaming_IgnoresChildToolMessages(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	mainCM := mock_model.NewMockToolCallingChatModel(ctrl)
	subCM := mock_model.NewMockToolCallingChatModel(ctrl)
	mainCM.EXPECT().WithTools(gomock.Any()).Return(mainCM, nil).AnyTimes()
	subCM.EXPECT().WithTools(gomock.Any()).Return(subCM, nil).AnyTimes()

	var mainStreamCall int32
	mainCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&mainStreamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					writer.Send(schema.AssistantMessage("delegate", []schema.ToolCall{{
						ID:   "task_call",
						Type: "function",
						Function: schema.FunctionCall{
							Name:      constant.ToolTask,
							Arguments: `{"subagent":"stream_sub","task":"use child tool then answer","wait_for_done":true}`,
						},
					}}), nil)
				case 2:
					if len(input) != 1 {
						t.Errorf("expected exactly one tool message in second parent model input, got %d: %+v", len(input), input)
						return
					}
					toolMsg := input[0]
					if toolMsg == nil {
						t.Errorf("expected non-nil tool message")
						return
					}
					if toolMsg.Role != schema.Tool {
						t.Errorf("expected tool role, got %s", toolMsg.Role)
						return
					}
					if toolMsg.Content != "child-final-answer" {
						t.Errorf("expected parent task result to ignore child tool output, got %q", toolMsg.Content)
						return
					}
					writer.Send(schema.AssistantMessage("parent-saw-child-final-answer", nil), nil)
				default:
					t.Errorf("unexpected parent model stream call %d", callIndex)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	var subStreamCall int32
	subCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](2)
			callIndex := atomic.AddInt32(&subStreamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					writer.Send(schema.AssistantMessage("call child tool", []schema.ToolCall{{
						ID:   "child_tool_call",
						Type: "function",
						Function: schema.FunctionCall{
							Name:      "child_json_tool",
							Arguments: `{}`,
						},
					}}), nil)
				case 2:
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						MultiContent: []schema.ChatMessagePart{{
							Type: schema.ChatMessagePartTypeText,
							Text: "child-final-answer",
						}},
					}, nil)
				default:
					t.Errorf("unexpected child model stream call %d", callIndex)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	agent, err := New(ctx,
		WithModel(mainCM),
		WithSubAgents(&subagent.SubAgent{
			Name:        "stream_sub",
			Description: "subagent that uses a tool before final answer",
			Model:       subCM,
			Tools: []tool.BaseTool{
				&namedInvokableTool{name: "child_json_tool", result: `{}`},
			},
			MaxSteps: 3,
		}),
		WithSubAgentTaskStreaming(),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(&noopContextManager{}),
	)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("delegate to child")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	msgs, err := collectAgentMessages(out)
	if err != nil {
		t.Fatalf("collectAgentMessages err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 final assistant message, got %d", len(msgs))
	}
	if msgs[0].Content != "parent-saw-child-final-answer" {
		t.Fatalf("unexpected final content: %s", msgs[0].Content)
	}
}

// TestDeepAgent_StreamToolCallEnabled_Performance
// 主要验证:
// 开启 stream tool call 后，DeepAgent 能把“模型生成 tool call”和“工具执行”两段时间流水线化，
// 整体耗时符合预期，而不是退化成“等全部 tool call 吐完再统一执行”。
//
// 验证思路:
//   - mock chatModel 顺序吐出 4 个 tool call，每个 tool call 需要 100ms 才完整闭合；
//   - fake streamable tool 的 StreamableRun 立即返回，但后台 goroutine 额外花 100ms 才吐完结果；
//   - 理论上如果是串行，总耗时会接近 800ms；如果是边吐边执行，总耗时会接近 500ms；
//   - 测试记录首个 tool call 完整时间、首个工具启动时间、全部 tool call 完整时间和总耗时，
//     并严格断言总耗时小于 600ms。
func TestDeepAgent_StreamToolCallEnabled_Performance(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	toolStartedCh := make(chan time.Time, 8)
	testTool := &observingStreamableTool{
		startedCh: toolStartedCh,
		delay:     100 * time.Millisecond,
	}
	toolInfo, _ := testTool.Info(ctx)

	firstCompleteCh := make(chan time.Time, 1)
	allCompleteCh := make(chan time.Time, 1)

	var streamCall int32
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](8)
			callIndex := atomic.AddInt32(&streamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					for i := 0; i < 4; i++ {
						callID := fmt.Sprintf("perf_call_%d", i)
						idx := i
						writer.Send(&schema.Message{
							Role: schema.Assistant,
							ToolCalls: []schema.ToolCall{{
								ID:    callID,
								Index: &idx,
								Function: schema.FunctionCall{
									Name:      toolInfo.Name,
									Arguments: `{"delta":`,
								},
							}},
						}, nil)
						time.Sleep(100 * time.Millisecond)
						writer.Send(&schema.Message{
							Role: schema.Assistant,
							ToolCalls: []schema.ToolCall{{
								ID:    callID,
								Index: &idx,
								Function: schema.FunctionCall{
									Arguments: `1}`,
								},
							}},
						}, nil)
						now := time.Now()
						if i == 0 {
							firstCompleteCh <- now
						}
					}
					allCompleteCh <- time.Now()
				case 2:
					writer.Send(schema.AssistantMessage("done-performance", nil), nil)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	agent, err := New(ctx,
		WithModel(cm),
		WithTools(testTool),
		WithStreamToolCall(),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(&noopContextManager{}),
	)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	start := time.Now()
	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("run perf")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}

	msgs, err := collectAgentMessages(out)
	if err != nil {
		t.Fatalf("collectAgentMessages err: %v", err)
	}
	elapsed := time.Since(start)

	firstToolCallCompleteAt := <-firstCompleteCh
	firstToolStartedAt := <-toolStartedCh
	allToolCallsCompleteAt := <-allCompleteCh

	t.Logf("performance timeline: firstToolCallComplete=%s firstToolStarted=%s allToolCallsComplete=%s totalElapsed=%s",
		firstToolCallCompleteAt.Sub(start).String(),
		firstToolStartedAt.Sub(start).String(),
		allToolCallsCompleteAt.Sub(start).String(),
		elapsed.String(),
	)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 final assistant message, got %d", len(msgs))
	}
	if msgs[0].Content != "done-performance" {
		t.Fatalf("unexpected final content: %s", msgs[0].Content)
	}
	if firstToolStartedAt.Before(firstToolCallCompleteAt) {
		t.Fatalf("tool should not start before the first tool call is complete: started=%v complete=%v", firstToolStartedAt, firstToolCallCompleteAt)
	}
	if atomic.LoadInt64(&testTool.total) != 4 {
		t.Fatalf("unexpected total=%d", atomic.LoadInt64(&testTool.total))
	}
	if elapsed >= 600*time.Millisecond {
		t.Fatalf("expected total elapsed < 600ms, got %s", elapsed)
	}
}

// TestDeepAgent_StreamToolCallEnabled_ApprovalInterruptResume_DropsIncompleteFutureCalls
// 主要验证:
//  1. 在 DeepAgent 开启 stream tool call 时，如果第 1 个审批工具已经完整收集并触发中断，而第 2 个 tool call 只吐出一半，
//     恢复后不会再继续处理那个未完整的后续 tool call。
//  2. 恢复时给出审批通过数据后，只会执行第 1 个工具，并把这一条 tool message 传给下一轮模型。
//
// 验证思路:
// - 首轮模型流式输出一个完整的审批工具调用，再输出一个未闭合的后续 tool call；
// - 第一次运行拿到 approval interrupt；
// - 第二次运行用 WithResumeData 提供审批通过结果；
// - 在恢复后的模型调用里断言它只收到了 1 条 tool message，且对应第 1 个 call。
func TestDeepAgent_StreamToolCallEnabled_ApprovalInterruptResume_DropsIncompleteFutureCalls(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	store := checkpointer.NewInMemoryStore()
	approvedToolBase := &countingResultTool{name: "approval_tool", result: `{"approved":true}`}
	lostTool := &countingResultTool{name: "lost_tool", result: `{"lost":false}`}

	approvedTool := deeptools.NewInvokablePolicyTool(approvedToolBase, deeptools.ApprovalGate(func(context.Context, *deeptools.ApprovalInfo) bool {
		return true
	}))

	var streamCall int32
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&streamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					idx0 := 0
					idx1 := 1
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "approve_call",
							Index: &idx0,
							Function: schema.FunctionCall{
								Name:      "approval_tool",
								Arguments: `{"task":"approve"}`,
							},
						}},
					}, nil)
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "lost_call",
							Index: &idx1,
							Function: schema.FunctionCall{
								Arguments: `{"task":"lo`,
							},
						}},
					}, nil)
				case 2:
					if len(input) != 1 {
						writer.Send(nil, fmt.Errorf("expected only 1 tool message after resume, got %d", len(input)))
						return
					}
					if input[0].ToolCallID != "approve_call" {
						writer.Send(nil, fmt.Errorf("expected approve_call only, got %+v", input))
						return
					}
					if input[0].Content != `{"approved":true}` {
						writer.Send(nil, fmt.Errorf("unexpected resumed tool output: %s", input[0].Content))
						return
					}
					writer.Send(schema.AssistantMessage("resume-only-first-tool", nil), nil)
				default:
					writer.Send(schema.AssistantMessage("unexpected", nil), nil)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	agent, err := New(ctx,
		WithModel(cm),
		WithTools(approvedTool, lostTool),
		WithStreamToolCall(),
		WithCheckpointStore(store),
		WithContextManager(&noopContextManager{}),
	)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	const checkpointID = "stream-approval-partial"
	_, err = agent.Stream(ctx, []*schema.Message{schema.UserMessage("run partial approval")}, WithCheckpointID(checkpointID))
	if err == nil {
		t.Fatal("expected interrupt error, got nil")
	}

	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatalf("expected interrupt info, got err=%v", err)
	}
	if len(info.InterruptContexts) == 0 {
		t.Fatalf("expected interrupt contexts, got %+v", info)
	}
	if _, ok := info.InterruptContexts[0].Info.(*deeptools.ApprovalInfo); !ok {
		t.Fatalf("expected approval interrupt info, got %+v", info.InterruptContexts[0].Info)
	}

	resumeData := map[string]any{
		info.InterruptContexts[0].ID: &deeptools.ApprovalResult{Approved: true},
	}
	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("resume partial approval")},
		WithCheckpointID(checkpointID),
		WithResumeData(resumeData),
	)
	if err != nil {
		t.Fatalf("resume Stream err: %v", err)
	}

	msgs, err := collectAgentMessages(out)
	if err != nil {
		t.Fatalf("collectAgentMessages err: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "resume-only-first-tool" {
		t.Fatalf("unexpected final messages: %+v", msgs)
	}
	if approvedToolBase.RunCount() != 1 {
		t.Fatalf("approved tool should execute exactly once after resume, got %d", approvedToolBase.RunCount())
	}
	if lostTool.RunCount() != 0 {
		t.Fatalf("incomplete future tool should not execute, got %d", lostTool.RunCount())
	}
}

// TestDeepAgent_StreamToolCallEnabled_ApprovalInterruptResume_RerunsOnlyInterruptedCall
// 主要验证:
//  1. 当 4 个 tool call 都已经完整进入 tools 节点，其中前 3 个已执行、最后 1 个审批中断时，
//     恢复后只需要补执行最后 1 个。
//  2. 下一轮模型看到的是按原 tool call 顺序排列的 4 条最终 tool messages。
//
// 验证思路:
// - 首轮模型一次性流式吐出 4 个完整 tool calls，最后一个包上 HITL approval wrapper；
// - 第一次运行返回审批中断；
// - 第二次运行提供审批通过结果；
// - 在恢复后的模型调用中检查 4 条 tool messages 顺序和内容，并断言前 3 个工具没有重跑。
func TestDeepAgent_StreamToolCallEnabled_ApprovalInterruptResume_RerunsOnlyInterruptedCall(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	store := checkpointer.NewInMemoryStore()
	tool1 := &countingResultTool{name: "plain_tool_1", result: `{"tool":1}`}
	tool2 := &countingResultTool{name: "plain_tool_2", result: `{"tool":2}`}
	tool3 := &countingResultTool{name: "plain_tool_3", result: `{"tool":3}`}
	tool4Base := &countingResultTool{name: "approval_tool_4", result: `{"tool":4}`}
	tool4 := deeptools.NewInvokablePolicyTool(tool4Base, deeptools.ApprovalGate(func(context.Context, *deeptools.ApprovalInfo) bool {
		return true
	}))

	var streamCall int32
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&streamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{
							{ID: "call_1", Function: schema.FunctionCall{Name: "plain_tool_1", Arguments: `{"n":1}`}},
							{ID: "call_2", Function: schema.FunctionCall{Name: "plain_tool_2", Arguments: `{"n":2}`}},
							{ID: "call_3", Function: schema.FunctionCall{Name: "plain_tool_3", Arguments: `{"n":3}`}},
							{ID: "call_4", Function: schema.FunctionCall{Name: "approval_tool_4", Arguments: `{"n":4}`}},
						},
					}, nil)
				case 2:
					if len(input) != 4 {
						writer.Send(nil, fmt.Errorf("expected 4 tool messages after resume, got %d", len(input)))
						return
					}
					sort.Slice(input, func(i, j int) bool {
						return input[i].ToolCallID < input[j].ToolCallID
					})
					expected := []struct {
						callID  string
						content string
					}{
						{callID: "call_1", content: `{"tool":1}`},
						{callID: "call_2", content: `{"tool":2}`},
						{callID: "call_3", content: `{"tool":3}`},
						{callID: "call_4", content: `{"tool":4}`},
					}
					for i, exp := range expected {
						if input[i].ToolCallID != exp.callID {
							writer.Send(nil, fmt.Errorf("tool message %d expected callID %s, got %s", i, exp.callID, input[i].ToolCallID))
							return
						}
						if input[i].Content != exp.content {
							writer.Send(nil, fmt.Errorf("tool message %d expected content %s, got %s", i, exp.content, input[i].Content))
							return
						}
					}
					writer.Send(schema.AssistantMessage("resume-rerun-last-only", nil), nil)
				default:
					writer.Send(schema.AssistantMessage("unexpected", nil), nil)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	agent, err := New(ctx,
		WithModel(cm),
		WithTools(tool1, tool2, tool3, tool4),
		WithStreamToolCall(),
		WithCheckpointStore(store),
		WithContextManager(&noopContextManager{}),
	)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	const checkpointID = "stream-approval-rerun-last"
	_, err = agent.Stream(ctx, []*schema.Message{schema.UserMessage("run full approval")}, WithCheckpointID(checkpointID))
	if err == nil {
		t.Fatal("expected interrupt error, got nil")
	}

	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatalf("expected interrupt info, got err=%v", err)
	}
	if len(info.InterruptContexts) == 0 {
		t.Fatalf("expected interrupt contexts, got %+v", info)
	}
	if _, ok := info.InterruptContexts[0].Info.(*deeptools.ApprovalInfo); !ok {
		t.Fatalf("expected approval interrupt info, got %+v", info.InterruptContexts[0].Info)
	}

	resumeData := map[string]any{
		info.InterruptContexts[0].ID: &deeptools.ApprovalResult{Approved: true},
	}
	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("resume full approval")},
		WithCheckpointID(checkpointID),
		WithResumeData(resumeData),
	)
	if err != nil {
		t.Fatalf("resume Stream err: %v", err)
	}

	msgs, err := collectAgentMessages(out)
	if err != nil {
		t.Fatalf("collectAgentMessages err: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "resume-rerun-last-only" {
		t.Fatalf("unexpected final messages: %+v", msgs)
	}
	if tool1.RunCount() != 1 || tool2.RunCount() != 1 || tool3.RunCount() != 1 {
		t.Fatalf("completed tools should not rerun, got counts: %d %d %d", tool1.RunCount(), tool2.RunCount(), tool3.RunCount())
	}
	if tool4Base.RunCount() != 1 {
		t.Fatalf("approval tool should execute exactly once after approval, got %d", tool4Base.RunCount())
	}
}

// TestDeepAgent_StreamToolCallEnabled_DownstreamModelSeesFinalOrderedToolMessages
// 主要验证:
//  1. 开启 stream tool call 后，tools 节点内部虽然会产出按槽位对齐的多 chunk 流，
//     但下游 chatModel 实际收到的仍然是收敛后的普通 []*schema.Message。
//  2. 多个 tool call 场景下，下游看到的工具消息顺序与原始 tool call 顺序一致；
//     流式工具的多段结果也会先 concat 成最终内容，再传给下游模型。
//
// 验证思路:
//   - 首轮 mock chatModel 流式产出 3 个 tool calls，其中第 2 个工具本身会流式吐出两段结果；
//   - tools 节点内部会产生多个按槽位对齐的 chunk，但 graph 会在 tools -> executor 这条边上先做收敛；
//   - 第二轮 mock chatModel 直接检查它收到的输入，确认看到的是最终的 3 条 tool messages，
//     顺序稳定，内容已是 concat 后结果，而不是中间 chunk。
func TestDeepAgent_StreamToolCallEnabled_DownstreamModelSeesFinalOrderedToolMessages(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	chunkedTool := &chunkedStreamableTool{}
	staticTool0 := &namedInvokableTool{name: "static_tool_0", result: `{"total":1}`}
	staticTool2 := &namedInvokableTool{name: "static_tool_2", result: `{"total":3}`}

	tool0Info, _ := staticTool0.Info(ctx)
	tool1Info, _ := chunkedTool.Info(ctx)
	tool2Info, _ := staticTool2.Info(ctx)

	var streamCall int32
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](8)
			callIndex := atomic.AddInt32(&streamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					idx0 := 0
					idx1 := 1
					idx2 := 2
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "call_0",
							Index: &idx0,
							Function: schema.FunctionCall{
								Name:      tool0Info.Name,
								Arguments: `{"delta":1}`,
							},
						}},
					}, nil)
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "call_1",
							Index: &idx1,
							Function: schema.FunctionCall{
								Name:      tool1Info.Name,
								Arguments: `{"label":"mid`,
							},
						}},
					}, nil)
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "call_1",
							Index: &idx1,
							Function: schema.FunctionCall{
								Arguments: `dle"}`,
							},
						}},
					}, nil)
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID:    "call_2",
							Index: &idx2,
							Function: schema.FunctionCall{
								Name:      tool2Info.Name,
								Arguments: `{"delta":3}`,
							},
						}},
					}, nil)
				case 2:
					if len(input) != 3 {
						writer.Send(nil, fmt.Errorf("expected 3 final tool messages, got %d messages", len(input)))
						return
					}

					toolMsgs := input
					expected := []struct {
						callID  string
						content string
					}{
						{callID: "call_0", content: `{"total":1}`},
						{callID: "call_1", content: "middle_part1middle_part2"},
						{callID: "call_2", content: `{"total":3}`},
					}

					for i, exp := range expected {
						msg := toolMsgs[i]
						if msg == nil {
							writer.Send(nil, fmt.Errorf("tool message %d is nil", i))
							return
						}
						if msg.Role != schema.Tool {
							writer.Send(nil, fmt.Errorf("tool message %d expected role tool, got %s", i, msg.Role))
							return
						}
						if msg.ToolCallID != exp.callID {
							writer.Send(nil, fmt.Errorf("tool message %d expected callID %s, got %s", i, exp.callID, msg.ToolCallID))
							return
						}
						if msg.Content != exp.content {
							writer.Send(nil, fmt.Errorf("tool message %d expected content %s, got %s", i, exp.content, msg.Content))
							return
						}
					}

					for _, msg := range toolMsgs {
						if msg.Content == "middle_part1" || msg.Content == "middle_part2" {
							writer.Send(nil, fmt.Errorf("downstream model should not see intermediate tool chunks directly: %+v", msg))
							return
						}
					}

					writer.Send(schema.AssistantMessage("verified-final-tool-input", nil), nil)
				default:
					writer.Send(schema.AssistantMessage("unexpected", nil), nil)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	agent, err := New(ctx,
		WithModel(cm),
		WithTools(staticTool0, chunkedTool, staticTool2),
		WithStreamToolCall(),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(&noopContextManager{}),
	)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("run ordering")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}

	msgs, err := collectAgentMessages(out)
	if err != nil {
		t.Fatalf("collectAgentMessages err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 final assistant message, got %d", len(msgs))
	}
	if msgs[0].Content != "verified-final-tool-input" {
		t.Fatalf("unexpected final content: %s", msgs[0].Content)
	}
}

// TestDeepAgent_Callbacks_StreamToolCallEnabled_ToolCallbacksStillFire
// 主要验证:
// 1. 开启 WithStreamToolCall() 后，NewHandlerHelper().Tool(...) 仍然会命中真实工具执行。
// 2. invokable tool 和 streamable tool 的 callback 分支会按官方语义分别落到 OnEnd / OnEndWithStreamOutput。
//
// 验证思路:
// - 首轮模型发 2 个工具调用，一个走 invokable tool，一个走 streamable tool；
// - 通过 deepagents.WithCallbacks(...) 注入 Tool callback 记录事件；
// - 运行结束后断言 Tool.OnStart 至少触发 2 次，且两个真实 tool name 都出现；
// - 同时断言 invokable tool 命中 OnEnd，streamable tool 命中 OnEndWithStreamOutput。
func TestDeepAgent_Callbacks_StreamToolCallEnabled_ToolCallbacksStillFire(t *testing.T) {
	events, err := runCallbackScenario(t, true)
	if err != nil {
		t.Fatalf("runCallbackScenario err: %v", err)
	}

	toolStarts := filterCallbackEvents(events, "tool", "start")
	if len(toolStarts) < 2 {
		t.Fatalf("expected at least 2 tool start callbacks, got %d: %+v", len(toolStarts), toolStarts)
	}

	assertHasEvent(t, events, callbackEvent{
		scope:     "tool",
		event:     "start",
		component: string(components.ComponentOfTool),
		name:      "callback_invokable_tool",
	})
	assertHasEvent(t, events, callbackEvent{
		scope:     "tool",
		event:     "start",
		component: string(components.ComponentOfTool),
		name:      "callback_streamable_tool",
	})
	assertHasEvent(t, events, callbackEvent{
		scope:     "tool",
		event:     "end",
		component: string(components.ComponentOfTool),
		name:      "callback_invokable_tool",
		payload:   `{"kind":"invokable"}`,
	})
	assertHasEvent(t, events, callbackEvent{
		scope:     "tool",
		event:     "end_stream",
		component: string(components.ComponentOfTool),
		name:      "callback_streamable_tool",
	})
}

// TestDeepAgent_Callbacks_StreamToolCallEnabled_LambdaCallbacksWrapOuterToolsNode
// 主要验证:
// 开启 WithStreamToolCall() 后，外层 tools 节点在 graph 视角上已经是 Lambda，而不是官方 ToolsNode。
//
// 验证思路:
// - 继续沿用同一组工具调用场景；
// - 注入 Lambda callback，记录 stream input / stream output 生命周期事件；
// - 断言会收到名为 tools 的 Lambda 节点回调，说明外层节点身份已经变成 graph lambda。
func TestDeepAgent_Callbacks_StreamToolCallEnabled_LambdaCallbacksWrapOuterToolsNode(t *testing.T) {
	events, err := runCallbackScenario(t, true)
	if err != nil {
		t.Fatalf("runCallbackScenario err: %v", err)
	}

	assertHasEvent(t, events, callbackEvent{
		scope:     "lambda",
		event:     "start_stream",
		component: string(compose.ComponentOfLambda),
		name:      constant.NodeKeyTools,
	})
	assertHasEvent(t, events, callbackEvent{
		scope:     "lambda",
		event:     "end_stream",
		component: string(compose.ComponentOfLambda),
		name:      constant.NodeKeyTools,
	})
}

// TestDeepAgent_Callbacks_StreamToolCallDisabled_NoLambdaToolsWrapper
// 主要验证:
// 1. 不开启 WithStreamToolCall() 时，工具级 callback 仍然正常；
// 2. 这时不会出现外层 tools lambda 的 callback。
//
// 验证思路:
// - 使用与开启场景相同的工具和模型响应；
// - 不传 WithStreamToolCall()；
// - 断言 Tool callback 仍能命中，同时不存在名为 tools 的 Lambda callback。
func TestDeepAgent_Callbacks_StreamToolCallDisabled_NoLambdaToolsWrapper(t *testing.T) {
	events, err := runCallbackScenario(t, false)
	if err != nil {
		t.Fatalf("runCallbackScenario err: %v", err)
	}

	assertHasEvent(t, events, callbackEvent{
		scope:     "tool",
		event:     "start",
		component: string(components.ComponentOfTool),
		name:      "callback_invokable_tool",
	})
	assertHasEvent(t, events, callbackEvent{
		scope:     "tool",
		event:     "end_stream",
		component: string(components.ComponentOfTool),
		name:      "callback_streamable_tool",
	})
	assertNoEvent(t, events, callbackEvent{
		scope:     "lambda",
		component: string(compose.ComponentOfLambda),
		name:      constant.NodeKeyTools,
	})
}

// TestDeepAgent_Callbacks_StreamToolCallEnabled_ToolStreamCallbackCarriesToolNameChunksAndEOF
// 主要验证:
// 1. Tool.OnEndWithStreamOutput 可以告诉我们“是哪个流式工具”在输出。
// 2. callback 里拿到的 stream 副本能读到完整分片内容，并能感知分片何时结束。
//
// 验证思路:
// - 让 callback_streamable_tool 返回两个 chunk；
// - 在 Tool.OnEndWithStreamOutput 里主动读 output 副本，记录每个 chunk 和 EOF 完成标记；
// - 断言 info.Name 是真实工具名，chunk 内容与工具输出一致，最后会收到 done 事件。
func TestDeepAgent_Callbacks_StreamToolCallEnabled_ToolStreamCallbackCarriesToolNameChunksAndEOF(t *testing.T) {
	events, err := runCallbackScenario(t, true)
	if err != nil {
		t.Fatalf("runCallbackScenario err: %v", err)
	}

	assertHasEvent(t, events, callbackEvent{
		scope:     "tool",
		event:     "end_stream",
		component: string(components.ComponentOfTool),
		name:      "callback_streamable_tool",
	})
	assertHasEvent(t, events, callbackEvent{
		scope:     "tool",
		event:     "stream_chunk",
		component: string(components.ComponentOfTool),
		name:      "callback_streamable_tool",
		payload:   `{"kind":"stream_part1"}`,
	})
	assertHasEvent(t, events, callbackEvent{
		scope:     "tool",
		event:     "stream_chunk",
		component: string(components.ComponentOfTool),
		name:      "callback_streamable_tool",
		payload:   `{"kind":"stream_part2"}`,
	})
	assertHasEvent(t, events, callbackEvent{
		scope:     "tool",
		event:     "stream_done",
		component: string(components.ComponentOfTool),
		name:      "callback_streamable_tool",
	})
}

// TestDeepAgent_Callbacks_StreamToolCallEnabled_LambdaStreamCallbackCarriesSlotAlignedChunksAndEOF
// 主要验证:
// 1. Lambda.OnEndWithStreamOutput 看到的是外层 tools 节点对 graph 的输出，而不是工具原始字符串流。
// 2. 这里拿到的每个 chunk 都是按 tool-call 顺序对齐槽位的 []*schema.Message 数组。
// 3. callback 可以感知整个 tools 输出流何时结束。
//
// 验证思路:
// - 首轮模型仍然发 invokable + streamable 两个 tool calls；
// - 在 Lambda.OnEndWithStreamOutput 中把 output 副本读完，并把每个 chunk 规格化成可断言字符串；
// - 断言会看到:
//   - invokable tool 对应槽位 chunk
//   - streamable tool 的两个槽位 chunk
//   - 最后有 stream_done 标记
func TestDeepAgent_Callbacks_StreamToolCallEnabled_LambdaStreamCallbackCarriesSlotAlignedChunksAndEOF(t *testing.T) {
	events, err := runCallbackScenario(t, true)
	if err != nil {
		t.Fatalf("runCallbackScenario err: %v", err)
	}

	assertHasEvent(t, events, callbackEvent{
		scope:     "lambda",
		event:     "stream_chunk",
		component: string(compose.ComponentOfLambda),
		name:      constant.NodeKeyTools,
		payload:   `[callback_call_0:{"kind":"invokable"}|nil]`,
	})
	assertHasEvent(t, events, callbackEvent{
		scope:     "lambda",
		event:     "stream_chunk",
		component: string(compose.ComponentOfLambda),
		name:      constant.NodeKeyTools,
		payload:   `[nil|callback_call_1:{"kind":"stream_part1"}]`,
	})
	assertHasEvent(t, events, callbackEvent{
		scope:     "lambda",
		event:     "stream_chunk",
		component: string(compose.ComponentOfLambda),
		name:      constant.NodeKeyTools,
		payload:   `[nil|callback_call_1:{"kind":"stream_part2"}]`,
	})
	assertHasEvent(t, events, callbackEvent{
		scope:     "lambda",
		event:     "stream_done",
		component: string(compose.ComponentOfLambda),
		name:      constant.NodeKeyTools,
	})
}

func collectAgentMessages(reader *schema.StreamReader[*schema.Message]) ([]*schema.Message, error) {
	defer reader.Close()

	var msgs []*schema.Message
	for {
		msg, err := reader.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return msgs, nil
			}
			return nil, err
		}
		msgs = append(msgs, msg)
	}
}

func runCallbackScenario(t *testing.T, enableStreamToolCall bool) ([]callbackEvent, error) {
	t.Helper()

	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	invokableTool := &namedInvokableTool{name: "callback_invokable_tool", result: `{"kind":"invokable"}`}
	streamableTool := &chunkedNamedStreamableTool{name: "callback_streamable_tool", first: `{"kind":"stream_part1"}`, second: `{"kind":"stream_part2"}`}

	recorder := &callbackRecorder{}
	handler := cbutils.NewHandlerHelper().
		Tool(&cbutils.ToolCallbackHandler{
			OnStart: func(ctx context.Context, info *callbacks.RunInfo, input *tool.CallbackInput) context.Context {
				recorder.add(callbackEvent{
					scope:     "tool",
					event:     "start",
					component: string(info.Component),
					name:      info.Name,
					typ:       info.Type,
					payload:   input.ArgumentsInJSON,
				})
				return ctx
			},
			OnEnd: func(ctx context.Context, info *callbacks.RunInfo, output *tool.CallbackOutput) context.Context {
				recorder.add(callbackEvent{
					scope:     "tool",
					event:     "end",
					component: string(info.Component),
					name:      info.Name,
					typ:       info.Type,
					payload:   output.Response,
				})
				return ctx
			},
			OnEndWithStreamOutput: func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[*tool.CallbackOutput]) context.Context {
				recorder.add(callbackEvent{
					scope:     "tool",
					event:     "end_stream",
					component: string(info.Component),
					name:      info.Name,
					typ:       info.Type,
				})
				readToolCallbackStream(recorder, info, output)
				return ctx
			},
			OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
				recorder.add(callbackEvent{
					scope:     "tool",
					event:     "error",
					component: string(info.Component),
					name:      info.Name,
					typ:       info.Type,
					payload:   err.Error(),
				})
				return ctx
			},
		}).
		Lambda(callbacks.NewHandlerBuilder().
			OnStartWithStreamInputFn(func(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
				recorder.add(callbackEvent{
					scope:     "lambda",
					event:     "start_stream",
					component: string(info.Component),
					name:      info.Name,
					typ:       info.Type,
				})
				return ctx
			}).
			OnEndWithStreamOutputFn(func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
				recorder.add(callbackEvent{
					scope:     "lambda",
					event:     "end_stream",
					component: string(info.Component),
					name:      info.Name,
					typ:       info.Type,
				})
				readLambdaCallbackStream(recorder, info, output)
				return ctx
			}).
			OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
				recorder.add(callbackEvent{
					scope:     "lambda",
					event:     "error",
					component: string(info.Component),
					name:      info.Name,
					typ:       info.Type,
					payload:   err.Error(),
				})
				return ctx
			}).
			Build()).
		Handler()

	var streamCall int32
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](4)
			callIndex := atomic.AddInt32(&streamCall, 1)
			go func() {
				defer writer.Close()
				switch callIndex {
				case 1:
					idx0 := 0
					idx1 := 1
					writer.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{
							{
								ID:    "callback_call_0",
								Index: &idx0,
								Function: schema.FunctionCall{
									Name:      "callback_invokable_tool",
									Arguments: `{"kind":"invokable"}`,
								},
							},
							{
								ID:    "callback_call_1",
								Index: &idx1,
								Function: schema.FunctionCall{
									Name:      "callback_streamable_tool",
									Arguments: `{"kind":"streamable"}`,
								},
							},
						},
					}, nil)
				case 2:
					writer.Send(schema.AssistantMessage("callbacks-done", nil), nil)
				}
			}()
			return reader, nil
		},
	).AnyTimes()

	opts := []Option{
		WithModel(cm),
		WithTools(invokableTool, streamableTool),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(&noopContextManager{}),
	}
	if enableStreamToolCall {
		opts = append(opts, WithStreamToolCall())
	}

	agent, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("run callbacks")}, WithCallbacks(handler))
	if err != nil {
		return nil, err
	}

	msgs, err := collectAgentMessages(out)
	if err != nil {
		return nil, err
	}
	if len(msgs) != 1 || msgs[0].Content != "callbacks-done" {
		return nil, fmt.Errorf("unexpected final messages: %+v", msgs)
	}

	return recorder.snapshot(), nil
}

type chunkedNamedStreamableTool struct {
	name   string
	first  string
	second string
}

func (t *chunkedNamedStreamableTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name,
		Desc: "named streamable tool for callback tests",
	}, nil
}

func (t *chunkedNamedStreamableTool) StreamableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (*schema.StreamReader[string], error) {
	reader, writer := schema.Pipe[string](2)
	go func() {
		defer writer.Close()
		writer.Send(t.first, nil)
		writer.Send(t.second, nil)
	}()
	return reader, nil
}

func filterCallbackEvents(events []callbackEvent, scope string, event string) []callbackEvent {
	var out []callbackEvent
	for _, item := range events {
		if item.scope == scope && item.event == event {
			out = append(out, item)
		}
	}
	return out
}

func assertHasEvent(t *testing.T, events []callbackEvent, want callbackEvent) {
	t.Helper()
	for _, item := range events {
		if want.scope != "" && item.scope != want.scope {
			continue
		}
		if want.event != "" && item.event != want.event {
			continue
		}
		if want.component != "" && item.component != want.component {
			continue
		}
		if want.name != "" && item.name != want.name {
			continue
		}
		if want.typ != "" && item.typ != want.typ {
			continue
		}
		if want.payload != "" && item.payload != want.payload {
			continue
		}
		return
	}
	t.Fatalf("expected event not found: want=%+v events=%+v", want, events)
}

func assertNoEvent(t *testing.T, events []callbackEvent, want callbackEvent) {
	t.Helper()
	for _, item := range events {
		if want.scope != "" && item.scope != want.scope {
			continue
		}
		if want.event != "" && item.event != want.event {
			continue
		}
		if want.component != "" && item.component != want.component {
			continue
		}
		if want.name != "" && item.name != want.name {
			continue
		}
		if want.typ != "" && item.typ != want.typ {
			continue
		}
		if want.payload != "" && item.payload != want.payload {
			continue
		}
		t.Fatalf("unexpected event found: want_absent=%+v events=%+v", want, events)
	}
}

func readToolCallbackStream(recorder *callbackRecorder, info *callbacks.RunInfo, output *schema.StreamReader[*tool.CallbackOutput]) {
	defer output.Close()

	for {
		item, err := output.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				recorder.add(callbackEvent{
					scope:     "tool",
					event:     "stream_done",
					component: string(info.Component),
					name:      info.Name,
					typ:       info.Type,
				})
				return
			}
			recorder.add(callbackEvent{
				scope:     "tool",
				event:     "stream_error",
				component: string(info.Component),
				name:      info.Name,
				typ:       info.Type,
				payload:   err.Error(),
			})
			return
		}

		payload := ""
		if item != nil {
			payload = item.Response
		}
		recorder.add(callbackEvent{
			scope:     "tool",
			event:     "stream_chunk",
			component: string(info.Component),
			name:      info.Name,
			typ:       info.Type,
			payload:   payload,
		})
	}
}

func readLambdaCallbackStream(recorder *callbackRecorder, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) {
	defer output.Close()

	for {
		item, err := output.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				recorder.add(callbackEvent{
					scope:     "lambda",
					event:     "stream_done",
					component: string(info.Component),
					name:      info.Name,
					typ:       info.Type,
				})
				return
			}
			recorder.add(callbackEvent{
				scope:     "lambda",
				event:     "stream_error",
				component: string(info.Component),
				name:      info.Name,
				typ:       info.Type,
				payload:   err.Error(),
			})
			return
		}

		msgs, ok := item.([]*schema.Message)
		if !ok {
			recorder.add(callbackEvent{
				scope:     "lambda",
				event:     "stream_chunk",
				component: string(info.Component),
				name:      info.Name,
				typ:       info.Type,
				payload:   fmt.Sprintf("%T", item),
			})
			continue
		}

		recorder.add(callbackEvent{
			scope:     "lambda",
			event:     "stream_chunk",
			component: string(info.Component),
			name:      info.Name,
			typ:       info.Type,
			payload:   formatMessageChunk(msgs),
		})
	}
}

func formatMessageChunk(msgs []*schema.Message) string {
	if len(msgs) == 0 {
		return "[]"
	}

	result := "["
	for i, msg := range msgs {
		if i > 0 {
			result += "|"
		}
		if msg == nil {
			result += "nil"
			continue
		}
		result += fmt.Sprintf("%s:%s", msg.ToolCallID, msg.Content)
	}
	result += "]"
	return result
}
