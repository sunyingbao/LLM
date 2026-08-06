package application

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"eino-cli/videoagent/backend/contract"
)

type (
	NodeKind        = contract.NodeKind
	NodeState       = contract.NodeState
	WorkflowNode    = contract.WorkflowNode
	NodeLayout      = contract.NodeLayout
	WorkflowEdge    = contract.WorkflowEdge
	Workflow        = contract.Workflow
	WorkflowVersion = contract.WorkflowVersion
	Project         = contract.Project
	Conversation    = contract.Conversation
	ProjectSession  = contract.ProjectSession
	MessagePart     = contract.MessagePart
	Message         = contract.Message
	AgentChatInput  = contract.AgentChatInput
	AgentChatReply  = contract.AgentChatReply
	CanvasOperation = contract.CanvasOperation
	RunInput        = contract.RunInput
	Artifact        = contract.Artifact
	NodeRun         = contract.NodeRun
	Run             = contract.Run
	Command         = contract.Command
	Result          = contract.Result
	FornaxConfig    = contract.FornaxConfig
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

	OperationAddNode        = contract.OperationAddNode
	OperationDeleteNode     = contract.OperationDeleteNode
	OperationConnect        = contract.OperationConnect
	OperationUpdateNode     = contract.OperationUpdateNode
	OperationUpdateInput    = contract.OperationUpdateInput
	OperationUpdateWorkflow = contract.OperationUpdateWorkflow
	OperationRun            = contract.OperationRun
	OperationRetry          = contract.OperationRetry
	OperationCancel         = contract.OperationCancel
	OperationPending        = contract.OperationPending
	OperationConfirmed      = contract.OperationConfirmed
	OperationApplied        = contract.OperationApplied
	OperationRejected       = contract.OperationRejected
)

func VideoWorkflow() Workflow { return contract.VideoWorkflow() }

var idSequence atomic.Uint64

func newID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UTC().UnixNano(), idSequence.Add(1))
}

func newSubmitKey(runID, nodeID, instanceKey string) string {
	return fmt.Sprintf("%s:%s:%s", runID, nodeID, instanceKey)
}

func artifactID(node NodeRun) string {
	if node.InstanceKey == "" {
		return node.NodeID
	}
	return node.NodeID + ":" + node.InstanceKey
}

func parsePositiveID(value string, fallback int64) int64 {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return fallback
	}
	return id
}
