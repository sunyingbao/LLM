package planning

import (
	"code.byted.org/gopkg/logs/v2"
	"context"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/core/types"
	"encoding/json"
	"fmt"
	"github.com/bytedance/sonic"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	utils2 "eino-cli/deepagent/core/utils"
)

// ProgressCallback 进度回调函数类型（Phase 3 用于 SSE 推送）
type ProgressCallback func(event TodoUpdateEvent)

// PlanningMiddleware 提供任务规划能力
// 让Agent能够分解复杂任务、跟踪进度、动态调整计划
type PlanningMiddleware struct {
	middleware.BaseMiddleware
	mu       sync.RWMutex
	todos    []*Todo
	toolMask deeptools.Mask
}

type PlanningConfig struct {
	// ToolMask 控制该中间件自身工具和工具提示词的可见性。
	ToolMask deeptools.Mask
}

// New 创建规划中间件
// initialTodos 可选，用于跨请求保持 todo 状态
func New() *PlanningMiddleware {
	return NewWithConfig(nil)
}

func NewWithConfig(cfg *PlanningConfig) *PlanningMiddleware {
	if cfg == nil {
		cfg = &PlanningConfig{}
	}
	return &PlanningMiddleware{toolMask: cfg.ToolMask}
}

func (m *PlanningMiddleware) Name() string {
	return constant.MiddlewarePlanning
}

func (m *PlanningMiddleware) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	tools := []tool.BaseTool{
		m.newWriteTodosTool(),
		m.newReadTodosTool(),
		m.newUpdateTodoTool(),
	}

	return deeptools.FilterToolsByMask(ctx, tools, m.toolMask, "PlanningMiddleware::Tools"), nil
}

// buildPrompt 构建规划系统提示词
func (m *PlanningMiddleware) buildPrompt(ctx context.Context) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	basePrompt := planningPromptText().defaultPrompt
	if m.toolMask != nil {
		visibleTools, err := m.Tools(ctx)
		if err != nil {
			return ""
		}
		visible := deeptools.ToolNameSet(ctx, visibleTools, "PlanningMiddleware::buildPrompt")
		basePrompt = m.buildBasePromptLocked(visible)
	}

	if len(m.todos) == 0 {
		return basePrompt
	}

	var sb strings.Builder
	if basePrompt != "" {
		sb.WriteString(basePrompt)
	}

	// 计算进度
	completed, total := 0, len(m.todos)
	var currentTask *Todo
	for _, t := range m.todos {
		if t.Status == TodoStatusCompleted {
			completed++
		}
		if t.Status == TodoStatusInProgress {
			currentTask = t
		}
	}

	// 注入执行状态摘要
	sb.WriteString("\n\n<execution-status>\n")
	sb.WriteString(fmt.Sprintf("Progress: %d/%d completed\n", completed, total))
	if currentTask != nil {
		sb.WriteString(fmt.Sprintf("Current task: [%s] %s\n", currentTask.ID, currentTask.Content))
		sb.WriteString("FOCUS: Complete the current task before starting new ones.\n")
	} else if completed < total {
		sb.WriteString("No task is in_progress. Pick the next pending task to continue.\n")
	}
	sb.WriteString("</execution-status>\n\n")

	// 完整 JSON
	todosJSON, _ := json.MarshalIndent(m.todos, "", "  ")
	sb.WriteString("<current-todos>\n")
	sb.Write(todosJSON)
	sb.WriteString("\n</current-todos>")

	return sb.String()
}

func (m *PlanningMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	prompt := m.buildPrompt(ctx)
	if prompt != "" {
		return []*schema.Message{schema.SystemMessage(prompt)}, nil
	}
	return nil, nil
}

func (m *PlanningMiddleware) BuildStateHandler() types.RunTimeStateful {
	return m
}

func (m *PlanningMiddleware) MarshalRuntimeState() string {
	data, _ := sonic.MarshalString(m.todos)
	return data
}

