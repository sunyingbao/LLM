package gormstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/memory"
	"eino-cli/deepagent/core/memory/gorm_store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DefaultSourceTable       = "t_memory_source"
	DefaultStage1OutputTable = "t_memory_stage1_output"
	DefaultStage2JobTable    = "t_memory_stage2_job"
	DefaultBaselineTable     = "t_memory_baseline"
)

type GormStoreTables struct {
	Source       string
	Stage1Output string
	Stage2Job    string
	Baseline     string
}

// Generator supplies database row ids for the memory GORM tables. SDK users
// should pass a process-safe and deployment-wide unique generator.
type Generator interface {
	NextID(context.Context) (int64, error)
}

type GormStoreConfig struct {
	Tables    GormStoreTables
	Generator Generator
}

type GormStore struct {
	db        *gorm.DB
	tables    GormStoreTables
	generator Generator
}

// NewGormStore binds memory state transitions to caller-provisioned tables. It
// never creates, migrates, or inspects schema; hosts own database rollout.
func NewGormStore(db *gorm.DB, cfg GormStoreConfig) *GormStore {
	tables := normalizeTables(cfg.Tables)
	return &GormStore{db: db, tables: tables, generator: cfg.Generator}
}

func normalizeTables(tables GormStoreTables) GormStoreTables {
	if strings.TrimSpace(tables.Source) == "" {
		tables.Source = DefaultSourceTable
	}
	if strings.TrimSpace(tables.Stage1Output) == "" {
		tables.Stage1Output = DefaultStage1OutputTable
	}
	if strings.TrimSpace(tables.Stage2Job) == "" {
		tables.Stage2Job = DefaultStage2JobTable
	}
	if strings.TrimSpace(tables.Baseline) == "" {
		tables.Baseline = DefaultBaselineTable
	}
	return tables
}

func (s *GormStore) TouchSource(ctx context.Context, req memory.TouchSourceRequest) error {
	if s == nil || s.db == nil {
		return errors.New("memory: gorm store not initialized")
	}
	rowID, err := s.nextID(ctx)
	if err != nil {
		return err
	}
	now := req.SourceUpdatedAt
	if now.IsZero() {
		now = time.Now()
	}
	row := map[string]any{
		"id":                rowID,
		"user_id":           strings.TrimSpace(req.Memory.UserID),
		"source_thread_id":  strings.TrimSpace(req.SourceThreadID),
		"source_updated_at": timePtr(now),
		"eligible_at":       timePtr(req.EligibleAt),
		"mode":              string(req.Mode),
		"status":            string(memory.SourceStatusPending),
		"updated_at":        timePtr(now),
	}
	err = s.db.WithContext(ctx).Table(s.tables.Source).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}, {Name: "source_thread_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"source_updated_at": keepLatestTimeExpr("source_updated_at", row["source_updated_at"]),
				"eligible_at":       keepLatestTouchValueExpr("source_updated_at", row["source_updated_at"], row["eligible_at"], "eligible_at"),
				"mode":              keepLatestTouchValueExpr("source_updated_at", row["source_updated_at"], row["mode"], "mode"),
				"updated_at":        keepLatestTimeExpr("updated_at", row["updated_at"]),
			}),
		}).
		Create(row).Error
	if err == nil {
		logs.CtxInfo(ctx, "[memory gorm] touch source: user_id=%s source_thread_id=%s eligible_at=%s mode=%s",
			row["user_id"], row["source_thread_id"], timeValue(row["eligible_at"].(*time.Time)).Format(time.RFC3339Nano), row["mode"])
	}
	return err
}

