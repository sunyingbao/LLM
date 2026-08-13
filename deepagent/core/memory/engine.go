package memory

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/agentthread"
)

type Config struct {
	Store              Store
	History            agentthread.HistoryRolloutStore
	Workspace          *Workspace
	Extract            Stage1ExtractFunc
	Stage1HistoryInput Stage1HistoryInputConfig
	Now                func() time.Time
}

type Engine struct {
	store              Store
	history            agentthread.HistoryRolloutStore
	workspace          *Workspace
	extract            Stage1ExtractFunc
	stage1HistoryInput Stage1HistoryInputConfig
	now                func() time.Time
}

func NewEngine(cfg Config) (*Engine, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("memory engine: store is required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Engine{
		store:              cfg.Store,
		history:            cfg.History,
		workspace:          cfg.Workspace,
		extract:            cfg.Extract,
		stage1HistoryInput: cfg.Stage1HistoryInput,
		now:                now,
	}, nil
}

func (e *Engine) RunStage1(ctx context.Context, req RunStage1Request) (Stage1Output, error) {
	start := time.Now()
	if strings.TrimSpace(req.Memory.UserID) == "" {
		return Stage1Output{}, fmt.Errorf("memory stage1: user id is required")
	}
	logs.CtxInfo(ctx, "[memory] stage1 extract start: user_id=%s source_thread_id=%s source_turn_id=%s", req.Memory.UserID, req.SourceThreadID, req.SourceTurnID)
	if !req.Memory.WriteEnabled {
		out := e.newStage1Output(req, Stage1ExtractResult{}, Stage1Skipped, "memory write disabled")
		logs.CtxInfo(ctx, "[memory] stage1 extract skipped: user_id=%s source_thread_id=%s reason=%s", req.Memory.UserID, req.SourceThreadID, out.ErrorSummary)
		return out, e.store.SaveStage1Output(ctx, out)
	}
	if e.history == nil {
		return Stage1Output{}, fmt.Errorf("memory stage1: history store is required")
	}
	if e.extract == nil {
		return Stage1Output{}, fmt.Errorf("memory stage1: extractor is required")
	}
	historyInput, err := buildStage1HistoryInput(ctx, e.history, req, e.stage1HistoryInput)
	if err != nil {
		logs.CtxError(ctx, "[memory] stage1 history input failed: user_id=%s source_thread_id=%s err=%v", req.Memory.UserID, req.SourceThreadID, err)
		return Stage1Output{}, err
	}
	result, err := e.extract(ctx, Stage1ExtractInput{
		Memory:          req.Memory,
		SourceThreadID:  req.SourceThreadID,
		SourceTurnID:    req.SourceTurnID,
		RolloutPath:     req.RolloutPath,
		RolloutCWD:      req.RolloutCWD,
		RolloutContents: historyInput.Contents,
	})
	if err != nil {
		logs.CtxError(ctx, "[memory] stage1 extract failed: user_id=%s source_thread_id=%s err=%v", req.Memory.UserID, req.SourceThreadID, err)
		out := e.newStage1Output(req, Stage1ExtractResult{}, Stage1Failed, err.Error())
		out.SourceUpdatedAt = historyInput.SourceUpdatedAt
		if saveErr := e.store.SaveStage1Output(ctx, out); saveErr != nil {
			return Stage1Output{}, saveErr
		}
		return out, err
	}
	status := Stage1Succeeded
	if strings.TrimSpace(result.RawMemory) == "" && strings.TrimSpace(result.RolloutSummary) == "" {
		status = Stage1SucceededNoOutput
	}
	out := e.newStage1Output(req, result, status, "")
	out.SourceUpdatedAt = historyInput.SourceUpdatedAt
	if err := e.store.SaveStage1Output(ctx, out); err != nil {
		logs.CtxError(ctx, "[memory] stage1 save output failed: user_id=%s source_thread_id=%s output_id=%s err=%v", out.UserID, out.SourceThreadID, out.ID, err)
		return Stage1Output{}, err
	}
	logs.CtxInfo(ctx, "[memory] stage1 extract done: user_id=%s source_thread_id=%s output_id=%s status=%s source_updated_at=%s duration_ms=%d",
		out.UserID, out.SourceThreadID, out.ID, out.Status, out.SourceUpdatedAt.UTC().Format(time.RFC3339Nano), time.Since(start).Milliseconds())
	return out, nil
}

