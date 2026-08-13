package memory

import (
	"strings"

	"eino-cli/deepagent/core/constant"
)

type memoryPromptCatalog struct {
	prefix string
	suffix string

	learningTitle      string
	learningIntro      string
	whenToUpdateTitle  string
	whenToUpdate       []string
	whenNotUpdateTitle string
	whenNotUpdate      []string
	sourcesTitle       string
}

var memoryPromptCatalogZH = memoryPromptCatalog{
	prefix: constant.MemorySystemPromptPrefix,
	suffix: constant.MemorySystemPromptSuffix,

	learningTitle:      "### 学习指南",
	learningIntro:      "你可以通过更新记忆文件来维护长期记忆。",
	whenToUpdateTitle:  "**何时更新记忆：**",
	whenToUpdate:       []string{"用户明确要求你记住某些内容", "收到关于你角色或行为的反馈", "学到了有价值的项目知识或模式"},
	whenNotUpdateTitle: "**何时不更新记忆：**",
	whenNotUpdate:      []string{"临时性信息（一次性任务）", "敏感信息（密码、密钥等）", "可以通过其他方式获取的信息"},
	sourcesTitle:       "**当前记忆系统文件路径：**",
}

var memoryPromptCatalogEN = memoryPromptCatalog{
	prefix: `## Persistent Memory

The following long-term memory contains important context, preferences, and learned information:

<memory>
`,
	suffix: `
</memory>
`,

	learningTitle:      "### Learning Guide",
	learningIntro:      "You can maintain long-term memory by updating the memory files.",
	whenToUpdateTitle:  "**When to update memory:**",
	whenToUpdate:       []string{"The user explicitly asks you to remember something", "You receive feedback about your role or behavior", "You learn valuable project knowledge or recurring patterns"},
	whenNotUpdateTitle: "**When not to update memory:**",
	whenNotUpdate:      []string{"Temporary information for a one-off task", "Sensitive information such as passwords or secrets", "Information that can be obtained in other ways"},
	sourcesTitle:       "**Current memory file paths:**",
}

func memoryPromptText() memoryPromptCatalog {
	if constant.IsEnglishPromptLang() {
		return memoryPromptCatalogEN
	}
	return memoryPromptCatalogZH
}

func formatMemoryLearningGuide(sources []string) string {
	catalog := memoryPromptText()
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(catalog.learningTitle)
	sb.WriteString("\n\n")
	sb.WriteString(catalog.learningIntro)
	sb.WriteString("\n\n")
	sb.WriteString(catalog.whenToUpdateTitle)
	sb.WriteString("\n")
	for _, item := range catalog.whenToUpdate {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(catalog.whenNotUpdateTitle)
	sb.WriteString("\n")
	for _, item := range catalog.whenNotUpdate {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}
	if len(sources) > 0 {
		sb.WriteString("\n")
		sb.WriteString(catalog.sourcesTitle)
		sb.WriteString("\n")
		for _, source := range sources {
			sb.WriteString("- ")
			sb.WriteString(source)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