func (s *GormStore) ClaimStage1Sources(ctx context.Context, req memory.ClaimStage1Request) ([]memory.ClaimedSource, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("memory: gorm store not initialized")
	}
	if req.Limit <= 0 {
		return nil, nil
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	var claimed []memory.ClaimedSource
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []model.TMemorySource
		if err := tx.Table(s.tables.Source).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("mode = ?", memory.SourceModeEnabled).
			Where("eligible_at IS NOT NULL AND eligible_at <= ?", now).
			Where("(lease_until IS NULL OR lease_until <= ?)", now).
			Where("(last_stage1_success_source_updated_at IS NULL OR source_updated_at > last_stage1_success_source_updated_at)").
			Order("eligible_at ASC, updated_at ASC").
			Limit(req.Limit).
			Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			token, err := randomToken()
			if err != nil {
				return err
			}
			leaseUntil := now.Add(req.LeaseTTL)
			txr := tx.Table(s.tables.Source).
				Where("user_id = ? AND source_thread_id = ?", row.UserId, row.SourceThreadId).
				Updates(map[string]any{
					"status":          memory.SourceStatusLeased,
					"owner":           strings.TrimSpace(req.Owner),
					"ownership_token": token,
					"lease_until":     timePtr(leaseUntil),
					"attempts":        gorm.Expr("attempts + ?", 1),
					"updated_at":      timePtr(now),
				})
			if txr.Error != nil {
				return txr.Error
			}
			if txr.RowsAffected == 0 {
				continue
			}
			candidate := sourceCandidateFrom(row)
			candidate.Status = memory.SourceStatusLeased
			candidate.Owner = strings.TrimSpace(req.Owner)
			candidate.OwnershipToken = token
			candidate.LeaseUntil = leaseUntil
			candidate.Attempts++
			candidate.UpdatedAt = now
			claimed = append(claimed, memory.ClaimedSource{
				SourceCandidate:        candidate,
				ClaimedSourceUpdatedAt: candidate.SourceUpdatedAt,
			})
		}
		return nil
	})
	if err == nil && len(claimed) > 0 {
		logs.CtxInfo(ctx, "[memory gorm] claimed stage1 sources: owner=%s count=%d", strings.TrimSpace(req.Owner), len(claimed))
	}
	return claimed, err
}

func (s *GormStore) CompleteStage1Source(ctx context.Context, req memory.CompleteStage1SourceRequest) error {
	if s == nil || s.db == nil {
		return errors.New("memory: gorm store not initialized")
	}
	completedAt := req.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	tx := s.db.WithContext(ctx).Table(s.tables.Source).
		Where("user_id = ? AND source_thread_id = ? AND ownership_token = ?", strings.TrimSpace(req.UserID), strings.TrimSpace(req.SourceThreadID), strings.TrimSpace(req.OwnershipToken)).
		Updates(map[string]any{
			"status":                                memory.SourceStatusSucceeded,
			"last_stage1_success_source_updated_at": timePtr(req.ProcessedSourceUpdatedAt),
			"last_stage1_output_key":                strings.TrimSpace(req.Stage1OutputID),
			"error_summary":                         "",
			"owner":                                 "",
			"ownership_token":                       "",
			"lease_until":                           nil,
			"updated_at":                            timePtr(completedAt),
		})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return memory.ErrStage1SourceLeaseLost
	}
	logs.CtxInfo(ctx, "[memory gorm] completed stage1 source: user_id=%s source_thread_id=%s output_key=%s",
		strings.TrimSpace(req.UserID), strings.TrimSpace(req.SourceThreadID), strings.TrimSpace(req.Stage1OutputID))
	return nil
}

func (s *GormStore) FailStage1Source(ctx context.Context, req memory.FailStage1SourceRequest) error {
	if s == nil || s.db == nil {
		return errors.New("memory: gorm store not initialized")
	}
	failedAt := req.FailedAt
	if failedAt.IsZero() {
		failedAt = time.Now()
	}
	tx := s.db.WithContext(ctx).Table(s.tables.Source).
		Where("user_id = ? AND source_thread_id = ? AND ownership_token = ?", strings.TrimSpace(req.UserID), strings.TrimSpace(req.SourceThreadID), strings.TrimSpace(req.OwnershipToken)).
		Updates(map[string]any{
			"status":          memory.SourceStatusFailed,
			"error_summary":   strings.TrimSpace(req.ErrorSummary),
			"owner":           "",
			"ownership_token": "",
			"lease_until":     nil,
			"updated_at":      timePtr(failedAt),
		})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return memory.ErrStage1SourceLeaseLost
	}
	logs.CtxInfo(ctx, "[memory gorm] failed stage1 source: user_id=%s source_thread_id=%s error=%s",
		strings.TrimSpace(req.UserID), strings.TrimSpace(req.SourceThreadID), strings.TrimSpace(req.ErrorSummary))
	return nil
}