func (e *Engine) RunClaimedStage1(ctx context.Context, source ClaimedSource) (Stage1Output, error) {
	req := RunStage1Request{
		Memory: UserMemoryContext{
			UserID:       source.UserID,
			WriteEnabled: source.Mode == SourceModeEnabled,
		},
		SourceThreadID: source.SourceThreadID,
	}
	out, err := e.RunStage1(ctx, req)
	if err != nil {
		_ = e.store.FailStage1Source(ctx, FailStage1SourceRequest{
			UserID:                   source.UserID,
			SourceThreadID:           source.SourceThreadID,
			OwnershipToken:           source.OwnershipToken,
			ProcessedSourceUpdatedAt: source.ClaimedSourceUpdatedAt,
			ErrorSummary:             err.Error(),
			FailedAt:                 e.now(),
		})
		return out, err
	}
	if err := e.store.CompleteStage1Source(ctx, CompleteStage1SourceRequest{
		UserID:                   source.UserID,
		SourceThreadID:           source.SourceThreadID,
		OwnershipToken:           source.OwnershipToken,
		ProcessedSourceUpdatedAt: source.ClaimedSourceUpdatedAt,
		Stage1OutputID:           out.ID,
		CompletedAt:              e.now(),
	}); err != nil {
		return out, err
	}
	return out, nil
}

func (e *Engine) ReadSummary(ctx context.Context, userID string) (Summary, error) {
	if e.workspace == nil {
		return Summary{}, fmt.Errorf("memory read: workspace is required")
	}
	if strings.TrimSpace(userID) == "" {
		return Summary{}, fmt.Errorf("memory read: user id is required")
	}
	return e.workspace.ForUser(userID).ReadSummary(ctx)
}

