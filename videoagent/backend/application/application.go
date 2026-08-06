package application

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"eino-cli/videoagent/backend/planning"
	"github.com/cloudwego/eino/components/model"
)

type RuntimeStatus struct {
	Mode       string `json:"mode"`
	Image      bool   `json:"image"`
	TTS        bool   `json:"tts"`
	Preview    bool   `json:"preview"`
	FinalVideo bool   `json:"finalvideo"`
}

type Application struct {
	Runner            *Runner
	Store             *Store
	Agent             ChatAgent
	runtime           RuntimeStatus
	callbackVerifier  CallbackVerifier
	callbackPublisher MessagePublisher
	callbackConsumer  MessageConsumer
	stopWorkers       context.CancelFunc
	workerWG          sync.WaitGroup
	workerMu          sync.Mutex
	workerErr         error
	pollInterval      time.Duration
	closeResources    func() error
}

func NewApplication(store *Store, clients Clients, agent ChatAgent) (*Application, error) {
	if store == nil {
		return nil, fmt.Errorf("workflow store is nil")
	}
	runner, err := NewRunner(store, clients)
	if err != nil {
		return nil, err
	}
	return &Application{
		Runner: runner,
		Store:  store,
		Agent:  agent,
		runtime: RuntimeStatus{
			Mode:       "remote",
			Image:      clients.Image != nil,
			TTS:        clients.TTS != nil,
			Preview:    clients.Video != nil,
			FinalVideo: clients.Video != nil,
		},
	}, nil
}

func (app *Application) RuntimeStatus() RuntimeStatus {
	if app == nil {
		return RuntimeStatus{}
	}
	return app.runtime
}

// UseChatModel replaces local planning and Canvas intent parsing with one injected model.
func (app *Application) UseChatModel(chatModel model.BaseChatModel) error {
	if app == nil || app.Runner == nil || app.Store == nil {
		return fmt.Errorf("application is not initialized")
	}
	planner, err := planning.NewModelPlanner(chatModel)
	if err != nil {
		return err
	}
	agent, err := NewCanvasAgent(chatModel, app.Store)
	if err != nil {
		return err
	}
	app.Runner.handler.clients.Planner = planner
	app.Agent = agent
	return nil
}

// UsePlanner replaces only the workflow planning capability.
func (app *Application) UsePlanner(planner Planner) error {
	if app == nil || app.Runner == nil {
		return fmt.Errorf("application is not initialized")
	}
	if planner == nil {
		return fmt.Errorf("planner is nil")
	}
	app.Runner.handler.clients.Planner = planner
	return nil
}

// SetMessageConsumer injects the production MQ adapter used for async callbacks.
func (app *Application) SetMessageConsumer(consumer MessageConsumer) {
	if app == nil {
		return
	}
	app.callbackConsumer = consumer
}

// SetMessagePublisher injects the production MQ publisher used by HTTP callbacks.
func (app *Application) SetMessagePublisher(publisher MessagePublisher) {
	if app == nil {
		return
	}
	app.callbackPublisher = publisher
}

// SetJobPollInterval enables polling providers that do not support callbacks.
func (app *Application) SetJobPollInterval(interval time.Duration) {
	if app == nil {
		return
	}
	app.pollInterval = interval
}

// SetCallbackVerifier injects the provider-specific callback authentication policy.
func (app *Application) SetCallbackVerifier(verifier CallbackVerifier) {
	if app == nil {
		return
	}
	app.callbackVerifier = verifier
}

// Close stops background workers and releases deployment-owned resources.
func (app *Application) Close() error {
	if app == nil {
		return nil
	}
	if app.stopWorkers != nil {
		app.stopWorkers()
	}
	app.workerWG.Wait()
	app.workerMu.Lock()
	closeErr := app.workerErr
	app.workerMu.Unlock()
	if app.closeResources != nil {
		closeErr = errors.Join(closeErr, app.closeResources())
	}
	return closeErr
}

// SetClose registers resource cleanup owned by the application builder.
func (app *Application) SetClose(closeResources func() error) {
	if app == nil {
		return
	}
	app.closeResources = closeResources
}

