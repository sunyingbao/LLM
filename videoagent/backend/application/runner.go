package videoagent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"eino-cli/videoagent/backend/contract"
)

var ErrCallbackNotReady = errors.New("callback job is not ready")
var ErrJobPending = contract.ErrJobPending
var ErrClaimHeartbeat = errors.New("node claim heartbeat failed")
var errRunCancelRequested = contract.ErrRunCancelRequested

// Runner advances the persisted graph. It never resubmits an uncertain job.
type Runner struct {
	store          *Store
	handler        nodeHandler
	catalog        NodeCatalog
	monitor        Monitor
	claimHeartbeat time.Duration
	Metrics        *Metrics
}

func NewRunner(store *Store, clients Clients) (*Runner, error) {
	if store == nil {
		return nil, fmt.Errorf("workflow store is nil")
	}
	if err := clients.Validate(); err != nil {
		return nil, err
	}
	metrics := NewMetrics()
	return &Runner{
		store: store, handler: nodeHandler{clients: clients, store: store}, catalog: defaultNodeCatalog(),
		monitor: metrics, claimHeartbeat: nodeClaimTTL / 3, Metrics: metrics,
	}, nil
}

// SetMonitor replaces the optional execution observer while keeping built-in counters available.
func (runner *Runner) SetMonitor(monitor Monitor) {
	if runner != nil && monitor != nil {
		runner.monitor = MonitorFunc(func(ctx context.Context, event RunEvent) {
			if runner.Metrics != nil {
				runner.Metrics.Record(ctx, event)
			}
			monitor.Record(ctx, event)
		})
	}
}

// StartRun persists the static workflow before executing any model or remote call.
func (runner *Runner) StartRun(ctx context.Context, projectID string, input RunInput) (run Run, err error) {
	project, err := runner.store.GetProject(ctx, projectID)
	if err != nil && !errors.Is(err, ErrProjectNotFound) {
		return run, err
	}
	if errors.Is(err, ErrProjectNotFound) || len(project.WorkflowVersions) == 0 {
		version, saveErr := runner.SaveWorkflow(ctx, projectID, VideoWorkflow())
		if saveErr != nil {
			return run, saveErr
		}
		return runner.StartWorkflow(ctx, projectID, version.Workflow, input)
	}
	version, err := currentWorkflow(project)
	if err != nil {
		return run, err
	}
	return runner.StartWorkflow(ctx, projectID, version.Workflow, input)
}

// SaveWorkflow validates and publishes the latest editable canvas version.
func (runner *Runner) SaveWorkflow(ctx context.Context, projectID string, workflow Workflow) (WorkflowVersion, error) {
	if projectID == "" {
		return WorkflowVersion{}, fmt.Errorf("project id is empty")
	}
	if err := runner.catalog.ValidateDraft(workflow); err != nil {
		return WorkflowVersion{}, err
	}
	versionID := newID("workflow")
	var version WorkflowVersion
	_, err := runner.store.UpdateProject(projectID, true, func(project *Project) error {
		version = WorkflowVersion{ID: versionID, ProjectID: projectID, Revision: len(project.WorkflowVersions) + 1, Workflow: cloneWorkflow(workflow)}
		project.WorkflowVersions = append(project.WorkflowVersions, version)
		project.CurrentWorkflowVersion = version.ID
		return nil
	})
	if err != nil {
		return WorkflowVersion{}, err
	}
	return version, nil
}

// StartWorkflow validates a user-edited graph and stores an immutable Run snapshot.
func (runner *Runner) StartWorkflow(ctx context.Context, projectID string, workflow Workflow, input RunInput) (run Run, err error) {
	return runner.startWorkflow(ctx, projectID, workflow, input, newID("run"))
}

