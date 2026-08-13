package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type listSkillsInput struct {
	Verbose bool `json:"verbose,omitempty" jsonschema:"description=是否返回详细信息,可选"`
}

func (m *Middleware) newListSkillsTool() tool.BaseTool {
	t, _ := utils.InferTool(
		constant.ToolListSkills,
		skillToolDesc(constant.ToolListSkills),
		func(ctx context.Context, input *listSkillsInput) (string, error) {
			m.mu.Lock()
			defer m.mu.Unlock()

			if len(m.skills) == 0 {
				return constant.MsgNoSkills, nil
			}

			skillList := make([]map[string]interface{}, 0, len(m.skills))
			for _, skill := range m.skills {
				if skill == nil {
					continue
				}
				info := map[string]interface{}{
					"name":        skill.Name,
					"description": skill.Description,
					"active":      m.activeSkills[skill.Name],
				}
				if skill.License != "" {
					info["license"] = skill.License
				}
				skillList = append(skillList, info)
			}

			output, _ := json.MarshalIndent(skillList, "", "  ")
			return string(output), nil
		},
		skillToolOptions(constant.ToolListSkills)...,
	)
	return t
}

type activateSkillInput struct {
	Name string `json:"name" jsonschema:"description=要激活的技能名称,required"`
}

func (m *Middleware) newActivateSkillTool() tool.BaseTool {
	t, _ := utils.InferTool(
		constant.ToolActivateSkill,
		skillToolDesc(constant.ToolActivateSkill),
		func(ctx context.Context, input *activateSkillInput) (string, error) {
			m.mu.Lock()
			defer m.mu.Unlock()

			skill := findSkillByName(m.skills, input.Name)
			if skill == nil {
				return fmt.Sprintf("[Error] activate_skill failed: %s: %s", constant.ErrMsgSkillNotFound, input.Name), nil
			}

			if len(skill.Metadata) > 0 {
				if m.toNextModelInput == nil {
					m.toNextModelInput = make(map[string]string, len(skill.Metadata))
				}
				for k, v := range skill.Metadata {
					m.toNextModelInput[k] = v
				}
			}

			skillRoot := skillDir(skill.Path)
			if skill.Content != "" {
				m.activeSkills[input.Name] = true
				return fmt.Sprintf(constant.SkillActivatedMessageFormat, input.Name, skillRoot, skill.Content), nil
			}

			m.activeSkills[input.Name] = true
			return fmt.Sprintf(constant.SkillActivatedSimpleFormat, input.Name, skillRoot), nil
		},
		skillToolOptions(constant.ToolActivateSkill)...,
	)
	return t
}

type deactivateSkillInput struct {
	Name string `json:"name" jsonschema:"description=要停用的技能名称,required"`
}

func (m *Middleware) newDeactivateSkillTool() tool.BaseTool {
	t, _ := utils.InferTool(
		constant.ToolDeactivateSkill,
		skillToolDesc(constant.ToolDeactivateSkill),
		func(ctx context.Context, input *deactivateSkillInput) (string, error) {
			m.mu.Lock()
			defer m.mu.Unlock()

			if !m.activeSkills[input.Name] {
				return fmt.Sprintf(constant.MsgSkillNotActive, input.Name), nil
			}

			delete(m.activeSkills, input.Name)
			return fmt.Sprintf(constant.MsgSkillDeactivated, input.Name), nil
		},
		skillToolOptions(constant.ToolDeactivateSkill)...,
	)
	return t
}
