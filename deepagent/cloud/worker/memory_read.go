//go:build !windows

package worker

import (
	"strconv"

	"eino-cli/deepagent/core/memory"
	"eino-cli/deepagent/core/middleware"
)

func (b *threadBuilder) memoryReadMiddleware(threadInfoUserID int64) middleware.Middleware {
	if !b.cfg.Memory.Enabled || b.deps.MemoryWorkspace == nil || threadInfoUserID == 0 {
		return nil
	}
	return memory.NewSummaryMiddleware(memory.SummaryMiddlewareConfig{
		UserID:    strconv.FormatInt(threadInfoUserID, 10),
		Workspace: b.deps.MemoryWorkspace,
	})
}
