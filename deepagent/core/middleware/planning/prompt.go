package planning

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"
	jsonschema "github.com/eino-contrib/jsonschema"

	"eino-cli/deepagent/core/constant"
)

type planningPromptCatalog struct {
	defaultPrompt string

	title                  string
	intro                  string
	communicationTitle     string
	communicationGuide     string
	toolsTitle             string
	serialTitle            string
	serialLines            []string
	guidelinesTitle        string
	guidelines             []string
	statusLine             string
	disciplineTitle        string
	disciplineIntro        string
	disciplineSteps        []string
	disciplineNegativeHead string
	disciplineNegatives    []string

	toolPromptLines   map[string]string
	toolDescriptions  map[string]string
	paramDescriptions map[string]map[string]string
}

var planningPromptCatalogZH = planningPromptCatalog{
	defaultPrompt: constant.PlanningSystemPrompt,

	title:              "## 任务规划",
	intro:              "你有任务规划和跟踪的能力。",
	communicationTitle: "### 重要：用户沟通规则（必须遵守）",
	communicationGuide: "与用户沟通时，绝对禁止提及任何内部工具名称、ID 或执行机制。\n正确的沟通方式应只描述计划、进度和结果，不解释内部状态机制。",
	toolsTitle:         "### 可用工具",
	serialTitle:        "### 核心原则：Todo Items 串行执行",
	serialLines: []string{
		"Todo 列表中的每个 Item 是一个串行步骤，必须按顺序逐个执行：",
		"- 同一时刻只能有一个 Item 处于 in_progress 状态",
		"- 完成当前 Item 后才能开始下一个",
		"- 不允许同时将多个 Item 设为 in_progress",
		"",
		"如果当前 in_progress 的 Item 内部需要并行处理，使用 task 工具启动子代理（详见“子代理委托”章节）。并行只发生在单个 Item 内部，不跨 Item。",
	},
	guidelinesTitle:        "### 使用指南",
	guidelines:             []string{"收到复杂任务时，先创建任务列表分解成多个步骤", "开始处理某个任务时，将其状态更新为 \"in_progress\"", "完成任务后，将其状态更新为 \"completed\"", "如果任务失败或需要跳过，使用 \"failed\" 或 \"skipped\"", "根据执行情况动态调整计划"},
	statusLine:             "任务状态：pending(待处理) | in_progress(进行中，同时只能有一个) | completed(已完成) | failed(失败) | skipped(已跳过)",
	disciplineTitle:        "### 执行纪律",
	disciplineIntro:        "创建任务列表后，严格按以下流程执行：",
	disciplineSteps:        []string{"创建计划后，立即将第一个任务设为 in_progress 并开始执行", "专注完成当前任务", "完成后标记为 completed，立即将下一个任务设为 in_progress", "重复直到所有任务完成"},
	disciplineNegativeHead: "绝对不要：",
	disciplineNegatives:    []string{"写完计划后不按计划执行", "同时将多个 Item 设为 in_progress", "跳过任务或改变顺序（除非有充分理由并更新计划）", "把多个 Item 的工作合并到一次子代理调用中"},
	toolPromptLines: map[string]string{
		constant.ToolWriteTodos: "- write_todos: 创建或替换任务列表（返回完整的当前任务列表）",
		constant.ToolUpdateTodo: "- update_todo: 更新任务状态（返回完整的当前任务列表）",
		constant.ToolReadTodos:  "- read_todos: 读取当前任务列表和状态",
	},
	toolDescriptions: map[string]string{
		constant.ToolWriteTodos: constant.ToolWriteTodosDesc,
		constant.ToolReadTodos:  constant.ToolReadTodosDesc,
		constant.ToolUpdateTodo: constant.ToolUpdateTodoDesc,
	},
}

