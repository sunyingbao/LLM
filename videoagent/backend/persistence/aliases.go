package persistence

import "eino-cli/videoagent/backend/contract"

type (
	Artifact        = contract.Artifact
	AgentChatReply  = contract.AgentChatReply
	CallbackMessage = contract.CallbackMessage
	CanvasOperation = contract.CanvasOperation
	Command         = contract.Command
	Conversation    = contract.Conversation
	Message         = contract.Message
	NodeKind        = contract.NodeKind
	NodeRun         = contract.NodeRun
	Project         = contract.Project
	ProjectSession  = contract.ProjectSession
	Result          = contract.Result
	Run             = contract.Run
	StoredVideo     = contract.StoredVideo
	SubmittedJob    = contract.SubmittedJob
	Workflow        = contract.Workflow
	WorkflowVersion = contract.WorkflowVersion
)

const (
	RequirementNode          = contract.RequirementNode
	ClipScriptNode           = contract.ClipScriptNode
	CompetitionReferenceNode = contract.CompetitionReferenceNode
	PromptTTSNode            = contract.PromptTTSNode
	CharacterReferenceNode   = contract.CharacterReferenceNode
	PreviewNode              = contract.PreviewNode
	FinalVideoNode           = contract.FinalVideoNode

	Pending   = contract.Pending
	Running   = contract.Running
	Waiting   = contract.Waiting
	Succeeded = contract.Succeeded
	Failed    = contract.Failed
	Canceled  = contract.Canceled

	OperationPending   = contract.OperationPending
	OperationConfirmed = contract.OperationConfirmed
	OperationApplied   = contract.OperationApplied
	OperationRejected  = contract.OperationRejected
)
