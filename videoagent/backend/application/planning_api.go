package videoagent

import (
	"eino-cli/videoagent/backend/planning"
	"github.com/cloudwego/eino/components/model"
)

type (
	ModelPlanner        = planning.ModelPlanner
	PromptPlanner       = planning.PromptPlanner
	PromptStageConfig   = planning.PromptStageConfig
	PromptPlannerConfig = planning.PromptPlannerConfig
	PromptRuntimeConfig = planning.PromptRuntimeConfig
	ClipMixPlanner      = planning.ClipMixPlanner
	ClipMixConfig       = planning.ClipMixConfig
)

func NewModelPlanner(chatModel model.BaseChatModel) (*ModelPlanner, error) {
	return planning.NewModelPlanner(chatModel)
}

func NewStageModelPlanner(requirementModel, clipScriptModel, resourceModel model.BaseChatModel) (*ModelPlanner, error) {
	return planning.NewStageModelPlanner(requirementModel, clipScriptModel, resourceModel)
}

func NewPromptPlanner(executor PromptExecutor, config PromptPlannerConfig) (*PromptPlanner, error) {
	return planning.NewPromptPlanner(executor, config)
}

func DefaultPromptPlannerConfig() PromptPlannerConfig {
	return planning.DefaultPromptPlannerConfig()
}

func NewClipMixPlanner(gateway ModelGateway, modelName string) (*ClipMixPlanner, error) {
	return planning.NewClipMixPlanner(gateway, modelName)
}
