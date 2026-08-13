//go:build llm

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"eino-cli/deepagent/cloud/worker/bootstrap/config"
	"eino-cli/deepagent/cloud/worker/bootstrap/internal/chatmodel"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	memorypkg "eino-cli/deepagent/core/memory"
	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func TestMemoryLLMClosedLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	m := buildAcceptanceModel(t, ctx)
	root, err := os.MkdirTemp("", "memory-llm-acceptance-*")
	require.NoError(t, err)
	t.Logf("memory workspace root: %s", root)

	history := &acceptanceHistoryStore{}
	appendLLMAcceptanceHistory(t, ctx, history)

	backend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     root,
		VirtualMode: true,
	})
	workspace := memorypkg.NewWorkspace(backend, "memory")
	store := memorypkg.NewInMemoryStore()
	engine, err := memorypkg.NewEngine(memorypkg.Config{
		Store:     store,
		History:   history,
		Workspace: workspace,
		Extract:   memorypkg.NewModelStage1ExtractFunc(m),
	})
	require.NoError(t, err)

	stage1, err := engine.RunStage1(ctx, memorypkg.RunStage1Request{
		Memory:         memorypkg.UserMemoryContext{UserID: "llm-user", WriteEnabled: true, WorkspaceRoot: "memory"},
		SourceThreadID: "thread-memory-llm",
		SourceTurnID:   "turn-1",
		RolloutPath:    "acceptance/thread-memory-llm.jsonl",
		RolloutCWD:     "/repo/aic_agent_sdk",
	})
	require.NoError(t, err)
	require.Equal(t, memorypkg.Stage1Succeeded, stage1.Status)
	require.NotEmpty(t, strings.TrimSpace(stage1.RawMemory))
	t.Logf("stage1 raw memory:\n%s", stage1.RawMemory)
	t.Logf("stage1 rollout summary:\n%s", stage1.RolloutSummary)
	requireMemoryDetailSignals(t, stage1.RawMemory+"\n"+stage1.RolloutSummary)

	claimed, err := store.ClaimStage2Jobs(ctx, memorypkg.ClaimStage2Request{
		Now:      time.Now(),
		Owner:    "llm-acceptance",
		LeaseTTL: 3 * time.Minute,
		Limit:    1,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	prepared, err := engine.PrepareStage2(ctx, claimed[0], 20)
	require.NoError(t, err)
	require.False(t, prepared.Noop)
	require.NoError(t, runLLMStage2ThreadPrompt(ctx, m, engine, workspace, prepared.Spec))
	userWorkspace := workspace.ForUser(prepared.Spec.UserID)

	summary, err := userWorkspace.ReadSummary(ctx)
	require.NoError(t, err)
	require.True(t, summary.Found)
	require.True(t, strings.HasPrefix(summary.Content, "v1\n") || summary.Content == "v1")

	memoryText, err := userWorkspace.ReadMemory(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(memoryText))
	requireMemoryDetailSignals(t, memoryText+"\n"+summary.Content)

	t.Logf("MEMORY.md:\n%s", memoryText)
	t.Logf("memory_summary.md:\n%s", summary.Content)
}

func TestMemoryLLMSchedulerClosedLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	m := buildAcceptanceModel(t, ctx)
	root, err := os.MkdirTemp("", "memory-llm-scheduler-*")
	require.NoError(t, err)
	t.Logf("memory scheduler workspace root: %s", root)

	history := &acceptanceHistoryStore{}
	appendLLMAcceptanceHistory(t, ctx, history)

	backend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     root,
		VirtualMode: true,
	})
	workspace := memorypkg.NewWorkspace(backend, "memory")
	store := memorypkg.NewInMemoryStore()
	now := time.Now()
	engine, err := memorypkg.NewEngine(memorypkg.Config{
		Store:     store,
		History:   history,
		Workspace: workspace,
		Extract:   memorypkg.NewModelStage1ExtractFunc(m),
		Now:       func() time.Time { return now },
	})
	require.NoError(t, err)
	require.NoError(t, store.TouchSource(ctx, memorypkg.TouchSourceRequest{
		Memory:          memorypkg.UserMemoryContext{UserID: "llm-user", WriteEnabled: true, WorkspaceRoot: "memory"},
		SourceThreadID:  "thread-memory-llm",
		SourceUpdatedAt: now.Add(-time.Minute),
		EligibleAt:      now.Add(-time.Minute),
		Mode:            memorypkg.SourceModeEnabled,
	}))

	host := &acceptanceStage2Host{threadID: "memory-stage2-thread"}
	loop := memorypkg.NewMemoryJobLoop(memorypkg.MemoryJobLoopConfig{
		Store:                    store,
		Engine:                   engine,
		Stage2ThreadHost:         host,
		Owner:                    "llm-acceptance",
		Stage1LeaseTTL:           time.Minute,
		Stage1MaxClaimedPerScan:  1,
		Stage2LeaseTTL:           3 * time.Minute,
		Stage2MaxUsersPerScan:    1,
		Stage2OutputLimitPerUser: 20,
		Now:                      func() time.Time { return now },
	})
	result, err := loop.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Stage1Succeeded)
	require.Equal(t, 1, result.Stage2ThreadCreated)
	require.NotEmpty(t, host.spec.InitialPrompt)
	require.NoError(t, runLLMStage2ThreadPrompt(ctx, m, engine, workspace, host.spec))
	userWorkspace := workspace.ForUser(host.spec.UserID)

	summary, err := userWorkspace.ReadSummary(ctx)
	require.NoError(t, err)
	require.True(t, summary.Found)

	memoryText, err := userWorkspace.ReadMemory(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(memoryText))
	requireMemoryDetailSignals(t, memoryText+"\n"+summary.Content)

	t.Logf("scheduler MEMORY.md:\n%s", memoryText)
	t.Logf("scheduler memory_summary.md:\n%s", summary.Content)
}

