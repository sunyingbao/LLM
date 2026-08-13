package deepagents

import (
	"context"
	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/contextmanager"
	"eino-cli/deepagent/core/middleware/plan"
	"eino-cli/deepagent/core/utils"
	"eino-cli/deepagent/mock/mock_model"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/tidwall/gjson"
	"go.uber.org/mock/gomock"
	"io"
	"math/rand"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

/*
1. 你可以通过查看 run_test.go 来学习如何 mock eino 的 chatModel 然后通过 mock 工具调用的 message 返回来驱动 agent 完成指定的工具调用
2. 你可以通过查看 graph.go, run.go, graph_builder.go 来学习如何构建 agent 的运行时图
3. 你可以通过查看 middleware 中各文件来学习中间件的使用，中间件就是 graph 中的各个关键位点的 hook
*/

type fakeToolCounter struct {
	total int64
}

type fakeToolCounterReq struct {
	Delta int64 `json:"delta"`
}

type testReactLoopPolicy struct {
	afterModel func(context.Context, ReactLoopAfterModelInput) (ReactLoopBranchDecision, error)
	afterTools func(context.Context, ReactLoopAfterToolsInput) (ReactLoopBranchDecision, error)
}

type toolCallCountingMiddleware struct {
	middleware.BaseMiddleware
	hits int64
}

type beforeAgentFuncMiddleware struct {
	middleware.BaseMiddleware
	before func(context.Context) error
}

func (m *beforeAgentFuncMiddleware) Name() string {
	return "before_agent_func"
}

func (m *beforeAgentFuncMiddleware) BeforeAgent(ctx context.Context) error {
	if m.before == nil {
		return nil
	}
	return m.before(ctx)
}

func TestDeepAgentInterruptQueuesBeforeHandleReady(t *testing.T) {
	agent := &DeepAgent{}
	if !agent.Interrupt(compose.WithGraphInterruptTimeout(time.Second)) {
		t.Fatal("expected early interrupt to be accepted")
	}

	calls := 0
	agent.setGraphInterruptHandle(func(opts ...compose.GraphInterruptOption) {
		calls++
		if len(opts) != 1 {
			t.Fatalf("expected pending interrupt options to be preserved, got %d", len(opts))
		}
	})
	if calls != 1 {
		t.Fatalf("expected pending interrupt to be applied once, got %d", calls)
	}

	if !agent.Interrupt() {
		t.Fatal("expected duplicate interrupt to be idempotently accepted")
	}
	if calls != 1 {
		t.Fatalf("expected duplicate interrupt not to call handle again, got %d", calls)
	}
}

func TestDeepAgentInterruptUsesReadyHandleOnce(t *testing.T) {
	agent := &DeepAgent{}
	calls := 0
	agent.setGraphInterruptHandle(func(opts ...compose.GraphInterruptOption) {
		calls++
	})

	if !agent.Interrupt() {
		t.Fatal("expected interrupt to be accepted")
	}
	if calls != 1 {
		t.Fatalf("expected ready interrupt handle to be called once, got %d", calls)
	}

	if !agent.Interrupt() {
		t.Fatal("expected duplicate interrupt to be idempotently accepted")
	}
	if calls != 1 {
		t.Fatalf("expected duplicate interrupt not to call handle again, got %d", calls)
	}
}

func TestDeepAgentPrepareRunAcceptsInterruptDuringBeforeAgent(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)

	var agent *DeepAgent
	mw := &beforeAgentFuncMiddleware{
		before: func(context.Context) error {
			if agent == nil {
				t.Fatal("agent should be assigned before BeforeAgent runs")
			}
			if !agent.Interrupt() {
				t.Fatal("expected interrupt during BeforeAgent to be accepted")
			}
			return nil
		},
	}
	var err error
	agent, err = New(ctx,
		WithModel(cm),
		WithMiddleware(mw),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(contextmanager.New()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, _, err = agent.prepareRun(ctx, nil)
	if err != nil {
		t.Fatalf("prepareRun() error = %v", err)
	}
	agent.graphInterruptMu.Lock()
	used := agent.graphInterruptUsed
	pending := len(agent.pendingGraphInterrupt)
	agent.graphInterruptMu.Unlock()
	if !used {
		t.Fatal("expected interrupt during BeforeAgent to be applied to the current run")
	}
	if pending != 0 {
		t.Fatalf("expected pending interrupt to be drained, got %d", pending)
	}
}

func (p testReactLoopPolicy) AfterModel(ctx context.Context, input ReactLoopAfterModelInput) (ReactLoopBranchDecision, error) {
	if p.afterModel == nil {
		return ReactLoopBranchDefault, nil
	}
	return p.afterModel(ctx, input)
}

func (p testReactLoopPolicy) AfterTools(ctx context.Context, input ReactLoopAfterToolsInput) (ReactLoopBranchDecision, error) {
	if p.afterTools == nil {
		return ReactLoopBranchDefault, nil
	}
	return p.afterTools(ctx, input)
}

func (t *fakeToolCounter) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "counter",
		Desc: "incr counter",
		ParamsOneOf: schema.NewParamsOneOfByParams(
			map[string]*schema.ParameterInfo{
				"delta": {
					Desc:     "number to add",
					Required: true,
					Type:     schema.Integer,
				},
			}),
	}, nil
}