func (e *Engine) PrepareStage2(ctx context.Context, job ClaimedStage2Job, limit int) (PrepareStage2Result, error) {
	start := time.Now()
	if strings.TrimSpace(job.UserID) == "" {
		return PrepareStage2Result{}, fmt.Errorf("memory stage2: user id is required")
	}
	logs.CtxInfo(ctx, "[memory] stage2 prepare inputs start: user_id=%s watermark=%s limit=%d", job.UserID, job.ClaimedInputWatermark, limit)
	if strings.TrimSpace(job.OwnershipToken) == "" {
		return PrepareStage2Result{}, fmt.Errorf("memory stage2: ownership token is required")
	}
	if e.workspace == nil {
		return PrepareStage2Result{}, fmt.Errorf("memory stage2: workspace is required")
	}
	workspace := e.workspace.ForUser(job.UserID)
	if limit <= 0 {
		limit = 100
	}
	outputs, err := e.store.ListStage1Outputs(ctx, job.UserID, ListStage1Options{})
	if err != nil {
		return PrepareStage2Result{}, err
	}
	outputs = selectStage2Outputs(outputs, limit)
	if len(outputs) == 0 {
		logs.CtxInfo(ctx, "[memory] stage2 prepare noop: user_id=%s reason=no_outputs duration_ms=%d", job.UserID, time.Since(start).Milliseconds())
		return PrepareStage2Result{Noop: true}, nil
	}
	hash, err := workspace.SyncInputs(ctx, outputs)
	if err != nil {
		return PrepareStage2Result{}, err
	}
	if baseline, found, err := e.store.LoadBaseline(ctx, job.UserID); err != nil {
		return PrepareStage2Result{}, err
	} else if found && baseline.Hash == hash {
		logs.CtxInfo(ctx, "[memory] stage2 prepare noop: user_id=%s reason=baseline_unchanged output_count=%d hash=%s duration_ms=%d", job.UserID, len(outputs), hash, time.Since(start).Milliseconds())
		return PrepareStage2Result{Noop: true, SyncedHash: hash, OutputCount: len(outputs)}, nil
	}

	diff := renderWorkspaceDiff(hash, outputs)
	if err := workspace.WriteWorkspaceDiff(ctx, diff); err != nil {
		return PrepareStage2Result{}, err
	}
	raw, err := workspace.ReadRawMemories(ctx)
	if err != nil {
		return PrepareStage2Result{}, err
	}
	currentMemory, err := workspace.ReadMemory(ctx)
	if err != nil {
		return PrepareStage2Result{}, err
	}
	currentSummary, err := workspace.ReadSummary(ctx)
	if err != nil {
		return PrepareStage2Result{}, err
	}
	startedHashes, err := workspace.artifactHashes(ctx)
	if err != nil {
		return PrepareStage2Result{}, err
	}
	input := ConsolidateInput{
		Memory: UserMemoryContext{
			UserID:        job.UserID,
			WriteEnabled:  true,
			WorkspaceRoot: workspace.Root(),
		},
		RawMemories:       raw,
		CurrentMemory:     currentMemory,
		CurrentSummary:    currentSummary.Content,
		WorkspaceDiff:     diff,
		SelectedStage1IDs: stage1IDs(outputs),
	}
	spec := Stage2ThreadSpec{
		UserID:              job.UserID,
		OwnershipToken:      job.OwnershipToken,
		InputWatermark:      job.ClaimedInputWatermark,
		InputHash:           hash,
		StartedArtifactHash: startedHashes.artifactHash,
		StartedMemoryHash:   startedHashes.memoryHash,
		StartedSummaryHash:  startedHashes.summaryHash,
		WorkspaceRoot:       workspace.Root(),
		InitialPrompt:       BuildStage2ThreadPrompt(input),
	}
	metadata, err := BuildStage2Metadata(nil, spec)
	if err != nil {
		return PrepareStage2Result{}, err
	}
	spec.Metadata = metadata
	logs.CtxInfo(ctx, "[memory] stage2 prepare inputs done: user_id=%s output_count=%d hash=%s workspace=%s duration_ms=%d", job.UserID, len(outputs), hash, workspace.Root(), time.Since(start).Milliseconds())
	return PrepareStage2Result{SyncedHash: hash, OutputCount: len(outputs), Spec: spec}, nil
}

func (e *Engine) CompleteStage2Thread(ctx context.Context, req MarkStage2DoneRequest) error {
	if e.workspace == nil {
		return fmt.Errorf("memory stage2 complete: workspace is required")
	}
	return CompleteStage2Thread(ctx, e.store, e.workspace.ForUser(req.UserID), req)
}

