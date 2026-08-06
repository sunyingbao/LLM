package application

import (
	"context"

	"github.com/cloudwego/eino/components/model"

	modelimpl "eino-cli/videoagent/backend/model"
)

type (
	ChatModelConfig   = modelimpl.ChatModelConfig
	CredentialsConfig = modelimpl.CredentialsConfig
	ModelCredential   = modelimpl.ModelCredential
)

func NewChatModel(ctx context.Context, config ChatModelConfig) (model.BaseChatModel, error) {
	return modelimpl.NewChatModel(ctx, config)
}

func NewFornaxPromptExecutor(config FornaxConfig) (PromptExecutor, error) {
	return modelimpl.NewFornaxPromptExecutor(config)
}

func NewBytedanceModelGateway() (ModelGateway, error) {
	return modelimpl.NewBytedanceModelGateway()
}