func (t *fakeToolCounter) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	delta := gjson.Get(argumentsInJSON, "delta").Int()
	t.total += delta

	return fmt.Sprintf(`{"total": %v}`, t.total), nil
}

func (m *toolCallCountingMiddleware) Name() string {
	return "tool_call_counting"
}

func (m *toolCallCountingMiddleware) WrapToolCall() compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				atomic.AddInt64(&m.hits, 1)
				return next(ctx, input)
			}
		},
	}
}

func randStr() string {
	seeds := []rune("this is a seed")
	b := make([]rune, 8)
	for i := range b {
		b[i] = seeds[rand.Intn(len(seeds))]
	}
	return string(b)
}

func TestDeepAgent_StreamSimple(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()
	fakeTool := &fakeToolCounter{total: 0}
	fakeToolInfo, _ := fakeTool.Info(ctx)
	mockRspList := []*schema.Message{
		schema.AssistantMessage("1st tool call",
			[]schema.ToolCall{
				{
					ID: randStr(),
					Function: schema.FunctionCall{
						Name:      fakeToolInfo.Name,
						Arguments: utils.ToString(&fakeToolCounterReq{Delta: 1}),
					},
				},
			}),
		schema.AssistantMessage("2rd tool call",
			[]schema.ToolCall{
				{
					ID: randStr(),
					Function: schema.FunctionCall{
						Name:      fakeToolInfo.Name,
						Arguments: utils.ToString(&fakeToolCounterReq{Delta: 1}),
					},
				},
			}),
		schema.AssistantMessage("3rd 并发 tool call",
			[]schema.ToolCall{
				{
					ID: randStr(),
					Function: schema.FunctionCall{
						Name:      fakeToolInfo.Name,
						Arguments: utils.ToString(&fakeToolCounterReq{Delta: 1}),
					},
				},
				{
					ID: randStr(),
					Function: schema.FunctionCall{
						Name:      fakeToolInfo.Name,
						Arguments: utils.ToString(&fakeToolCounterReq{Delta: 1}),
					},
				},
			}),
		schema.AssistantMessage("我已经完成了全部的工具调用", nil),
	}
	msgIndex := 0
	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, input []*schema.Message, opts ...model.Option) (
			*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()
			msg := mockRspList[msgIndex]
			msgIndex++
			sw.Send(msg, nil)
			return sr, nil
		}).AnyTimes()

	opts := []Option{
		WithModel(cm),
		WithTools(fakeTool),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(contextmanager.New()),
	}
	agent, err := New(ctx, opts...)
	if err != nil {
		t.Errorf("New() error = %v", err)
		return
	}
	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("请计算 1+1")})
	if err != nil {
		t.Errorf("Stream() error = %v", err)
		return
	}
	msgs := make([]*schema.Message, 0)
	for {
		msg, err := out.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		msgs = append(msgs, msg)
	}

	if len(msgs) != 1 {
		t.Fatalf("expect 1 messages, but got %v", len(msgs))
		return
	}

	if msgs[0].Content != "我已经完成了全部的工具调用" {
		t.Fatalf("expect message content is '我已经完成了全部的工具调用', but got %v", msgs[0].Content)
		return
	}

	if fakeTool.total != 4 {
		t.Fatalf("expect fakeTool.total is 4, but got %v", fakeTool.total)
		return
	}
}