func (runner *Runner) startWorkflow(ctx context.Context, projectID string, workflow Workflow, input RunInput, runID string) (run Run, err error) {
	if projectID == "" {
		return run, fmt.Errorf("project id is empty")
	}
	if err = runner.catalog.Validate(workflow); err != nil {
		return run, err
	}
	if existing, getErr := runner.store.Get(ctx, runID); getErr == nil {
		return existing, nil
	}

	nodeRuns := make([]NodeRun, 0, len(workflow.Nodes))
	for _, node := range workflow.Nodes {
		nodeRuns = append(nodeRuns, NodeRun{
			NodeID:    node.ID,
			Kind:      node.Kind,
			Config:    append([]byte(nil), node.Config...),
			State:     Pending,
			SubmitKey: newSubmitKey(runID, node.ID, ""),
		})
	}
	workflowID := "workflow:" + runID
	run = Run{
		ID:        runID,
		ProjectID: projectID,
		Workflow:  WorkflowVersion{ID: workflowID, ProjectID: projectID, Workflow: cloneWorkflow(workflow)},
		Input:     input,
		NodeRuns:  nodeRuns,
	}
	if project, projectErr := runner.store.GetProject(ctx, projectID); projectErr == nil {
		run.Workflow.Revision = len(project.WorkflowVersions) + 1
	}
	if err = runner.store.Create(ctx, run); err != nil {
		return run, err
	}
	if err = runner.Advance(ctx, runID); err != nil {
		return run, err
	}
	return runner.store.Get(ctx, runID)
}

// ConfirmOperation applies a confirmed Canvas operation or starts its workflow.
func (runner *Runner) ConfirmOperation(ctx context.Context, operationID string) (CanvasOperation, *Run, error) {
	operation, err := runner.store.GetOperation(ctx, operationID)
	if err != nil {
		return CanvasOperation{}, nil, err
	}
	if operation.Status == OperationRejected {
		return CanvasOperation{}, nil, fmt.Errorf("operation is rejected")
	}
	if operation.Status == OperationApplied {
		if operation.RunID == "" {
			return operation, nil, nil
		}
		run, err := runner.store.Get(ctx, operation.RunID)
		if err != nil {
			return operation, nil, err
		}
		return operation, &run, nil
	}
	if operation.Status != OperationPending && operation.Status != OperationConfirmed {
		return CanvasOperation{}, nil, fmt.Errorf("operation is not pending: %s", operation.Status)
	}
	if operation.Type == OperationRun {
		project, err := runner.store.GetProject(ctx, operation.ProjectID)
		if err != nil {
			return CanvasOperation{}, nil, err
		}
		if len(project.WorkflowVersions) == 0 {
			return CanvasOperation{}, nil, fmt.Errorf("project has no workflow: %s", operation.ProjectID)
		}
		latest, err := currentWorkflow(project)
		if err != nil {
			return CanvasOperation{}, nil, err
		}
		input, err := decode[RunInput](operation.Payload)
		if err != nil {
			return CanvasOperation{}, nil, err
		}
		if err := runner.catalog.Validate(latest.Workflow); err != nil {
			return operation, nil, err
		}
		if operation.Status == OperationPending {
			operation, err = runner.store.ClaimOperation(ctx, operationID, "run:operation:"+operationID)
			if err != nil {
				operation, err = runner.store.GetOperation(ctx, operationID)
				if err != nil || (operation.Status != OperationConfirmed && operation.Status != OperationApplied) {
					return CanvasOperation{}, nil, err
				}
			}
		}
		runID := operation.RunID
		if runID == "" {
			runID = "run:operation:" + operationID
		}
		run, err := runner.startWorkflow(ctx, operation.ProjectID, latest.Workflow, input, runID)
		if err != nil {
			return operation, nil, err
		}
		if operation.Status != OperationApplied {
			if err := runner.store.ApplyOperation(ctx, operationID, OperationConfirmed, OperationApplied); err != nil {
				return operation, &run, err
			}
		}
		operation.Status = OperationApplied
		return operation, &run, nil
	}
	if operation.Type == OperationRetry || operation.Type == OperationCancel {
		return runner.confirmRunOperation(ctx, operationID, operation)
	}

	versionID := "workflow:operation:" + operationID
	if operation.Status == OperationPending {
		operation, err = runner.store.ClaimOperation(ctx, operationID, "")
		if err != nil {
			operation, err = runner.store.GetOperation(ctx, operationID)
			if err != nil || (operation.Status != OperationConfirmed && operation.Status != OperationApplied) {
				return CanvasOperation{}, nil, err
			}
		}
	}
	_, err = runner.store.UpdateProject(operation.ProjectID, false, func(project *Project) error {
		for _, version := range project.WorkflowVersions {
			if version.ID == versionID {
				return nil
			}
		}
		latest, err := currentWorkflow(*project)
		if err != nil {
			return err
		}
		updated, err := applyWorkflowOperation(latest.Workflow, operation)
		if err != nil {
			return err
		}
		if err := runner.catalog.ValidateDraft(updated); err != nil {
			return err
		}
		version := WorkflowVersion{ID: versionID, ProjectID: operation.ProjectID, Revision: len(project.WorkflowVersions) + 1, Workflow: cloneWorkflow(updated)}
		project.WorkflowVersions = append(project.WorkflowVersions, version)
		project.CurrentWorkflowVersion = version.ID
		return nil
	})
	if err != nil {
		return operation, nil, err
	}
	if operation.Status != OperationApplied {
		if err := runner.store.ApplyOperation(ctx, operationID, OperationConfirmed, OperationApplied); err != nil {
			return operation, nil, err
		}
	}
	operation.Status = OperationApplied
	return operation, nil, nil
}

