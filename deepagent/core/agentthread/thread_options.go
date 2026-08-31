package agentthread

import "context"

// DefaultThreadOptions contains the context and persistence dependencies used
// by the SDK's default AgentThread assembly path.
type DefaultThreadOptions struct {
	HistoryStore       HistoryRolloutStore
	CompactionStrategy CompactionStrategy
	TokenCounter       TokenCounter
	ContextWindow      int64
}

// ResumeTurnOptions configures one checkpoint/interruption resume run.
type ResumeTurnOptions struct {
	CheckpointID        string
	WriteToCheckpointID string
	ForceNewRun         bool
	ResumeInterruptIDs  []string
	ResumeData          map[string]any
	// RunnerConfig directly supplies the config for this resume run. Prefer
	// TurnRunnerConfig for dynamic per-resume resolution; this field is kept for
	// callers that already materialize the full runner config.
	RunnerConfig *TurnRunnerConfig
	// TurnRunnerConfig can override the thread-level base runner config when
	// this resume starts a run.
	TurnRunnerConfig  TurnRunnerConfigResolver
	ConfigMeta        any
	OnTurnRunnerStart OnTurnRunnerStartFunc
}

// SubmitInputResult describes how one user input was accepted by the thread.
type SubmitInputResult struct {
	TurnID             string
	TurnHandle         *TurnHandle
	StartNewTurn       bool
	QueuedToActiveTurn bool
	RunnerConfig       *TurnRunnerConfig
}

type SubmitInputOptions struct {
	TurnRunnerConfig  TurnRunnerConfigResolver
	InputMeta         any
	OnAccepted        func(SubmitInputResult)
	OnTurnRunnerStart OnTurnRunnerStartFunc
}

type SubmitInputOption func(*SubmitInputOptions)

func WithSubmitInputAcceptedHook(f func(SubmitInputResult)) SubmitInputOption {
	return func(opts *SubmitInputOptions) {
		opts.OnAccepted = f
	}
}

func WithTurnRunnerConfig(cfg *TurnRunnerConfig) SubmitInputOption {
	cloned := cfg.Clone()
	return WithTurnRunnerConfigResolver(func(context.Context, TurnRunnerConfigRequest) (*TurnRunnerConfig, error) {
		return cloned.Clone(), nil
	})
}

func WithTurnRunnerConfigResolver(resolver TurnRunnerConfigResolver) SubmitInputOption {
	return func(opts *SubmitInputOptions) {
		opts.TurnRunnerConfig = resolver
	}
}

func WithInputMeta(meta any) SubmitInputOption {
	return func(opts *SubmitInputOptions) {
		opts.InputMeta = meta
	}
}

type TurnIDProvider func(ctx context.Context, threadID string, input *Message) string

type Option func(*DeepAgentThread)

func WithTurnIDProvider(provider TurnIDProvider) Option {
	return func(t *DeepAgentThread) {
		if provider != nil {
			t.turnIDProvider = provider
		}
	}
}

func WithBaseTurnRunnerConfig(cfg *TurnRunnerConfig) Option {
	return func(t *DeepAgentThread) {
		if cfg != nil {
			t.cfg = cfg.Clone()
		}
	}
}
