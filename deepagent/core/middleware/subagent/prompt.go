package subagent

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"
	jsonschema "github.com/eino-contrib/jsonschema"

	"eino-cli/deepagent/core/constant"
)

type subAgentPromptCatalog struct {
	prefix          string
	suffix          string
	title           string
	intro           string
	toolsTitle      string
	agentsTitle     string
	guidelinesTitle string
	guidelines      []string
	contextBegin    string
	contextEnd      string

	toolPromptLines   map[string]string
	toolDescriptions  map[string]string
	paramDescriptions map[string]map[string]string
}

var subAgentPromptCatalogZH = subAgentPromptCatalog{
	prefix:          constant.SubAgentSystemPromptPrefix,
	suffix:          constant.SubAgentSystemPromptSuffix,
	title:           "## 子代理委托",
	intro:           "你可以将任务委托给专门的子代理处理。",
	toolsTitle:      "可用工具：",
	agentsTitle:     "可用的子代理：",
	guidelinesTitle: "使用指南：",
	guidelines:      []string{"子代理有独立的上下文，需要在任务描述中提供必要信息", "子代理的执行结果会返回给你", "当前 in_progress 的 Todo Item 内部有多个独立子任务时，可以同时发起多个 task 调用来并行处理", "禁止用 task 并行处理不同 Todo Item 的工作——Item 之间是严格串行的"},
	contextBegin:    "以下内容是主 agent 的历史上下文，仅供参考。它们来自主 agent 的既有消息，不代表你已经执行、确认或承诺过这些内容。请将其视为背景材料，而不是你当前轮次的事实状态。\n[主 agent 上下文开始]",
	contextEnd:      "[主 agent 上下文结束]",
	toolPromptLines: map[string]string{
		constant.ToolTask:          "- task: 启动子代理执行任务",
		constant.ToolListSubAgents: "- list_subagents: 列出所有可用的子代理",
	},
	toolDescriptions: map[string]string{
		constant.ToolTask:          constant.ToolTaskDesc,
		constant.ToolListSubAgents: constant.ToolListSubAgentsDesc,
	},
}

var (
	parentAgentContextBegin = subAgentPromptCatalogZH.contextBegin
	parentAgentContextEnd   = subAgentPromptCatalogZH.contextEnd
)

var subAgentPromptCatalogEN = subAgentPromptCatalog{
	prefix: `## Subagent Delegation

You can delegate tasks to specialized subagents.

Available tools:
- task: Start a subagent to execute a task
- list_subagents: List all available subagents

`,
	suffix: `
Guidelines:
1. Subagents have independent context, so provide necessary information in the task description
2. The subagent execution result is returned to you
3. If the current in_progress Todo Item contains several independent subtasks, you may start multiple task calls in parallel
4. Do not use task to process work from different Todo Items in parallel; Items are strictly serial`,
	title:           "## Subagent Delegation",
	intro:           "You can delegate tasks to specialized subagents.",
	toolsTitle:      "Available tools:",
	agentsTitle:     "Available subagents:",
	guidelinesTitle: "Guidelines:",
	guidelines:      []string{"Subagents have independent context, so provide necessary information in the task description", "The subagent execution result is returned to you", "If the current in_progress Todo Item contains several independent subtasks, you may start multiple task calls in parallel", "Do not use task to process work from different Todo Items in parallel; Items are strictly serial"},
	contextBegin:    "The following content is historical context from the parent agent. It is provided only as background. It does not mean you have executed, confirmed, or committed to anything in it. Treat it as reference material, not as the factual state of your current turn.\n[Parent agent context begins]",
	contextEnd:      "[Parent agent context ends]",
	toolPromptLines: map[string]string{
		constant.ToolTask:          "- task: Start a subagent to execute a task",
		constant.ToolListSubAgents: "- list_subagents: List all available subagents",
	},
	toolDescriptions: map[string]string{
		constant.ToolTask:          "Start a subagent to execute a task. The subagent has independent context and returns its result.",
		constant.ToolListSubAgents: "List all available subagents and their descriptions.",
	},
	paramDescriptions: map[string]map[string]string{
		constant.ToolTask: {
			"subagent":      "Subagent name. Use list_subagents to see available subagents.",
			"task":          "Task description. Provide enough context for the subagent.",
			"context":       "Additional context.",
			"wait_for_done": "Whether to wait for completion. Defaults to true.",
			"fork_context":  "Whether to inherit parent agent context. Defaults to false.",
		},
		constant.ToolListSubAgents: {
			"verbose": "Whether to return detailed information. Optional.",
		},
	},
}