func currentWorkflow(project Project) (WorkflowVersion, error) {
	if project.CurrentWorkflowVersion != "" {
		for _, version := range project.WorkflowVersions {
			if version.ID == project.CurrentWorkflowVersion {
				return version, nil
			}
		}
		return WorkflowVersion{}, fmt.Errorf("current workflow version not found: %s", project.CurrentWorkflowVersion)
	}
	if len(project.WorkflowVersions) == 0 {
		return WorkflowVersion{}, fmt.Errorf("project has no workflow: %s", project.ID)
	}
	return project.WorkflowVersions[len(project.WorkflowVersions)-1], nil
}

func (runner *Runner) confirmRunOperation(ctx context.Context, operationID string, operation CanvasOperation) (CanvasOperation, *Run, error) {
	if operation.RunID == "" {
		input, err := decode[struct {
			RunID string `json:"run_id"`
		}](operation.Payload)
		if err != nil || input.RunID == "" {
			return operation, nil, fmt.Errorf("%s operation requires run_id", operation.Type)
		}
		operation.RunID = input.RunID
	}
	if operation.Status == OperationPending {
		claimed, err := runner.store.ClaimOperation(ctx, operationID, operation.RunID)
		if err != nil {
			operation, err = runner.store.GetOperation(ctx, operationID)
			if err != nil {
				return CanvasOperation{}, nil, err
			}
			if operation.Status == OperationApplied {
				run, runErr := runner.store.Get(ctx, operation.RunID)
				return operation, &run, runErr
			}
			if operation.Status != OperationConfirmed {
				return operation, nil, err
			}
		} else {
			operation = claimed
		}
	}
	if operation.Type == OperationRetry {
		if err := runner.store.Retry(operation.RunID); err != nil {
			return operation, nil, err
		}
	} else if err := runner.Cancel(ctx, operation.RunID); err != nil {
		return operation, nil, err
	}
	if err := runner.store.ApplyOperation(ctx, operationID, OperationConfirmed, OperationApplied); err != nil {
		return operation, nil, err
	}
	operation.Status = OperationApplied
	run, err := runner.store.Get(ctx, operation.RunID)
	if err != nil {
		return operation, nil, err
	}
	if operation.Type == OperationRetry {
		err = runner.Advance(ctx, operation.RunID)
		if err != nil {
			return operation, &run, err
		}
		run, err = runner.store.Get(ctx, operation.RunID)
	}
	return operation, &run, err
}

