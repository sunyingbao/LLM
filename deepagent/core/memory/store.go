package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrStage1SourceLeaseLost = errors.New("memory: stage1 source lease lost")
var ErrStage2JobLeaseLost = errors.New("memory: stage2 job lease lost")

type Store interface {
	TouchSource(ctx context.Context, req TouchSourceRequest) error
	ClaimStage1Sources(ctx context.Context, req ClaimStage1Request) ([]ClaimedSource, error)
	CompleteStage1Source(ctx context.Context, req CompleteStage1SourceRequest) error
	FailStage1Source(ctx context.Context, req FailStage1SourceRequest) error

	SaveStage1Output(ctx context.Context, out Stage1Output) error
	ListStage1Outputs(ctx context.Context, userID string, opts ListStage1Options) ([]Stage1Output, error)

	EnqueueStage2(ctx context.Context, req EnqueueStage2Request) error
	ClaimStage2Jobs(ctx context.Context, req ClaimStage2Request) ([]ClaimedStage2Job, error)
	BindStage2Thread(ctx context.Context, req BindStage2ThreadRequest) error
	ValidateStage2Thread(ctx context.Context, req ValidateStage2ThreadRequest) error
	MarkStage2Done(ctx context.Context, req MarkStage2DoneRequest) error
	MarkStage2Error(ctx context.Context, req MarkStage2ErrorRequest) error
	HeartbeatStage2(ctx context.Context, req HeartbeatStage2Request) error

	LoadBaseline(ctx context.Context, userID string) (Baseline, bool, error)
}

type InMemoryStore struct {
	mu        sync.Mutex
	sources   map[string]SourceCandidate
	outputs   map[string][]Stage1Output
	stage2    map[string]Stage2Job
	baselines map[string]Baseline
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		sources:   map[string]SourceCandidate{},
		outputs:   map[string][]Stage1Output{},
		stage2:    map[string]Stage2Job{},
		baselines: map[string]Baseline{},
	}
}

func (s *InMemoryStore) TouchSource(_ context.Context, req TouchSourceRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := sourceKey(req.Memory.UserID, req.SourceThreadID)
	candidate := s.sources[key]
	candidate.UserID = strings.TrimSpace(req.Memory.UserID)
	candidate.SourceThreadID = strings.TrimSpace(req.SourceThreadID)
	candidate.SourceUpdatedAt = req.SourceUpdatedAt
	candidate.EligibleAt = req.EligibleAt
	candidate.Mode = req.Mode
	if candidate.Status == "" {
		candidate.Status = SourceStatusPending
	}
	candidate.UpdatedAt = req.SourceUpdatedAt
	s.sources[key] = candidate
	return nil
}

func (s *InMemoryStore) ClaimStage1Sources(_ context.Context, req ClaimStage1Request) ([]ClaimedSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Limit <= 0 {
		return nil, nil
	}
	claimed := make([]ClaimedSource, 0, req.Limit)
	keys := make([]string, 0, len(s.sources))
	for key := range s.sources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		candidate := s.sources[key]
		if !sourceClaimable(candidate, req.Now) {
			continue
		}
		token, err := randomToken()
		if err != nil {
			return nil, err
		}
		candidate.Status = SourceStatusLeased
		candidate.Owner = req.Owner
		candidate.OwnershipToken = token
		candidate.LeaseUntil = req.Now.Add(req.LeaseTTL)
		candidate.Attempts++
		candidate.UpdatedAt = req.Now
		s.sources[key] = candidate
		claimed = append(claimed, ClaimedSource{
			SourceCandidate:        candidate,
			ClaimedSourceUpdatedAt: candidate.SourceUpdatedAt,
		})
		if len(claimed) >= req.Limit {
			break
		}
	}
	return claimed, nil
}

func (s *InMemoryStore) CompleteStage1Source(_ context.Context, req CompleteStage1SourceRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := sourceKey(req.UserID, req.SourceThreadID)
	candidate, ok := s.sources[key]
	if !ok || candidate.OwnershipToken != req.OwnershipToken {
		return ErrStage1SourceLeaseLost
	}
	candidate.Status = SourceStatusSucceeded
	candidate.LastStage1SuccessSourceUpdatedAt = req.ProcessedSourceUpdatedAt
	candidate.LastStage1OutputID = req.Stage1OutputID
	candidate.ErrorSummary = ""
	candidate.Owner = ""
	candidate.OwnershipToken = ""
	candidate.LeaseUntil = time.Time{}
	candidate.UpdatedAt = req.CompletedAt
	s.sources[key] = candidate
	return nil
}

