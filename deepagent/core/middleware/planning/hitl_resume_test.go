package planning_test

import (
	"code.byted.org/gopkg/logs/v2"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware/contextmanager"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/mock/mock_model"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/mock/gomock"
)

// 一个最小可运行的需要审批工具：首次运行被审批打断，获批后返回固定字符串
type needApprovalTool struct{}

func (t *needApprovalTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "needs_approval",
		Desc: "a tool that requires approval before running",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"note": {Desc: "placeholder", Required: false, Type: schema.String},
		}),
	}, nil
}

func (t *needApprovalTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return "approved_ok", nil
}

// TestPlanning_HITL_Resume_PreservesTodos 模拟：
// 1) 模型先调用 write_todos 写入 4 个任务
// 2) 接着调用一个需要审批的工具触发中断
// 3) 获批恢复后，再调用 read_todos 读取当前 todo 列表
// 4) 最终 assistant 把 read_todos 的输出回显，断言仍包含 total:4，证明 todo 状态未丢失
func TestPlanning_HITL_Resume_PreservesTodos(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	// 单一 ChatModel mock，覆盖首轮与恢复轮的 Stream 行为（仅依赖 input 决策）
	mainCM := mock_model.NewMockToolCallingChatModel(ctrl)
	mainCM.EXPECT().WithTools(gomock.Any()).Return(mainCM, nil).AnyTimes()

	// 需要审批的伪工具
	base := &needApprovalTool{}
	wrapped := deeptools.NewInvokableApprovableTool(base, func(ctx context.Context, info *deeptools.ApprovalInfo) bool { return true })

	// 主模型 Stream：根据历史消息推进阶段（同一 mock 适配两轮）
	// - 若检测到工具输出包含 approved_ok：任务完成，回显最早 write_todos 的输出。
	// - 否则：
	//   * toolMsgCount==0 -> 发起 write_todos（4 个 echo 任务）
	//   * toolMsgCount==1 且未发 needs_approval -> 发起 needs_approval（触发中断）
	//   * 其他 -> 再次发起 needs_approval（用于恢复轮驱动工具执行）
	mainCM.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			defer sw.Close()

			// 统计 Tool 消息数量，判断阶段
			toolMsgCount := 0
			for _, m := range input {
				if m.Role == schema.Tool {
					toolMsgCount++
				}
			}

			// 判断是否已经发过某个工具调用（看 assistant tool_calls）
			hasToolCall := func(name string) bool {
				for _, m := range input {
					if m.Role == schema.Assistant && len(m.ToolCalls) > 0 {
						for _, tc := range m.ToolCalls {
							if tc.Function.Name == name {
								return true
							}
						}
					}
				}
				return false
			}

			// 是否已有批准后的工具输出
			hasApproved := false
			for _, m := range input {
				if m.Role == schema.Tool && strings.Contains(m.Content, "approved_ok") {
					hasApproved = true
					break
				}
			}

			// 最近一次工具输出（期望在 read_todos 后为 todos 列表）
			lastToolOutput := func() string {
				for i := len(input) - 1; i >= 0; i-- {
					if input[i].Role == schema.Tool {
						return input[i].Content
					}
				}
				return ""
			}

			if hasToolCall(constant.ToolReadTodos) {
				// read_todos 已调用：回显最近一次工具输出并结束
				sw.Send(schema.AssistantMessage(fmt.Sprintf("todos_output=%s", lastToolOutput()), nil), nil)
				return sr, nil
			}

			switch {
			case toolMsgCount == 0:
				// 第一次：写入 4 个 todos
				args := `{"todos":[{"content":"echo t1"},{"content":"echo t2"},{"content":"echo t3"},{"content":"echo t4"}]}`
				tc := schema.ToolCall{
					ID:   "write_todos_call",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      constant.ToolWriteTodos,
						Arguments: args,
					},
				}
				logs.CtxInfo(ctx, "[LLM] run write todos tool")
				sw.Send(schema.AssistantMessage("plan: write 4 todos", []schema.ToolCall{tc}), nil)
				return sr, nil

			case toolMsgCount == 1 && !hasToolCall("needs_approval"):
				// 第二次：需要审批的工具（会中断）
				tc := schema.ToolCall{
					ID:   "needs_approval_call",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "needs_approval",
						Arguments: `{"note":"approve it"}`,
					},
				}
				logs.CtxInfo(ctx, "[LLM] run approvable tool")
				sw.Send(schema.AssistantMessage("run approvable tool", []schema.ToolCall{tc}), nil)
				return sr, nil
			case toolMsgCount == 2:
				if !hasApproved {
					logs.CtxError(ctx, "[LLM] unexpected tool message count %d。这个时候必然应该已经有 ap 猜对", toolMsgCount)
					return nil, errors.New("[LLM] unexpected tool message count")
				} else {
					logs.CtxInfo(ctx, "[LLM] run read todos tool")
					tc := schema.ToolCall{ID: "read_todos_call", Type: "function", Function: schema.FunctionCall{Name: constant.ToolReadTodos, Arguments: `{"verbose":true}`}}
					sw.Send(schema.AssistantMessage("read todos", []schema.ToolCall{tc}), nil)
				}
				return sr, nil
			default:
				// 其他情况：恢复轮驱动工具执行
				logs.CtxInfo(ctx, "[LLM] resume turn driver tool")
				tc := schema.ToolCall{ID: "needs_approval_call", Type: "function", Function: schema.FunctionCall{Name: "needs_approval", Arguments: `{"note":"approve it"}`}}
				sw.Send(schema.AssistantMessage("run approvable tool (resume)", []schema.ToolCall{tc}), nil)
				return sr, nil
			}
		},
	).AnyTimes()

	// 使用文件型 checkpoint store，模拟“重启进程后仍可恢复”
	tmpDir, err := os.MkdirTemp("", "planning_hitl_resume_*")
	if err != nil {
		t.Fatalf("MkdirTemp error: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	store, err := checkpointer.NewFileStore(filepath.Join(tmpDir, "ckpt"))
	if err != nil {
		t.Fatalf("NewFileStore error: %v", err)
	}

	// 构建 Agent：启用 Planning + 简单上下文管理器 + checkpoint store
	agent, err := deepagents.New(ctx,
		deepagents.WithModel(mainCM),
		deepagents.WithPlanning(),
		deepagents.WithTools(wrapped),
		deepagents.WithContextManager(contextmanager.New()),
		deepagents.WithCheckpointStore(store),
	)
	if err != nil {
		t.Fatalf("New error: %v", err)
	}

	// 固定 checkpoint id，确保第二次运行能够从同一个 checkpoint 恢复
	const checkpointID = "ck-planning-hitl-resume-1"

	// 第一次运行：预期中断
	_, err = agent.Stream(ctx, []*schema.Message{schema.UserMessage("start")}, deepagents.WithCheckpointID(checkpointID))
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

	// 重新构建一个全新的 Agent（模拟重启），复用同一个 ChatModel mock
	newAgent, err := deepagents.New(ctx,
		deepagents.WithModel(mainCM),
		deepagents.WithPlanning(),
		deepagents.WithTools(wrapped),
		deepagents.WithContextManager(contextmanager.New()),
		deepagents.WithCheckpointStore(store),
	)
	if err != nil {
		t.Fatalf("New(2) error: %v", err)
	}

	// 恢复：仅使用 ResumeData（键为原始 interruptID），不传 WithResume，确保不再次中断
	resumeData := map[string]any{interruptID: &deeptools.ApprovalResult{Approved: true}}
	sr, err := newAgent.Stream(ctx, []*schema.Message{schema.UserMessage("resume")},
		deepagents.WithCheckpointID(checkpointID),
		deepagents.WithResumeData(resumeData),
	)
	if err != nil {
		t.Fatalf("resume Stream error: %v", err)
	}

	// 消费流，提取最终消息
	var last *schema.Message
	for {
		m, e := sr.Recv()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			t.Fatalf("stream recv error: %v", e)
		}
		last = m
	}

	if last == nil {
		t.Fatalf("expect final assistant message, got nil")
	}
	// 断言 todos 总数为 4（最终 assistant 回显了 write_todos 的输出，其中包含 summary.total）
	if last.Role != schema.Assistant || last.Content == "" {
		t.Fatalf("unexpected final message: role=%s content=%q", last.Role, last.Content)
	}
	if !strings.Contains(last.Content, `"total": 4`) {
		t.Fatalf("expect read_todos output to contain total=4, got: %s", last.Content)
	}
}
