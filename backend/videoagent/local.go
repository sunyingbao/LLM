package videoagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	Jobs       map[string]LocalJob `json:"jobs"`
	SubmitKeys map[string]string   `json:"submit_keys"`
}

// LocalJobs is the JSON-backed replacement for a task database in local mode.
type LocalJobs struct {
	path string
	mu   sync.Mutex
}

func NewLocalJobs(path string) *LocalJobs {
	return &LocalJobs{path: path}
}

func (jobs *LocalJobs) Submit(provider string, kind NodeKind, submitKey string) (job LocalJob, created bool, err error) {
	err = jobs.update(func(data *localJobData) error {
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

	data, err := jobs.load()
	if err != nil {
		return err
	}
	if err := change(&data); err != nil {
		return err
	}
	return jobs.save(data)
}

func (jobs *LocalJobs) load() (localJobData, error) {
	payload, err := os.ReadFile(jobs.path)
	if os.IsNotExist(err) {
		return localJobData{Jobs: map[string]LocalJob{}, SubmitKeys: map[string]string{}}, nil
	}
	if err != nil {
		return localJobData{}, err
	}
	data := localJobData{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return localJobData{}, err
	}
	if data.Jobs == nil {
		data.Jobs = map[string]LocalJob{}
	}
	if data.SubmitKeys == nil {
		data.SubmitKeys = map[string]string{}
	}
	return data, nil
}

func (jobs *LocalJobs) save(data localJobData) error {
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
	jobs    *LocalJobs
	runner  *Runner
	pending chan string
	done    chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
}

func NewLocalQueue(jobs *LocalJobs, runner *Runner) *LocalQueue {
	return &LocalQueue{jobs: jobs, runner: runner, pending: make(chan string, 128), done: make(chan struct{})}
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
	select {
	case <-queue.done:
		return
	default:
		close(queue.done)
		queue.wg.Wait()
	}
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
	if err := queue.runner.OnCallback(context.Background(), job.Provider, "local:"+job.ID, job.ID); err != nil {
		queue.Enqueue(jobID)
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
	return Requirement{Objective: input.Brief, Audience: "interested shoppers", Selling: []string{input.ProductName, "comfortable", "easy to style"}}, nil
}

func (clients *LocalClients) CreateClipScript(_ context.Context, requirement Requirement) (ClipScript, error) {
	return ClipScript{Title: requirement.Objective, Scenes: []Scene{{ID: "scene-1", Voiceover: "Show the product benefit", Visual: "Product close-up"}, {ID: "scene-2", Voiceover: "Invite the viewer to act", Visual: "Lifestyle usage"}}}, nil
}

func (clients *LocalClients) PlanCompetition(_ context.Context, clipScript ClipScript, _ RunInput) ([]ResourcePlan, error) {
	return []ResourcePlan{{ID: "competition-1", Prompt: clipScript.Scenes[0].Visual, Model: "local-image"}}, nil
}

func (clients *LocalClients) PlanTTS(_ context.Context, clipScript ClipScript) ([]ResourcePlan, error) {
	return []ResourcePlan{{ID: "speaker-1", Speaker: "local-narrator", Text: clipScript.Scenes[0].Voiceover}}, nil
}

func (clients *LocalClients) PlanCharacterReferences(_ context.Context, clipScript ClipScript, _ RunInput) ([]ResourcePlan, error) {
	return []ResourcePlan{{ID: "character-1", Prompt: clipScript.Scenes[1].Visual, Model: "local-image", FallbackModel: "local-image-fallback"}}, nil
}

func (clients *LocalClients) SubmitImage(_ context.Context, request ImageRequest) (SubmittedJob, error) {
	return clients.submit("image", CompetitionReferenceNode, request.SubmitKey)
}

func (clients *LocalClients) GetImage(_ context.Context, jobID string) (JobStatus, error) {
	return clients.jobs.Status(jobID)
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

func (clients *LocalClients) FindTTSBySubmitKey(_ context.Context, key string) (SubmittedJob, bool, error) {
	return clients.jobs.Find(key)
}

func (clients *LocalClients) SubmitPreview(_ context.Context, request VideoRequest) (SubmittedJob, error) {
	return clients.submit("video", PreviewNode, request.SubmitKey)
}

func (clients *LocalClients) GetPreview(_ context.Context, jobID string) (JobStatus, error) {
	return clients.jobs.Status(jobID)
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

// LocalApplication wires the local store, queue and direct local clients into a runnable service.
type LocalApplication struct {
	Runner *Runner
	Store  *Store
	Queue  *LocalQueue
}

func NewLocalApplication(dataDir string) (*LocalApplication, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("local data directory is empty")
	}
	store := NewStore(filepath.Join(dataDir, "workflow.json"))
	jobs := NewLocalJobs(filepath.Join(dataDir, "jobs.json"))
	clients := &LocalClients{jobs: jobs}
	runner, err := NewRunner(store, Clients{Planner: clients, Image: clients, TTS: clients, Video: clients, Audit: clients, Shield: clients})
	if err != nil {
		return nil, err
	}
	queue := NewLocalQueue(jobs, runner)
	clients.queue = queue
	return &LocalApplication{Runner: runner, Store: store, Queue: queue}, nil
}

func (application *LocalApplication) Start(ctx context.Context) error {
	if application == nil || application.Runner == nil || application.Store == nil || application.Queue == nil {
		return fmt.Errorf("local application is not initialized")
	}
	application.Queue.Start()
	runs, err := application.Store.List(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if err := application.Runner.Recover(ctx, run.ID); err != nil {
			return err
		}
	}
	return application.Queue.Recover()
}

func (application *LocalApplication) Close() {
	if application != nil && application.Queue != nil {
		application.Queue.Close()
	}
}
