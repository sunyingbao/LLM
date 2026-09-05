package subagent_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"

	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware/contextmanager"
	subagentmw "eino-cli/deepagent/core/middleware/subagent"
	"eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/mock/mock_model"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/tidwall/gjson"
	"go.uber.org/mock/gomock"
)

// 简单计数器工具，参考 deepagents/run_test.go
type fakeToolCounter struct {
	name  string
	total int64
	runs  int64
}

func (t *fakeToolCounter) toolName() string {
	if t.name != "" {
		return t.name
	}
	return "counter"
}

func (t *fakeToolCounter) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.toolName(),
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
	atomic.AddInt64(&t.runs, 1)
	delta := gjson.Get(argumentsInJSON, "delta").Int()
	total := atomic.AddInt64(&t.total, delta)
	return fmt.Sprintf(`{"tool":%q,"total":%v}`, t.toolName(), total), nil
}

func (t *fakeToolCounter) RunCount() int64 {
	return atomic.LoadInt64(&t.runs)
}

func TestSubAgent_Task_HITL_Resume(t *testing.T) {
	runSubAgentTaskHITLResume(t, false)
}

func TestSubAgent_TaskStreaming_HITL_Resume(t *testing.T) {
	runSubAgentTaskHITLResume(t, true)
}

func runSubAgentTaskHITLResume(t *testing.T, taskStreaming bool) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	// 主模型与子模型
	mainCM := mock_model.NewMockToolCallingChatModel(ctrl)
	subCM := mock_model.NewMockToolCallingChatModel(ctrl)
	mainCM.EXPECT().WithTools(gomock.Any()).Return(mainCM, nil).AnyTimes()
	subCM.EXPECT().WithTools(gomock.Any()).Return(subCM, nil).AnyTimes()

	// 两个普通工具 + 一个审批工具。首次运行时只有 approval_counter 中断。
	plain1 := &fakeToolCounter{name: "plain_counter_1"}
	approvalBase := &fakeToolCounter{name: "approval_counter"}
	approvalTool := tools.NewInvokablePolicyTool(approvalBase, tools.ApprovalGate(func(ctx context.Context, info *tools.ApprovalInfo) bool { return true }))
	plain2 := &fakeToolCounter{name: "plain_counter_2"}

	// 主模型 Stream：遍历输入检测子 agent 输出；若未检测到则发起/重发 task 调用
	taskCallID := "task_call_id"
	mainCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()

			// 检测是否已经存在 task 工具的输出（ToolMessage）
			var toolOutput string
			for _, m := range input {
				if m.Role == schema.Tool && m.Content != "" {
					// 如果能匹配到最近一次的 task 调用 ID，进一步确认；否则也视为子 agent 输出
					if m.ToolCallID == taskCallID || m.ToolCallID != "" {
						toolOutput = m.Content
						break
					}
				}
			}

			if toolOutput != "" {
				sw.Send(schema.AssistantMessage(fmt.Sprintf("我已确认收到子agent回复%s", toolOutput), nil), nil)
				return sr, nil
			}

			// 未检测到子 agent 输出，则发起/重发 task 工具调用
			args := `{"subagent":"test_sub","task":"请把计数器加到 3","wait_for_done":true}`
			tc := schema.ToolCall{
				ID:   taskCallID,
				Type: "function",
				Function: schema.FunctionCall{
					Name:      constant.ToolTask,
					Arguments: args,
				},
			}
			sw.Send(schema.AssistantMessage("发起子任务", []schema.ToolCall{tc}), nil)
			return sr, nil
		}).AnyTimes()

	// 子模型第一轮一次性 emit 3 个工具调用；
	// resume 后模型应看到 3 条 tool message，其中两个普通工具结果来自中断前已完成执行。
	subModelCalls := 0
	subModelResponse := func(input []*schema.Message) (*schema.Message, error) {
		if subModelCalls == 0 {
			toolCalls := []schema.ToolCall{
				{
					ID:   "plain_call_1",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      plain1.toolName(),
						Arguments: `{"delta":1}`,
					},
				},
				{
					ID:   "approval_call",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      approvalBase.toolName(),
						Arguments: `{"delta":10}`,
					},
				},
				{
					ID:   "plain_call_2",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      plain2.toolName(),
						Arguments: `{"delta":2}`,
					},
				},
			}
			subModelCalls++
			return schema.AssistantMessage("子任务第1步：并行工具调用", toolCalls), nil
		}
		subModelCalls++
		got := map[string]string{}
		for _, msg := range input {
			if msg.Role == schema.Tool {
				got[msg.ToolCallID] = msg.Content
			}
		}
		expected := map[string]string{
			"plain_call_1":  `{"tool":"plain_counter_1","total":1}`,
			"approval_call": `{"tool":"approval_counter","total":10}`,
			"plain_call_2":  `{"tool":"plain_counter_2","total":2}`,
		}
		for callID, content := range expected {
			if got[callID] != content {
				return nil, fmt.Errorf("subagent resume input for %s = %q, want %q; all=%v", callID, got[callID], content, got)
			}
		}
		return schema.AssistantMessage("xxxxxx", nil), nil
	}

	if taskStreaming {
		subCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
				msg, err := subModelResponse(input)
				if err != nil {
					return nil, err
				}
				return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
			}).AnyTimes()
	} else {
		subCM.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
				return subModelResponse(input)
			}).AnyTimes()
	}

	// 构造主代理（提供 CheckpointStore 以支持中断恢复 + SimpleContextManager）
	store := checkpointer.NewInMemoryStore()
	opts := []deepagents.Option{
		deepagents.WithModel(mainCM),
		deepagents.WithSubAgents(&subagentmw.SubAgent{
			Name:         "test_sub",
			Description:  "测试子代理",
			SystemPrompt: "你是一个简单子代理",
			Tools:        []tool.BaseTool{plain1, approvalTool, plain2},
			Model:        subCM,
			MaxSteps:     4,
		}),
		deepagents.WithContextManager(contextmanager.New()),
		deepagents.WithCheckpointStore(store),
	}
	if taskStreaming {
		opts = append(opts, deepagents.WithSubAgentTaskStreaming())
	}
	agent, err := deepagents.New(ctx, opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	parentCheckpointID := "subagent-task-hitl-resume"
	if taskStreaming {
		parentCheckpointID = "subagent-task-streaming-hitl-resume"
	}

	// 首次运行：预期中断错误
	_, err = agent.Stream(ctx, []*schema.Message{schema.UserMessage("请处理子任务")}, deepagents.WithCheckpointID(parentCheckpointID))
	if err == nil {
		t.Fatalf("expect interrupt error, got nil")
	}
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatalf("expect interrupt info, got err=%v", err)
	}
	if len(info.InterruptContexts) == 0 {
		t.Fatalf("interrupt info has empty contexts")
	}
	interruptID := info.InterruptContexts[0].ID
	if plain1.RunCount() != 1 || plain2.RunCount() != 1 {
		t.Fatalf("plain tools should complete before interrupt, got plain1=%d plain2=%d", plain1.RunCount(), plain2.RunCount())
	}
	if approvalBase.RunCount() != 0 {
		t.Fatalf("approval tool should not run before approval, got %d", approvalBase.RunCount())
	}

	// 恢复运行：审批通过
	resume := map[string]any{interruptID: &tools.ApprovalResult{Approved: true}}
	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("请处理子任务")},
		deepagents.WithCheckpointID(parentCheckpointID),
		deepagents.WithResumeData(resume),
		deepagents.WithResume(interruptID),
	)
	if err != nil {
		t.Fatalf("resume Stream() error = %v", err)
	}

	// 消费流并断言
	var msgs []*schema.Message
	for {
		m, e := out.Recv()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			t.Fatalf("stream recv error: %v", e)
		}
		msgs = append(msgs, m)
	}

	if len(msgs) != 1 {
		t.Fatalf("expect 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "我已确认收到子agent回复xxxxxx" {
		t.Fatalf("unexpected final content: %s", msgs[0].Content)
	}
	if plain1.RunCount() != 1 || plain2.RunCount() != 1 {
		t.Fatalf("completed tools should not rerun after resume, got plain1=%d plain2=%d", plain1.RunCount(), plain2.RunCount())
	}
	if approvalBase.RunCount() != 1 {
		t.Fatalf("approval tool should run exactly once after resume, got %d", approvalBase.RunCount())
	}
}

