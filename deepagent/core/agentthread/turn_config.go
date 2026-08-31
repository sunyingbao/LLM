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

// turnStartRequest is the normalized internal request used by SubmitInput and
// ResumeTurn before a turn is created. Public option types are converted to
// this shape at the boundary so the lifecycle code has one path to follow.
type turnStartRequest struct {
	turnID         string
	input          *Message
	inputMeta      any
	resume         *ResumeTurnOptions
	trigger        TurnRunnerConfigTrigger
	explicitConfig *TurnRunnerConfig
	configResolver TurnRunnerConfigResolver
	onRunnerStart  OnTurnRunnerStartFunc
}

func (request turnStartRequest) configRequest() TurnRunnerConfigRequest {
	return TurnRunnerConfigRequest{
		TurnID:    request.turnID,
		Trigger:   request.trigger,
		Input:     request.input,
		InputMeta: request.inputMeta,
		Resume:    resumeTurnConfigRequestFromOptions(request.resume),
	}
}
