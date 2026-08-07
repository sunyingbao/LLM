package application

import (
	"context"
	"time"

	"eino-cli/videoagent/backend/workflow"
)

type (
	PortDefinition = workflow.PortDefinition
	NodeDefinition = workflow.NodeDefinition
	NodeCatalog    = workflow.NodeCatalog
)

func defaultNodeCatalog() NodeCatalog {
	return workflow.DefaultNodeCatalog()
}

func cloneWorkflow(value Workflow) Workflow {
	return workflow.Clone(value)
}

func applyWorkflowOperation(value Workflow, operation CanvasOperation) (Workflow, error) {
	return workflow.ApplyOperation(value, operation)
}

func validOperationType(operationType string) bool {
	return workflow.ValidOperationType(operationType)
}

func proposeOperation(ctx context.Context, store *Store, operation CanvasOperation) (CanvasOperation, bool, error) {
	operation.ID = newID("operation")
	operation.Status = OperationPending
	operation.CreatedAt = time.Now().UTC()
	return store.CreateOrGetOperation(ctx, operation)
}