func (runner *Runner) RejectOperation(ctx context.Context, operationID string) (CanvasOperation, error) {
	operation, err := runner.store.GetOperation(ctx, operationID)
	if err != nil {
		return CanvasOperation{}, err
	}
	if operation.Status != OperationPending {
		return CanvasOperation{}, fmt.Errorf("operation is not pending: %s", operation.Status)
	}
	if err := runner.store.ApplyOperation(ctx, operationID, OperationPending, OperationRejected); err != nil {
		return CanvasOperation{}, err
	}
	operation.Status = OperationRejected
	return operation, nil
}

// Advance executes ready nodes until the graph only contains waiting or blocked nodes.
func (runner *Runner) Advance(ctx context.Context, runID string) error {
	for {
		command, claimed, err := runner.store.ClaimSubmitted(runID)
		if err != nil {
			return err
		}
		if claimed {
			if _, err := runner.refresh(ctx, command); err != nil {
				return err
			}
			continue
		}

		command, claimed, err = runner.store.ClaimReady(runID)
		if err != nil {
			return err
		}
		if !claimed {
			_, err = runner.finishCancellation(ctx, runID)
			return err
		}

		if command.NodeRun.InstanceKey != "" {
			if err := runner.store.MarkSubmitStarted(command); err != nil {
				if errors.Is(err, errRunCancelRequested) {
					_, finishErr := runner.finishCancellation(ctx, runID)
					return finishErr
				}
				return err
			}
		}
		runner.record(ctx, RunEvent{Action: MonitorNodeStarted, RunID: command.RunID, NodeID: command.NodeRun.NodeID, Kind: command.NodeRun.Kind, State: Running})
		startedAt := time.Now()
		result, err := runner.withClaimHeartbeat(ctx, command, runner.handler.Start)
		if err != nil {
			if errors.Is(err, ErrClaimHeartbeat) {
				if requeueErr := runner.store.Requeue(command); requeueErr != nil {
					return errors.Join(err, requeueErr, runner.store.ReleaseClaim(command))
				}
				return err
			}
			result = failedResult(command, err)
		}
		result = runner.cancelLateSubmission(ctx, command, result)
		if err := runner.store.Apply(command, result); err != nil {
			return err
		}
		if result.SubmissionUnknown {
			runner.record(ctx, RunEvent{Action: MonitorSubmissionUnknown, RunID: command.RunID, NodeID: command.NodeRun.NodeID, Kind: command.NodeRun.Kind, State: result.State, Provider: result.Provider, Message: result.Message})
		}
		runner.record(ctx, RunEvent{Action: monitorAction(result.State), RunID: command.RunID, NodeID: command.NodeRun.NodeID, Kind: command.NodeRun.Kind, State: result.State, Provider: result.Provider, Message: result.Message, DurationMS: time.Since(startedAt).Milliseconds()})
	}
}

// Poll uses the same refresh path as callbacks and never submits a new job.
func (runner *Runner) Poll(ctx context.Context, runID string) error {
	if err := runner.Advance(ctx, runID); err != nil {
		return err
	}
	skipped := make(map[string]bool)
	for {
		command, claimed, err := runner.store.ClaimWaiting(runID, skipped)
		if err != nil {
			return err
		}
		if !claimed {
			return runner.Advance(ctx, runID)
		}
		skipped[nodeRunKey(command.NodeRun)] = true
		if _, err := runner.refresh(ctx, command); err != nil {
			return err
		}
	}
}

// Recover restores a Run after process restart without resubmitting a started job.
func (runner *Runner) Recover(ctx context.Context, runID string) error {
	if err := runner.store.Recover(runID); err != nil {
		return err
	}
	return runner.Poll(ctx, runID)
}

// Restore repairs persisted orchestration state without querying remote jobs.
func (runner *Runner) Restore(ctx context.Context, runID string) error {
	if err := runner.store.Recover(runID); err != nil {
		return err
	}
	return runner.Advance(ctx, runID)
}

// Retry clears failed nodes and advances the same immutable workflow version.
func (runner *Runner) Retry(ctx context.Context, runID string) error {
	if err := runner.store.Retry(runID); err != nil {
		return err
	}
	return runner.Advance(ctx, runID)
}

