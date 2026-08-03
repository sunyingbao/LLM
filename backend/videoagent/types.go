// Package videoagent owns the recoverable workflow from requirement analysis
// to storyboard resource preparation.
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
	StoryboardNode           NodeKind = "storyboard"
	CompetitionReferenceNode NodeKind = "competition_reference_image"
	PromptTTSNode            NodeKind = "prompt_tts"
	CharacterReferenceNode   NodeKind = "character_reference_image"
)

type NodeState string

const (
	Pending   NodeState = "pending"
	Running   NodeState = "running"
	Waiting   NodeState = "waiting"
	Succeeded NodeState = "succeeded"
	Failed    NodeState = "failed"
)

type Node struct {
	ID   string   `json:"node_id"`
	Kind NodeKind `json:"kind"`
}

type Edge struct {
	From string `json:"from_node"`
	To   string `json:"to_node"`
}

type WorkflowVersion struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// StoryboardWorkflow is immutable for a Run once it has been persisted.
func StoryboardWorkflow() WorkflowVersion {
	return WorkflowVersion{
		Nodes: []Node{
			{ID: "requirement", Kind: RequirementNode},
			{ID: "storyboard", Kind: StoryboardNode},
			{ID: "competition", Kind: CompetitionReferenceNode},
			{ID: "tts", Kind: PromptTTSNode},
			{ID: "character_reference", Kind: CharacterReferenceNode},
		},
		Edges: []Edge{
			{From: "requirement", To: "storyboard"},
			{From: "storyboard", To: "competition"},
			{From: "storyboard", To: "tts"},
			{From: "storyboard", To: "character_reference"},
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
	Output            json.RawMessage
	Artifacts         []Artifact
	Children          []NodeRun
	FallbackSubmitted bool
	Message           string
}

func (state NodeState) terminal() bool {
	return state == Succeeded || state == Failed
}

func (kind NodeKind) resource() bool {
	return kind == CompetitionReferenceNode || kind == PromptTTSNode || kind == CharacterReferenceNode
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
