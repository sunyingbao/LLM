package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
)

type LocalJob struct {
	ID        string    `json:"job_id"`
	Provider  string    `json:"provider"`
	Kind      NodeKind  `json:"kind"`
	SubmitKey string    `json:"submit_key"`
	State     JobState  `json:"state"`
	Status    JobStatus `json:"status"`
	Delivered bool      `json:"delivered"`
}

type localJobData struct {
	Revision   int64               `json:"revision" bson:"revision"`
	Jobs       map[string]LocalJob `json:"jobs" bson:"jobs"`
	SubmitKeys map[string]string   `json:"submit_keys" bson:"submit_keys"`
}

type jobStateBackend interface {
	Load() (localJobData, error)
	Save(localJobData) error
}

// LocalJobs keeps task submission and delivery semantics independent of storage.
type LocalJobs struct {
	path    string
	backend jobStateBackend
	mu      sync.Mutex
}

func NewLocalJobs(path string) *LocalJobs {
	return &LocalJobs{path: path}
}

func (jobs *LocalJobs) Submit(provider string, kind NodeKind, submitKey string) (job LocalJob, created bool, err error) {
	err = jobs.update(func(data *localJobData) error {
		job, created = LocalJob{}, false
		if jobID := data.SubmitKeys[submitKey]; jobID != "" {
			job = data.Jobs[jobID]
			return nil
		}
		job = LocalJob{ID: newID("job"), Provider: provider, Kind: kind, SubmitKey: submitKey, State: JobPending, Status: JobStatus{State: JobPending}}
		data.Jobs[job.ID] = job
		data.SubmitKeys[submitKey] = job.ID
		created = true
		return nil
	})
	return
}

func (jobs *LocalJobs) Find(submitKey string) (job SubmittedJob, found bool, err error) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()

	data, err := jobs.load()
	if err != nil {
		return job, false, err
	}
	jobID := data.SubmitKeys[submitKey]
	if jobID == "" {
		return job, false, nil
	}
	stored, exists := data.Jobs[jobID]
	if !exists {
		return job, false, fmt.Errorf("job not found: %s", jobID)
	}
	return SubmittedJob{Provider: stored.Provider, JobID: stored.ID}, true, nil
}

func (jobs *LocalJobs) Status(jobID string) (JobStatus, error) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()

	data, err := jobs.load()
	if err != nil {
		return JobStatus{}, err
	}
	job, exists := data.Jobs[jobID]
	if !exists {
		return JobStatus{}, fmt.Errorf("job not found: %s", jobID)
	}
	return job.Status, nil
}

func (jobs *LocalJobs) Complete(jobID string) (LocalJob, error) {
	var completed LocalJob
	err := jobs.update(func(data *localJobData) error {
		completed = LocalJob{}
		job, exists := data.Jobs[jobID]
		if !exists {
			return fmt.Errorf("job not found: %s", jobID)
		}
		if job.State == JobPending {
			job.State = JobSucceeded
			job.Status = localJobStatus(job)
			data.Jobs[jobID] = job
		}
		completed = job
		return nil
	})
	return completed, err
}

func (jobs *LocalJobs) Cancel(jobID string) error {
	return jobs.update(func(data *localJobData) error {
		job, exists := data.Jobs[jobID]
		if !exists {
			return fmt.Errorf("job not found: %s", jobID)
		}
		if job.State == JobPending {
			job.State = JobFailed
			job.Status = JobStatus{State: JobFailed, Message: "job canceled"}
			data.Jobs[jobID] = job
		}
		return nil
	})
}

func (jobs *LocalJobs) PendingDelivery() ([]string, error) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()

	data, err := jobs.load()
	if err != nil {
		return nil, err
	}
	jobIDs := make([]string, 0)
	for jobID, job := range data.Jobs {
		if !job.Delivered {
			jobIDs = append(jobIDs, jobID)
		}
	}
	return jobIDs, nil
}

func (jobs *LocalJobs) MarkDelivered(jobID string) error {
	return jobs.update(func(data *localJobData) error {
		job, exists := data.Jobs[jobID]
		if !exists {
			return fmt.Errorf("job not found: %s", jobID)
		}
		job.Delivered = true
		data.Jobs[jobID] = job
		return nil
	})
}