func (s *GormStore) SaveStage1Output(ctx context.Context, out memory.Stage1Output) error {
	if s == nil || s.db == nil {
		return errors.New("memory: gorm store not initialized")
	}
	if strings.TrimSpace(out.ID) == "" {
		return nil
	}
	values := stage1OutputValues(out)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rowID, err := s.nextID(ctx)
		if err != nil {
			return err
		}
		updateValues := stage1OutputValues(out)
		values["id"] = rowID
		if err := tx.Table(s.tables.Stage1Output).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "stage1_key"}},
				DoUpdates: clause.Assignments(updateValues),
			}).
			Create(values).Error; err != nil {
			return err
		}
		if !selectableStage1Output(out) {
			return nil
		}
		return s.enqueueStage2(ctx, tx, memory.EnqueueStage2Request{
			UserID:         out.UserID,
			InputWatermark: stage2WatermarkForOutput(out),
			Now:            out.GeneratedAt,
		})
	})
	if err == nil {
		logs.CtxInfo(ctx, "[memory gorm] saved stage1 output: user_id=%s source_thread_id=%s output_key=%s status=%s selectable=%t",
			strings.TrimSpace(out.UserID), strings.TrimSpace(out.SourceThreadID), strings.TrimSpace(out.ID), out.Status, selectableStage1Output(out))
	}
	return err
}

func (s *GormStore) ListStage1Outputs(ctx context.Context, userID string, opts memory.ListStage1Options) ([]memory.Stage1Output, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("memory: gorm store not initialized")
	}
	var rows []model.TMemoryStage1Output
	db := s.db.WithContext(ctx).Table(s.tables.Stage1Output).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Order("source_updated_at ASC, generated_at ASC, id ASC")
	if opts.Limit > 0 {
		db = db.Limit(opts.Limit)
	}
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]memory.Stage1Output, 0, len(rows))
	for _, row := range rows {
		out = append(out, stage1OutputFrom(row))
	}
	return out, nil
}

func (s *GormStore) EnqueueStage2(ctx context.Context, req memory.EnqueueStage2Request) error {
	if s == nil || s.db == nil {
		return errors.New("memory: gorm store not initialized")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.enqueueStage2(ctx, tx, req)
	})
}

func (s *GormStore) enqueueStage2(ctx context.Context, tx *gorm.DB, req memory.EnqueueStage2Request) error {
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		return nil
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	watermark := strings.TrimSpace(req.InputWatermark)
	if watermark == "" {
		watermark = now.UTC().Format(time.RFC3339Nano)
	}
	var row model.TMemoryStage2Job
	res := tx.WithContext(ctx).Table(s.tables.Stage2Job).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ?", userID).
		Limit(1).
		Find(&row)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		rowID, idErr := s.nextID(ctx)
		if idErr != nil {
			return idErr
		}
		return tx.WithContext(ctx).Table(s.tables.Stage2Job).Create(map[string]any{
			"id":              rowID,
			"user_id":         userID,
			"status":          string(memory.Stage2Pending),
			"input_watermark": watermark,
			"updated_at":      timePtr(now),
		}).Error
	}
	status := row.Status
	if status == "" || status == string(memory.Stage2Done) || status == string(memory.Stage2Error) {
		status = string(memory.Stage2Pending)
	}
	return tx.WithContext(ctx).Table(s.tables.Stage2Job).
		Where("user_id = ?", userID).
		Updates(map[string]any{
			"input_watermark": maxWatermark(row.InputWatermark, watermark),
			"status":          status,
			"updated_at":      timePtr(now),
		}).Error
}