func TestMemoryLLMMixedSignalQuality(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	m := buildAcceptanceModel(t, ctx)
	root, err := os.MkdirTemp("", "memory-llm-mixed-*")
	require.NoError(t, err)
	t.Logf("mixed memory workspace root: %s", root)

	history := &acceptanceHistoryStore{}
	appendMixedSignalAcceptanceHistory(t, ctx, history)

	backend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     root,
		VirtualMode: true,
	})
	workspace := memorypkg.NewWorkspace(backend, "memory")
	store := memorypkg.NewInMemoryStore()
	engine, err := memorypkg.NewEngine(memorypkg.Config{
		Store:     store,
		History:   history,
		Workspace: workspace,
		Extract:   memorypkg.NewModelStage1ExtractFunc(m),
	})
	require.NoError(t, err)

	stage1, err := engine.RunStage1(ctx, memorypkg.RunStage1Request{
		Memory:         memorypkg.UserMemoryContext{UserID: "mixed-user", WriteEnabled: true, WorkspaceRoot: "memory"},
		SourceThreadID: "thread-memory-mixed",
		SourceTurnID:   "turn-1",
		RolloutPath:    "acceptance/thread-memory-mixed.jsonl",
		RolloutCWD:     "/repo/aic_agent_sdk",
	})
	require.NoError(t, err)
	require.Equal(t, memorypkg.Stage1Succeeded, stage1.Status)
	require.NotEmpty(t, strings.TrimSpace(stage1.RawMemory))
	requireSemanticHit(t, stage1.RawMemory+"\n"+stage1.RolloutSummary, []string{"可复用经验", "可重用经验", "reusable experience", "reusable lessons"})
	requireSemanticHit(t, stage1.RawMemory+"\n"+stage1.RolloutSummary, []string{"最新明确更正", "最新显式修正", "显式修正", "latest explicit correction", "newer correction"})
	requireNotContainsAny(t, stage1.RawMemory+"\n"+stage1.RolloutSummary, []string{"TEMP-JUNK-MIXED-QUALITY"})

	claimed, err := store.ClaimStage2Jobs(ctx, memorypkg.ClaimStage2Request{
		Now:      time.Now(),
		Owner:    "llm-mixed-acceptance",
		LeaseTTL: 3 * time.Minute,
		Limit:    1,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	prepared, err := engine.PrepareStage2(ctx, claimed[0], 20)
	require.NoError(t, err)
	require.False(t, prepared.Noop)
	require.NoError(t, runLLMStage2ThreadPrompt(ctx, m, engine, workspace, prepared.Spec))
	userWorkspace := workspace.ForUser(prepared.Spec.UserID)

	summary, err := userWorkspace.ReadSummary(ctx)
	require.NoError(t, err)
	require.True(t, summary.Found)
	require.True(t, strings.HasPrefix(summary.Content, "v1\n") || summary.Content == "v1")
	memoryText, err := userWorkspace.ReadMemory(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(memoryText))

	consolidated := memoryText + "\n" + summary.Content
	requireSemanticHit(t, consolidated, reusableMemoryNeedles())
	requireSemanticHit(t, consolidated, conflictResolutionNeedles())
	requireSemanticHit(t, consolidated, distributedCoordinatorNeedles())
	requireNotContainsAny(t, consolidated, []string{"TEMP-JUNK-MIXED-QUALITY", "purple pineapple", "最早偏好优先", "oldest preference wins"})

	answer, err := askLLMWithMemorySummary(ctx, m, summary.Content, "如果下一次用户问 memory 系统的效果、冲突偏好、分布式 worker stage 启动逻辑，应该记住哪些原则？")
	require.NoError(t, err)
	requireSemanticHit(t, answer, reusableMemoryNeedles())
	requireSemanticHit(t, answer, conflictResolutionNeedles())
	requireSemanticHit(t, answer, distributedCoordinatorNeedles())
	requireNotContainsAny(t, answer, []string{"TEMP-JUNK-MIXED-QUALITY", "purple pineapple", "最早偏好优先", "oldest preference wins"})

	t.Logf("mixed stage1 raw memory:\n%s", stage1.RawMemory)
	t.Logf("mixed MEMORY.md:\n%s", memoryText)
	t.Logf("mixed memory_summary.md:\n%s", summary.Content)
	t.Logf("mixed readback answer:\n%s", answer)
}

func buildAcceptanceModel(t *testing.T, ctx context.Context) model.ToolCallingChatModel {
	t.Helper()
	cfg, err := config.Load(nil)
	if err != nil {
		t.Skipf("skip LLM acceptance: worker config unavailable: %v", err)
	}
	modelID := ""
	if role, ok := cfg.Roles["main"]; ok {
		modelID = role.DefaultModel
	}
	if modelID == "" {
		for id := range cfg.Models {
			modelID = id
			break
		}
	}
	if modelID == "" {
		t.Skip("skip LLM acceptance: no worker model configured")
	}
	modelCfg, ok := cfg.Models[modelID]
	if !ok {
		t.Skipf("skip LLM acceptance: model %q missing from config", modelID)
	}
	modelCfg.MaxTokens = 4096
	m, err := chatmodel.NewBuilder().Build(ctx, modelCfg)
	if err != nil {
		t.Skipf("skip LLM acceptance: build model %q: %v", modelID, err)
	}
	return m
}

func runLLMStage2ThreadPrompt(ctx context.Context, m model.ToolCallingChatModel, engine *memorypkg.Engine, workspace *memorypkg.Workspace, spec memorypkg.Stage2ThreadSpec) error {
	resp, err := m.Generate(ctx, []*schema.Message{schema.UserMessage(`Execute this internal memory_consolidation task and return JSON only.

The JSON shape is:
{"memory":"<complete MEMORY.md content>","memory_summary":"<complete memory_summary.md content>"}

Thread prompt:
` + spec.InitialPrompt)})
	if err != nil {
		return err
	}
	var parsed struct {
		Memory        string `json:"memory"`
		MemorySummary string `json:"memory_summary"`
	}
	if err := sonic.UnmarshalString(extractLLMJSON(resp.Content), &parsed); err != nil {
		return err
	}
	userWorkspace := workspace.ForUser(spec.UserID)
	if err := userWorkspace.WriteConsolidated(ctx, parsed.Memory, parsed.MemorySummary); err != nil {
		return err
	}
	return engine.CompleteStage2Thread(ctx, memorypkg.MarkStage2DoneRequest{
		UserID:                  spec.UserID,
		OwnershipToken:          spec.OwnershipToken,
		CompletedInputWatermark: spec.InputWatermark,
		BaselineHash:            spec.InputHash,
		StartedArtifactHash:     spec.StartedArtifactHash,
		StartedMemoryHash:       spec.StartedMemoryHash,
		StartedSummaryHash:      spec.StartedSummaryHash,
		CompletedAt:             time.Now(),
	})
}

func askLLMWithMemorySummary(ctx context.Context, m model.ToolCallingChatModel, summary string, question string) (string, error) {
	resp, err := m.Generate(ctx, []*schema.Message{
		schema.SystemMessage("Answer from the supplied memory summary. Keep only reusable user/workflow preferences. Do not repeat temporary QA markers or stale contradicted preferences."),
		schema.UserMessage("memory_summary.md:\n" + summary + "\n\nQuestion:\n" + question),
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func extractLLMJSON(content string) string {
	content = strings.TrimSpace(content)
	candidates := append(extractFencedJSONCandidates(content), extractMemoryJSONCandidates(content)...)
	if len(candidates) == 0 {
		return content
	}
	last := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.start >= last.start {
			last = candidate
		}
	}
	return last.content
}

type jsonCandidate struct {
	start   int
	content string
}

func extractFencedJSONCandidates(content string) []jsonCandidate {
	rest := content
	offset := 0
	var candidates []jsonCandidate
	for {
		fenceStart := strings.Index(rest, "```")
		if fenceStart < 0 {
			return candidates
		}
		absoluteFenceStart := offset + fenceStart
		afterFence := rest[fenceStart+len("```"):]
		lineEnd := strings.IndexByte(afterFence, '\n')
		if lineEnd < 0 {
			return candidates
		}
		lang := strings.TrimSpace(afterFence[:lineEnd])
		bodyStart := lineEnd + 1
		fenceEnd := strings.Index(afterFence[bodyStart:], "```")
		if fenceEnd < 0 {
			return candidates
		}
		body := strings.TrimSpace(afterFence[bodyStart : bodyStart+fenceEnd])
		if (lang == "" || strings.EqualFold(lang, "json")) && isMemoryJSON(body) {
			candidates = append(candidates, jsonCandidate{
				start:   absoluteFenceStart + len("```") + bodyStart,
				content: body,
			})
		}
		consumed := fenceStart + len("```") + bodyStart + fenceEnd + len("```")
		offset += consumed
		rest = afterFence[bodyStart+fenceEnd+len("```"):]
	}
}

func extractMemoryJSONCandidates(content string) []jsonCandidate {
	var candidates []jsonCandidate
	for start := strings.Index(content, "{"); start >= 0; {
		if object, ok := balancedJSONObject(content[start:]); ok && isMemoryJSON(object) {
			candidates = append(candidates, jsonCandidate{start: start, content: object})
		}
		next := strings.Index(content[start+1:], "{")
		if next < 0 {
			break
		}
		start += 1 + next
	}
	return candidates
}

func balancedJSONObject(content string) (string, bool) {
	depth := 0
	inString := false
	escaped := false
	for i, r := range content {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch r {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[:i+1], true
			}
		}
	}
	return "", false
}

func isMemoryJSON(content string) bool {
	var parsed struct {
		Memory        string `json:"memory"`
		MemorySummary string `json:"memory_summary"`
	}
	return sonic.UnmarshalString(content, &parsed) == nil &&
		strings.TrimSpace(parsed.Memory) != "" &&
		strings.HasPrefix(strings.TrimSpace(parsed.MemorySummary), "v1")
}

func TestExtractLLMJSONAllowsPreamble(t *testing.T) {
	got := extractLLMJSON("I'll now consolidate the memory.\n\n```json\n{\"memory\":\"m\",\"memory_summary\":\"v1\\n- s\"}\n```")
	require.Equal(t, `{"memory":"m","memory_summary":"v1\n- s"}`, got)
}

func TestExtractLLMJSONSkipsPreambleShape(t *testing.T) {
	got := extractLLMJSON("Use shape {\"memory\":\"<complete>\",\"memory_summary\":\"<complete>\"}.\nFinal:\n{\"memory\":\"m\",\"memory_summary\":\"v1\\n- s\"}")
	require.Equal(t, `{"memory":"m","memory_summary":"v1\n- s"}`, got)
}

func TestExtractLLMJSONSkipsFencedPreambleShape(t *testing.T) {
	got := extractLLMJSON("Example:\n```json\n{\"memory\":\"<complete>\",\"memory_summary\":\"<complete>\"}\n```\nFinal:\n```json\n{\"memory\":\"m\",\"memory_summary\":\"v1\\n- s\"}\n```")
	require.Equal(t, `{"memory":"m","memory_summary":"v1\n- s"}`, got)
}

func TestExtractLLMJSONPrefersFinalValidFencedBlock(t *testing.T) {
	got := extractLLMJSON("Example:\n```json\n{\"memory\":\"example\",\"memory_summary\":\"v1\\n- example\"}\n```\nFinal:\n```json\n{\"memory\":\"final\",\"memory_summary\":\"v1\\n- final\"}\n```")
	require.Equal(t, `{"memory":"final","memory_summary":"v1\n- final"}`, got)
}

func TestExtractLLMJSONPrefersFinalRawObjectAfterFencedExample(t *testing.T) {
	got := extractLLMJSON("Example:\n```json\n{\"memory\":\"example\",\"memory_summary\":\"v1\\n- example\"}\n```\nFinal:\n{\"memory\":\"final\",\"memory_summary\":\"v1\\n- final\"}")
	require.Equal(t, `{"memory":"final","memory_summary":"v1\n- final"}`, got)
}

func TestExtractLLMJSONPrefersFinalValidObject(t *testing.T) {
	got := extractLLMJSON("Example {\"memory\":\"example\",\"memory_summary\":\"v1\\n- example\"}\nFinal {\"memory\":\"final\",\"memory_summary\":\"v1\\n- final\"}")
	require.Equal(t, `{"memory":"final","memory_summary":"v1\n- final"}`, got)
}

type acceptanceHistoryStore struct {
	records []*agentthread.HistoryRecord
}

type acceptanceStage2Host struct {
	threadID string
	spec     memorypkg.Stage2ThreadSpec
}

func (h *acceptanceStage2Host) CreateStage2Thread(_ context.Context, req memorypkg.Stage2CreateThreadRequest) (memorypkg.Stage2CreatedThread, error) {
	h.spec = req.Spec
	return memorypkg.Stage2CreatedThread{ThreadID: h.threadID}, nil
}

func (h *acceptanceStage2Host) CloseStage2Thread(context.Context, string, string) error {
	return nil
}

func (s *acceptanceHistoryStore) Append(_ context.Context, rec *agentthread.HistoryRecord) error {
	if rec != nil && rec.Seq == 0 {
		rec.Seq = int64(len(s.records) + 1)
	}
	s.records = append(s.records, rec)
	return nil
}

func (s *acceptanceHistoryStore) List(_ context.Context, q agentthread.ListQuery) ([]*agentthread.HistoryRecord, error) {
	var out []*agentthread.HistoryRecord
	for _, rec := range s.records {
		if q.ThreadID != "" && rec.ThreadID != q.ThreadID {
			continue
		}
		if q.TurnID != "" && rec.TurnID != q.TurnID {
			continue
		}
		out = append(out, rec)
	}
	if q.Order == agentthread.ListOrderDESC {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func appendLLMAcceptanceHistory(t *testing.T, ctx context.Context, history *acceptanceHistoryStore) {
	t.Helper()
	msgs := []*schema.Message{
		schema.UserMessage("我想做一个和 Codex 类似的 memory 系统。先写设计文档，不要直接开始实现。"),
		schema.AssistantMessage("我会先调研 Codex memory，再写总览设计。", nil),
		schema.UserMessage("这个文档需要体现设计思路。我看完基本就知道要怎么实现，最多细节不清楚。"),
		schema.AssistantMessage("我会把目标、功能、模块边界、数据流和验收标准写清楚。", nil),
		schema.UserMessage("prompt 是这个模块的核心，不能删得太严重。初版应该尽量复用 Codex，只删除不兼容的表述。"),
	}
	for i, msg := range msgs {
		require.NoError(t, history.Append(ctx, &agentthread.HistoryRecord{
			Type:      agentthread.HistoryRecordMessage,
			ThreadID:  "thread-memory-llm",
			TurnID:    "turn-1",
			MessageID: int64(i + 1),
			Message:   msg,
			CreateAt:  time.Unix(int64(100+i), 0).Unix(),
		}))
	}
}

func appendMixedSignalAcceptanceHistory(t *testing.T, ctx context.Context, history *acceptanceHistoryStore) {
	t.Helper()
	msgs := []*schema.Message{
		schema.UserMessage("我在测试 memory 管线。TEMP-JUNK-MIXED-QUALITY 和 purple pineapple 都是一次性噪声，只用于确认写入链路，不要当成长久记忆。"),
		schema.AssistantMessage("收到，我会把它们视为测试噪声。", nil),
		schema.UserMessage("先假设冲突偏好里最早偏好优先，oldest preference wins。"),
		schema.AssistantMessage("我先记下这个临时假设。", nil),
		schema.UserMessage("修正一下：最早偏好优先是错误的。真正应该记住的是最新明确更正优先，旧偏好应该降权或者废弃。"),
		schema.AssistantMessage("明白，冲突处理以最新明确更正为准。", nil),
		schema.UserMessage("memory 系统的效果不是机械记 marker，而是抽取有用信息：可复用经验、稳定偏好、失败教训，垃圾信息不能污染长期记忆。"),
		schema.AssistantMessage("我会把 memory 效果理解为语义层面的可复用经验抽取。", nil),
		schema.UserMessage("分布式 worker 下，stage 启动逻辑必须基于 store 状态和 lease semantics 推理，不能依赖某个进程本地 timer，因为 thread 可能被 release 到其他机器。"),
		schema.AssistantMessage("我会按 store 状态和租约语义来设计分布式启动逻辑。", nil),
	}
	for i, msg := range msgs {
		require.NoError(t, history.Append(ctx, &agentthread.HistoryRecord{
			Type:      agentthread.HistoryRecordMessage,
			ThreadID:  "thread-memory-mixed",
			TurnID:    "turn-1",
			MessageID: int64(i + 1),
			Message:   msg,
			CreateAt:  time.Unix(int64(200+i), 0).Unix(),
		}))
	}
}

func requireSemanticHit(t *testing.T, text string, needles []string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, needle := range needles {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return
		}
	}
	t.Fatalf("expected one semantic marker %v in:\n%s", needles, text)
}

func reusableMemoryNeedles() []string {
	return []string{
		"可复用经验",
		"可复用的经验",
		"可复用的知识",
		"可重用经验",
		"可重用的经验",
		"复用性",
		"语义提取",
		"核心语义",
		"reusable lessons",
		"reusable experience",
		"semantic extraction",
	}
}

func conflictResolutionNeedles() []string {
	return []string{
		"最新明确更正",
		"最新的明确纠正",
		"最新的明确修正",
		"最新的明确用户修正",
		"最新用户修正",
		"最新用户修正优先",
		"明确纠正优先",
		"最近一次明确修正",
		"最近一次的明确更正",
		"最近一次明确的修正",
		"最近一次明确用户修正",
		"最新显式修正",
		"最新显式纠正",
		"最新的显式用户修正",
		"最新的显式纠正",
		"显式用户修正",
		"显式修正",
		"显式纠正",
		"最高优先级",
		"权威",
		"latest explicit correction",
		"latest explicit user correction",
		"most recent explicit correction",
		"newer correction",
	}
}

func distributedCoordinatorNeedles() []string {
	return []string{
		"store 状态",
		"store state",
		"状态存储",
		"中央状态存储",
		"lease semantics",
		"租约语义",
		"租约机制",
	}
}

func requireNotContainsAny(t *testing.T, text string, needles []string) {
	t.Helper()
	for _, needle := range needles {
		require.NotContains(t, text, needle)
	}
}

func requireMemoryDetailSignals(t *testing.T, text string) {
	t.Helper()
	requireSemanticHit(t, text, []string{"design-first", "design document first", "before implementation", "technical design document", "technical design documents", "先写设计文档", "不要直接开始实现"})
	requireSemanticHit(t, text, []string{"implementation blueprint", "implementation path", "implementation-oriented", "implementation understanding", "enables execution", "design rationale", "architectural rationale", "architectural thinking", "architectural documentation", "architecture", "know how to implement", "knows how to implement", "complete enough", "设计思路", "知道要怎么实现"})
	requireSemanticHit(t, text, []string{"prompt preservation", "prompt is core", "prompt design is core", "core component", "reuse codex", "reusing codex", "复用 codex", "不能删得太严重"})
}
