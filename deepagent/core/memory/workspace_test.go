package memory

import (
	"context"
	"strings"
	"testing"

	"eino-cli/deepagent/core/backends"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceReadSummaryMissingIsNotError(t *testing.T) {
	ctx := context.Background()
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	ws := NewWorkspace(backend, "memory")

	summary, err := ws.ReadSummary(ctx)
	require.NoError(t, err)
	require.False(t, summary.Found)
	require.Empty(t, summary.Content)
}

func TestWorkspaceSyncInputsAndHash(t *testing.T) {
	ctx := context.Background()
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	ws := NewWorkspace(backend, "memory")

	hash, err := ws.SyncInputs(ctx, []Stage1Output{{
		ID:             "o1",
		UserID:         "user-1",
		SourceThreadID: "thread-1",
		SourceTurnID:   "turn-1",
		RawMemory:      "prefers concrete docs",
		RolloutSummary: "docs preference",
	}})
	require.NoError(t, err)
	require.NotEmpty(t, hash)

	raw, err := backend.Read(ctx, "memory/raw_memories.md", nil, nil)
	require.NoError(t, err)
	require.Contains(t, raw, "prefers concrete docs")
	require.Contains(t, raw, "thread-1")

	files, err := backend.GlobInfo(ctx, "*.md", "memory/rollout_summaries")
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.True(t, strings.Contains(files[0].Path, "thread-1"))
}

func TestWorkspaceWriteConsolidatedAndReadSummary(t *testing.T) {
	ctx := context.Background()
	backend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	ws := NewWorkspace(backend, "memory")

	require.NoError(t, ws.WriteConsolidated(ctx, "# Memory\n\n- stable preference", "v1\n- summary"))

	mem, err := ws.ReadMemory(ctx)
	require.NoError(t, err)
	require.Equal(t, "# Memory\n\n- stable preference", mem)

	summary, err := ws.ReadSummary(ctx)
	require.NoError(t, err)
	require.True(t, summary.Found)
	require.Equal(t, "v1\n- summary", summary.Content)
}

func TestWorkspaceWriteConsolidatedSanitizesDisposableMarkers(t *testing.T) {
	ctx := context.Background()
	backend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	ws := NewWorkspace(backend, "memory")

	require.NoError(t, ws.WriteConsolidated(ctx,
		"# Memory\n\n- ignore TEMP-JUNK-MIXED-QUALITY and QA-RAPID-A-12345\n- Temporary noise includes \"purple pineapple\"",
		"v1\n- WEBUI-MEM-12345 is temporary",
	))

	mem, err := ws.ReadMemory(ctx)
	require.NoError(t, err)
	require.NotContains(t, mem, "TEMP-JUNK-MIXED-QUALITY")
	require.NotContains(t, mem, "QA-RAPID-A-12345")
	require.Contains(t, mem, "explicitly temporary test marker")
	require.Contains(t, mem, "purple pineapple")
	require.NotContains(t, mem, "temporary noise payload")

	summary, err := ws.ReadSummary(ctx)
	require.NoError(t, err)
	require.NotContains(t, summary.Content, "WEBUI-MEM-12345")
	require.Contains(t, summary.Content, "explicitly temporary test marker")
}

func TestWorkspaceWriteConsolidatedRemovesExplicitRawNoiseTerms(t *testing.T) {
	ctx := context.Background()
	backend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	ws := NewWorkspace(backend, "memory")
	_, err := ws.SyncInputs(ctx, []Stage1Output{{
		ID:             "stage1-1",
		UserID:         "user-1",
		SourceThreadID: "thread-1",
		SourceTurnID:   "turn-1",
		RawMemory:      `when testing the memory pipeline, the user said "TEMP-JUNK-MIXED-QUALITY 和 purple pineapple 都是一次性噪声，只用于确认写入链路，不要当成长久记忆。"`,
		RolloutSummary: "# Summary\n\nThe user marked those strings as test noise.",
	}})
	require.NoError(t, err)

	require.NoError(t, ws.WriteConsolidated(ctx,
		"# Memory\n\n- The user said reusable lessons matter.\n- Do not preserve TEMP-JUNK-MIXED-QUALITY or purple pineapple as durable memory.",
		"v1\n- Filter purple pineapple from durable memory.",
	))

	mem, err := ws.ReadMemory(ctx)
	require.NoError(t, err)
	require.NotContains(t, mem, "TEMP-JUNK-MIXED-QUALITY")
	require.NotContains(t, mem, "purple pineapple")
	require.Contains(t, mem, "explicitly temporary test marker")
	require.Contains(t, mem, "The user said reusable lessons matter.")
	require.NotContains(t, mem, "explicitly temporary test marker or explicitly temporary test marker")

	summary, err := ws.ReadSummary(ctx)
	require.NoError(t, err)
	require.NotContains(t, summary.Content, "purple pineapple")
}

func TestWorkspaceWriteConsolidatedKeepsUsefulQuoteBeforeNoiseSentence(t *testing.T) {
	ctx := context.Background()
	backend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	ws := NewWorkspace(backend, "memory")
	_, err := ws.SyncInputs(ctx, []Stage1Output{{
		ID:             "stage1-1",
		UserID:         "user-1",
		SourceThreadID: "thread-1",
		SourceTurnID:   "turn-1",
		RawMemory:      `用户要求保留 "real worker E2E" 作为验证原则。TEMP-JUNK-MIXED-QUALITY 和 purple pineapple 都是一次性噪声，只用于确认写入链路，不要当成长久记忆。`,
		RolloutSummary: "# Summary\n\nThe user marked only the second sentence as test noise.",
	}})
	require.NoError(t, err)

	require.NoError(t, ws.WriteConsolidated(ctx,
		"# Memory\n\n- real worker E2E is a durable validation principle.\n- purple pineapple is disposable.",
		"v1\n- real worker E2E matters; filter purple pineapple.",
	))

	mem, err := ws.ReadMemory(ctx)
	require.NoError(t, err)
	require.Contains(t, mem, "real worker E2E")
	require.NotContains(t, mem, "purple pineapple")

	summary, err := ws.ReadSummary(ctx)
	require.NoError(t, err)
	require.Contains(t, summary.Content, "real worker E2E")
	require.NotContains(t, summary.Content, "purple pineapple")
}

func TestWorkspaceWriteConsolidatedRemovesExplicitlyRejectedPreferenceTerms(t *testing.T) {
	ctx := context.Background()
	backend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	ws := NewWorkspace(backend, "memory")
	_, err := ws.SyncInputs(ctx, []Stage1Output{
		{
			ID:             "stage1-1",
			UserID:         "user-1",
			SourceThreadID: "thread-1",
			SourceTurnID:   "turn-1",
			RawMemory:      "修正一下：最早偏好优先是错误的。真正应该记住的是最新明确更正优先，旧偏好应该降权或者废弃。",
			RolloutSummary: "# Summary\n\nThe user rejected the old preference and provided the replacement.",
		},
		{
			ID:             "stage1-2",
			UserID:         "user-1",
			SourceThreadID: "thread-2",
			SourceTurnID:   "turn-1",
			RawMemory:      `The user initially stated "oldest preference wins" but then explicitly corrected this to latest explicit correction wins.`,
			RolloutSummary: "# Summary\n\nThe English stale preference should not survive.",
		},
		{
			ID:             "stage1-3",
			UserID:         "user-1",
			SourceThreadID: "thread-3",
			SourceTurnID:   "turn-1",
			RawMemory:      `when discussing conflict resolution, the user first stated "先假设冲突偏好里最早偏好优先，oldest preference wins。" then corrected with "修正一下：最早偏好优先是错误的。真正应该记住的是最新明确更正优先，旧偏好应该降权或者废弃。"`,
			RolloutSummary: "# Summary\n\nA mixed Chinese and English stale preference should not survive.",
		},
	})
	require.NoError(t, err)

	require.NoError(t, ws.WriteConsolidated(ctx,
		"# Memory\n\n- 最早偏好优先 is stale; oldest preference wins is stale; earliest preference first is stale; \"先假设冲突偏好里最早偏好优先，oldest preference wins。\" was rejected; use 最新明确更正优先.",
		"v1\n- Do not use 最早偏好优先, oldest preference wins, or earliest preference first.",
	))

	mem, err := ws.ReadMemory(ctx)
	require.NoError(t, err)
	require.NotContains(t, mem, "最早偏好优先")
	require.NotContains(t, mem, "oldest preference wins")
	require.NotContains(t, mem, "oldest preference")
	require.NotContains(t, mem, "earliest preference first")
	require.NotContains(t, mem, "earliest preference")
	require.Contains(t, mem, "最新明确更正优先")
	require.Contains(t, mem, "an older rejected preference")

	summary, err := ws.ReadSummary(ctx)
	require.NoError(t, err)
	require.NotContains(t, summary.Content, "最早偏好优先")
	require.NotContains(t, summary.Content, "oldest preference wins")
	require.NotContains(t, summary.Content, "earliest preference first")
}

func TestWorkspaceAgentBackendWritesIntoMemoryRoot(t *testing.T) {
	ctx := context.Background()
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	ws := NewWorkspace(backend, "memory")
	agentBackend := ws.AgentBackend()
	require.NotNil(t, agentBackend)

	_, err := agentBackend.Write(ctx, "MEMORY.md", "# Memory\n\n- stable")
	require.NoError(t, err)
	_, err = agentBackend.Write(ctx, "memory_summary.md", "v1\n- stable")
	require.NoError(t, err)
	_, err = agentBackend.Write(ctx, "memory/rollout_summaries/source.md", "details")
	require.NoError(t, err)

	mem, err := ws.ReadMemory(ctx)
	require.NoError(t, err)
	require.Equal(t, "# Memory\n\n- stable", mem)
	summary, err := ws.ReadSummary(ctx)
	require.NoError(t, err)
	require.True(t, summary.Found)
	require.Equal(t, "v1\n- stable", summary.Content)
	details, err := backend.Read(ctx, "memory/rollout_summaries/source.md", nil, nil)
	require.NoError(t, err)
	require.Equal(t, "details", details)
}

func TestWorkspaceAgentBackendRejectsParentRelativePaths(t *testing.T) {
	ctx := context.Background()
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	ws := NewWorkspace(backend, "memory")
	agentBackend := ws.AgentBackend()

	result, err := agentBackend.Write(ctx, "../outside.md", "nope")
	require.NoError(t, err)
	require.Equal(t, backends.ErrInvalidPath, result.Error)

	_, err = agentBackend.Read(ctx, "a/../../outside.md", nil, nil)
	require.ErrorIs(t, err, backends.ErrInvalidPath)
}

func TestWorkspaceForUserIsolatesUserRoots(t *testing.T) {
	ctx := context.Background()
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	base := NewWorkspace(backend, "memory")
	userA := base.ForUser("123")
	userB := base.ForUser("456")

	require.NotEqual(t, userA.Root(), userB.Root())
	require.NoError(t, userA.WriteConsolidated(ctx, "# A", "v1\n- A"))
	require.NoError(t, userB.WriteConsolidated(ctx, "# B", "v1\n- B"))

	memA, err := userA.ReadMemory(ctx)
	require.NoError(t, err)
	memB, err := userB.ReadMemory(ctx)
	require.NoError(t, err)
	require.Equal(t, "# A", memA)
	require.Equal(t, "# B", memB)
}