func (s *GormStore) ClaimStage2Jobs(ctx context.Context, req memory.ClaimStage2Request) ([]memory.ClaimedStage2Job, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("memory: gorm store not initialized")
	}
	if req.Limit <= 0 {
		return nil, nil
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	var claimed []memory.ClaimedStage2Job
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		db := tx.Table(s.tables.Stage2Job).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id <> ''").
			Where("input_watermark <> ''").
			Where("(status <> ? OR lease_until IS NULL OR lease_until <= ?)", memory.Stage2Running, now).
			Where("(retry_at IS NULL OR retry_at <= ?)", now).
			Where("(status <> ? OR input_watermark <> last_success_watermark)", memory.Stage2Done)
		if req.SuccessCooldown > 0 {
			db = db.Where("(last_success_at IS NULL OR last_success_at <= ?)", now.Add(-req.SuccessCooldown))
		}
		var rows []model.TMemoryStage2Job
		if err := db.Order("updated_at ASC, user_id ASC").Limit(req.Limit).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			token, err := randomToken()
			if err != nil {
				return err
			}
			leaseUntil := now.Add(req.LeaseTTL)
			txr := tx.Table(s.tables.Stage2Job).
				Where("user_id = ?", row.UserId).
				Updates(map[string]any{
					"status":           memory.Stage2Running,
					"lease_owner":      strings.TrimSpace(req.Owner),
					"ownership_token":  token,
					"lease_until":      timePtr(leaseUntil),
					"stage2_thread_id": "",
					"last_error":       "",
					"updated_at":       timePtr(now),
				})
			if txr.Error != nil {
				return txr.Error
			}
			if txr.RowsAffected == 0 {
				continue
			}
			job := stage2JobFrom(row)
			job.Status = memory.Stage2Running
			job.LeaseOwner = strings.TrimSpace(req.Owner)
			job.OwnershipToken = token
			job.LeaseUntil = leaseUntil
			job.Stage2ThreadID = ""
			job.LastError = ""
			job.UpdatedAt = now
			claimed = append(claimed, memory.ClaimedStage2Job{
				Stage2Job:             job,
				ClaimedInputWatermark: job.InputWatermark,
			})
		}
		return nil
	})
	if err == nil && len(claimed) > 0 {
		logs.CtxInfo(ctx, "[memory gorm] claimed stage2 jobs: owner=%s count=%d", strings.TrimSpace(req.Owner), len(claimed))
	}
	return claimed, err
}

func (s *GormStore) BindStage2Thread(ctx context.Context, req memory.BindStage2ThreadRequest) error {
	return s.bindOrValidateStage2Thread(ctx, req.UserID, req.OwnershipToken, req.ThreadID, req.UpdatedAt)
}

func (s *GormStore) ValidateStage2Thread(ctx context.Context, req memory.ValidateStage2ThreadRequest) error {
	return s.bindOrValidateStage2Thread(ctx, req.UserID, req.OwnershipToken, req.ThreadID, req.ValidatedAt)
}

func (s *GormStore) bindOrValidateStage2Thread(ctx context.Context, userID, token, threadID string, at time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("memory: gorm store not initialized")
	}
	if at.IsZero() {
		at = time.Now()
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return memory.ErrStage2JobLeaseLost
	}
	tx := s.db.WithContext(ctx).Table(s.tables.Stage2Job).
		Where("user_id = ? AND status = ? AND ownership_token = ?", strings.TrimSpace(userID), memory.Stage2Running, strings.TrimSpace(token)).
		Where("(lease_until IS NULL OR lease_until >= ?)", at).
		Where("(stage2_thread_id = '' OR stage2_thread_id IS NULL OR stage2_thread_id = ?)", threadID).
		Updates(map[string]any{
			"stage2_thread_id": threadID,
			"updated_at":       timePtr(at),
		})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return memory.ErrStage2JobLeaseLost
	}
	return nil
}

func (s *GormStore) MarkStage2Done(ctx context.Context, req memory.MarkStage2DoneRequest) error {
	if s == nil || s.db == nil {
		return errors.New("memory: gorm store not initialized")
	}
	completedAt := req.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.TMemoryStage2Job
		if err := tx.Table(s.tables.Stage2Job).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ?", strings.TrimSpace(req.UserID)).
			Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return memory.ErrStage2JobLeaseLost
			}
			return err
		}
		job := stage2JobFrom(row)
		if !stage2OwnerValid(job, true, req.OwnershipToken, completedAt) {
			return memory.ErrStage2JobLeaseLost
		}
		watermark := strings.TrimSpace(req.CompletedInputWatermark)
		if watermark == "" {
			watermark = job.InputWatermark
		}
		if err := tx.Table(s.tables.Stage2Job).
			Where("user_id = ?", job.UserID).
			Updates(map[string]any{
				"status":                 memory.Stage2Done,
				"last_success_watermark": watermark,
				"last_success_at":        timePtr(completedAt),
				"lease_owner":            "",
				"ownership_token":        "",
				"lease_until":            nil,
				"stage2_thread_id":       "",
				"retry_at":               nil,
				"last_error":             "",
				"updated_at":             timePtr(completedAt),
			}).Error; err != nil {
			return err
		}
		if hash := strings.TrimSpace(req.BaselineHash); hash != "" {
			rowID, err := s.nextID(ctx)
			if err != nil {
				return err
			}
			return tx.Table(s.tables.Baseline).
				Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "user_id"}},
					DoUpdates: clause.Assignments(map[string]any{
						"hash":       hash,
						"updated_at": timePtr(completedAt),
					}),
				}).
				Create(map[string]any{
					"id":         rowID,
					"user_id":    job.UserID,
					"hash":       hash,
					"updated_at": timePtr(completedAt),
				}).Error
		}
		return nil
	})
}

