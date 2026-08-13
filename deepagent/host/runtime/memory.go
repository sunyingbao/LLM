package runtime

import (
	"context"

	sdkmiddleware "eino-cli/deepagent/core/middleware"
	"github.com/cloudwego/eino/schema"

	"eino-cli/backend/config"
	"eino-cli/backend/consts"
	memorystore "eino-cli/backend/memory/store"
	agentmemory "eino-cli/deepagent/memory/sgadk"
)

type sgadkMemoryMiddleware struct {
	sdkmiddleware.BaseMiddleware
	store     *memorystore.Store
	agentName string
}

func newSGADKMemoryMiddleware() (memory *sgadkMemoryMiddleware) {
	memory = &sgadkMemoryMiddleware{store: memorystore.NewStore(), agentName: consts.DefaultAgentKey}
	return memory
}

func (memory *sgadkMemoryMiddleware) Name() (name string) {
	return "sgadk-memory"
}

func (memory *sgadkMemoryMiddleware) BuildInitialContext(ctx context.Context) (messages []*schema.Message, err error) {
	if memory == nil {
		return nil, nil
	}
	if block := agentmemory.GetMemoryPromptBlock(memory.store, memory.agentName, 2000); block != "" {
		messages = append(messages, schema.SystemMessage(block))
	}
	if block := agentmemory.GetDreamMemoryPromptBlock(config.DreamMemoryDir(), 16*1024); block != "" {
		messages = append(messages, schema.SystemMessage(block))
	}
	return messages, nil
}

var _ sdkmiddleware.Middleware = (*sgadkMemoryMiddleware)(nil)
