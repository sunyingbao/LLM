package local

import (
	"eino-cli/deepagent/cloud/protocol/timeline"
	runtimeclient "eino-cli/deepagent/runtime"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/inprocess"
)

func threadFromState(state *inprocess.ThreadState) (thread *runtimeclient.Thread) {
	if state == nil {
		return nil
	}
	thread = &runtimeclient.Thread{
		Ref:       runtimeclient.GlobalThreadRef{Runtime: runtimeclient.RuntimeLocal, Namespace: state.SessionID, ThreadID: state.ID},
		Workspace: runtimeclient.WorkspaceSpec{Cwd: state.Profile.Cwd}, Title: state.Title,
		State: runtimeclient.ThreadStateIdle, CreatedAtMS: state.CreatedAt.UnixMilli(), UpdatedAtMS: state.UpdatedAt.UnixMilli(),
	}
	if state.PendingBlock != nil {
		thread.State = runtimeclient.ThreadStateBlocked
	} else if state.ClosedAt != nil {
		thread.State = runtimeclient.ThreadStateInterrupted
	}
	return thread
}

func timelineEventFromWorker(event *agentworker.Event) (converted timeline.Event) {
	if event == nil {
		return converted
	}
	converted = timeline.Event{EventID: event.ID, EventType: string(event.Type), ThreadID: event.ThreadID, TurnID: event.TurnID, CreatedAtMs: event.TS.UnixMilli(), Payload: timeline.NormalizePayload(event.Payload)}
	return converted
}
