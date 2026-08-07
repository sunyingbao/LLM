// Package contract defines the recoverable workflow from requirement analysis
// through clipscript resources, preview, and finalvideo.
package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrJobPending         = errors.New("remote job is still pending")
	ErrRunCancelRequested = errors.New("run cancellation is requested")
)

type NodeKind string

const (
	RequirementNode          NodeKind = "requirement"
	ClipScriptNode           NodeKind = "clipscript"
	CompetitionReferenceNode NodeKind = "competition_reference_image"
	PromptTTSNode            NodeKind = "prompt_tts"
	CharacterReferenceNode   NodeKind = "character_reference_image"
	PreviewNode              NodeKind = "preview"
	FinalVideoNode           NodeKind = "finalvideo"
)

type NodeState string

const (
	Pending   NodeState = "pending"
	Running   NodeState = "running"
	Waiting   NodeState = "waiting"
	Succeeded NodeState = "succeeded"
	Failed    NodeState = "failed"
	Canceled  NodeState = "canceled"
)

type WorkflowNode struct {
	ID     string          `json:"node_id"`
	Kind   NodeKind        `json:"kind"`
	Config json.RawMessage `json:"config,omitempty"`
	Layout NodeLayout      `json:"layout,omitempty"`
}

type NodeLayout struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type WorkflowEdge struct {
	FromNodeID string `json:"from_node"`
	FromPort   string `json:"from_port,omitempty"`
	ToNodeID   string `json:"to_node"`
	ToPort     string `json:"to_port,omitempty"`
}

type Workflow struct {
	Nodes []WorkflowNode `json:"nodes"`
	Edges []WorkflowEdge `json:"edges"`
}

type WorkflowVersion struct {
	ID        string `json:"workflow_version_id"`
	ProjectID string `json:"project_id"`
	Revision  int    `json:"revision"`
	Workflow
}

type Project struct {
	ID                     string            `json:"project_id"`
	Name                   string            `json:"name,omitempty"`
	CurrentWorkflowVersion string            `json:"current_workflow_version,omitempty"`
	WorkflowVersions       []WorkflowVersion `json:"workflow_versions,omitempty"`
}

type Conversation struct {
	ID        string    `json:"conversation_id"`
	ProjectID string    `json:"project_id"`
	CreatedAt time.Time `json:"created_at"`
}

type ProjectSession struct {
	Conversation *Conversation `json:"conversation,omitempty"`
	RunID        string        `json:"run_id,omitempty"`
}

type MessagePart struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
}

type Message struct {
	ID             string        `json:"message_id"`
	ConversationID string        `json:"conversation_id"`
	IdempotencyKey string        `json:"idempotency_key,omitempty"`
	Role           string        `json:"role"`
	Parts          []MessagePart `json:"parts"`
	CreatedAt      time.Time     `json:"created_at"`
}