func (s *GormStore) MarkStage2Error(ctx context.Context, req memory.MarkStage2ErrorRequest) error {
	if s == nil || s.db == nil {
		return errors.New("memory: gorm store not initialized")
	}
	failedAt := req.FailedAt
	if failedAt.IsZero() {
		failedAt = time.Now()
	}
	tx := s.db.WithContext(ctx).Table(s.tables.Stage2Job).
		Where("user_id = ? AND status = ? AND ownership_token = ?", strings.TrimSpace(req.UserID), memory.Stage2Running, strings.TrimSpace(req.OwnershipToken)).
		Where("(lease_until IS NULL OR lease_until >= ?)", failedAt).
		Updates(map[string]any{
			"status":           memory.Stage2Error,
			"lease_owner":      "",
			"ownership_token":  "",
			"lease_until":      nil,
			"stage2_thread_id": "",
			"retry_at":         timePtr(req.RetryAt),
			"last_error":       strings.TrimSpace(req.ErrorSummary),
			"updated_at":       timePtr(failedAt),
		})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return memory.ErrStage2JobLeaseLost
	}
	logs.CtxInfo(ctx, "[memory gorm] failed stage2 job: user_id=%s retry_at=%s error=%s",
		strings.TrimSpace(req.UserID), req.RetryAt.Format(time.RFC3339Nano), strings.TrimSpace(req.ErrorSummary))
	return nil
}

func (s *GormStore) HeartbeatStage2(ctx context.Context, req memory.HeartbeatStage2Request) error {
	if s == nil || s.db == nil {
		return errors.New("memory: gorm store not initialized")
	}
	heartbeatAt := req.HeartbeatAt
	if heartbeatAt.IsZero() {
		heartbeatAt = time.Now()
	}
	if req.LeaseTTL <= 0 {
		return memory.ErrStage2JobLeaseLost
	}
	tx := s.db.WithContext(ctx).Table(s.tables.Stage2Job).
		Where("user_id = ? AND status = ? AND ownership_token = ?", strings.TrimSpace(req.UserID), memory.Stage2Running, strings.TrimSpace(req.OwnershipToken)).
		Where("(lease_until IS NULL OR lease_until >= ?)", heartbeatAt).
		Updates(map[string]any{
			"lease_until": timePtr(heartbeatAt.Add(req.LeaseTTL)),
			"updated_at":  timePtr(heartbeatAt),
		})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return memory.ErrStage2JobLeaseLost
	}
	return nil
}

func (s *GormStore) LoadBaseline(ctx context.Context, userID string) (memory.Baseline, bool, error) {
	if s == nil || s.db == nil {
		return memory.Baseline{}, false, errors.New("memory: gorm store not initialized")
	}
	var row model.TMemoryBaseline
	err := s.db.WithContext(ctx).Table(s.tables.Baseline).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return memory.Baseline{}, false, nil
	}
	if err != nil {
		return memory.Baseline{}, false, err
	}
	return memory.Baseline{UserID: row.UserId, Hash: row.Hash, UpdatedAt: timeValue(row.UpdatedAt)}, true, nil
}

func sourceCandidateFrom(r model.TMemorySource) memory.SourceCandidate {
	return memory.SourceCandidate{
		UserID:                           r.UserId,
		SourceThreadID:                   r.SourceThreadId,
		SourceUpdatedAt:                  timeValue(r.SourceUpdatedAt),
		EligibleAt:                       timeValue(r.EligibleAt),
		Mode:                             memory.SourceMode(r.Mode),
		Status:                           memory.SourceStatus(r.Status),
		LastStage1SuccessSourceUpdatedAt: timeValue(r.LastStage1SuccessSourceUpdatedAt),
		LeaseUntil:                       timeValue(r.LeaseUntil),
		Owner:                            r.Owner,
		OwnershipToken:                   r.OwnershipToken,
		Attempts:                         r.Attempts,
		LastStage1OutputID:               r.LastStage1OutputKey,
		ErrorSummary:                     r.ErrorSummary,
		UpdatedAt:                        timeValue(r.UpdatedAt),
	}
}

