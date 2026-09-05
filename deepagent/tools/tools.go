package tools

import (
	"eino-cli/deepagent/backend/config"
	"eino-cli/deepagent/backend/sandbox"

	"github.com/cloudwego/eino/components/tool"
)

func BuildExtensionTools(cfg *config.Config, manager sandbox.SandboxManager) (extensions []tool.BaseTool) {
	extensions = []tool.BaseTool{
		mustBuild(GetAskClarificationTool()),
		mustBuild(GetDeleteFileTool(manager)),
		mustBuild(GetRgTool(manager)),
		mustBuild(GetSemanticSearchTool(manager)),
		mustBuild(GetReadLintsTool(manager)),
		mustBuild(GetShellTool(manager, cfg)),
		mustBuild(GetAwaitShellTool()),
	}
	return extensions
}

func mustBuild(base tool.BaseTool, err error) (built tool.BaseTool) {
	if err != nil {
		panic(err)
	}
	return base
}
