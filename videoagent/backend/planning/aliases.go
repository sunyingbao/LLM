package planning

import "eino-cli/videoagent/backend/contract"

type (
	Requirement      = contract.Requirement
	Character        = contract.Character
	Scene            = contract.Scene
	ClipScript       = contract.ClipScript
	ResourcePlan     = contract.ResourcePlan
	CutPlacement     = contract.CutPlacement
	RunInput         = contract.RunInput
	Artifact         = contract.Artifact
	PromptRequest    = contract.PromptRequest
	PromptExecutor   = contract.PromptExecutor
	Planner          = contract.Planner
	PreviewPlanner   = contract.PreviewPlanner
	FornaxConfig     = contract.FornaxConfig
	ModelGateway     = contract.ModelGateway
	ModelTaskRequest = contract.ModelTaskRequest
	ModelTaskStatus  = contract.ModelTaskStatus
	PreviewStrategy  = contract.PreviewStrategy
)

const (
	Succeeded               = contract.Succeeded
	PreviewStrategyT2V      = contract.PreviewStrategyT2V
	PreviewStrategyI2V      = contract.PreviewStrategyI2V
	PreviewStrategyR2V      = contract.PreviewStrategyR2V
	PreviewStrategyMaterial = contract.PreviewStrategyMaterial
)
