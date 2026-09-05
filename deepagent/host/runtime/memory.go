package runtime

import (
	"context"

	sdkmiddleware "eino-cli/deepagent/core/middleware"
	"github.com/cloudwego/eino/schema"

	"eino-cli/deepagent/backend/consts"
	memorystore "eino-cli/deepagent/backend/memory/store"
	agentmemory "eino-cli/deepagent/memory/facts"
)

type memoryMiddleware struct {
	sdkmiddleware.BaseMiddleware
	store     *memorystore.Store
	agentName string
}

func newMemoryMiddleware() (memory *memoryMiddleware) {
	memory = &memoryMiddleware{store: memorystore.NewStore(), agentName: consts.DefaultAgentKey}
	return memory
}

func (memory *memoryMiddleware) Name() (name string) {
	return "memory"
}

func (memory *memoryMiddleware) BuildInitialContext(ctx context.Context) (messages []*schema.Message, err error) {
	if memory == nil {
		return nil, nil
	}
	if block := agentmemory.GetMemoryPromptBlock(memory.store, memory.agentName, 2000); block != "" {
		messages = append(messages, schema.SystemMessage(block))
	}
	return messages, nil
}

var _ sdkmiddleware.Middleware = (*memoryMiddleware)(nil)
