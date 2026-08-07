package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var errJobStateConflict = errors.New("job state changed before save")

type LocalJob struct {
	ID        string    `json:"job_id"`
	Kind      NodeKind  `json:"kind"`
	SubmitKey string    `json:"submit_key"`
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

type fileJobBackend struct {
	path string
}

// LocalJobs keeps task submission and delivery semantics independent of storage.
type LocalJobs struct {
	backend jobStateBackend
	mu      sync.Mutex
}

func NewLocalJobs(path string) *LocalJobs {
	return &LocalJobs{backend: fileJobBackend{path: path}}
}

func (jobs *LocalJobs) Submit(kind NodeKind, submitKey string) (job LocalJob, created bool, err error) {
	err = jobs.update(func(data *localJobData) error {
		job, created = LocalJob{}, false
		if jobID := data.SubmitKeys[submitKey]; jobID != "" {
			job = data.Jobs[jobID]
			return nil
		}
		job = LocalJob{ID: newID("job"), Kind: kind, SubmitKey: submitKey, Status: JobStatus{State: JobPending}}
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

	data, err := jobs.backend.Load()
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
	return submittedJob(stored), true, nil
}

func (jobs *LocalJobs) Status(jobID string) (JobStatus, error) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()

	data, err := jobs.backend.Load()
	if err != nil {
		return JobStatus{}, err
	}
	job, err := getLocalJob(data, jobID)
	if err != nil {
		return JobStatus{}, err
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
		if job.Status.State == JobPending {
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
		if job.Status.State == JobPending {
			job.Status = JobStatus{State: JobFailed, Message: "job canceled"}
			data.Jobs[jobID] = job
		}
		return nil
	})
}

func (jobs *LocalJobs) PendingDelivery() ([]string, error) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()

	data, err := jobs.backend.Load()
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
		job, err := getLocalJob(*data, jobID)
		if err != nil {
			return err
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
		data, err := jobs.backend.Load()
		if err != nil {
			return err
		}
		if err := change(&data); err != nil {
			return err
		}
		if err := jobs.backend.Save(data); err != nil {
			if errors.Is(err, errJobStateConflict) && attempt < 2 {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("mongo job state changed after retries")
}

func (backend fileJobBackend) Load() (localJobData, error) {
	payload, err := os.ReadFile(backend.path)
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

func (backend fileJobBackend) Save(data localJobData) error {
	if err := os.MkdirAll(filepath.Dir(backend.path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	temporaryPath := backend.path + ".tmp"
	if err := os.WriteFile(temporaryPath, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(temporaryPath, backend.path)
}

func emptyLocalJobData() localJobData {
	return localJobData{
		Jobs:       map[string]LocalJob{},
		SubmitKeys: map[string]string{},
	}
}

func getLocalJob(data localJobData, jobID string) (LocalJob, error) {
	job, exists := data.Jobs[jobID]
	if !exists {
		return LocalJob{}, fmt.Errorf("job not found: %s", jobID)
	}
	return job, nil
}

func submittedJob(job LocalJob) SubmittedJob {
	return SubmittedJob{Provider: localJobProvider(job.Kind), JobID: job.ID}
}

func localJobProvider(kind NodeKind) string {
	if kind == PromptTTSNode {
		return "tts"
	}
	if kind == PreviewNode || kind == FinalVideoNode {
		return "video"
	}
	return "image"
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
	jobURI := func(kind string, extension string) (string, string) {
		return "local://" + kind + "/" + job.ID + extension, "http://local/" + kind + "/" + job.ID
	}
	switch job.Kind {
	case PromptTTSNode:
		uri, url := jobURI("tts", ".mp3")
		return JobStatus{State: JobSucceeded, URI: uri, URL: url, ExampleURI: "local://tts/" + job.ID + "-example.mp3", ExampleURL: url + "/example"}
	case PreviewNode:
		uri, url := jobURI("preview", ".mp4")
		return JobStatus{State: JobSucceeded, URI: uri, URL: url}
	case FinalVideoNode:
		uri, url := jobURI("finalvideo", ".mp4")
		return JobStatus{State: JobSucceeded, URI: uri, URL: url}
	default:
		uri, url := jobURI("image", ".png")
		return JobStatus{State: JobSucceeded, URI: uri, URL: url}
	}
}