func (s *InMemoryStore) FailStage1Source(_ context.Context, req FailStage1SourceRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := sourceKey(req.UserID, req.SourceThreadID)
	candidate, ok := s.sources[key]
	if !ok || candidate.OwnershipToken != req.OwnershipToken {
		return ErrStage1SourceLeaseLost
	}
	candidate.Status = SourceStatusFailed
	candidate.ErrorSummary = req.ErrorSummary
	candidate.Owner = ""
	candidate.OwnershipToken = ""
	candidate.LeaseUntil = time.Time{}
	candidate.UpdatedAt = req.FailedAt
	s.sources[key] = candidate
	return nil
}

func (s *InMemoryStore) SaveStage1Output(_ context.Context, out Stage1Output) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outputs[out.UserID] = append(s.outputs[out.UserID], out)
	if selectableStage1Output(out) {
		s.enqueueStage2Locked(EnqueueStage2Request{
			UserID:         out.UserID,
			InputWatermark: stage2WatermarkForOutput(out),
			Now:            out.GeneratedAt,
		})
	}
	return nil
}

func (s *InMemoryStore) ListStage1Outputs(_ context.Context, userID string, opts ListStage1Options) ([]Stage1Output, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.outputs[userID]
	if opts.Limit > 0 && len(src) > opts.Limit {
		src = src[:opts.Limit]
	}
	out := make([]Stage1Output, len(src))
	copy(out, src)
	return out, nil
}

func (s *InMemoryStore) EnqueueStage2(_ context.Context, req EnqueueStage2Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueueStage2Locked(req)
	return nil
}

func (s *InMemoryStore) enqueueStage2Locked(req EnqueueStage2Request) {
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		return
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	watermark := strings.TrimSpace(req.InputWatermark)
	if watermark == "" {
		watermark = now.UTC().Format(time.RFC3339Nano)
	}
	job := s.stage2[userID]
	job.UserID = userID
	job.InputWatermark = maxWatermark(job.InputWatermark, watermark)
	if job.Status == "" || job.Status == Stage2Done || job.Status == Stage2Error {
		job.Status = Stage2Pending
	}
	job.UpdatedAt = now
	s.stage2[userID] = job
}

func (s *InMemoryStore) ClaimStage2Jobs(_ context.Context, req ClaimStage2Request) ([]ClaimedStage2Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Limit <= 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(s.stage2))
	for key := range s.stage2 {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	claimed := make([]ClaimedStage2Job, 0, req.Limit)
	for _, key := range keys {
		job := s.stage2[key]
		if !stage2Claimable(job, req.Now, req.SuccessCooldown) {
			continue
		}
		token, err := randomToken()
		if err != nil {
			return nil, err
		}
		job.Status = Stage2Running
		job.LeaseOwner = req.Owner
		job.OwnershipToken = token
		job.LeaseUntil = req.Now.Add(req.LeaseTTL)
		job.Stage2ThreadID = ""
		job.LastError = ""
		job.UpdatedAt = req.Now
		s.stage2[key] = job
		claimed = append(claimed, ClaimedStage2Job{
			Stage2Job:             job,
			ClaimedInputWatermark: job.InputWatermark,
		})
		if len(claimed) >= req.Limit {
			break
		}
	}
	return claimed, nil
}

func (s *InMemoryStore) BindStage2Thread(_ context.Context, req BindStage2ThreadRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.stage2[strings.TrimSpace(req.UserID)]
	if !stage2OwnerValid(job, ok, req.OwnershipToken, req.UpdatedAt) {
		return ErrStage2JobLeaseLost
	}
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		return ErrStage2JobLeaseLost
	}
	if job.Stage2ThreadID != "" && job.Stage2ThreadID != threadID {
		return ErrStage2JobLeaseLost
	}
	job.Stage2ThreadID = threadID
	job.UpdatedAt = req.UpdatedAt
	s.stage2[job.UserID] = job
	return nil
}

func (s *InMemoryStore) ValidateStage2Thread(_ context.Context, req ValidateStage2ThreadRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := req.ValidatedAt
	if at.IsZero() {
		at = time.Now()
	}
	job, ok := s.stage2[strings.TrimSpace(req.UserID)]
	if !stage2OwnerValid(job, ok, req.OwnershipToken, at) {
		return ErrStage2JobLeaseLost
	}
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		return ErrStage2JobLeaseLost
	}
	if job.Stage2ThreadID == "" {
		job.Stage2ThreadID = threadID
		job.UpdatedAt = at
		s.stage2[job.UserID] = job
		return nil
	}
	if job.Stage2ThreadID != threadID {
		return ErrStage2JobLeaseLost
	}
	return nil
}