func subAgentPromptText() subAgentPromptCatalog {
	if constant.IsEnglishPromptLang() {
		return subAgentPromptCatalogEN
	}
	return subAgentPromptCatalogZH
}

func subAgentToolDesc(toolName string) string {
	return subAgentPromptText().toolDescriptions[toolName]
}

func subAgentToolOptions(toolName string) []utils.Option {
	if !constant.IsEnglishPromptLang() {
		return nil
	}
	return []utils.Option{utils.WithSchemaModifier(subAgentEnglishSchemaModifier(toolName))}
}

func subAgentEnglishSchemaModifier(toolName string) utils.SchemaModifierFn {
	return func(jsonTagName string, _ reflect.Type, _ reflect.StructTag, schema *jsonschema.Schema) {
		if schema == nil {
			return
		}
		if fields := subAgentPromptCatalogEN.paramDescriptions[toolName]; fields != nil {
			if desc := fields[jsonTagName]; desc != "" {
				schema.Description = desc
			}
		}
	}
}

func (m *SubAgentMiddleware) buildDefaultPromptLocked() string {
	catalog := subAgentPromptText()
	var sb strings.Builder
	sb.WriteString(catalog.prefix)

	if len(m.subAgents) > 0 {
		sb.WriteString(catalog.agentsTitle)
		sb.WriteString("\n")
		for name, sa := range m.subAgents {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", name, sa.Description))
		}
	}

	sb.WriteString(catalog.suffix)
	return sb.String()
}

func (m *SubAgentMiddleware) buildMaskedPromptLocked(visible map[string]bool) string {
	if !constant.IsEnglishPromptLang() {
		return m.buildMaskedPromptLegacyLocked(visible)
	}

	catalog := subAgentPromptText()
	if !visible[constant.ToolTask] && !visible[constant.ToolListSubAgents] {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(catalog.title)
	sb.WriteString("\n\n")
	if visible[constant.ToolTask] {
		sb.WriteString(catalog.intro)
		sb.WriteString("\n\n")
	}
	sb.WriteString(catalog.toolsTitle)
	sb.WriteString("\n")
	if visible[constant.ToolTask] {
		sb.WriteString(catalog.toolPromptLines[constant.ToolTask])
		sb.WriteString("\n")
	}
	if visible[constant.ToolListSubAgents] {
		sb.WriteString(catalog.toolPromptLines[constant.ToolListSubAgents])
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	if visible[constant.ToolTask] && len(m.subAgents) > 0 {
		sb.WriteString(catalog.agentsTitle)
		sb.WriteString("\n")
		for name, sa := range m.subAgents {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", name, sa.Description))
		}
		sb.WriteString("\n")
	}

	if visible[constant.ToolTask] {
		sb.WriteString(catalog.guidelinesTitle)
		sb.WriteString("\n")
		for i, guide := range catalog.guidelines {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, guide))
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

func (m *SubAgentMiddleware) buildMaskedPromptLegacyLocked(visible map[string]bool) string {
	var sb strings.Builder
	sb.WriteString("## 子代理委托\n\n")
	if visible[constant.ToolTask] {
		sb.WriteString("你可以将任务委托给专门的子代理处理。\n\n")
	}
	sb.WriteString("可用工具：\n")
	if visible[constant.ToolTask] {
		sb.WriteString("- task: 启动子代理执行任务\n")
	}
	if visible[constant.ToolListSubAgents] {
		sb.WriteString("- list_subagents: 列出所有可用的子代理\n")
	}
	sb.WriteString("\n")

	if visible[constant.ToolTask] && len(m.subAgents) > 0 {
		sb.WriteString("可用的子代理：\n")
		for name, sa := range m.subAgents {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", name, sa.Description))
		}
		sb.WriteString("\n")
	}

	if visible[constant.ToolTask] {
		sb.WriteString("使用指南：\n")
		sb.WriteString("1. 子代理有独立的上下文，需要在任务描述中提供必要信息\n")
		sb.WriteString("2. 子代理的执行结果会返回给你\n")
		sb.WriteString("3. 当前 in_progress 的 Todo Item 内部有多个独立子任务时，可以同时发起多个 task 调用来并行处理\n")
		sb.WriteString("4. 禁止用 task 并行处理不同 Todo Item 的工作——Item 之间是严格串行的")
	}

	return sb.String()
}