func TestDeepAgentMaxModelCallsStopsBeforeNextModelCall(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	fakeTool := &fakeToolCounter{total: 0}
	fakeToolInfo, _ := fakeTool.Info(ctx)
	callCount := 0
	cm.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			callCount++
			return schema.AssistantMessage("call counter", []schema.ToolCall{{
				ID: randStr(),
				Function: schema.FunctionCall{
					Name:      fakeToolInfo.Name,
					Arguments: utils.ToString(&fakeToolCounterReq{Delta: 1}),
				},
			}}), nil
		},
	).Times(2)

	agent, err := New(ctx,
		WithModel(cm),
		WithTools(fakeTool),
		WithMaxModelCalls(2),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(contextmanager.New()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(ctx, []*schema.Message{schema.UserMessage("hi")})
	if !errors.Is(err, ErrExceedMaxModelCalls) {
		t.Fatalf("Run() error = %v, want ErrExceedMaxModelCalls", err)
	}
	if callCount != 2 {
		t.Fatalf("model calls = %d, want 2", callCount)
	}
	if fakeTool.total != 2 {
		t.Fatalf("tool total = %d, want 2", fakeTool.total)
	}
}

func TestDeepAgentMaxModelCallsResetsForFreshRun(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			return schema.AssistantMessage("ok", nil), nil
		},
	).Times(2)

	agent, err := New(ctx,
		WithModel(cm),
		WithMaxModelCalls(1),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(contextmanager.New()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for i := 0; i < 2; i++ {
		msg, err := agent.Run(ctx, []*schema.Message{schema.UserMessage("hi")})
		if err != nil {
			t.Fatalf("Run(%d) error = %v", i, err)
		}
		if msg.Content != "ok" {
			t.Fatalf("Run(%d) content = %q, want ok", i, msg.Content)
		}
	}
}

func TestDeepAgent_ToolArgumentTypeErrorReturnsToolResult(t *testing.T) {
	for _, enableStreamToolCall := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_tool_call_%t", enableStreamToolCall), func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			cm := mock_model.NewMockToolCallingChatModel(ctrl)
			cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

			badArgs := `{"plan":"[{\"step\":\"检查Clip 1生成状态\",\"status\":\"completed\"}]","explanation":"ok"}`
			responses := []*schema.Message{
				schema.AssistantMessage("calling update_plan", []schema.ToolCall{{
					ID: "call_bad_plan",
					Function: schema.FunctionCall{
						Name:      "update_plan",
						Arguments: badArgs,
					},
				}}),
				schema.AssistantMessage("final after tool error", nil),
			}
			msgIndex := 0
			cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
					if msgIndex == 1 {
						foundToolError := false
						for _, msg := range input {
							if msg.Role == schema.Tool && strings.Contains(msg.Content, "Tool invocation failed") {
								foundToolError = true
							}
						}
						if !foundToolError {
							t.Fatalf("second model input did not contain tool error result: %+v", input)
						}
					}
					sr, sw := schema.Pipe[*schema.Message](1)
					defer sw.Close()
					msg := responses[msgIndex]
					msgIndex++
					sw.Send(msg, nil)
					return sr, nil
				}).Times(2)

			opts := []Option{
				WithModel(cm),
				WithCheckpointStore(checkpointer.NewInMemoryStore()),
				WithContextManager(contextmanager.New()),
				WithMiddleware(plan.New(nil)),
			}
			if enableStreamToolCall {
				opts = append(opts, WithStreamToolCall())
			}
			agent, err := New(ctx, opts...)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("please update plan")})
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			msg, err := out.Recv()
			if err != nil {
				t.Fatalf("Recv() error = %v", err)
			}
			if msg.Content != "final after tool error" {
				t.Fatalf("content = %q", msg.Content)
			}
		})
	}
}

func TestDeepAgent_StreamableToolArgumentTypeErrorReturnsToolResult(t *testing.T) {
	for _, enableStreamToolCall := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_tool_call_%t", enableStreamToolCall), func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			cm := mock_model.NewMockToolCallingChatModel(ctrl)
			cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

			type taskArgs struct {
				WaitForDone bool `json:"wait_for_done"`
			}

			streamTool, err := toolutils.InferStreamTool[taskArgs, string](
				"task",
				"test task",
				func(ctx context.Context, input taskArgs) (*schema.StreamReader[string], error) {
					return schema.StreamReaderFromArray([]string{"unexpected"}), nil
				},
			)
			if err != nil {
				t.Fatalf("InferStreamTool err: %v", err)
			}

			responses := []*schema.Message{
				schema.AssistantMessage("calling task", []schema.ToolCall{{
					ID: "call_bad_task",
					Function: schema.FunctionCall{
						Name:      "task",
						Arguments: `{"wait_for_done":"true"}`,
					},
				}}),
				schema.AssistantMessage("final after streamable tool error", nil),
			}
			msgIndex := 0
			cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
					if msgIndex == 1 {
						foundToolError := false
						for _, msg := range input {
							if msg.Role == schema.Tool && strings.Contains(msg.Content, "Tool invocation failed") {
								foundToolError = true
							}
						}
						if !foundToolError {
							t.Fatalf("second model input did not contain streamable tool error result: %+v", input)
						}
					}
					sr, sw := schema.Pipe[*schema.Message](1)
					defer sw.Close()
					msg := responses[msgIndex]
					msgIndex++
					sw.Send(msg, nil)
					return sr, nil
				}).Times(2)

			opts := []Option{
				WithModel(cm),
				WithTools(streamTool),
				WithCheckpointStore(checkpointer.NewInMemoryStore()),
				WithContextManager(contextmanager.New()),
			}
			if enableStreamToolCall {
				opts = append(opts, WithStreamToolCall())
			}
			agent, err := New(ctx, opts...)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("please run task")})
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			msg, err := out.Recv()
			if err != nil {
				t.Fatalf("Recv() error = %v", err)
			}
			if msg.Content != "final after streamable tool error" {
				t.Fatalf("content = %q", msg.Content)
			}
		})
	}
}