func (s *InMemoryStore) MarkStage2Done(_ context.Context, req MarkStage2DoneRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.stage2[strings.TrimSpace(req.UserID)]
	completedAt := req.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	if !stage2OwnerValid(job, ok, req.OwnershipToken, completedAt) {
		return ErrStage2JobLeaseLost
	}
	job.Status = Stage2Done
	job.LastSuccessWatermark = strings.TrimSpace(req.CompletedInputWatermark)
	if job.LastSuccessWatermark == "" {
		job.LastSuccessWatermark = job.InputWatermark
	}
	job.LastSuccessAt = completedAt
	job.LeaseOwner = ""
	job.OwnershipToken = ""
	job.LeaseUntil = time.Time{}
	job.Stage2ThreadID = ""
	job.RetryAt = time.Time{}
	job.LastError = ""
	job.UpdatedAt = completedAt
	s.stage2[job.UserID] = job
	if hash := strings.TrimSpace(req.BaselineHash); hash != "" {
		s.baselines[job.UserID] = Baseline{UserID: job.UserID, Hash: hash, UpdatedAt: completedAt}
	}
	return nil
}

func (s *InMemoryStore) MarkStage2Error(_ context.Context, req MarkStage2ErrorRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.stage2[strings.TrimSpace(req.UserID)]
	failedAt := req.FailedAt
	if failedAt.IsZero() {
		failedAt = time.Now()
	}
	if !stage2OwnerValid(job, ok, req.OwnershipToken, failedAt) {
		return ErrStage2JobLeaseLost
	}
	job.Status = Stage2Error
	job.LeaseOwner = ""
	job.OwnershipToken = ""
	job.LeaseUntil = time.Time{}
	job.Stage2ThreadID = ""
	job.RetryAt = req.RetryAt
	job.LastError = strings.TrimSpace(req.ErrorSummary)
	job.UpdatedAt = failedAt
	s.stage2[job.UserID] = job
	return nil
}

func (s *InMemoryStore) HeartbeatStage2(_ context.Context, req HeartbeatStage2Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	heartbeatAt := req.HeartbeatAt
	if heartbeatAt.IsZero() {
		heartbeatAt = time.Now()
	}
	if req.LeaseTTL <= 0 {
		return ErrStage2JobLeaseLost
	}
	job, ok := s.stage2[strings.TrimSpace(req.UserID)]
	if !stage2OwnerValid(job, ok, req.OwnershipToken, heartbeatAt) {
		return ErrStage2JobLeaseLost
	}
	job.LeaseUntil = heartbeatAt.Add(req.LeaseTTL)
	job.UpdatedAt = heartbeatAt
	s.stage2[job.UserID] = job
	return nil
}

func (s *InMemoryStore) LoadBaseline(_ context.Context, userID string) (Baseline, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.baselines[userID]
	return b, ok, nil
}

func sourceClaimable(candidate SourceCandidate, now time.Time) bool {
	if candidate.Mode != SourceModeEnabled {
		return false
	}
	if candidate.EligibleAt.After(now) {
		return false
	}
	if !candidate.LeaseUntil.IsZero() && candidate.LeaseUntil.After(now) {
		return false
	}
	if !candidate.LastStage1SuccessSourceUpdatedAt.IsZero() &&
		!candidate.SourceUpdatedAt.After(candidate.LastStage1SuccessSourceUpdatedAt) {
		return false
	}
	return true
}

func stage2Claimable(job Stage2Job, now time.Time, successCooldown time.Duration) bool {
	if strings.TrimSpace(job.UserID) == "" {
		return false
	}
	if job.Status == Stage2Running && job.LeaseUntil.After(now) {
		return false
	}
	if !job.RetryAt.IsZero() && job.RetryAt.After(now) {
		return false
	}
	if job.Status == Stage2Done && job.InputWatermark == job.LastSuccessWatermark {
		return false
	}
	if successCooldown > 0 && !job.LastSuccessAt.IsZero() && job.LastSuccessAt.Add(successCooldown).After(now) {
		return false
	}
	return strings.TrimSpace(job.InputWatermark) != ""
}

func stage2OwnerValid(job Stage2Job, ok bool, token string, at time.Time) bool {
	if !ok || job.Status != Stage2Running || job.OwnershipToken != strings.TrimSpace(token) {
		return false
	}
	if at.IsZero() {
		at = time.Now()
	}
	return job.LeaseUntil.IsZero() || !job.LeaseUntil.Before(at)
}

func selectableStage1Output(out Stage1Output) bool {
	return out.Status == Stage1Succeeded && strings.TrimSpace(out.RawMemory) != ""
}

func stage2WatermarkForOutput(out Stage1Output) string {
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

func sourceKey(userID, sourceThreadID string) string {
	return strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(sourceThreadID)
}

func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