// Cancel stops orchestration for a Run and makes later callbacks no-ops.
func (runner *Runner) Cancel(ctx context.Context, runID string) error {
	if runner == nil || runner.store == nil {
		return fmt.Errorf("runner is not initialized")
	}
	if err := runner.store.RequestCancel(runID); err != nil {
		return err
	}
	run, err := runner.store.Get(ctx, runID)
	if err != nil {
		return err
	}
	if err := runner.handler.Cancel(ctx, run); err != nil {
		runner.record(ctx, RunEvent{Action: MonitorCancelFailed, RunID: runID, Message: err.Error()})
		log.Printf("cancel remote video jobs for run %s: %v", runID, err)
		return err
	}
	canceledJobs := make(map[string]string)
	for _, node := range run.NodeRuns {
		if !node.State.Terminal() && node.JobID != "" {
			canceledJobs[nodeRunKey(node)] = node.JobID
		}
	}
	return runner.store.CompleteCancelIfIdle(runID, canceledJobs)
}

func (runner *Runner) cancelLateSubmission(ctx context.Context, command Command, result Result) Result {
	if result.JobID == "" || (result.State != Waiting && result.State != Running) {
		return result
	}
	run, err := runner.store.Get(ctx, command.RunID)
	if err != nil || (!run.CancelRequested && !run.Canceled) {
		return result
	}
	node := command.NodeRun
	node.State = Waiting
	node.Provider = result.Provider
	node.JobID = result.JobID
	if err := runner.handler.Cancel(ctx, Run{NodeRuns: []NodeRun{node}}); err != nil {
		runner.record(ctx, RunEvent{Action: MonitorCancelFailed, RunID: command.RunID, NodeID: node.NodeID, Kind: node.Kind, State: Waiting, Provider: node.Provider, Message: err.Error()})
		log.Printf("cancel late submission %s/%s for run %s: %v", node.NodeID, node.InstanceKey, command.RunID, err)
		return result
	}
	result.State = Canceled
	result.Message = "run canceled"
	for index := range result.Artifacts {
		result.Artifacts[index].Status = string(Canceled)
		result.Artifacts[index].Message = result.Message
	}
	return result
}

// ProcessCallback resumes one persisted job from a durable MQ message.
func (runner *Runner) ProcessCallback(ctx context.Context, message CallbackMessage) error {
	if message.Provider == "" || message.EventID == "" || (message.JobID == "" && message.SubmitKey == "") {
		return fmt.Errorf("callback provider, event id and job id or submit key are required")
	}
	command, claimed, needsRefresh, duplicate, err := runner.store.ClaimCallback(message)
	if err != nil {
		runner.record(ctx, RunEvent{
			Action: MonitorCallback, Provider: message.Provider,
			Message: message.Provider + ":" + firstNonEmpty(message.JobID, message.SubmitKey) + " claim failed: " + err.Error(),
		})
		return err
	}
	event := RunEvent{Action: MonitorCallback, Message: message.Provider + ":" + firstNonEmpty(message.JobID, message.SubmitKey)}
	if claimed {
		event.RunID, event.NodeID, event.Kind = command.RunID, command.NodeRun.NodeID, command.NodeRun.Kind
		event.State, event.Provider = command.NodeRun.State, command.NodeRun.Provider
	}
	runner.record(ctx, event)
	if duplicate {
		return nil
	}
	if !claimed {
		if isPollingMessage(message) {
			return nil
		}
		return ErrCallbackNotReady
	}
	if needsRefresh {
		state, err := runner.refresh(ctx, command)
		if err != nil {
			return err
		}
		if state == Waiting {
			return ErrJobPending
		}
	}
	canceling, err := runner.finishCancellation(ctx, command.RunID)
	if err != nil {
		return err
	}
	if canceling {
		return runner.store.CompleteCallback(message)
	}
	if err := runner.Advance(ctx, command.RunID); err != nil {
		return err
	}
	return runner.store.CompleteCallback(message)
}

