package application

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	stored, found, err := jobs.findBySubmitKey(submitKey)
	if err != nil {
		return job, false, err
	}
	if !found {
		return job, false, nil
	}
	return submittedJob(stored), true, nil
}

func (jobs *LocalJobs) Status(jobID string) (JobStatus, error) {
	job, err := jobs.get(jobID)
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

func (jobs *LocalJobs) findBySubmitKey(submitKey string) (LocalJob, bool, error) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()

	data, err := jobs.load()
	if err != nil {
		return LocalJob{}, false, err
	}
	jobID := data.SubmitKeys[submitKey]
	if jobID == "" {
		return LocalJob{}, false, nil
	}
	job, exists := data.Jobs[jobID]
	if !exists {
		return LocalJob{}, false, fmt.Errorf("job not found: %s", jobID)
	}
	return job, true, nil
}

func (jobs *LocalJobs) get(jobID string) (LocalJob, error) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()

	data, err := jobs.load()
	if err != nil {
		return LocalJob{}, err
	}
	return getLocalJob(data, jobID)
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

func getLocalJob(data localJobData, jobID string) (LocalJob, error) {
	job, exists := data.Jobs[jobID]
	if !exists {
		return LocalJob{}, fmt.Errorf("job not found: %s", jobID)
	}
	return job, nil
}

func submittedJob(job LocalJob) SubmittedJob {
	return SubmittedJob{Provider: job.Provider, JobID: job.ID}
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