func EnsureProject(ctx context.Context, store *Store, projectID string) error {
	if store == nil || projectID == "" {
		return fmt.Errorf("store and project id are required")
	}
	_, err := store.UpdateProject(projectID, true, func(project *Project) error {
		if len(project.WorkflowVersions) == 0 {
			workflow := VideoWorkflow()
			version := WorkflowVersion{ID: newID("workflow"), ProjectID: projectID, Revision: 1, Workflow: cloneWorkflow(workflow)}
			project.WorkflowVersions = []WorkflowVersion{version}
			project.CurrentWorkflowVersion = version.ID
			return nil
		}

		current, err := currentWorkflow(*project)
		if err != nil {
			return err
		}
		workflow, changed := upgradeDefaultWorkflow(current.Workflow)
		if !changed {
			return nil
		}
		if err := defaultNodeCatalog().Validate(workflow); err != nil {
			return err
		}
		version := WorkflowVersion{
			ID:        newID("workflow"),
			ProjectID: projectID,
			Revision:  len(project.WorkflowVersions) + 1,
			Workflow:  workflow,
		}
		project.WorkflowVersions = append(project.WorkflowVersions, version)
		project.CurrentWorkflowVersion = version.ID
		return nil
	})
	return err
}

func upgradeDefaultWorkflow(workflow Workflow) (Workflow, bool) {
	current := cloneWorkflow(workflow)
	wanted := VideoWorkflow()
	if !hasDefaultNodes(current.Nodes, wanted.Nodes) {
		return Workflow{}, false
	}
	changed := false
	for _, edge := range wanted.Edges {
		if containsWorkflowEdge(current.Edges, edge) {
			continue
		}
		current.Edges = append(current.Edges, edge)
		changed = true
	}
	return current, changed
}

func hasDefaultNodes(nodes, wanted []WorkflowNode) bool {
	if len(nodes) != len(wanted) {
		return false
	}
	kindByID := make(map[string]NodeKind, len(nodes))
	for _, node := range nodes {
		kindByID[node.ID] = node.Kind
	}
	for _, node := range wanted {
		if kindByID[node.ID] != node.Kind {
			return false
		}
	}
	return true
}

func containsWorkflowEdge(edges []WorkflowEdge, wanted WorkflowEdge) bool {
	for _, edge := range edges {
		if edge == wanted {
			return true
		}
	}
	return false
}

// Start restores unfinished runs and starts callback, polling, and recovery workers.
func (app *Application) Start(ctx context.Context) error {
	if app == nil || app.Runner == nil || app.Store == nil {
		return fmt.Errorf("application is not initialized")
	}
	runs, err := app.Store.List(ctx)
	if err != nil {
		return err
	}
	failedRunIDs := make([]string, 0)
	for _, run := range runs {
		err := app.Runner.Restore(ctx, run.ID)
		if err == nil {
			continue
		}
		app.Runner.record(ctx, RunEvent{Action: MonitorRestoreFailed, RunID: run.ID, Message: err.Error()})
		log.Printf("restore video run %s: %v", run.ID, err)
		failedRunIDs = append(failedRunIDs, run.ID)
	}
	if app.callbackConsumer == nil && len(failedRunIDs) == 0 {
		return nil
	}
	poller, err := app.newPoller()
	if err != nil {
		return err
	}
	workerCtx, cancel := context.WithCancel(ctx)
	app.stopWorkers = cancel
	if len(failedRunIDs) > 0 {
		retryInterval := app.pollInterval
		if retryInterval <= 0 {
			retryInterval = time.Minute
		}
		app.startWorker(workerCtx, func(ctx context.Context) error {
			return app.retryRestore(ctx, failedRunIDs, retryInterval)
		})
	}
	if app.callbackConsumer != nil {
		app.startWorker(workerCtx, func(ctx context.Context) error {
			return ConsumeCallbacks(ctx, app.callbackConsumer, CallbackProcessor{Runner: app.Runner})
		})
	}
	if poller != nil {
		app.startWorker(workerCtx, poller.Run)
	}
	return nil
}

func (app *Application) newPoller() (*JobPoller, error) {
	if app.callbackConsumer == nil || app.pollInterval <= 0 {
		return nil, nil
	}
	return NewJobPoller(app.Store, app.callbackPublisher, app.pollInterval)
}

func (app *Application) startWorker(ctx context.Context, work func(context.Context) error) {
	app.workerWG.Add(1)
	go func() {
		defer app.workerWG.Done()
		err := work(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			return
		}
		app.workerMu.Lock()
		app.workerErr = errors.Join(app.workerErr, err)
		app.workerMu.Unlock()
	}()
}

func (app *Application) retryRestore(ctx context.Context, runIDs []string, interval time.Duration) error {
	pending := make(map[string]struct{}, len(runIDs))
	for _, runID := range runIDs {
		pending[runID] = struct{}{}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for len(pending) > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		for runID := range pending {
			err := app.Runner.Restore(ctx, runID)
			if err == nil {
				delete(pending, runID)
				continue
			}
			app.Runner.record(ctx, RunEvent{Action: MonitorRestoreFailed, RunID: runID, Message: err.Error()})
			log.Printf("retry restore video run %s: %v", runID, err)
		}
	}
	return nil
}
