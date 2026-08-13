package skill

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"eino-cli/deepagent/core/constant"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/core/types"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type Middleware struct {
	loader Loader

	mu               sync.Mutex
	skills           []*SkillMetadata
	activeSkills     map[string]bool
	toNextModelInput map[string]string
	toolMask         deeptools.Mask
}

type MiddlewareConfig struct {
	// ToolMask 控制该中间件自身工具和工具提示词的可见性。
	ToolMask deeptools.Mask
}

func New(loader Loader) *Middleware {
	return NewWithConfig(loader, nil)
}

func NewWithConfig(loader Loader, cfg *MiddlewareConfig) *Middleware {
	if cfg == nil {
		cfg = &MiddlewareConfig{}
	}
	return &Middleware{
		loader:       loader,
		activeSkills: make(map[string]bool),
		toolMask:     cfg.ToolMask,
	}
}

func (m *Middleware) SetToolMask(mask deeptools.Mask) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolMask = mask
}

func (m *Middleware) Name() string {
	return constant.MiddlewareSkills
}

func (m *Middleware) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	tools := []tool.BaseTool{
		m.newListSkillsTool(),
		m.newActivateSkillTool(),
		m.newDeactivateSkillTool(),
	}
	return deeptools.FilterToolsByMask(ctx, tools, m.toolMask, "SkillsMiddleware::Tools"), nil
}

func (m *Middleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	m.mu.Lock()
	if m.toolMask == nil {
		defer m.mu.Unlock()
		if prompt := m.buildPromptLocked(nil); prompt != "" {
			return []*schema.Message{schema.SystemMessage(prompt)}, nil
		}
		return nil, nil
	}
	m.mu.Unlock()

	visibleTools, err := m.Tools(ctx)
	if err != nil {
		return nil, err
	}
	visible := deeptools.ToolNameSet(ctx, visibleTools, "SkillsMiddleware::BuildInitialContext")

	m.mu.Lock()
	defer m.mu.Unlock()
	if prompt := m.buildPromptLocked(visible); prompt != "" {
		return []*schema.Message{schema.SystemMessage(prompt)}, nil
	}
	return nil, nil
}

func (m *Middleware) buildPromptLocked(visible map[string]bool) string {
	if len(m.skills) == 0 {
		return ""
	}
	if visible == nil {
		return fmt.Sprintf(constant.SkillsSystemPromptTemplate, m.formatSkillsListLocked())
	}
	if !visible[constant.ToolListSkills] && !visible[constant.ToolActivateSkill] && !visible[constant.ToolDeactivateSkill] {
		return ""
	}

	var parts []string
	parts = append(parts, "## Skills System")
	parts = append(parts, "You have access to a skills library that provides specialized capabilities and domain knowledge.")
	parts = append(parts, "**Available Skills:**\n\n"+m.formatSkillsListLocked())

	if visible[constant.ToolActivateSkill] {
		parts = append(parts, `**How to Use Skills (Progressive Disclosure):**

Skills follow a progressive disclosure pattern - you see their name and description above, but only load full instructions when needed:

1. Recognize when a skill applies: Check if the user's task matches a skill's description
2. Activate the skill: Use `+"`activate_skill(name)`"+` to load full instructions into context
3. Follow the skill's instructions: Contains step-by-step workflows, best practices, and examples`)
		if visible[constant.ToolDeactivateSkill] {
			parts = append(parts, "4. Deactivate when done: Use `deactivate_skill(name)` to free up context space")
		}
	}

	var toolLines []string
	if visible[constant.ToolListSkills] {
		toolLines = append(toolLines, "- `list_skills`: View all available skills and their status")
	}
	if visible[constant.ToolActivateSkill] {
		toolLines = append(toolLines, "- `activate_skill`: Load a skill's full instructions")
	}
	if visible[constant.ToolDeactivateSkill] {
		toolLines = append(toolLines, "- `deactivate_skill`: Unload a skill's instructions")
	}
	if len(toolLines) > 0 {
		parts = append(parts, "**Available Tools:**\n"+strings.Join(toolLines, "\n"))
	}

	return strings.Join(parts, "\n\n")
}

