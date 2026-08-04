package videoagent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestOperationConfirmationCreatesNewWorkflowVersion(t *testing.T) {
	runner, store, _, _, _ := testRunner(t)
	if _, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"}); err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	payload, err := encode(WorkflowNode{ID: "extra", Kind: RequirementNode})
	if err != nil {
		t.Fatalf("encode node: %v", err)
	}
	operation := CanvasOperation{ID: "operation-1", ProjectID: "project-1", Type: OperationAddNode, Payload: payload, Status: OperationPending}
	if err := store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatalf("CreateOperation() error = %v", err)
	}
	confirmed, run, err := runner.ConfirmOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatalf("ConfirmOperation() error = %v", err)
	}
	if run != nil || confirmed.Status != OperationApplied {
		t.Fatalf("confirmed operation = %#v, run = %#v", confirmed, run)
	}
	project, err := store.GetProject(context.Background(), operation.ProjectID)
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if len(project.WorkflowVersions) != 2 || len(project.WorkflowVersions[1].Nodes) != 8 {
		t.Fatalf("workflow versions = %#v, want a new version with the added node", project.WorkflowVersions)
	}
}

func TestOperationConfirmationRepairsAppliedWorkflowStatus(t *testing.T) {
	runner, store, _, _, _ := testRunner(t)
	if _, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"}); err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	payload, err := encode(WorkflowNode{ID: "extra", Kind: RequirementNode})
	if err != nil {
		t.Fatalf("encode node: %v", err)
	}
	operation := CanvasOperation{ID: "operation-recover", ProjectID: "project-1", Type: OperationAddNode, Payload: payload, Status: OperationPending}
	if err := store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatalf("CreateOperation() error = %v", err)
	}
	if _, err := store.claimOperation(context.Background(), operation.ID, ""); err != nil {
		t.Fatalf("claimOperation() error = %v", err)
	}
	project, err := store.GetProject(context.Background(), operation.ProjectID)
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	current, err := currentWorkflow(project)
	if err != nil {
		t.Fatalf("currentWorkflow() error = %v", err)
	}
	updated, err := applyWorkflowOperation(current.Workflow, operation)
	if err != nil {
		t.Fatalf("applyWorkflowOperation() error = %v", err)
	}
	versionID := "workflow:operation:" + operation.ID
	project.WorkflowVersions = append(project.WorkflowVersions, WorkflowVersion{
		ID: versionID, ProjectID: project.ID, Revision: len(project.WorkflowVersions) + 1, Workflow: updated,
	})
	project.CurrentWorkflowVersion = versionID
	if err := store.SaveProject(context.Background(), project); err != nil {
		t.Fatalf("SaveProject() error = %v", err)
	}

	confirmed, _, err := runner.ConfirmOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatalf("ConfirmOperation() error = %v", err)
	}
	persisted, err := store.GetOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatalf("GetOperation() error = %v", err)
	}
	if confirmed.Status != OperationApplied || persisted.Status != OperationApplied {
		t.Fatalf("operation status = %q, persisted = %q, want applied", confirmed.Status, persisted.Status)
	}
}

func TestUpdateWorkflowOperationPersistsEditedCanvas(t *testing.T) {
	runner, store, _, _, _ := testRunner(t)
	if _, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"}); err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	project, err := store.GetProject(context.Background(), "project-1")
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	workflow := project.WorkflowVersions[0].Workflow
	workflow.Nodes[0].Layout = NodeLayout{X: 123, Y: 456}
	payload, err := encode(workflow)
	if err != nil {
		t.Fatalf("encode workflow: %v", err)
	}
	operation := CanvasOperation{ID: "operation-workflow", ProjectID: "project-1", Type: OperationUpdateWorkflow, Payload: payload, Status: OperationPending}
	if err := store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatalf("CreateOperation() error = %v", err)
	}
	if _, _, err := runner.ConfirmOperation(context.Background(), operation.ID); err != nil {
		t.Fatalf("ConfirmOperation() error = %v", err)
	}
	project, err = store.GetProject(context.Background(), operation.ProjectID)
	if err != nil {
		t.Fatalf("GetProject() after confirm error = %v", err)
	}
	if len(project.WorkflowVersions) != 2 || project.WorkflowVersions[1].Nodes[0].Layout.X != 123 {
		t.Fatalf("workflow version was not updated: %#v", project.WorkflowVersions)
	}
}

func TestUpdateInputOperationPersistsNodeConfig(t *testing.T) {
	runner, store, _, _, _ := testRunner(t)
	if _, err := runner.StartRun(context.Background(), "project-1", RunInput{ProductName: "shoe"}); err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	payload, err := encode(struct {
		NodeID string          `json:"node_id"`
		Config json.RawMessage `json:"config"`
	}{NodeID: "clipscript", Config: json.RawMessage(`{"prompt":"new brief"}`)})
	if err != nil {
		t.Fatalf("encode input update: %v", err)
	}
	operation := CanvasOperation{ID: "operation-input", ProjectID: "project-1", Type: OperationUpdateInput, Payload: payload, Status: OperationPending}
	if err := store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatalf("CreateOperation() error = %v", err)
	}
	if _, _, err := runner.ConfirmOperation(context.Background(), operation.ID); err != nil {
		t.Fatalf("ConfirmOperation() error = %v", err)
	}
	project, err := store.GetProject(context.Background(), operation.ProjectID)
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	var config map[string]string
	if err := json.Unmarshal(project.WorkflowVersions[1].Nodes[1].Config, &config); err != nil {
		t.Fatalf("decode node config: %v", err)
	}
	if config["prompt"] != "new brief" {
		t.Fatalf("node config = %#v, want updated config", config)
	}
}