func (m *PlanningMiddleware) UnmarshalRuntimeState(data string) error {
	err := sonic.UnmarshalString(data, &m.todos)

	logs.Info("[PlanningMiddleware] resume data:%s", utils2.ToString(m.todos))
	return err
}

// GetTodos 获取当前任务列表
func (m *PlanningMiddleware) GetTodos() []*Todo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Todo, len(m.todos))
	copy(result, m.todos)
	return result
}

// formatTodosListLocked 格式化当前完整的 todo 列表（调用方需持有锁）
func (m *PlanningMiddleware) formatTodosListLocked() string {
	if len(m.todos) == 0 {
		return constant.MsgNoTasks
	}

	stats := map[TodoStatus]int{}
	for _, todo := range m.todos {
		stats[todo.Status]++
	}

	var result struct {
		Summary struct {
			Total      int `json:"total"`
			Pending    int `json:"pending"`
			InProgress int `json:"in_progress"`
			Completed  int `json:"completed"`
			Failed     int `json:"failed"`
			Skipped    int `json:"skipped"`
		} `json:"summary"`
		Todos []*Todo `json:"todos"`
	}

	result.Summary.Total = len(m.todos)
	result.Summary.Pending = stats[TodoStatusPending]
	result.Summary.InProgress = stats[TodoStatusInProgress]
	result.Summary.Completed = stats[TodoStatusCompleted]
	result.Summary.Failed = stats[TodoStatusFailed]
	result.Summary.Skipped = stats[TodoStatusSkipped]
	result.Todos = m.todos

	output, _ := json.MarshalIndent(result, "", "  ")
	return string(output)
}

// ==================== write_todos 工具 ====================

type writeTodosInput struct {
	Todos []todoItem `json:"todos" jsonschema:"description=任务列表，每个元素必须是包含 content 字段的对象"`
}

type todoItem struct {
	Content  string `json:"content" jsonschema:"description=任务描述，required"`
	Priority int    `json:"priority,omitempty" jsonschema:"description=优先级（1-5，1最高），可选"`
}

func (m *PlanningMiddleware) newWriteTodosTool() tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolWriteTodos,
		planningToolDesc(constant.ToolWriteTodos),
		func(ctx context.Context, input *writeTodosInput) (string, error) {
			m.mu.Lock()
			defer m.mu.Unlock()

			now := time.Now().Format(time.RFC3339)
			m.todos = make([]*Todo, len(input.Todos))

			for i, item := range input.Todos {
				priority := item.Priority
				if priority <= 0 {
					priority = constant.DefaultTodoPriority // 默认中等优先级
				}

				m.todos[i] = &Todo{
					ID:        uuid.New().String()[:8],
					Content:   item.Content,
					Status:    TodoStatusPending,
					Priority:  priority,
					CreatedAt: now,
				}
			}

			// 返回完整的任务列表 + 执行引导
			result := m.formatTodosListLocked()
			result += m.formatWriteGuidanceLocked()
			return result, nil
		},
		planningToolOptions(constant.ToolWriteTodos)...,
	)
	return t
}

// formatWriteGuidanceLocked 生成 write_todos 后的执行引导（调用方需持有锁）
func (m *PlanningMiddleware) formatWriteGuidanceLocked() string {
	for _, todo := range m.todos {
		if todo.Status == TodoStatusPending {
			return fmt.Sprintf(
				"\n\n[System] Plan created with %d tasks. Start executing now — set task [%s] to in_progress.",
				len(m.todos), todo.ID,
			)
		}
	}
	return ""
}

// formatNextTaskGuidanceLocked 生成完成任务后的下一步引导（调用方需持有锁）
func (m *PlanningMiddleware) formatNextTaskGuidanceLocked() string {
	for _, todo := range m.todos {
		if todo.Status == TodoStatusPending {
			return fmt.Sprintf(
				"\n\n[System] Task done. Continue with [%s] — set it to in_progress now.",
				todo.ID,
			)
		}
	}
	return "\n\n[System] All tasks completed."
}

// ==================== read_todos 工具 ====================

