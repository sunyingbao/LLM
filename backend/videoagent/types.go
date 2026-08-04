// Package videoagent owns the recoverable workflow from requirement analysis
// through clipscript resources, preview, and finalvideo.
package videoagent

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"
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
		},
	}
}

type RunInput struct {
	ProductName      string   `json:"product_name"`
	ProductImageURLs []string `json:"product_image_urls,omitempty"`
	Brief            string   `json:"brief"`
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
	InstanceKey       string          `json:"instance_key,omitempty"`
	State             NodeState       `json:"state"`
	Provider          string          `json:"provider,omitempty"`
	JobID             string          `json:"job_id,omitempty"`
	SubmitKey         string          `json:"submit_key"`
	Output            json.RawMessage `json:"output,omitempty"`
	Artifacts         []Artifact      `json:"artifacts,omitempty"`
	FallbackSubmitted bool            `json:"fallback_submitted,omitempty"`
	SubmitStarted     bool            `json:"submit_started,omitempty"`
	Message           string          `json:"message,omitempty"`
}

type Run struct {
	ID        string          `json:"run_id"`
	ProjectID string          `json:"project_id"`
	Workflow  WorkflowVersion `json:"workflow"`
	Input     RunInput        `json:"input"`
	NodeRuns  []NodeRun       `json:"node_runs"`
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
	ClearJobID        bool
	Artifacts         []Artifact
	Children          []NodeRun
	FallbackSubmitted bool
	ResetSubmission   bool
	Message           string
}

func (state NodeState) terminal() bool {
	return state == Succeeded || state == Failed
}

func (kind NodeKind) resource() bool {
	switch kind {
	case CompetitionReferenceNode, PromptTTSNode, CharacterReferenceNode, PreviewNode, FinalVideoNode:
		return true
	default:
		return false
	}
}

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
