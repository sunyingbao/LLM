package main

import (
	"context"

	"eino-cli/videoagent/backend/contract"
	videomodel "eino-cli/videoagent/backend/model"
	"eino-cli/videoagent/backend/planning"
	"github.com/cloudwego/eino/components/model"
)

func loadModels(ctx context.Context, options runOptions, credentials *videomodel.CredentialsConfig) (model.BaseChatModel, contract.Planner, error) {
	var chatModel model.BaseChatModel
	if options.modelConfigPath != "" || credentials != nil {
		loaded, err := loadChatModel(ctx, options.modelConfigPath, options.chatModelKey, credentials)
		if err != nil {
			return nil, nil, err
		}
		chatModel = loaded
	}
	if credentials == nil && options.promptConfigPath == "" {
		return chatModel, nil, nil
	}
	planner, err := loadWorkflowPlanner(ctx, options.promptConfigPath, credentials, chatModel)
	if err != nil {
		return nil, nil, err
	}
	return chatModel, planner, nil
}

func loadChatModel(ctx context.Context, path, promptKey string, credentials *videomodel.CredentialsConfig) (model.BaseChatModel, error) {
	if credentials != nil {
		config, err := credentials.ChatModelConfig(promptKey)
		if err != nil {
			return nil, err
		}
		return videomodel.NewChatModel(ctx, config)
	}
	config, err := readJSON[videomodel.ChatModelConfig](path)
	if err != nil {
		return nil, err
	}
	return videomodel.NewChatModel(ctx, config)
}

func loadWorkflowPlanner(ctx context.Context, promptConfigPath string, credentials *videomodel.CredentialsConfig, chatModel model.BaseChatModel) (contract.Planner, error) {
	if promptConfigPath != "" {
		return loadPromptPlanner(promptConfigPath, credentials)
	}
	if credentials != nil {
		return loadCredentialPlanner(ctx, *credentials)
	}
	return planning.NewModelPlanner(chatModel)
}

func loadCredentialPlanner(ctx context.Context, credentials videomodel.CredentialsConfig) (contract.Planner, error) {
	requirementModel, err := loadCredentialModel(ctx, credentials, "aic.aic_tool.user_req_analysis")
	if err != nil {
		return nil, err
	}
	clipScriptModel, err := loadCredentialModel(ctx, credentials, "jichuang.creative.dr_script_e2e")
	if err != nil {
		return nil, err
	}
	return planning.NewStageModelPlanner(requirementModel, clipScriptModel, requirementModel)
}

func loadCredentialModel(ctx context.Context, credentials videomodel.CredentialsConfig, promptKey string) (model.BaseChatModel, error) {
	config, err := credentials.ChatModelConfig(promptKey)
	if err != nil {
		return nil, err
	}
	return videomodel.NewChatModel(ctx, config)
}

func loadPromptPlanner(path string, credentials *videomodel.CredentialsConfig) (contract.Planner, error) {
	config := planning.PromptRuntimeConfig{Planner: planning.DefaultPromptPlannerConfig()}
	if path != "" {
		loaded, err := readJSON[planning.PromptRuntimeConfig](path)
		if err != nil {
			return nil, err
		}
		config = loaded
	}
	if credentials != nil {
		config.Fornax = credentials.Fornax
	}
	executor, err := videomodel.NewFornaxPromptExecutor(config.Fornax)
	if err != nil {
		return nil, err
	}
	return planning.NewPromptPlanner(executor, config.Planner)
}