// readTodosInput 输入结构体
// 添加占位参数避免空参数导致 OpenRouter API 500 错误
type readTodosInput struct {
	// Verbose 是否返回详细信息，可选参数
	Verbose bool `json:"verbose,omitempty" jsonschema:"description=是否返回详细信息,可选"`
}

func (m *PlanningMiddleware) newReadTodosTool() tool.BaseTool {
	t, _ := utils.InferTool(
		constant.ToolReadTodos,
		planningToolDesc(constant.ToolReadTodos),
		func(ctx context.Context, input *readTodosInput) (string, error) {
			m.mu.RLock()
			defer m.mu.RUnlock()
			return m.formatTodosListLocked(), nil
		},
		planningToolOptions(constant.ToolReadTodos)...,
	)
	return t
}

// ==================== update_todo 工具 ====================

// todoUpdate 单个任务更新项
type todoUpdate struct {
	// 支持两种参数名：todo_id 和 id（兼容不同的 Agent 调用方式）
	TodoID string     `json:"todo_id,omitempty" jsonschema:"description=任务ID (由 write_todos 返回的8位字符串)"`
	ID     string     `json:"id,omitempty" jsonschema:"description=任务ID (别名，与 todo_id 相同)"`
	Status TodoStatus `json:"status" jsonschema:"description=新状态: pending, in_progress, completed, failed, skipped"`
}

// getID 获取任务 ID（兼容 todo_id 和 id 两种参数名）
func (u *todoUpdate) getID() string {
	if u.TodoID != "" {
		return u.TodoID
	}
	return u.ID
}

type updateTodoInput struct {
	// 批量更新
	Updates []todoUpdate `json:"updates" jsonschema:"description=批量更新列表，支持一次更新多个任务的状态"`
}

func (m *PlanningMiddleware) newUpdateTodoTool() tool.BaseTool {
	t, _ := utils.InferTool(
		constant.ToolUpdateTodo,
		planningToolDesc(constant.ToolUpdateTodo),
		func(ctx context.Context, input *updateTodoInput) (string, error) {
			m.mu.Lock()
			defer m.mu.Unlock()

			if len(input.Updates) == 0 {
				return fmt.Sprintf("[Error] update_todo failed: %s", constant.ErrMsgMissingTaskID), nil
			}

			// 构建 ID 到 Todo 的索引
			todoMap := make(map[string]*Todo, len(m.todos))
			for _, todo := range m.todos {
				todoMap[todo.ID] = todo
			}

			var errors []string
			for _, upd := range input.Updates {
				todoID := upd.getID()
				if todoID == "" {
					errors = append(errors, fmt.Sprintf("[Error] %s", constant.ErrMsgMissingTaskID))
					continue
				}

				target, ok := todoMap[todoID]
				if !ok {
					var existingIDs []string
					for _, todo := range m.todos {
						existingIDs = append(existingIDs, todo.ID)
					}
					errors = append(errors, fmt.Sprintf("[Error] %s: %s (当前存在的任务ID: %v)", constant.ErrMsgTaskNotFound, todoID, existingIDs))
					continue
				}

				// 如果设置为 in_progress，先将其他 in_progress 任务改为 pending
				if upd.Status == TodoStatusInProgress {
					for _, todo := range m.todos {
						if todo.Status == TodoStatusInProgress && todo.ID != todoID {
							todo.Status = TodoStatusPending
						}
					}
				}

				target.Status = upd.Status
				if upd.Status == TodoStatusCompleted {
					target.CompletedAt = time.Now().Format(time.RFC3339)
				}
			}

			result := m.formatTodosListLocked()
			if len(errors) > 0 {
				result = strings.Join(errors, "\n") + "\n\n" + result
			}

			// 如果最后一个更新是 completed，追加下一步引导
			lastUpdate := input.Updates[len(input.Updates)-1]
			if lastUpdate.Status == TodoStatusCompleted {
				result += m.formatNextTaskGuidanceLocked()
			}

			return result, nil
		},
		planningToolOptions(constant.ToolUpdateTodo)...,
	)
	return t
}
