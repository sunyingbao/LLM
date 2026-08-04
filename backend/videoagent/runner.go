package videoagent

import (
	"context"
	"fmt"
)

// Runner advances the persisted graph. It never resubmits an uncertain job.
type Runner struct {
	store   *Store
	handler nodeHandler
	catalog NodeCatalog
}

func NewRunner(store *Store, clients Clients) (*Runner, error) {
	if store == nil {
		return nil, fmt.Errorf("workflow store is nil")
	}
	if clients.Planner == nil || clients.Image == nil || clients.TTS == nil || clients.Video == nil || clients.Audit == nil || clients.Shield == nil {
		return nil, fmt.Errorf("video agent clients are incomplete")
	}
	return &Runner{store: store, handler: nodeHandler{clients: clients}, catalog: defaultNodeCatalog()}, nil
}

// StartRun persists the static workflow before executing any model or remote call.
func (runner *Runner) StartRun(ctx context.Context, projectID string, input RunInput) (run Run, err error) {
	return runner.StartWorkflow(ctx, projectID, VideoWorkflow(), input)
}

// StartWorkflow validates a user-edited graph and stores an immutable Run snapshot.
func (runner *Runner) StartWorkflow(ctx context.Context, projectID string, workflow Workflow, input RunInput) (run Run, err error) {
	if projectID == "" {
		return run, fmt.Errorf("project id is empty")
	}
	if err = runner.catalog.validate(workflow); err != nil {
		return run, err
	}

	runID := newID("run")
	nodeRuns := make([]NodeRun, 0, len(workflow.Nodes))
	for _, node := range workflow.Nodes {
		nodeRuns = append(nodeRuns, NodeRun{
			NodeID:    node.ID,
			Kind:      node.Kind,
			State:     Pending,
			SubmitKey: newSubmitKey(runID, node.ID, ""),
		})
	}
	run = Run{
		ID:        runID,
		ProjectID: projectID,
		Workflow:  WorkflowVersion{ID: newID("workflow"), ProjectID: projectID, Revision: 1, Workflow: cloneWorkflow(workflow)},
		Input:     input,
		NodeRuns:  nodeRuns,
	}
	if err = runner.store.Create(ctx, run); err != nil {
		return run, err
	}
	if err = runner.Advance(ctx, runID); err != nil {
		return run, err
	}
	return runner.store.Get(ctx, runID)
}

// Advance executes ready nodes until the graph only contains waiting or blocked nodes.
func (runner *Runner) Advance(ctx context.Context, runID string) error {
	for {
		command, claimed, err := runner.store.claimSubmitted(runID)
		if err != nil {
			return err
		}
		if claimed {
			if err := runner.refresh(ctx, command); err != nil {
				return err
			}
			continue
		}

		command, claimed, err = runner.store.claimReady(runID)
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}

		if command.NodeRun.InstanceKey != "" {
			if err := runner.store.markSubmitStarted(command); err != nil {
				return err
			}
		}
		result, err := runner.handler.Start(ctx, command)
		if err != nil {
			result = failedResult(command, err)
		}
		if err := runner.store.apply(command, result); err != nil {
			return err
		}
	}
}

// Poll uses the same refresh path as callbacks and never submits a new job.
func (runner *Runner) Poll(ctx context.Context, runID string) error {
	if err := runner.Advance(ctx, runID); err != nil {
		return err
	}
	skipped := make(map[string]bool)
	for {
		command, claimed, err := runner.store.claimWaiting(runID, skipped)
		if err != nil {
			return err
		}
		if !claimed {
			return runner.Advance(ctx, runID)
		}
		skipped[nodeRunKey(command.NodeRun)] = true
		if err := runner.refresh(ctx, command); err != nil {
			return err
		}
	}
}

// Recover restores a Run after process restart without resubmitting a started job.
func (runner *Runner) Recover(ctx context.Context, runID string) error {
	if err := runner.store.recover(runID); err != nil {
		return err
	}
	return runner.Poll(ctx, runID)
}

// OnCallback deduplicates delivery, refreshes the existing job, then advances dependents.
func (runner *Runner) OnCallback(ctx context.Context, provider, eventID, jobID string) error {
	if provider == "" || eventID == "" || jobID == "" {
		return fmt.Errorf("callback provider, event id and job id are required")
	}
	command, claimed, err := runner.store.claimCallback(provider, eventID, jobID)
	if err != nil || !claimed {
		return err
	}
	if err := runner.refresh(ctx, command); err != nil {
		return err
	}
	return runner.Advance(ctx, command.RunID)
}

func (runner *Runner) refresh(ctx context.Context, command Command) error {
	result, err := runner.handler.Refresh(ctx, command)
	if err != nil {
		if requeueErr := runner.store.requeue(command); requeueErr != nil {
			return requeueErr
		}
		return err
	}
	return runner.store.apply(command, result)
}

func failedResult(command Command, err error) Result {
	return Result{
		State: Failed,
		Artifacts: []Artifact{{
			ID:      artifactID(command.NodeRun),
			Kind:    string(command.NodeRun.Kind),
			Status:  string(Failed),
			Message: err.Error(),
		}},
		Message: err.Error(),
	}
}
