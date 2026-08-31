package agentthread

import "context"

type TurnRunnerConfigTrigger string

const (
	TurnRunnerConfigForSubmit TurnRunnerConfigTrigger = "submit"
	TurnRunnerConfigForResume TurnRunnerConfigTrigger = "resume"
)

// TurnRunnerConfigRequest describes the turn run that is about to start.
type TurnRunnerConfigRequest struct {
	ThreadID  string
	TurnID    string
	Trigger   TurnRunnerConfigTrigger
	Input     *Message
	InputMeta any
	Resume    *ResumeTurnConfigRequest
	Base      *TurnRunnerConfig
}

type ResumeTurnConfigRequest struct {
	CheckpointID        string
	WriteToCheckpointID string
	ForceNewRun         bool
	ResumeInterruptIDs  []string
	ResumeData          map[string]any
	ConfigMeta          any
}

type TurnRunnerConfigResolver func(ctx context.Context, req TurnRunnerConfigRequest) (*TurnRunnerConfig, error)
