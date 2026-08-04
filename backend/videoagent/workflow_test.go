package videoagent

import (
	"context"
	"strings"
	"testing"
)

func TestWorkflowValidationRejectsInvalidPort(t *testing.T) {
	workflow := VideoWorkflow()
	workflow.Edges[0].ToPort = "preview"

	err := defaultNodeCatalog().validate(workflow)
	if err == nil || !strings.Contains(err.Error(), "input port is not registered") {
		t.Fatalf("validate() error = %v, want invalid input port", err)
	}
}

func TestWorkflowValidationRejectsCycle(t *testing.T) {
	workflow := Workflow{
		Nodes: []WorkflowNode{
			{ID: "first", Kind: RequirementNode},
			{ID: "second", Kind: RequirementNode},
		},
		Edges: []WorkflowEdge{
			{FromNodeID: "first", ToNodeID: "second"},
			{FromNodeID: "second", ToNodeID: "first"},
		},
	}

	err := defaultNodeCatalog().validate(workflow)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("validate() error = %v, want cycle error", err)
	}
}

func TestStartWorkflowPersistsCustomLayoutAndVersion(t *testing.T) {
	runner, store, _, _, _ := testRunner(t)
	workflow := Workflow{
		Nodes: []WorkflowNode{
			{ID: "input", Kind: RequirementNode, Layout: NodeLayout{X: 24, Y: 48}},
			{ID: "script", Kind: ClipScriptNode, Layout: NodeLayout{X: 240, Y: 48}},
		},
		Edges: []WorkflowEdge{{FromNodeID: "input", FromPort: "requirement", ToNodeID: "script", ToPort: "requirement"}},
	}

	run, err := runner.StartWorkflow(context.Background(), "project-1", workflow, RunInput{ProductName: "shoe"})
	if err != nil {
		t.Fatalf("StartWorkflow() error = %v", err)
	}
	stored, err := store.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Workflow.ID == "" || stored.Workflow.ProjectID != "project-1" {
		t.Fatalf("workflow version = %#v, want project snapshot", stored.Workflow)
	}
	if stored.Workflow.Nodes[1].Layout.X != 240 || stored.Workflow.Nodes[1].ID != "script" {
		t.Fatalf("stored workflow nodes = %#v, want custom layout", stored.Workflow.Nodes)
	}
	if node := nodeRun(stored, "script", ""); node.State != Succeeded {
		t.Fatalf("custom script node state = %s, want succeeded", node.State)
	}
}
