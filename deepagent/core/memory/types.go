package memory

import (
	"context"
	"time"
)

type UserMemoryContext struct {
	// UserID is the owner of the long-term memory namespace.
	UserID string
	// ReadEnabled controls whether runtime prompts may read the user's summary.
	ReadEnabled bool
	// WriteEnabled controls whether this source is eligible for Stage 1 learning.
	WriteEnabled bool
	// WorkspaceRoot is the user memory root visible to the consolidation agent.
	WorkspaceRoot string
	// BackendID names the sandbox/backend that stores the memory workspace.
	BackendID string
	// BusinessMetadata carries host-specific labels that are not interpreted by memory.
	BusinessMetadata map[string]string
}

type Stage1Status string

const (
	Stage1Succeeded         Stage1Status = "succeeded"
	Stage1SucceededNoOutput Stage1Status = "succeeded_no_output"
	Stage1Failed            Stage1Status = "failed"
	Stage1Skipped           Stage1Status = "skipped"
)

type Stage1Output struct {
	// ID is a stable per-extraction id used by Stage 2 and rollout summaries.
	ID string
	// UserID is the memory owner.
	UserID string
	// SourceThreadID is the conversation thread learned by Stage 1.
	SourceThreadID string
	// SourceTurnID is the source turn when the host can provide one.
	SourceTurnID string
	// RawMemory is detailed evidence extracted from the source history.
	RawMemory string
	// RolloutSummary is the compact routing summary for this Stage 1 output.
	RolloutSummary string
	// Status records whether extraction produced usable memory.
	Status Stage1Status
	// ErrorSummary stores the latest extraction failure or skip reason.
	ErrorSummary string
	// GeneratedAt is when the Stage 1 output was produced.
	GeneratedAt time.Time
	// SourceUpdatedAt is the source history watermark covered by this output.
	SourceUpdatedAt time.Time
	// UsageCount is reserved for later citation-driven ranking.
	UsageCount int
	// LastUsedAt is reserved for later citation-driven ranking.
	LastUsedAt time.Time
}

type SourceMode string

const (
	SourceModeEnabled  SourceMode = "enabled"
	SourceModeDisabled SourceMode = "disabled"
	SourceModePolluted SourceMode = "polluted"
)

type SourceStatus string

const (
	SourceStatusPending   SourceStatus = "pending"
	SourceStatusLeased    SourceStatus = "leased"
	SourceStatusSucceeded SourceStatus = "succeeded"
	SourceStatusFailed    SourceStatus = "failed"
	SourceStatusSkipped   SourceStatus = "skipped"
)

type SourceCandidate struct {
	// UserID is the memory owner.
	UserID string
	// SourceThreadID is the source thread observed by the host.
	SourceThreadID string
	// SourceUpdatedAt is the latest source history watermark seen by the host.
	SourceUpdatedAt time.Time
	// EligibleAt delays Stage 1 until the source has been idle long enough.
	EligibleAt time.Time
	// Mode lets the host disable or quarantine noisy sources before Stage 1.
	Mode SourceMode
	// Status records the Stage 1 lease/result state for this source.
	Status SourceStatus
	// LastStage1SuccessSourceUpdatedAt is the last source watermark successfully learned.
	LastStage1SuccessSourceUpdatedAt time.Time
	// LeaseUntil is the Stage 1 lease expiry.
	LeaseUntil time.Time
	// Owner identifies the worker currently holding the Stage 1 lease.
	Owner string
	// OwnershipToken authenticates completion/failure for the current lease.
	OwnershipToken string
	// Attempts counts Stage 1 claims for observability and retry policy.
	Attempts int
	// LastStage1OutputID points to the latest successful Stage 1 output.
	LastStage1OutputID string
	// ErrorSummary stores the latest Stage 1 failure.
	ErrorSummary string
	// UpdatedAt is the row update time.
	UpdatedAt time.Time
}

type TouchSourceRequest struct {
	Memory          UserMemoryContext
	SourceThreadID  string
	SourceUpdatedAt time.Time
	EligibleAt      time.Time
	Mode            SourceMode
}

type ClaimStage1Request struct {
	Now      time.Time
	Owner    string
	LeaseTTL time.Duration
	Limit    int
}

type ClaimedSource struct {
	SourceCandidate
	ClaimedSourceUpdatedAt time.Time
}

type CompleteStage1SourceRequest struct {
	UserID                   string
	SourceThreadID           string
	OwnershipToken           string
	ProcessedSourceUpdatedAt time.Time
	Stage1OutputID           string
	CompletedAt              time.Time
}

type FailStage1SourceRequest struct {
	UserID                   string
	SourceThreadID           string
	OwnershipToken           string
	ProcessedSourceUpdatedAt time.Time
	ErrorSummary             string
	FailedAt                 time.Time
}

type ListStage1Options struct {
	Limit int
}

type Baseline struct {
	// UserID is the memory owner.
	UserID string
	// Hash is the last Stage 2 input set successfully consolidated.
	Hash string
	// UpdatedAt is when the baseline was recorded.
	UpdatedAt time.Time
}

type Stage2Status string

const (
	Stage2Pending Stage2Status = "pending"
	Stage2Running Stage2Status = "running"
	Stage2Done    Stage2Status = "done"
	Stage2Error   Stage2Status = "error"
)

