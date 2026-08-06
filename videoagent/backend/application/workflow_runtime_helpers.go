package application

func nodeRunKey(node NodeRun) string {
	return node.NodeID + ":" + node.InstanceKey
}
