package videoagent

import "fmt"

type PortDefinition struct {
	Name         string `json:"name"`
	ArtifactKind string `json:"artifact_kind"`
	Required     bool   `json:"required,omitempty"`
}

type NodeDefinition struct {
	Kind    NodeKind         `json:"kind"`
	Inputs  []PortDefinition `json:"inputs,omitempty"`
	Outputs []PortDefinition `json:"outputs,omitempty"`
}

type NodeCatalog map[NodeKind]NodeDefinition

func defaultNodeCatalog() NodeCatalog {
	return NodeCatalog{
		RequirementNode:          {Kind: RequirementNode, Outputs: []PortDefinition{{Name: "requirement", ArtifactKind: "requirement"}}},
		ClipScriptNode:           {Kind: ClipScriptNode, Inputs: []PortDefinition{{Name: "requirement", ArtifactKind: "requirement", Required: true}}, Outputs: []PortDefinition{{Name: "clipscript", ArtifactKind: "clipscript"}}},
		CompetitionReferenceNode: {Kind: CompetitionReferenceNode, Inputs: []PortDefinition{{Name: "clipscript", ArtifactKind: "clipscript", Required: true}}, Outputs: []PortDefinition{{Name: "competition_reference_image", ArtifactKind: "competition_reference_image"}}},
		PromptTTSNode:            {Kind: PromptTTSNode, Inputs: []PortDefinition{{Name: "clipscript", ArtifactKind: "clipscript", Required: true}}, Outputs: []PortDefinition{{Name: "voice_preview", ArtifactKind: "voice_preview"}}},
		CharacterReferenceNode:   {Kind: CharacterReferenceNode, Inputs: []PortDefinition{{Name: "clipscript", ArtifactKind: "clipscript", Required: true}}, Outputs: []PortDefinition{{Name: "character_reference_image", ArtifactKind: "character_reference_image"}}},
		PreviewNode: {Kind: PreviewNode, Inputs: []PortDefinition{
			{Name: "clipscript", ArtifactKind: "clipscript", Required: true},
			{Name: "resources", ArtifactKind: "resource"},
		}, Outputs: []PortDefinition{{Name: "preview_video", ArtifactKind: "preview_video"}}},
		FinalVideoNode: {Kind: FinalVideoNode, Inputs: []PortDefinition{
			{Name: "clipscript", ArtifactKind: "clipscript", Required: true},
			{Name: "preview_video", ArtifactKind: "preview_video", Required: true},
			{Name: "resources", ArtifactKind: "resource"},
		}, Outputs: []PortDefinition{{Name: "finalvideo", ArtifactKind: "finalvideo"}}},
	}
}

func (catalog NodeCatalog) validate(workflow Workflow) error {
	return catalog.validateWorkflow(workflow, true)
}

func (catalog NodeCatalog) validateDraft(workflow Workflow) error {
	return catalog.validateWorkflow(workflow, false)
}

func (catalog NodeCatalog) validateWorkflow(workflow Workflow, requireInputs bool) error {
	if requireInputs && len(workflow.Nodes) == 0 {
		return fmt.Errorf("workflow has no nodes")
	}
	nodes := make(map[string]WorkflowNode, len(workflow.Nodes))
	for _, node := range workflow.Nodes {
		if node.ID == "" {
			return fmt.Errorf("workflow node id is empty")
		}
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("duplicate workflow node: %s", node.ID)
		}
		if _, exists := catalog[node.Kind]; !exists {
			return fmt.Errorf("workflow node kind is not registered: %s", node.Kind)
		}
		nodes[node.ID] = node
	}
	if err := catalog.validateEdges(workflow.Edges, nodes, requireInputs); err != nil {
		return err
	}
	return validateAcyclic(workflow)
}

func (catalog NodeCatalog) validateEdges(edges []WorkflowEdge, nodes map[string]WorkflowNode, requireInputs bool) error {
	seen := make(map[string]struct{}, len(edges))
	connectedInputs := make(map[string]map[string]bool, len(nodes))
	for _, edge := range edges {
		from, fromExists := nodes[edge.FromNodeID]
		to, toExists := nodes[edge.ToNodeID]
		if !fromExists || !toExists {
			return fmt.Errorf("workflow edge references missing node: %s -> %s", edge.FromNodeID, edge.ToNodeID)
		}
		key := edge.FromNodeID + ":" + edge.FromPort + "->" + edge.ToNodeID + ":" + edge.ToPort
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate workflow edge: %s", key)
		}
		seen[key] = struct{}{}
		if edge.FromPort == "" && edge.ToPort == "" {
			continue
		}
		if edge.FromPort == "" || edge.ToPort == "" {
			return fmt.Errorf("workflow edge ports are incomplete: %s", key)
		}
		fromPort, ok := findPort(catalog[from.Kind].Outputs, edge.FromPort)
		if !ok {
			return fmt.Errorf("output port is not registered: %s.%s", from.ID, edge.FromPort)
		}
		toPort, ok := findPort(catalog[to.Kind].Inputs, edge.ToPort)
		if !ok {
			return fmt.Errorf("input port is not registered: %s.%s", to.ID, edge.ToPort)
		}
		if toPort.ArtifactKind != "resource" && fromPort.ArtifactKind != toPort.ArtifactKind {
			return fmt.Errorf("incompatible workflow ports: %s.%s -> %s.%s", from.ID, edge.FromPort, to.ID, edge.ToPort)
		}
		if connectedInputs[edge.ToNodeID] == nil {
			connectedInputs[edge.ToNodeID] = make(map[string]bool)
		}
		connectedInputs[edge.ToNodeID][edge.ToPort] = true
	}
	if requireInputs {
		for nodeID, node := range nodes {
			for _, input := range catalog[node.Kind].Inputs {
				if input.Required && !connectedInputs[nodeID][input.Name] {
					return fmt.Errorf("required input is not connected: %s.%s", nodeID, input.Name)
				}
			}
		}
	}
	return nil
}

func findPort(ports []PortDefinition, name string) (PortDefinition, bool) {
	for _, port := range ports {
		if port.Name == name {
			return port, true
		}
	}
	return PortDefinition{}, false
}

func validateAcyclic(workflow Workflow) error {
	graph := make(map[string][]string, len(workflow.Nodes))
	for _, edge := range workflow.Edges {
		graph[edge.FromNodeID] = append(graph[edge.FromNodeID], edge.ToNodeID)
	}
	state := make(map[string]uint8, len(workflow.Nodes))
	var visit func(string) error
	visit = func(nodeID string) error {
		if state[nodeID] == 1 {
			return fmt.Errorf("workflow contains a cycle at node: %s", nodeID)
		}
		if state[nodeID] == 2 {
			return nil
		}
		state[nodeID] = 1
		for _, next := range graph[nodeID] {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[nodeID] = 2
		return nil
	}
	for _, node := range workflow.Nodes {
		if err := visit(node.ID); err != nil {
			return err
		}
	}
	return nil
}

func cloneWorkflow(workflow Workflow) Workflow {
	clone := Workflow{
		Nodes: append([]WorkflowNode(nil), workflow.Nodes...),
		Edges: append([]WorkflowEdge(nil), workflow.Edges...),
	}
	for index := range clone.Nodes {
		clone.Nodes[index].Config = append([]byte(nil), clone.Nodes[index].Config...)
	}
	return clone
}