func stage1OutputValues(out memory.Stage1Output) map[string]any {
	return map[string]any{
		"stage1_key":        strings.TrimSpace(out.ID),
		"user_id":           strings.TrimSpace(out.UserID),
		"source_thread_id":  strings.TrimSpace(out.SourceThreadID),
		"source_turn_id":    strings.TrimSpace(out.SourceTurnID),
		"raw_memory":        out.RawMemory,
		"rollout_summary":   out.RolloutSummary,
		"status":            string(out.Status),
		"error_summary":     out.ErrorSummary,
		"generated_at":      timePtr(out.GeneratedAt),
		"source_updated_at": timePtr(out.SourceUpdatedAt),
		"usage_count":       out.UsageCount,
		"last_used_at":      timePtr(out.LastUsedAt),
	}
}

func stage1OutputFrom(r model.TMemoryStage1Output) memory.Stage1Output {
	return memory.Stage1Output{
		ID:              r.Stage1Key,
		UserID:          r.UserId,
		SourceThreadID:  r.SourceThreadId,
		SourceTurnID:    r.SourceTurnID,
		RawMemory:       r.RawMemory,
		RolloutSummary:  r.RolloutSummary,
		Status:          memory.Stage1Status(r.Status),
		ErrorSummary:    r.ErrorSummary,
		GeneratedAt:     timeValue(r.GeneratedAt),
		SourceUpdatedAt: timeValue(r.SourceUpdatedAt),
		UsageCount:      r.UsageCount,
		LastUsedAt:      timeValue(r.LastUsedAt),
	}
}

func (s *GormStore) nextID(ctx context.Context) (int64, error) {
	if s == nil || s.generator == nil {
		return 0, errors.New("memory: gorm store id generator is required")
	}
	id, err := s.generator.NextID(ctx)
	if err != nil {
		return 0, fmt.Errorf("memory: generate gorm row id: %w", err)
	}
	if id <= 0 {
		return 0, fmt.Errorf("memory: generated gorm row id must be positive, got %d", id)
	}
	return id, nil
}

func stage2JobFrom(r model.TMemoryStage2Job) memory.Stage2Job {
	return memory.Stage2Job{
		UserID:               r.UserId,
		Status:               memory.Stage2Status(r.Status),
		InputWatermark:       r.InputWatermark,
		LastSuccessWatermark: r.LastSuccessWatermark,
		LastSuccessAt:        timeValue(r.LastSuccessAt),
		Stage2ThreadID:       r.Stage2ThreadId,
		LeaseOwner:           r.LeaseOwner,
		OwnershipToken:       r.OwnershipToken,
		LeaseUntil:           timeValue(r.LeaseUntil),
		RetryAt:              timeValue(r.RetryAt),
		LastError:            r.LastError,
		UpdatedAt:            timeValue(r.UpdatedAt),
	}
}

func stage2OwnerValid(job memory.Stage2Job, ok bool, token string, at time.Time) bool {
	if !ok || job.Status != memory.Stage2Running || job.OwnershipToken != strings.TrimSpace(token) {
		return false
	}
	if at.IsZero() {
		at = time.Now()
	}
	return job.LeaseUntil.IsZero() || !job.LeaseUntil.Before(at)
}

func selectableStage1Output(out memory.Stage1Output) bool {
	return out.Status == memory.Stage1Succeeded && strings.TrimSpace(out.RawMemory) != ""
}

func stage2WatermarkForOutput(out memory.Stage1Output) string {
	parts := []string{
		strings.TrimSpace(out.SourceUpdatedAt.UTC().Format(time.RFC3339Nano)),
		strings.TrimSpace(out.ID),
	}
	return strings.Join(parts, ":")
}

func maxWatermark(a, b string) string {
	if strings.TrimSpace(a) == "" || b > a {
		return b
	}
	return a
}

func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeValue(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func keepLatestTimeExpr(column string, incoming any) clause.Expr {
	return gorm.Expr("CASE WHEN "+column+" IS NULL OR "+column+" < ? THEN ? ELSE "+column+" END", incoming, incoming)
}

func keepLatestTouchValueExpr(sourceColumn string, incomingSource any, incomingValue any, currentColumn string) clause.Expr {
	return gorm.Expr("CASE WHEN "+sourceColumn+" IS NULL OR "+sourceColumn+" < ? THEN ? ELSE "+currentColumn+" END", incomingSource, incomingValue)
}