func TestDeepAgent_RunCanEndAfterToolsWithoutSecondModelTurn(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	fakeTool := &fakeToolCounter{}
	fakeToolInfo, _ := fakeTool.Info(ctx)
	cm.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			return schema.AssistantMessage("call tool",
				[]schema.ToolCall{{
					ID: randStr(),
					Function: schema.FunctionCall{
						Name:      fakeToolInfo.Name,
						Arguments: utils.ToString(&fakeToolCounterReq{Delta: 2}),
					},
				}}), nil
		}).Times(1)

	agent, err := New(ctx,
		WithModel(cm),
		WithTools(fakeTool),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(contextmanager.New()),
		WithReactLoopBranchPolicy(testReactLoopPolicy{
			afterTools: func(context.Context, ReactLoopAfterToolsInput) (ReactLoopBranchDecision, error) {
				return ReactLoopBranchToEnd, nil
			},
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	out, err := agent.Run(ctx, []*schema.Message{schema.UserMessage("run tool and stop")})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out == nil {
		t.Fatal("Run() output is nil")
	}
	if out.Role != schema.Tool {
		t.Fatalf("expected terminal tool message, got role=%s content=%q", out.Role, out.Content)
	}
	if out.Content != `{"total": 2}` {
		t.Fatalf("unexpected output content: %q", out.Content)
	}
	if fakeTool.total != 2 {
		t.Fatalf("expect fakeTool.total is 2, but got %v", fakeTool.total)
	}
}

func TestDeepAgent_ToolNodeHandlers_NonStream(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	fakeTool := &fakeToolCounter{}
	fakeToolInfo, _ := fakeTool.Info(ctx)
	toolMW := &toolCallCountingMiddleware{}

	preHandler := func(ctx context.Context, input *schema.Message) (*schema.Message, error) {
		copied := *input
		copied.ToolCalls = append([]schema.ToolCall(nil), input.ToolCalls...)
		copied.ToolCalls[0].Function.Arguments = utils.ToString(&fakeToolCounterReq{Delta: 3})
		return &copied, nil
	}
	postHandler := func(ctx context.Context, output []*schema.Message) ([]*schema.Message, error) {
		if len(output) != 1 {
			t.Fatalf("expected one tool message, got %d", len(output))
		}
		return []*schema.Message{
			schema.ToolMessage(`{"total": 11}`, output[0].ToolCallID, schema.WithToolName(output[0].ToolName)),
		}, nil
	}

	var generateCount int32
	cm.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			switch atomic.AddInt32(&generateCount, 1) {
			case 1:
				return schema.AssistantMessage("call tool",
					[]schema.ToolCall{{
						ID: randStr(),
						Function: schema.FunctionCall{
							Name:      fakeToolInfo.Name,
							Arguments: utils.ToString(&fakeToolCounterReq{Delta: 1}),
						},
					}}), nil
			case 2:
				last := input[len(input)-1]
				if last.Role != schema.Tool {
					t.Fatalf("expected second model turn to receive tool message, got role=%s", last.Role)
				}
				if last.Content != `{"total": 11}` {
					t.Fatalf("expected post handler result to reach second model turn, got %q", last.Content)
				}
				return schema.AssistantMessage("done", nil), nil
			default:
				t.Fatalf("unexpected Generate call count: %d", generateCount)
				return nil, nil
			}
		}).Times(2)

	agent, err := NewFromSpec(ctx, &DeepAgentSpec{
		Model:               cm,
		Middlewares:         []middleware.Middleware{contextmanager.New(), toolMW},
		Tools:               []tool.BaseTool{fakeTool},
		CheckpointStore:     checkpointer.NewInMemoryStore(),
		ToolNodePreHandler:  preHandler,
		ToolNodePostHandler: postHandler,
	})
	if err != nil {
		t.Fatalf("NewFromSpec() error = %v", err)
	}

	out, err := agent.Run(ctx, []*schema.Message{schema.UserMessage("please call tool")})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Content != "done" {
		t.Fatalf("unexpected final content: %q", out.Content)
	}
	if fakeTool.total != 3 {
		t.Fatalf("expected pre handler to rewrite delta to 3, got %d", fakeTool.total)
	}
	if atomic.LoadInt64(&toolMW.hits) != 1 {
		t.Fatalf("expected WrapToolCall middleware to be hit once, got %d", atomic.LoadInt64(&toolMW.hits))
	}
}

