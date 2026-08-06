package workflow

import "eino-cli/videoagent/backend/contract"

type (
	NodeKind        = contract.NodeKind
	NodeState       = contract.NodeState
	WorkflowNode    = contract.WorkflowNode
	NodeLayout      = contract.NodeLayout
	WorkflowEdge    = contract.WorkflowEdge
	Workflow        = contract.Workflow
	WorkflowVersion = contract.WorkflowVersion
	CanvasOperation = contract.CanvasOperation
)

const (
	RequirementNode          = contract.RequirementNode
	ClipScriptNode           = contract.ClipScriptNode
	CompetitionReferenceNode = contract.CompetitionReferenceNode
	PromptTTSNode            = contract.PromptTTSNode
	CharacterReferenceNode   = contract.CharacterReferenceNode
	PreviewNode              = contract.PreviewNode
	FinalVideoNode           = contract.FinalVideoNode

	OperationAddNode        = contract.OperationAddNode
	OperationDeleteNode     = contract.OperationDeleteNode
	OperationConnect        = contract.OperationConnect
	OperationUpdateNode     = contract.OperationUpdateNode
	OperationUpdateInput    = contract.OperationUpdateInput
	OperationUpdateWorkflow = contract.OperationUpdateWorkflow
	OperationRun            = contract.OperationRun
	OperationRetry          = contract.OperationRetry
	OperationCancel         = contract.OperationCancel
)

func VideoWorkflow() Workflow {
	return contract.VideoWorkflow()
}
