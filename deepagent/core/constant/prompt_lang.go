package constant

import (
	"os"
	"strings"
)

// PromptLangEnv 控制 SDK 内置模型可见文案的语言。
const PromptLangEnv = "DEEPAGENT_PROMPT_LANG"

// IsEnglishPromptLang 返回是否启用英文内置提示词。
func IsEnglishPromptLang() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(PromptLangEnv)), "en")
}