var planningPromptCatalogEN = planningPromptCatalog{
	defaultPrompt: `## Task Planning

You have task planning and progress tracking capabilities.

### User Communication Rules

When communicating with the user, do not mention internal tool names, IDs, or execution mechanisms. Describe plans, progress, and results without explaining internal state mechanics.

### Available Tools

- write_todos: Create or replace the task list and return the complete current list
- update_todo: Update task status and return the complete current list
- read_todos: Read the current task list and status

### Core Principle: Todo Items Run Serially

Each todo item is a serial step and must be executed in order:
- Only one item can be in_progress at a time
- Finish the current item before starting the next one
- Do not set multiple items to in_progress at the same time

If the current in_progress item contains several independent subtasks, you may use the task tool to run subagents in parallel. Parallelism is allowed only inside a single item, never across items.

### Guidelines

1. For complex tasks, first create a task list
2. When starting a task, set it to "in_progress"
3. When finishing a task, set it to "completed"
4. If a task fails or should be skipped, use "failed" or "skipped"
5. The write and update operations return the complete task list, so there is no need to call read_todos only to confirm
6. Adjust the plan as execution changes

Task statuses: pending | in_progress (only one at a time) | completed | failed | skipped

### Execution Discipline

After creating a task list, follow this process:
1. Set the first task to in_progress and start it immediately
2. Focus on completing the current task
3. Mark it completed, then immediately set the next task to in_progress
4. Repeat until all tasks are completed

Do not:
- Create a plan and then stop without executing it
- Set multiple items to in_progress
- Skip or reorder tasks unless there is a clear reason and the plan is updated
- Merge multiple todo items into one subagent call`,

	title:              "## Task Planning",
	intro:              "You have task planning and progress tracking capabilities.",
	communicationTitle: "### User Communication Rules",
	communicationGuide: "When communicating with the user, do not mention internal tool names, IDs, or execution mechanisms. Describe plans, progress, and results without explaining internal state mechanics.",
	toolsTitle:         "### Available Tools",
	serialTitle:        "### Core Principle: Todo Items Run Serially",
	serialLines: []string{
		"Each todo item is a serial step and must be executed in order:",
		"- Only one item can be in_progress at a time",
		"- Finish the current item before starting the next one",
		"- Do not set multiple items to in_progress at the same time",
		"",
		"If the current in_progress item contains several independent subtasks, you may use the task tool to run subagents in parallel. Parallelism is allowed only inside a single item, never across items.",
	},
	guidelinesTitle:        "### Guidelines",
	guidelines:             []string{"For complex tasks, first create a task list", "When starting a task, set it to \"in_progress\"", "When finishing a task, set it to \"completed\"", "If a task fails or should be skipped, use \"failed\" or \"skipped\"", "Adjust the plan as execution changes"},
	statusLine:             "Task statuses: pending | in_progress (only one at a time) | completed | failed | skipped",
	disciplineTitle:        "### Execution Discipline",
	disciplineIntro:        "After creating a task list, follow this process:",
	disciplineSteps:        []string{"Set the first task to in_progress and start it immediately", "Focus on completing the current task", "Mark it completed, then immediately set the next task to in_progress", "Repeat until all tasks are completed"},
	disciplineNegativeHead: "Do not:",
	disciplineNegatives:    []string{"Create a plan and then stop without executing it", "Set multiple items to in_progress", "Skip or reorder tasks unless there is a clear reason and the plan is updated", "Merge multiple todo items into one subagent call"},
	toolPromptLines: map[string]string{
		constant.ToolWriteTodos: "- write_todos: Create or replace the task list and return the complete current list",
		constant.ToolUpdateTodo: "- update_todo: Update task status and return the complete current list",
		constant.ToolReadTodos:  "- read_todos: Read the current task list and status",
	},
	toolDescriptions: map[string]string{
		constant.ToolWriteTodos: `Create or update the task list. This replaces all existing tasks.

Arguments example:
{"todos": [{"content": "First task"}, {"content": "Second task", "priority": 1}]}

The todos field must be an array of objects, and each object must contain content.`,
		constant.ToolReadTodos: "Read the current task list and status.",
		constant.ToolUpdateTodo: `Update task status. Supports single or batch updates. At most one task can be in_progress.

Single update: {"updates": [{"todo_id": "abc12345", "status": "completed"}]}
Batch update: {"updates": [{"todo_id": "abc12345", "status": "completed"}, {"todo_id": "def67890", "status": "in_progress"}]}

Batch updates are processed in order and follow the same state rules.`,
	},
	paramDescriptions: map[string]map[string]string{
		constant.ToolWriteTodos: {
			"todos":    "Task list. Each item must be an object with a content field.",
			"content":  "Task description. Required.",
			"priority": "Optional priority from 1 to 5, where 1 is highest.",
		},
		constant.ToolReadTodos: {
			"verbose": "Whether to return detailed information. Optional.",
		},
		constant.ToolUpdateTodo: {
			"updates": "Batch update list for changing one or more task statuses.",
			"todo_id": "Task ID returned by write_todos.",
			"id":      "Task ID alias, equivalent to todo_id.",
			"status":  "New status: pending, in_progress, completed, failed, or skipped.",
		},
	},
}

