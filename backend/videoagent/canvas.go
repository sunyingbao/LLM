package videoagent

import (
	"encoding/json"
	"fmt"
)

func validOperationType(operationType string) bool {
	switch operationType {
	case OperationAddNode, OperationDeleteNode, OperationConnect, OperationUpdateNode,
		OperationUpdateInput, OperationUpdateWorkflow, OperationRun, OperationRetry, OperationCancel:
		return true
	default:
		return false
	}
}

func applyWorkflowOperation(workflow Workflow, operation CanvasOperation) (Workflow, error) {
	workflow = cloneWorkflow(workflow)
	switch operation.Type {
	case OperationUpdateWorkflow:
		updated, err := decode[Workflow](operation.Payload)
		if err != nil {
			return Workflow{}, err
		}
		return cloneWorkflow(updated), nil
	case OperationAddNode:
		node, err := decode[WorkflowNode](operation.Payload)
		if err != nil {
			return Workflow{}, err
		}
		if node.ID == "" || node.Kind == "" {
			return Workflow{}, fmt.Errorf("added node id and kind are required")
		}
		for _, current := range workflow.Nodes {
			if current.ID == node.ID {
				return Workflow{}, fmt.Errorf("workflow node already exists: %s", node.ID)
			}
		}
		workflow.Nodes = append(workflow.Nodes, node)
	case OperationDeleteNode:
		input, err := decode[struct {
			NodeID string `json:"node_id"`
		}](operation.Payload)
		if err != nil {
			return Workflow{}, err
		}
		if input.NodeID == "" {
			input.NodeID = operation.TargetNodeID
		}
		filtered := workflow.Nodes[:0]
		for _, node := range workflow.Nodes {
			if node.ID != input.NodeID {
				filtered = append(filtered, node)
			}
		}
		workflow.Nodes = filtered
		remaining := workflow.Edges[:0]
		for _, edge := range workflow.Edges {
			if edge.FromNodeID != input.NodeID && edge.ToNodeID != input.NodeID {
				remaining = append(remaining, edge)
			}
		}
		workflow.Edges = remaining
	case OperationConnect:
		edge, err := decode[WorkflowEdge](operation.Payload)
		if err != nil {
			return Workflow{}, err
		}
		workflow.Edges = append(workflow.Edges, edge)
	case OperationUpdateNode, OperationUpdateInput:
		update, err := decode[struct {
			NodeID string          `json:"node_id"`
			Config json.RawMessage `json:"config,omitempty"`
			Layout NodeLayout      `json:"layout,omitempty"`
		}](operation.Payload)
		if err != nil {
			return Workflow{}, err
		}
		if update.NodeID == "" {
			update.NodeID = operation.TargetNodeID
		}
		for index := range workflow.Nodes {
			if workflow.Nodes[index].ID != update.NodeID {
				continue
			}
			workflow.Nodes[index].Config = append([]byte(nil), update.Config...)
			workflow.Nodes[index].Layout = update.Layout
			return workflow, nil
		}
		return Workflow{}, fmt.Errorf("workflow node not found: %s", update.NodeID)
	default:
		return Workflow{}, fmt.Errorf("unsupported workflow operation: %s", operation.Type)
	}
	return workflow, nil
}
