//go:build !windows

package worker

import (
	"context"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/compact"
	"eino-cli/deepagent/core/constant"
)

// buildThreadLevelConfig configures state that belongs to the whole thread,
// not a single model turn: durable history and context compaction.
func (b *threadBuilder) buildThreadLevelConfig(ctx context.Context, spec threadSpec, resources threadResources, threadProfile ResolvedThreadProfile, turnProfile ResolvedTurnProfile) agentthread.DefaultThreadOptions {
	modelProfile := turnProfile.Model
	contextWindow := modelProfile.ContextWindow
	if contextWindow <= 0 {
		contextWindow = int64(constant.LookupModelContextWindow(ctx, modelProfile.ModelName))
	}
	autoLimit := threadProfile.Compaction.AutoCompactLimitTokens
	if autoLimit <= 0 && contextWindow > 0 {
		autoLimit = int(float64(contextWindow) * 0.85)
	}
	if autoLimit <= 0 {
		autoLimit = 16000
	}
	keptUserTokens := threadProfile.Compaction.CompactKeptUserTokens
	logs.CtxInfo(ctx, "[cloudagent] enable compaction: thread_id=%s model=%s context_window=%d auto_limit=%d kept_user_tokens=%d",
		spec.ThreadID, modelProfile.ModelName, contextWindow, autoLimit, keptUserTokens)
	return agentthread.DefaultThreadOptions{
		HistoryStore: resources.History,
		CompactionStrategy: compact.NewCodexStrategy(
			modelProfile.ChatModel,
			autoLimit,
			keptUserTokens,
			nil,
			compact.WithPromptAppend(threadProfile.Compaction.PromptAppend),
		),
	}
}