type Stage2Job struct {
	// UserID is the single-user consolidation key; there is at most one active job per user.
	UserID string
	// Status records whether the user-level consolidation job is pending, running, done, or failed.
	Status Stage2Status
	// InputWatermark is the latest Stage 1 watermark waiting for consolidation.
	InputWatermark string
	// LastSuccessWatermark is the latest watermark completed by Stage 2.
	LastSuccessWatermark string
	// LastSuccessAt drives the success cooldown gate.
	LastSuccessAt time.Time
	// Stage2ThreadID is the coordinator thread currently bound to the running job.
	Stage2ThreadID string
	// LeaseOwner identifies the worker that claimed this Stage 2 job.
	LeaseOwner string
	// OwnershipToken authenticates the running Stage 2 thread and completion callback.
	OwnershipToken string
	// LeaseUntil is extended by the consolidation thread heartbeat.
	LeaseUntil time.Time
	// RetryAt delays the next Stage 2 claim after a failure.
	RetryAt time.Time
	// LastError stores the latest Stage 2 failure.
	LastError string
	// UpdatedAt is the row update time.
	UpdatedAt time.Time
}

type EnqueueStage2Request struct {
	UserID         string
	InputWatermark string
	Now            time.Time
}

type ClaimStage2Request struct {
	Now             time.Time
	Owner           string
	LeaseTTL        time.Duration
	SuccessCooldown time.Duration
	Limit           int
}

type ClaimedStage2Job struct {
	Stage2Job
	ClaimedInputWatermark string
}

type BindStage2ThreadRequest struct {
	UserID         string
	OwnershipToken string
	ThreadID       string
	UpdatedAt      time.Time
}

type ValidateStage2ThreadRequest struct {
	UserID         string
	OwnershipToken string
	ThreadID       string
	ValidatedAt    time.Time
}

type MarkStage2DoneRequest struct {
	UserID                  string
	OwnershipToken          string
	CompletedInputWatermark string
	BaselineHash            string
	StartedArtifactHash     string
	StartedMemoryHash       string
	StartedSummaryHash      string
	CompletedAt             time.Time
}

type MarkStage2ErrorRequest struct {
	UserID         string
	OwnershipToken string
	ErrorSummary   string
	RetryAt        time.Time
	FailedAt       time.Time
}

type HeartbeatStage2Request struct {
	UserID         string
	OwnershipToken string
	LeaseTTL       time.Duration
	HeartbeatAt    time.Time
}

type Stage2ThreadSpec struct {
	// UserID is the memory owner for the consolidation thread.
	UserID string
	// OwnershipToken authenticates the Stage 2 job lease.
	OwnershipToken string
	// InputWatermark is the Stage 1 watermark this thread must complete.
	InputWatermark string
	// InputHash is the synced Stage 1 input hash used as the completion baseline.
	InputHash string
	// StartedArtifactHash captures workspace artifacts before the thread edits files.
	StartedArtifactHash string
	// StartedMemoryHash captures MEMORY.md before the thread edits files.
	StartedMemoryHash string
	// StartedSummaryHash captures memory_summary.md before the thread edits files.
	StartedSummaryHash string
	// WorkspaceRoot is the sandbox memory root assigned to the thread.
	WorkspaceRoot string
	// InitialPrompt is the one-turn Stage 2 instruction delivered to the thread.
	InitialPrompt string
	// Metadata is the host thread metadata; memory internals are encoded under MetadataKey.
	Metadata map[string]string
}

type Stage2CreateThreadRequest struct {
	Spec Stage2ThreadSpec
}

type Stage2CreatedThread struct {
	ThreadID string
}

type Stage2ThreadHost interface {
	CreateStage2Thread(ctx context.Context, req Stage2CreateThreadRequest) (Stage2CreatedThread, error)
	CloseStage2Thread(ctx context.Context, threadID string, reason string) error
}

type Summary struct {
	Content string
	Found   bool
}

type RunStage1Request struct {
	Memory         UserMemoryContext
	SourceThreadID string
	SourceTurnID   string
	RolloutPath    string
	RolloutCWD     string
}

type PrepareStage2Result struct {
	Noop        bool
	SyncedHash  string
	OutputCount int
	Spec        Stage2ThreadSpec
}

type Stage1ExtractInput struct {
	Memory          UserMemoryContext
	SourceThreadID  string
	SourceTurnID    string
	RolloutPath     string
	RolloutCWD      string
	RolloutContents string
}

type Stage1ExtractResult struct {
	RawMemory      string
	RolloutSummary string
	RolloutSlug    string
}

type ConsolidateInput struct {
	Memory            UserMemoryContext
	RawMemories       string
	CurrentMemory     string
	CurrentSummary    string
	WorkspaceDiff     string
	SelectedStage1IDs []string
}

type MemoryJobLoopConfig struct {
	Store                    Store
	Engine                   *Engine
	Stage2ThreadHost         Stage2ThreadHost
	Owner                    string
	ScanInterval             time.Duration
	WakeupDebounce           time.Duration
	Stage1LeaseTTL           time.Duration
	Stage1MaxClaimedPerScan  int
	Stage2LeaseTTL           time.Duration
	Stage2SuccessCooldown    time.Duration
	Stage2ScanInterval       time.Duration
	Stage2MaxUsersPerScan    int
	Stage2OutputLimitPerUser int
	Now                      func() time.Time
}

type MemoryJobLoopRunResult struct {
	Stage1Claimed       int
	Stage1Succeeded     int
	Stage1Failed        int
	Stage2Attempted     int
	Stage2Succeeded     int
	Stage2Skipped       int
	Stage2ThreadCreated int
	Stage2Failed        int
}
