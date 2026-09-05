//go:build !windows

package worker

import (
	"context"
	"testing"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/compact"
)

func TestBuildThreadLevelConfigUsesEightyFivePercentOfContextWindow(t *testing.T) {
	b := &threadBuilder{}

	opts := b.buildThreadLevelConfig(context.Background(), threadSpec{
		ThreadID: "thread-1",
	}, threadResources{}, ResolvedTurnProfile{Model: ModelProfile{ContextWindow: 100000}})

	limiter, ok := opts.CompactionStrategy.(agentthread.AutoCompactLimiter)
	if !ok {
		t.Fatalf("compaction strategy does not expose auto compact limit: %T", opts.CompactionStrategy)
	}
	if got, want := limiter.AutoCompactTokenLimit(), int64(85000); got != want {
		t.Fatalf("auto compact token limit=%d, want %d", got, want)
	}
}

func TestBuildThreadLevelConfigKeepsExplicitAutoCompactLimit(t *testing.T) {
	b := &threadBuilder{}

	opts := b.buildThreadLevelConfig(context.Background(), threadSpec{
		ThreadID: "thread-1",
		Profile:  ResolvedThreadProfile{Compaction: CompactionConfig{AutoCompactLimitTokens: 12345}},
	}, threadResources{}, ResolvedTurnProfile{Model: ModelProfile{ContextWindow: 100000}})

	limiter, ok := opts.CompactionStrategy.(agentthread.AutoCompactLimiter)
	if !ok {
		t.Fatalf("compaction strategy does not expose auto compact limit: %T", opts.CompactionStrategy)
	}
	if got, want := limiter.AutoCompactTokenLimit(), int64(12345); got != want {
		t.Fatalf("auto compact token limit=%d, want %d", got, want)
	}
}

func TestBuildThreadLevelConfigPassesCompactPromptAppend(t *testing.T) {
	b := &threadBuilder{}

	opts := b.buildThreadLevelConfig(context.Background(), threadSpec{
		ThreadID: "thread-1",
		Profile:  ResolvedThreadProfile{Compaction: CompactionConfig{PromptAppend: "Preserve unresolved verification risks."}},
	}, threadResources{}, ResolvedTurnProfile{Model: ModelProfile{ContextWindow: 100000}})

	strategy, ok := opts.CompactionStrategy.(*compact.CodexStrategy)
	if !ok {
		t.Fatalf("compaction strategy=%T, want *compact.CodexStrategy", opts.CompactionStrategy)
	}
	if got := strategy.PromptAppend; got != "Preserve unresolved verification risks." {
		t.Fatalf("compact prompt append=%q", got)
	}
}