func (jobs *LocalJobs) update(change func(*localJobData) error) error {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()

	for attempt := 0; attempt < 3; attempt++ {
		data, err := jobs.load()
		if err != nil {
			return err
		}
		if err := change(&data); err != nil {
			return err
		}
		if err := jobs.save(data); err != nil {
			if jobs.backend != nil && strings.Contains(err.Error(), "changed before save") && attempt < 2 {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("mongo job state changed after retries")
}

func (jobs *LocalJobs) load() (localJobData, error) {
	if jobs.backend != nil {
		data, err := jobs.backend.Load()
		if err != nil {
			return localJobData{}, err
		}
		return normalizeLocalJobData(data), nil
	}

	payload, err := os.ReadFile(jobs.path)
	if os.IsNotExist(err) {
		return emptyLocalJobData(), nil
	}
	if err != nil {
		return localJobData{}, err
	}
	data := localJobData{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return localJobData{}, err
	}
	return normalizeLocalJobData(data), nil
}

func (jobs *LocalJobs) save(data localJobData) error {
	if jobs.backend != nil {
		return jobs.backend.Save(data)
	}

	if err := os.MkdirAll(filepath.Dir(jobs.path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	temporaryPath := jobs.path + ".tmp"
	if err := os.WriteFile(temporaryPath, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(temporaryPath, jobs.path)
}

func emptyLocalJobData() localJobData {
	return localJobData{
		Jobs:       map[string]LocalJob{},
		SubmitKeys: map[string]string{},
	}
}

func normalizeLocalJobData(data localJobData) localJobData {
	if data.Jobs == nil {
		data.Jobs = map[string]LocalJob{}
	}
	if data.SubmitKeys == nil {
		data.SubmitKeys = map[string]string{}
	}
	return data
}

func localJobStatus(job LocalJob) JobStatus {
	name := job.ID
	switch job.Kind {
	case PromptTTSNode:
		return JobStatus{State: JobSucceeded, URI: "local://tts/" + name + ".mp3", URL: "http://local/tts/" + name, ExampleURI: "local://tts/" + name + "-example.mp3", ExampleURL: "http://local/tts/" + name + "/example"}
	case PreviewNode:
		return JobStatus{State: JobSucceeded, URI: "local://preview/" + name + ".mp4", URL: "http://local/preview/" + name}
	case FinalVideoNode:
		return JobStatus{State: JobSucceeded, URI: "local://finalvideo/" + name + ".mp4", URL: "http://local/finalvideo/" + name}
	default:
		return JobStatus{State: JobSucceeded, URI: "local://image/" + name + ".png", URL: "http://local/image/" + name}
	}
}

// LocalQueue is a durable-job trigger. Jobs remain recoverable because LocalJobs is the source of truth.
type LocalQueue struct {
	jobs      *LocalJobs
	publisher MessagePublisher
	pending   chan string
	done      chan struct{}
	once      sync.Once
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func NewLocalQueue(jobs *LocalJobs, publisher MessagePublisher) *LocalQueue {
	return &LocalQueue{jobs: jobs, publisher: publisher, pending: make(chan string, 128), done: make(chan struct{})}
}

func (queue *LocalQueue) Start() {
	queue.once.Do(func() {
		queue.wg.Add(1)
		go func() {
			defer queue.wg.Done()
			for {
				select {
				case jobID := <-queue.pending:
					queue.process(jobID)
				case <-queue.done:
					return
				}
			}
		}()
	})
}

func (queue *LocalQueue) Close() {
	if queue == nil {
		return
	}
	queue.closeOnce.Do(func() {
		close(queue.done)
		queue.wg.Wait()
	})
}

func (queue *LocalQueue) Enqueue(jobID string) {
	select {
	case queue.pending <- jobID:
	case <-queue.done:
	}
}

func (queue *LocalQueue) Recover() error {
	jobIDs, err := queue.jobs.PendingDelivery()
	if err != nil {
		return err
	}
	for _, jobID := range jobIDs {
		queue.Enqueue(jobID)
	}
	return nil
}

func (queue *LocalQueue) process(jobID string) {
	job, err := queue.jobs.Complete(jobID)
	if err != nil {
		return
	}
	if queue.publisher == nil {
		return
	}
	message := CallbackMessage{Provider: job.Provider, EventID: "local:" + job.ID, JobID: job.ID}
	publishContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = queue.publisher.Publish(publishContext, message)
	cancel()
	if err != nil {
		select {
		case <-time.After(time.Second):
			queue.Enqueue(jobID)
		case <-queue.done:
		}
		return
	}
	_ = queue.jobs.MarkDelivered(jobID)
}

// LocalClients implements each direct model/media SDK boundary with deterministic local results.
type LocalClients struct {
	jobs  *LocalJobs
	queue *LocalQueue
}

func (clients *LocalClients) AnalyzeRequirement(_ context.Context, input RunInput) (Requirement, error) {
	markdown := fmt.Sprintf("# 需求分析\n\n商品：%s\n\n%s", input.ProductName, input.Brief)
	return Requirement{
		Objective: input.Brief,
		Audience:  "interested shoppers",
		Selling:   []string{input.ProductName, "comfortable", "easy to style"},
		Markdown:  markdown,
	}, nil
}

func (clients *LocalClients) CreateClipScript(_ context.Context, requirement Requirement, _ RunInput) (ClipScript, error) {
	return ClipScript{Title: requirement.Objective, Scenes: []Scene{{ID: "scene-1", Voiceover: "Show the product benefit", Visual: "Product close-up"}, {ID: "scene-2", Voiceover: "Invite the viewer to act", Visual: "Lifestyle usage"}}}, nil
}

func (clients *LocalClients) PlanCompetition(_ context.Context, clipScript ClipScript, _ RunInput) ([]ResourcePlan, error) {
	return []ResourcePlan{{ID: "competition-1", SceneID: clipScript.Scenes[0].ID, Prompt: clipScript.Scenes[0].Visual, Model: "local-image"}}, nil
}

func (clients *LocalClients) PlanTTS(_ context.Context, clipScript ClipScript) ([]ResourcePlan, error) {
	plans := make([]ResourcePlan, 0, len(clipScript.Scenes))
	for _, scene := range clipScript.Scenes {
		plans = append(plans, ResourcePlan{ID: "voice-" + scene.ID, SceneID: scene.ID, Speaker: "local-narrator", Text: scene.Voiceover})
	}
	return plans, nil
}

func (clients *LocalClients) PlanCharacterReferences(_ context.Context, clipScript ClipScript, _ RunInput) ([]ResourcePlan, error) {
	scene := clipScript.Scenes[len(clipScript.Scenes)-1]
	return []ResourcePlan{{ID: "character-1", SceneID: scene.ID, Prompt: scene.Visual, Model: "local-image", FallbackModel: "local-image-fallback"}}, nil
}

func (clients *LocalClients) SubmitImage(_ context.Context, request ImageRequest) (SubmittedJob, error) {
	return clients.submit("image", CompetitionReferenceNode, request.SubmitKey)
}

func (clients *LocalClients) GetImage(_ context.Context, jobID string) (JobStatus, error) {
	return clients.jobs.Status(jobID)
}

func (clients *LocalClients) CancelImage(_ context.Context, jobID string) error {
	return clients.jobs.Cancel(jobID)
}

func (clients *LocalClients) FindImageBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	return clients.jobs.Find(key)
}

func (clients *LocalClients) SubmitTTS(_ context.Context, request TTSRequest) (SubmittedJob, error) {
	return clients.submit("tts", PromptTTSNode, request.SubmitKey)
}

func (clients *LocalClients) GetTTS(_ context.Context, jobID string) (JobStatus, error) {
	return clients.jobs.Status(jobID)
}

func (clients *LocalClients) CancelTTS(_ context.Context, jobID string) error {
	return clients.jobs.Cancel(jobID)
}

func (clients *LocalClients) FindTTSBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	return clients.jobs.Find(key)
}

func (clients *LocalClients) SubmitPreview(_ context.Context, request VideoRequest) (SubmittedJob, error) {
	return clients.submit("video", PreviewNode, request.SubmitKey)
}

func (clients *LocalClients) GetPreview(_ context.Context, jobID string) (JobStatus, error) {
	return clients.jobs.Status(jobID)
}

func (clients *LocalClients) CancelVideo(_ context.Context, jobID string) error {
	return clients.jobs.Cancel(jobID)
}

func (clients *LocalClients) FindPreviewBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	return clients.jobs.Find(key)
}

func (clients *LocalClients) SubmitFinalVideo(_ context.Context, request VideoRequest) (SubmittedJob, error) {
	return clients.submit("video", FinalVideoNode, request.SubmitKey)
}

func (clients *LocalClients) GetFinalVideo(_ context.Context, jobID string) (JobStatus, error) {
	return clients.jobs.Status(jobID)
}

func (clients *LocalClients) FindFinalVideoBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	return clients.jobs.Find(key)
}

func (clients *LocalClients) CheckImage(context.Context, string) error { return nil }

func (clients *LocalClients) CheckPrompt(context.Context, string) error { return nil }

func (clients *LocalClients) submit(provider string, kind NodeKind, submitKey string) (SubmittedJob, error) {
	job, created, err := clients.jobs.Submit(provider, kind, submitKey)
	if err != nil {
		return SubmittedJob{}, err
	}
	if created && clients.queue != nil {
		clients.queue.Enqueue(job.ID)
	}
	return SubmittedJob{Provider: job.Provider, JobID: job.ID}, nil
}

// Application contains workflow dependencies shared by every deployment mode.
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
	cancelConsumer    context.CancelFunc
	consumerDone      chan error
	pollInterval      time.Duration
	pollerDone        chan error
	recoveryDone      chan error
	close             func() error
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

func (application *Application) RuntimeStatus() RuntimeStatus {
	if application == nil {
		return RuntimeStatus{}
	}
	return application.runtime
}

// UseChatModel replaces local planning and Canvas intent parsing with one injected model.
func (application *Application) UseChatModel(chatModel model.BaseChatModel) error {
	if application == nil || application.Runner == nil || application.Store == nil {
		return fmt.Errorf("application is not initialized")
	}
	planner, err := NewModelPlanner(chatModel)
	if err != nil {
		return err
	}
	agent, err := NewCanvasAgent(chatModel, application.Store)
	if err != nil {
		return err
	}
	application.Runner.handler.clients.Planner = planner
	application.Agent = agent
	return nil
}

// UsePlanner replaces only the workflow planning capability.
func (application *Application) UsePlanner(planner Planner) error {
	if application == nil || application.Runner == nil {
		return fmt.Errorf("application is not initialized")
	}
	if planner == nil {
		return fmt.Errorf("planner is nil")
	}
	application.Runner.handler.clients.Planner = planner
	return nil
}

// SetMessageConsumer injects the production MQ adapter used for async callbacks.
func (application *Application) SetMessageConsumer(consumer MessageConsumer) {
	if application != nil {
		application.callbackConsumer = consumer
	}
}

// SetMessagePublisher injects the production MQ publisher used by HTTP callbacks.
func (application *Application) SetMessagePublisher(publisher MessagePublisher) {
	if application != nil {
		application.callbackPublisher = publisher
	}
}

// SetJobPollInterval enables polling providers that do not support callbacks.
func (application *Application) SetJobPollInterval(interval time.Duration) {
	if application != nil {
		application.pollInterval = interval
	}
}

// SetCallbackVerifier injects the provider-specific callback authentication policy.
func (application *Application) SetCallbackVerifier(verifier CallbackVerifier) {
	if application != nil {
		application.callbackVerifier = verifier
	}
}

// Close releases deployment-specific resources such as a Mongo client.
func (application *Application) Close() error {
	if application == nil {
		return nil
	}
	if application.cancelConsumer != nil {
		application.cancelConsumer()
	}
	var closeErr error
	if application.consumerDone != nil {
		consumerErr := <-application.consumerDone
		application.consumerDone = nil
		application.cancelConsumer = nil
		if consumerErr != nil && !errors.Is(consumerErr, context.Canceled) {
			closeErr = errors.Join(closeErr, consumerErr)
		}
	}
	if application.pollerDone != nil {
		pollerErr := <-application.pollerDone
		application.pollerDone = nil
		if pollerErr != nil && !errors.Is(pollerErr, context.Canceled) {
			closeErr = errors.Join(closeErr, pollerErr)
		}
	}
	if application.recoveryDone != nil {
		recoveryErr := <-application.recoveryDone
		application.recoveryDone = nil
		if recoveryErr != nil && !errors.Is(recoveryErr, context.Canceled) {
			closeErr = errors.Join(closeErr, recoveryErr)
		}
	}
	if application.close != nil {
		closeErr = errors.Join(closeErr, application.close())
	}
	return closeErr
}

// SetClose registers the resource cleanup owned by the application builder.
func (application *Application) SetClose(close func() error) {
	if application != nil {
		application.close = close
	}
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
		if !containsWorkflowEdge(current.Edges, edge) {
			current.Edges = append(current.Edges, edge)
			changed = true
		}
	}
	return current, changed
}

func hasDefaultNodes(nodes, wanted []WorkflowNode) bool {
	if len(nodes) != len(wanted) {
		return false
	}
	byID := make(map[string]NodeKind, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node.Kind
	}
	for _, node := range wanted {
		if byID[node.ID] != node.Kind {
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

// Start recovers in-flight runs for an injected production application.
func (application *Application) Start(ctx context.Context) error {
	if application == nil || application.Runner == nil || application.Store == nil {
		return fmt.Errorf("application is not initialized")
	}
	runs, err := application.Store.List(ctx)
	if err != nil {
		return err
	}
	failedRuns := make([]string, 0)
	for _, run := range runs {
		if err := application.Runner.Restore(ctx, run.ID); err != nil {
			application.Runner.record(ctx, RunEvent{Action: MonitorRestoreFailed, RunID: run.ID, Message: err.Error()})
			log.Printf("restore video run %s: %v", run.ID, err)
			failedRuns = append(failedRuns, run.ID)
		}
	}
	var poller *JobPoller
	if application.callbackConsumer != nil && application.pollInterval > 0 {
		poller, err = NewJobPoller(application.Store, application.callbackPublisher, application.pollInterval)
		if err != nil {
			return err
		}
	}
	var workerCtx context.Context
	if application.callbackConsumer != nil || len(failedRuns) > 0 {
		var cancel context.CancelFunc
		workerCtx, cancel = context.WithCancel(ctx)
		application.cancelConsumer = cancel
	}
	if len(failedRuns) > 0 {
		interval := application.pollInterval
		if interval <= 0 {
			interval = time.Minute
		}
		application.recoveryDone = make(chan error, 1)
		go func() {
			application.recoveryDone <- application.retryRestore(workerCtx, failedRuns, interval)
		}()
	}
	if application.callbackConsumer != nil {
		application.consumerDone = make(chan error, 1)
		go func() {
			application.consumerDone <- ConsumeCallbacks(workerCtx, application.callbackConsumer, CallbackProcessor{Runner: application.Runner})
		}()
		if poller != nil {
			application.pollerDone = make(chan error, 1)
			go func() {
				application.pollerDone <- poller.Run(workerCtx)
			}()
		}
	}
	return nil
}

func (application *Application) retryRestore(ctx context.Context, runIDs []string, interval time.Duration) error {
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
			if err := application.Runner.Restore(ctx, runID); err != nil {
				application.Runner.record(ctx, RunEvent{Action: MonitorRestoreFailed, RunID: runID, Message: err.Error()})
				log.Printf("retry restore video run %s: %v", runID, err)
				continue
			}
			delete(pending, runID)
		}
	}
	return nil
}

// LocalApplication adds the local queue and restart recovery to Application.
type LocalApplication struct {
	*Application
	Queue *LocalQueue
}

func NewLocalApplication(dataDir string) (*LocalApplication, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("local data directory is empty")
	}
	store := NewStore(filepath.Join(dataDir, "workflow.json"))
	return newLocalApplication(dataDir, store, NewLocalJobs(filepath.Join(dataDir, "jobs.json")), nil)
}

func newLocalApplication(dataDir string, store *Store, jobs *LocalJobs, closeStore func() error) (*LocalApplication, error) {
	if dataDir == "" || store == nil || jobs == nil {
		return nil, fmt.Errorf("local data directory, store and jobs are required")
	}
	clients := &LocalClients{jobs: jobs}
	application, err := NewApplication(store, Clients{Planner: clients, Image: clients, TTS: clients, Video: clients, Audit: clients, Shield: clients}, nil)
	if err != nil {
		return nil, err
	}
	application.runtime.Mode = "local"
	application.SetCallbackVerifier(AllowAllCallbackVerifier{})
	application.SetClose(closeStore)
	queue := NewLocalQueue(jobs, nil)
	clients.queue = queue
	if err := EnsureProject(context.Background(), store, "demo"); err != nil {
		return nil, err
	}
	agent, err := NewCanvasAgent(nil, store)
	if err != nil {
		return nil, err
	}
	application.Agent = agent
	return &LocalApplication{Application: application, Queue: queue}, nil
}

// SetMessageQueue routes local job completion and provider callbacks through the same durable queue.
func (application *LocalApplication) SetMessageQueue(publisher MessagePublisher, consumer MessageConsumer) {
	if application == nil || application.Application == nil {
		return
	}
	application.SetMessagePublisher(publisher)
	application.SetMessageConsumer(consumer)
	if application.Queue != nil {
		application.Queue.publisher = publisher
	}
}

func (application *LocalApplication) Start(ctx context.Context) error {
	if application == nil || application.Runner == nil || application.Store == nil || application.Queue == nil {
		return fmt.Errorf("local application is not initialized")
	}
	if application.Queue.publisher == nil {
		return fmt.Errorf("local message publisher is not configured")
	}
	application.Queue.Start()
	if err := application.Application.Start(ctx); err != nil {
		return err
	}
	return application.Queue.Recover()
}

func (application *LocalApplication) Close() {
	if application != nil && application.Queue != nil {
		application.Queue.Close()
	}
	if application != nil && application.Application != nil {
		_ = application.Application.Close()
	}
}
