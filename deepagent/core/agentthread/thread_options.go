package agentthread

import "context"

// ThreadOptions contains the context and persistence dependencies used to
// create a DeepAgentThread.
type ThreadOptions struct {
	// ContextManager replaces the built-in memory context manager when non-nil.
	ContextManager     ContextManager
	HistoryStore       HistoryRolloutStore
	CompactionStrategy CompactionStrategy
	TokenCounter       TokenCounter
	ContextWindow      int64
}

// ResumeTurnOptions configures one checkpoint/interruption resume turn.
type ResumeTurnOptions struct {
	CheckpointID        string
	WriteToCheckpointID string
	ForceNewRun         bool
	ResumeInterruptIDs  []string
	ResumeData          map[string]any
	ConfigProvider      TurnConfigProvider
	OnTurnStart         OnTurnStartFunc
}

// SubmitInputResult describes how one user input was accepted by the thread.
type SubmitInputResult struct {
	TurnID     string
	TurnHandle *TurnHandle
	Started    bool
}

type submitInputOptions struct {
	ConfigProvider TurnConfigProvider
	InputMeta      any
	OnTurnStart    OnTurnStartFunc
}

type SubmitInputOption func(*submitInputOptions)

func WithTurnConfigProvider(provider TurnConfigProvider) (option SubmitInputOption) {
	option = func(opts *submitInputOptions) {
		opts.ConfigProvider = provider
	}
	return option
}

func WithInputMeta(meta any) (option SubmitInputOption) {
	option = func(opts *submitInputOptions) {
		opts.InputMeta = meta
	}
	return option
}

type TurnIDProvider func(ctx context.Context, threadID string, input *Message) string

type Option func(*DeepAgentThread)

func WithTurnIDProvider(provider TurnIDProvider) (option Option) {
	option = func(t *DeepAgentThread) {
		if provider != nil {
			t.turnIDProvider = provider
		}
	}
	return option
}