func TestSubAgent_TaskStreaming_ParentReceivesConcatenatedResult(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mainCM := mock_model.NewMockToolCallingChatModel(ctrl)
	subCM := mock_model.NewMockToolCallingChatModel(ctrl)
	mainCM.EXPECT().WithTools(gomock.Any()).Return(mainCM, nil).AnyTimes()
	subCM.EXPECT().WithTools(gomock.Any()).Return(subCM, nil).AnyTimes()

	taskCallID := "task_stream_call"
	mainCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()

			for _, msg := range input {
				if msg.Role == schema.Tool && msg.ToolCallID == taskCallID {
					if msg.Content != "hello world" {
						return nil, fmt.Errorf("task tool output = %q, want hello world", msg.Content)
					}
					sw.Send(schema.AssistantMessage("parent received hello world", nil), nil)
					return sr, nil
				}
			}

			sw.Send(schema.AssistantMessage("call task", []schema.ToolCall{{
				ID:   taskCallID,
				Type: "function",
				Function: schema.FunctionCall{
					Name:      constant.ToolTask,
					Arguments: `{"subagent":"test_sub","task":"stream answer","wait_for_done":true}`,
				},
			}}), nil)
			return sr, nil
		},
	).AnyTimes()

	subCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			return schema.StreamReaderFromArray([]*schema.Message{
				schema.AssistantMessage("hello ", nil),
				schema.AssistantMessage("world", nil),
			}), nil
		},
	).AnyTimes()

	agent, err := deepagents.New(ctx,
		deepagents.WithModel(mainCM),
		deepagents.WithSubAgents(&subagentmw.SubAgent{
			Name:        "test_sub",
			Description: "测试子代理",
			Model:       subCM,
			MaxSteps:    2,
		}),
		deepagents.WithContextManager(contextmanager.New()),
		deepagents.WithCheckpointStore(checkpointer.NewInMemoryStore()),
		deepagents.WithBackend(backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
			RootDir:     t.TempDir(),
			VirtualMode: true,
		})),
		deepagents.WithSubAgentTaskStreaming(),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	out, err := agent.Stream(ctx, []*schema.Message{schema.UserMessage("run")})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	msg, err := out.Recv()
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	if msg.Content != "parent received hello world" {
		t.Fatalf("unexpected final content: %q", msg.Content)
	}
	if _, err := out.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}