func TestDeepAgent_ToolNodePreHandlerError_NonStream(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	fakeTool := &fakeToolCounter{}
	fakeToolInfo, _ := fakeTool.Info(ctx)
	cm.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(schema.AssistantMessage("call tool",
			[]schema.ToolCall{{
				ID: randStr(),
				Function: schema.FunctionCall{
					Name:      fakeToolInfo.Name,
					Arguments: utils.ToString(&fakeToolCounterReq{Delta: 1}),
				},
			}}), nil).Times(1)

	agent, err := NewFromSpec(ctx, &DeepAgentSpec{
		Model:           cm,
		Middlewares:     []middleware.Middleware{contextmanager.New()},
		Tools:           []tool.BaseTool{fakeTool},
		CheckpointStore: checkpointer.NewInMemoryStore(),
		ToolNodePreHandler: func(ctx context.Context, input *schema.Message) (*schema.Message, error) {
			return nil, fmt.Errorf("tool node pre failed")
		},
	})
	if err != nil {
		t.Fatalf("NewFromSpec() error = %v", err)
	}

	_, err = agent.Run(ctx, []*schema.Message{schema.UserMessage("please call tool")})
	if err == nil || !strings.Contains(err.Error(), "tool node pre failed") {
		t.Fatalf("expected tool node pre handler error, got %v", err)
	}
}

func TestDeepAgent_ToolNodeHandlers_IgnoredWhenStreamToolCallEnabled(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)
	cm.EXPECT().WithTools(gomock.Any()).Return(cm, nil).AnyTimes()

	fakeTool := &fakeToolCounter{}
	fakeToolInfo, _ := fakeTool.Info(ctx)
	var preHits int64
	var postHits int64
	var streamCall int32

	cm.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()
			switch atomic.AddInt32(&streamCall, 1) {
			case 1:
				sw.Send(schema.AssistantMessage("call tool",
					[]schema.ToolCall{{
						ID: randStr(),
						Function: schema.FunctionCall{
							Name:      fakeToolInfo.Name,
							Arguments: utils.ToString(&fakeToolCounterReq{Delta: 1}),
						},
					}}), nil)
			case 2:
				sw.Send(schema.AssistantMessage("done", nil), nil)
			default:
				t.Fatalf("unexpected Stream call count: %d", streamCall)
			}
			return sr, nil
		}).AnyTimes()

	agent, err := New(ctx,
		WithModel(cm),
		WithTools(fakeTool),
		WithCheckpointStore(checkpointer.NewInMemoryStore()),
		WithContextManager(contextmanager.New()),
		WithStreamToolCall(),
		WithToolNodePreHandler(func(ctx context.Context, input *schema.Message) (*schema.Message, error) {
			atomic.AddInt64(&preHits, 1)
			return input, nil
		}),
		WithToolNodePostHandler(func(ctx context.Context, output []*schema.Message) ([]*schema.Message, error) {
			atomic.AddInt64(&postHits, 1)
			return output, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("please call tool")})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	var last *schema.Message
	for {
		msg, err := out.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
		last = msg
	}

	if last == nil || last.Content != "done" {
		t.Fatalf("unexpected final stream message: %+v", last)
	}
	if fakeTool.total != 1 {
		t.Fatalf("expected stream tool execution to remain unchanged, got total=%d", fakeTool.total)
	}
	if atomic.LoadInt64(&preHits) != 0 {
		t.Fatalf("expected ToolNodePreHandler to be ignored in stream mode, got %d hits", atomic.LoadInt64(&preHits))
	}
	if atomic.LoadInt64(&postHits) != 0 {
		t.Fatalf("expected ToolNodePostHandler to be ignored in stream mode, got %d hits", atomic.LoadInt64(&postHits))
	}
}
