//go:build !windows

package thread

import (
	agentworker "eino-cli/deepagent/worker"
)

const threadOutputObserverQueueSize = 256

func cloneThreadOutputItem(item agentworker.ThreadOutputItem) agentworker.ThreadOutputItem {
	return agentworker.ThreadOutputItem{
		Event: cloneWorkerEvent(item.Event),
		Yield: cloneThreadYield(item.Yield),
	}
}

func cloneWorkerEvent(event *agentworker.Event) *agentworker.Event {
	if event == nil {
		return nil
	}
	clone := *event
	clone.Payload = append([]byte(nil), event.Payload...)
	clone.Metadata = cloneStringMap(event.Metadata)
	clone.PersistToEventLog = cloneBoolPtr(event.PersistToEventLog)
	clone.FanoutToSession = cloneBoolPtr(event.FanoutToSession)
	return &clone
}

func cloneThreadYield(yield *agentworker.ThreadYield) *agentworker.ThreadYield {
	if yield == nil {
		return nil
	}
	clone := *yield
	if yield.Block != nil {
		block := *yield.Block
		clone.Block = &block
	}
	return &clone
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneBoolPtr(in *bool) *bool {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
