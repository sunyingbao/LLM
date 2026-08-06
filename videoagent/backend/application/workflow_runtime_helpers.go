package videoagent

func nodeRunKey(node NodeRun) string {
	return node.NodeID + ":" + node.InstanceKey
}