func CompleteStage2Thread(ctx context.Context, store Store, workspace *Workspace, req MarkStage2DoneRequest) error {
	if store == nil {
		return fmt.Errorf("memory stage2 complete: store is required")
	}
	if workspace == nil {
		return fmt.Errorf("memory stage2 complete: workspace is required")
	}
	memoryContent, found, err := workspace.readOptional(ctx, memoryFile)
	if err != nil {
		return err
	}
	if !found || strings.TrimSpace(memoryContent) == "" {
		return fmt.Errorf("memory stage2 complete: MEMORY.md is empty")
	}
	summary, err := workspace.ReadSummary(ctx)
	if err != nil {
		return err
	}
	if !summary.Found || strings.TrimSpace(summary.Content) == "" {
		return fmt.Errorf("memory stage2 complete: memory_summary.md is empty")
	}
	if !validSummaryHeader(summary.Content) {
		return fmt.Errorf("memory stage2 complete: memory_summary.md must start with v1")
	}
	if strings.TrimSpace(req.StartedArtifactHash) == "" {
		return fmt.Errorf("memory stage2 complete: started artifact hash is required")
	}
	if strings.TrimSpace(req.StartedMemoryHash) == "" {
		return fmt.Errorf("memory stage2 complete: started MEMORY.md hash is required")
	}
	if strings.TrimSpace(req.StartedSummaryHash) == "" {
		return fmt.Errorf("memory stage2 complete: started memory_summary.md hash is required")
	}
	currentArtifactHash := memoryArtifactHash(memoryContent, found, summary.Content, summary.Found)
	if currentArtifactHash == strings.TrimSpace(req.StartedArtifactHash) {
		return fmt.Errorf("memory stage2 complete: MEMORY.md and memory_summary.md were not updated")
	}
	if fileArtifactHash(memoryContent, found) == strings.TrimSpace(req.StartedMemoryHash) {
		return fmt.Errorf("memory stage2 complete: MEMORY.md was not updated")
	}
	if fileArtifactHash(summary.Content, summary.Found) == strings.TrimSpace(req.StartedSummaryHash) {
		return fmt.Errorf("memory stage2 complete: memory_summary.md was not updated")
	}
	return store.MarkStage2Done(ctx, req)
}

func (e *Engine) newStage1Output(req RunStage1Request, result Stage1ExtractResult, status Stage1Status, errorSummary string) Stage1Output {
	id := stableStage1ID(req.Memory.UserID, req.SourceThreadID, req.SourceTurnID, e.now())
	return Stage1Output{
		ID:             id,
		UserID:         strings.TrimSpace(req.Memory.UserID),
		SourceThreadID: strings.TrimSpace(req.SourceThreadID),
		SourceTurnID:   strings.TrimSpace(req.SourceTurnID),
		RawMemory:      strings.TrimSpace(sanitizeMemoryArtifactText(result.RawMemory)),
		RolloutSummary: strings.TrimSpace(sanitizeMemoryArtifactText(result.RolloutSummary)),
		Status:         status,
		ErrorSummary:   errorSummary,
		GeneratedAt:    e.now(),
	}
}

func stableStage1ID(userID, threadID, turnID string, t time.Time) string {
	h := sha1.Sum([]byte(userID + "\x00" + threadID + "\x00" + turnID + "\x00" + t.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(h[:])[:16]
}

func selectableStage1Outputs(outputs []Stage1Output) []Stage1Output {
	selected := make([]Stage1Output, 0, len(outputs))
	for _, out := range outputs {
		if !selectableStage1Output(out) {
			continue
		}
		selected = append(selected, out)
	}
	return selected
}

func selectStage2Outputs(outputs []Stage1Output, limit int) []Stage1Output {
	selected := selectableStage1Outputs(outputs)
	sort.SliceStable(selected, func(i, j int) bool {
		return stage2WatermarkForOutput(selected[i]) < stage2WatermarkForOutput(selected[j])
	})
	if limit > 0 && len(selected) > limit {
		selected = selected[len(selected)-limit:]
	}
	return selected
}

func stage1IDs(outputs []Stage1Output) []string {
	ids := make([]string, 0, len(outputs))
	for _, out := range outputs {
		ids = append(ids, out.ID)
	}
	return ids
}

func validSummaryHeader(content string) bool {
	content = strings.TrimSpace(content)
	return content == "v1" || strings.HasPrefix(content, "v1\n")
}

func renderWorkspaceDiff(hash string, outputs []Stage1Output) string {
	var b strings.Builder
	b.WriteString("# Phase 2 Workspace Diff\n\n")
	b.WriteString(fmt.Sprintf("- synced_input_hash: `%s`\n", hash))
	b.WriteString("- selected_stage1_outputs:\n")
	for _, out := range outputs {
		b.WriteString(fmt.Sprintf("  - `%s` thread=`%s` turn=`%s`\n", out.ID, out.SourceThreadID, out.SourceTurnID))
	}
	return b.String()
}
