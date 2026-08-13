package skill

import (
	"reflect"

	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/components/tool/utils"
	jsonschema "github.com/eino-contrib/jsonschema"
)

var skillToolDescriptionsEN = map[string]string{
	constant.ToolListSkills:      "List all available skills and their descriptions.",
	constant.ToolActivateSkill:   "Activate a skill and load its detailed instructions. The name parameter is the skill name.",
	constant.ToolDeactivateSkill: "Deactivate a skill. The name parameter is the skill name.",
}

var skillParamDescriptionsEN = map[string]map[string]string{
	constant.ToolListSkills: {
		"verbose": "Whether to return detailed information. Optional.",
	},
	constant.ToolActivateSkill: {
		"name": "Skill name to activate. Required.",
	},
	constant.ToolDeactivateSkill: {
		"name": "Skill name to deactivate. Required.",
	},
}

func skillToolDesc(toolName string) string {
	if constant.IsEnglishPromptLang() {
		return skillToolDescriptionsEN[toolName]
	}
	switch toolName {
	case constant.ToolListSkills:
		return constant.ToolListSkillsDesc
	case constant.ToolActivateSkill:
		return constant.ToolActivateSkillDesc
	case constant.ToolDeactivateSkill:
		return constant.ToolDeactivateSkillDesc
	default:
		return ""
	}
}

func skillToolOptions(toolName string) []utils.Option {
	if !constant.IsEnglishPromptLang() {
		return nil
	}
	return []utils.Option{utils.WithSchemaModifier(skillEnglishSchemaModifier(toolName))}
}

func skillEnglishSchemaModifier(toolName string) utils.SchemaModifierFn {
	return func(jsonTagName string, _ reflect.Type, _ reflect.StructTag, schema *jsonschema.Schema) {
		if schema == nil {
			return
		}
		if fields := skillParamDescriptionsEN[toolName]; fields != nil {
			if desc := fields[jsonTagName]; desc != "" {
				schema.Description = desc
			}
		}
	}
}