func planningPromptText() planningPromptCatalog {
	if constant.IsEnglishPromptLang() {
		return planningPromptCatalogEN
	}
	return planningPromptCatalogZH
}

func planningToolDesc(toolName string) string {
	return planningPromptText().toolDescriptions[toolName]
}

func planningToolOptions(toolName string) []utils.Option {
	if !constant.IsEnglishPromptLang() {
		return nil
	}
	return []utils.Option{utils.WithSchemaModifier(planningEnglishSchemaModifier(toolName))}
}

func planningEnglishSchemaModifier(toolName string) utils.SchemaModifierFn {
	return func(jsonTagName string, _ reflect.Type, _ reflect.StructTag, schema *jsonschema.Schema) {
		if schema == nil {
			return
		}
		if fields := planningPromptCatalogEN.paramDescriptions[toolName]; fields != nil {
			if desc := fields[jsonTagName]; desc != "" {
				schema.Description = desc
			}
		}
	}
}

func (m *PlanningMiddleware) buildBasePromptLocked(visible map[string]bool) string {
	catalog := planningPromptText()
	var toolLines []string
	for _, toolName := range []string{constant.ToolWriteTodos, constant.ToolUpdateTodo, constant.ToolReadTodos} {
		if visible[toolName] {
			toolLines = append(toolLines, catalog.toolPromptLines[toolName])
		}
	}
	if len(toolLines) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(catalog.title)
	sb.WriteString("\n\n")
	sb.WriteString(catalog.intro)
	sb.WriteString("\n\n")
	sb.WriteString(catalog.communicationTitle)
	sb.WriteString("\n\n")
	sb.WriteString(catalog.communicationGuide)
	sb.WriteString("\n\n")
	sb.WriteString(catalog.toolsTitle)
	sb.WriteString("\n\n")
	sb.WriteString(strings.Join(toolLines, "\n"))
	sb.WriteString("\n\n")
	sb.WriteString(catalog.serialTitle)
	sb.WriteString("\n\n")
	sb.WriteString(strings.Join(catalog.serialLines, "\n"))
	sb.WriteString("\n\n")
	sb.WriteString(catalog.guidelinesTitle)
	sb.WriteString("\n\n")
	for i, guide := range catalog.guidelines {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, guide))
	}
	sb.WriteString("\n")
	sb.WriteString(catalog.statusLine)
	sb.WriteString("\n\n")
	sb.WriteString(catalog.disciplineTitle)
	sb.WriteString("\n\n")
	sb.WriteString(catalog.disciplineIntro)
	sb.WriteString("\n")
	for i, step := range catalog.disciplineSteps {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
	}
	sb.WriteString("\n")
	sb.WriteString(catalog.disciplineNegativeHead)
	sb.WriteString("\n")
	for _, item := range catalog.disciplineNegatives {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