func (m *Middleware) formatSkillsList() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.formatSkillsListLocked()
}

func (m *Middleware) formatSkillsListLocked() string {
	if len(m.skills) == 0 {
		return constant.MsgNoSkills
	}

	var lines []string
	for _, skill := range m.skills {
		if skill == nil {
			continue
		}
		name := skill.Name
		active := ""
		if m.activeSkills[name] {
			active = " [ACTIVE]"
		}
		lines = append(lines, fmt.Sprintf("- **%s**: %s%s", name, skill.Description, active))
		if len(skill.AllowedTools) > 0 {
			lines = append(lines, fmt.Sprintf("  -> Allowed tools: %s", strings.Join(skill.AllowedTools, ", ")))
		}
		lines = append(lines, fmt.Sprintf("  -> Path: `%s`", skill.Path))
		if isForkSkill(skill.Context) {
			agent := skill.Agent
			if agent == "" {
				agent = constant.ExecutorName
			}
			lines = append(lines, "  -> Execution: This skill should be executed in a child agent via the `task` tool. The tool's `fork_context` param should be set to true.")
			if isNoJoin(skill.Join) {
				lines = append(lines, "  -> Join behavior: Set the tool's `wait_for_done` param to false so the main agent does not wait for the child agent to finish.")
			} else {
				lines = append(lines, "  -> Join behavior: Keep the tool's `wait_for_done` param omitted or set to true so the main agent waits for the child agent to finish.")
			}
			lines = append(lines, fmt.Sprintf("  -> Delegation rule: When you decide to use this skill, delegate the work to subagent `%s`. Only skip delegation if the user explicitly tells you not to delegate.", agent))
			lines = append(lines, fmt.Sprintf("  -> Child task requirements: Explicitly require the child agent to use skill `%s`.", name))
			lines = append(lines, fmt.Sprintf("  -> Recursion guard: The delegated task must explicitly instruct the child agent not to delegate this same skill `%s` again.", name))
		}
	}
	return strings.Join(lines, "\n")
}

func isForkSkill(context string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(context)), "fork")
}

func isNoJoin(join string) bool {
	return strings.EqualFold(strings.TrimSpace(join), "no")
}

func (m *Middleware) BeforeAgent(ctx context.Context) error {
	if m.loader == nil {
		return nil
	}
	skills, err := m.loader.ListSkills(ctx)
	if err != nil {
		return err
	}
	cloned := cloneSkills(skills)
	m.mu.Lock()
	m.skills = cloned
	m.mu.Unlock()
	return nil
}

func (m *Middleware) ModifyModelRequest(ctx context.Context, initialContext []*schema.Message, messages []*schema.Message, state *types.GraphState) ([]*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.toNextModelInput) == 0 || len(messages) == 0 {
		return messages, nil
	}

	lastMessage := messages[len(messages)-1]
	if lastMessage.Extra == nil {
		lastMessage.Extra = make(map[string]any)
	}
	for key, value := range m.toNextModelInput {
		lastMessage.Extra[key] = value
	}
	m.toNextModelInput = nil
	return messages, nil
}

func (m *Middleware) ModifyModelResponse(ctx context.Context, modelResp *schema.Message, state *types.GraphState) (*schema.Message, error) {
	return modelResp, nil
}

func (m *Middleware) ModifyModelStreamResponse(ctx context.Context, modelResp *schema.StreamReader[*schema.Message], state *types.GraphState) (*schema.StreamReader[*schema.Message], error) {
	return modelResp, nil
}
func (m *Middleware) WrapToolCall() compose.ToolMiddleware { return compose.ToolMiddleware{} }
func (m *Middleware) BuildStateHandler() types.RunTimeStateful {
	return nil
}