func (message Message) Text() string {
	parts := make([]string, 0, len(message.Parts))
	for _, part := range message.Parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

type AgentChatInput struct {
	ProjectID      string `json:"project_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	Text           string `json:"text"`
	RunInput
}

type AgentChatReply struct {
	Conversation Conversation     `json:"conversation"`
	Messages     []Message        `json:"messages"`
	Operation    *CanvasOperation `json:"operation,omitempty"`
}

type CanvasOperation struct {
	ID              string          `json:"operation_id"`
	ProjectID       string          `json:"project_id"`
	RunID           string          `json:"run_id,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	SourceMessageID string          `json:"source_message_id,omitempty"`
	Type            string          `json:"type"`
	TargetNodeID    string          `json:"target_node_id,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	Status          string          `json:"status"`
	CreatedAt       time.Time       `json:"created_at"`
}

const (
	OperationAddNode        = "add_node"
	OperationDeleteNode     = "delete_node"
	OperationConnect        = "connect"
	OperationUpdateNode     = "update_node"
	OperationUpdateInput    = "update_input"
	OperationUpdateWorkflow = "update_workflow"
	OperationRun            = "run"
	OperationRetry          = "retry"
	OperationCancel         = "cancel"

	OperationPending   = "pending"
	OperationConfirmed = "confirmed"
	OperationApplied   = "applied"
	OperationRejected  = "rejected"
)

// VideoWorkflow returns the default workflow used by a new local Run.
func VideoWorkflow() Workflow {
	return Workflow{
		Nodes: []WorkflowNode{
			{ID: "requirement", Kind: RequirementNode},
			{ID: "clipscript", Kind: ClipScriptNode},
			{ID: "competition", Kind: CompetitionReferenceNode},
			{ID: "tts", Kind: PromptTTSNode},
			{ID: "character_reference", Kind: CharacterReferenceNode},
			{ID: "preview", Kind: PreviewNode},
			{ID: "finalvideo", Kind: FinalVideoNode},
		},
		Edges: []WorkflowEdge{
			{FromNodeID: "requirement", FromPort: "requirement", ToNodeID: "clipscript", ToPort: "requirement"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "competition", ToPort: "clipscript"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "tts", ToPort: "clipscript"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "character_reference", ToPort: "clipscript"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "preview", ToPort: "clipscript"},
			{FromNodeID: "competition", FromPort: "competition_reference_image", ToNodeID: "preview", ToPort: "resources"},
			{FromNodeID: "tts", FromPort: "voice_preview", ToNodeID: "preview", ToPort: "resources"},
			{FromNodeID: "character_reference", FromPort: "character_reference_image", ToNodeID: "preview", ToPort: "resources"},
			{FromNodeID: "preview", FromPort: "preview_video", ToNodeID: "finalvideo", ToPort: "preview_video"},
			{FromNodeID: "tts", FromPort: "voice_preview", ToNodeID: "finalvideo", ToPort: "resources"},
			{FromNodeID: "clipscript", FromPort: "clipscript", ToNodeID: "finalvideo", ToPort: "clipscript"},
		},
	}
}

type RunInput struct {
	ProductName      string   `json:"product_name"`
	ProductImageURLs []string `json:"product_image_urls,omitempty"`
	Brief            string   `json:"brief"`
}

// FornaxConfig contains the identity used by the native Fornax SDK.
type FornaxConfig struct {
	AppID  int64  `json:"app_id,omitempty"`
	AK     string `json:"ak,omitempty"`
	SK     string `json:"sk,omitempty"`
	Region string `json:"region,omitempty"`
}

type Artifact struct {
	ID        string          `json:"artifact_id"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	ParentIDs []string        `json:"parent_ids,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Message   string          `json:"message,omitempty"`
}

type NodeRun struct {
	NodeID            string          `json:"node_id"`
	Kind              NodeKind        `json:"kind"`
	Config            json.RawMessage `json:"config,omitempty"`
	InstanceKey       string          `json:"instance_key,omitempty"`
	State             NodeState       `json:"state"`
	Provider          string          `json:"provider,omitempty"`
	JobID             string          `json:"job_id,omitempty"`
	SubmitKey         string          `json:"submit_key"`
	Attempt           int             `json:"attempt,omitempty"`
	ClaimToken        string          `json:"claim_token,omitempty"`
	ClaimedAt         *time.Time      `json:"claimed_at,omitempty"`
	Output            json.RawMessage `json:"output,omitempty"`
	Artifacts         []Artifact      `json:"artifacts,omitempty"`
	FallbackSubmitted bool            `json:"fallback_submitted,omitempty"`
	SubmitStarted     bool            `json:"submit_started,omitempty"`
	SubmissionUnknown bool            `json:"submission_unknown,omitempty"`
	Message           string          `json:"message,omitempty"`
}

func (node NodeRun) Key() string {
	return node.NodeID + ":" + node.InstanceKey
}

func (node NodeRun) ArtifactID() string {
	if node.InstanceKey == "" {
		return node.NodeID
	}
	return node.Key()
}

func SubmitKey(runID, nodeID, instanceKey string) string {
	return fmt.Sprintf("%s:%s:%s", runID, nodeID, instanceKey)
}

type Run struct {
	ID              string          `json:"run_id"`
	ProjectID       string          `json:"project_id"`
	Workflow        WorkflowVersion `json:"workflow"`
	Input           RunInput        `json:"input"`
	NodeRuns        []NodeRun       `json:"node_runs"`
	CancelRequested bool            `json:"cancel_requested,omitempty"`
	Canceled        bool            `json:"canceled,omitempty"`
}

type Command struct {
	RunID   string
	Input   RunInput
	NodeRun NodeRun
	Inputs  []Artifact
}

type Result struct {
	State             NodeState
	Provider          string
	JobID             string
	Artifacts         []Artifact
	Children          []NodeRun
	FallbackSubmitted bool
	SubmissionUnknown bool
	Message           string
}

func (state NodeState) Terminal() bool {
	return state == Succeeded || state == Failed || state == Canceled
}

func (run Run) Terminal() bool {
	if len(run.NodeRuns) == 0 {
		return false
	}
	for _, node := range run.NodeRuns {
		if !node.State.Terminal() {
			return false
		}
	}
	return true
}

func (kind NodeKind) Resource() bool {
	switch kind {
	case CompetitionReferenceNode, PromptTTSNode, CharacterReferenceNode, PreviewNode, FinalVideoNode:
		return true
	default:
		return false
	}
}