func (runner *Runner) finishCancellation(ctx context.Context, runID string) (bool, error) {
	run, err := runner.store.Get(ctx, runID)
	if err != nil {
		runner.record(ctx, RunEvent{Action: MonitorCancelFailed, RunID: runID, Message: err.Error()})
		return false, err
	}
	if !run.CancelRequested {
		return false, err
	}
	for _, node := range run.NodeRuns {
		if !node.State.Terminal() && (node.JobID != "" || node.SubmitStarted) {
			return true, nil
		}
	}
	if err := runner.store.CompleteCancel(runID); err != nil {
		runner.record(ctx, RunEvent{Action: MonitorCancelFailed, RunID: runID, Message: err.Error()})
		return true, err
	}
	return true, nil
}

func isPollingMessage(message CallbackMessage) bool {
	return strings.HasPrefix(message.EventID, "poll:") || strings.HasPrefix(message.EventID, "reconcile:")
}

func (runner *Runner) refresh(ctx context.Context, command Command) (NodeState, error) {
	startedAt := time.Now()
	result, err := runner.withClaimHeartbeat(ctx, command, runner.handler.Refresh)
	if err != nil {
		if requeueErr := runner.store.Requeue(command); requeueErr != nil {
			err = errors.Join(err, requeueErr, runner.store.ReleaseClaim(command))
		}
		runner.record(ctx, RunEvent{Action: MonitorReconcileFailed, RunID: command.RunID, NodeID: command.NodeRun.NodeID, Kind: command.NodeRun.Kind, State: Waiting, Provider: command.NodeRun.Provider, Message: err.Error(), DurationMS: time.Since(startedAt).Milliseconds()})
		return Waiting, err
	}
	if err := runner.store.Apply(command, result); err != nil {
		if requeueErr := runner.store.Requeue(command); requeueErr != nil {
			err = errors.Join(err, requeueErr, runner.store.ReleaseClaim(command))
		}
		runner.record(ctx, RunEvent{Action: MonitorReconcileFailed, RunID: command.RunID, NodeID: command.NodeRun.NodeID, Kind: command.NodeRun.Kind, State: Waiting, Provider: command.NodeRun.Provider, Message: err.Error(), DurationMS: time.Since(startedAt).Milliseconds()})
		return Waiting, err
	}
	runner.record(ctx, RunEvent{Action: monitorAction(result.State), RunID: command.RunID, NodeID: command.NodeRun.NodeID, Kind: command.NodeRun.Kind, State: result.State, Provider: result.Provider, Message: result.Message, DurationMS: time.Since(startedAt).Milliseconds()})
	_, err = runner.finishCancellation(ctx, command.RunID)
	return result.State, err
}

func (runner *Runner) withClaimHeartbeat(
	ctx context.Context,
	command Command,
	execute func(context.Context, Command) (Result, error),
) (Result, error) {
	if runner.claimHeartbeat <= 0 || command.NodeRun.ClaimToken == "" {
		return execute(ctx, command)
	}
	executeCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan struct{})
	stopped := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(runner.claimHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := runner.store.RenewClaim(command); err != nil {
					runner.record(ctx, RunEvent{Action: MonitorLeaseRenewalFailed, RunID: command.RunID, NodeID: command.NodeRun.NodeID, Kind: command.NodeRun.Kind, Message: err.Error()})
					claimErr := fmt.Errorf("%w: %v", ErrClaimHeartbeat, err)
					heartbeatErr <- claimErr
					cancel(claimErr)
					return
				}
			}
		}
	}()
	result, err := execute(executeCtx, command)
	close(done)
	<-stopped
	select {
	case claimErr := <-heartbeatErr:
		return Result{}, claimErr
	default:
	}
	return result, err
}

func (runner *Runner) record(ctx context.Context, event RunEvent) {
	if runner == nil || runner.monitor == nil {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	runner.monitor.Record(ctx, event)
}

func monitorAction(state NodeState) string {
	switch state {
	case Failed:
		return MonitorNodeFailed
	case Succeeded:
		return MonitorNodeCompleted
	default:
		return MonitorNodeWaiting
	}
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
