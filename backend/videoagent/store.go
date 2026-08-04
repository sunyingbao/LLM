package videoagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type callbackEvent struct {
	Provider string    `json:"provider"`
	EventID  string    `json:"event_id"`
	JobID    string    `json:"job_id"`
	At       time.Time `json:"at"`
}

type storeData struct {
	Runs     map[string]Run           `json:"runs"`
	Receipts map[string]time.Time     `json:"receipts"`
	Inbox    map[string]callbackEvent `json:"callback_inbox"`
}

// Store keeps all workflow state in one atomically replaced JSON file.
// It is intentionally single-process; production must use a shared database.
type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (store *Store) Create(_ context.Context, run Run) error {
	return store.update(func(data *storeData) error {
		if _, exists := data.Runs[run.ID]; exists {
			return fmt.Errorf("run already exists: %s", run.ID)
		}
		data.Runs[run.ID] = run
		return nil
	})
}

func (store *Store) Get(_ context.Context, runID string) (Run, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	data, err := store.load()
	if err != nil {
		return Run{}, err
	}
	run, exists := data.Runs[runID]
	if !exists {
		return Run{}, fmt.Errorf("run not found: %s", runID)
	}
	return run, nil
}

func (store *Store) List(_ context.Context) ([]Run, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	data, err := store.load()
	if err != nil {
		return nil, err
	}
	runs := make([]Run, 0, len(data.Runs))
	for _, run := range data.Runs {
		runs = append(runs, run)
	}
	return runs, nil
}

func (store *Store) claimReady(runID string) (command Command, claimed bool, err error) {
	err = store.update(func(data *storeData) error {
		run, exists := data.Runs[runID]
		if !exists {
			return fmt.Errorf("run not found: %s", runID)
		}
		for index := range run.NodeRuns {
			if !ready(run, run.NodeRuns[index]) {
				continue
			}
			run.NodeRuns[index].State = Running
			if run.NodeRuns[index].InstanceKey != "" {
				run.NodeRuns[index].Provider = providerFor(run.NodeRuns[index].Kind)
			}
			command = newCommand(run, run.NodeRuns[index])
			data.Runs[runID] = run
			claimed = true
			return nil
		}
		return nil
	})
	return
}

func (store *Store) markSubmitStarted(command Command) error {
	return store.update(func(data *storeData) error {
		run, exists := data.Runs[command.RunID]
		if !exists {
			return fmt.Errorf("run not found: %s", command.RunID)
		}
		index := findNodeRun(run, command.NodeRun)
		if index < 0 || run.NodeRuns[index].State != Running {
			return fmt.Errorf("node is not ready to submit: %s/%s", command.NodeRun.NodeID, command.NodeRun.InstanceKey)
		}
		run.NodeRuns[index].SubmitStarted = true
		data.Runs[command.RunID] = run
		return nil
	})
}

// claimSubmitted claims a submission that may have crossed a process crash.
// Refresh must query by job ID or submit key; it must never submit again.
func (store *Store) claimSubmitted(runID string) (command Command, claimed bool, err error) {
	err = store.update(func(data *storeData) error {
		run, exists := data.Runs[runID]
		if !exists {
			return fmt.Errorf("run not found: %s", runID)
		}
		for _, node := range run.NodeRuns {
			if node.InstanceKey != "" && node.State == Running && node.SubmitStarted {
				command = newCommand(run, node)
				claimed = true
				return nil
			}
		}
		return nil
	})
	return
}

func (store *Store) claimWaiting(runID string, skipped map[string]bool) (command Command, claimed bool, err error) {
	err = store.update(func(data *storeData) error {
		run, exists := data.Runs[runID]
		if !exists {
			return fmt.Errorf("run not found: %s", runID)
		}
		for index := range run.NodeRuns {
			node := &run.NodeRuns[index]
			if node.State != Waiting || skipped[nodeRunKey(*node)] {
				continue
			}
			node.State = Running
			command = newCommand(run, *node)
			data.Runs[runID] = run
			claimed = true
			return nil
		}
		return nil
	})
	return
}

// claimCallback stores an early callback until the submission is durable.
func (store *Store) claimCallback(provider, eventID, jobID string) (command Command, claimed bool, err error) {
	err = store.update(func(data *storeData) error {
		receiptKey := provider + ":" + eventID
		if _, duplicated := data.Receipts[receiptKey]; duplicated {
			return nil
		}
		for runID, run := range data.Runs {
			for index := range run.NodeRuns {
				node := &run.NodeRuns[index]
				if node.State != Waiting || node.Provider != provider || node.JobID != jobID {
					continue
				}
				node.State = Running
				command = newCommand(run, *node)
				data.Runs[runID] = run
				data.Receipts[receiptKey] = time.Now().UTC()
				claimed = true
				return nil
			}
		}
		data.Inbox[receiptKey] = callbackEvent{Provider: provider, EventID: eventID, JobID: jobID, At: time.Now().UTC()}
		return nil
	})
	return
}

func (store *Store) apply(command Command, result Result) error {
	return store.update(func(data *storeData) error {
		run, exists := data.Runs[command.RunID]
		if !exists {
			return fmt.Errorf("run not found: %s", command.RunID)
		}
		index := findNodeRun(run, command.NodeRun)
		if index < 0 {
			return fmt.Errorf("node run not found: %s/%s", command.NodeRun.NodeID, command.NodeRun.InstanceKey)
		}

		node := &run.NodeRuns[index]
		nodeID := node.NodeID
		node.State = result.State
		if result.Provider != "" {
			node.Provider = result.Provider
		}
		if result.ClearJobID {
			node.JobID = ""
		}
		if result.JobID != "" {
			node.JobID = result.JobID
		}
		if result.Artifacts != nil {
			node.Artifacts = result.Artifacts
		}
		if result.FallbackSubmitted {
			node.FallbackSubmitted = true
		}
		if result.ResetSubmission {
			node.SubmitStarted = false
		}
		if result.Message != "" {
			node.Message = result.Message
		}
		for _, child := range result.Children {
			if findNodeRun(run, child) >= 0 {
				return fmt.Errorf("duplicate node instance: %s/%s", child.NodeID, child.InstanceKey)
			}
			run.NodeRuns = append(run.NodeRuns, child)
		}
		consumeCallbackInbox(data, node)
		settleResourceController(&run, nodeID)
		data.Runs[command.RunID] = run
		return nil
	})
}

func (store *Store) requeue(command Command) error {
	return store.update(func(data *storeData) error {
		run, exists := data.Runs[command.RunID]
		if !exists {
			return fmt.Errorf("run not found: %s", command.RunID)
		}
		index := findNodeRun(run, command.NodeRun)
		if index < 0 {
			return fmt.Errorf("node run not found: %s/%s", command.NodeRun.NodeID, command.NodeRun.InstanceKey)
		}
		run.NodeRuns[index].State = Waiting
		data.Runs[command.RunID] = run
		return nil
	})
}

func (store *Store) recover(runID string) error {
	return store.update(func(data *storeData) error {
		run, exists := data.Runs[runID]
		if !exists {
			return fmt.Errorf("run not found: %s", runID)
		}
		for index := range run.NodeRuns {
			node := &run.NodeRuns[index]
			if node.State != Running {
				continue
			}
			if node.InstanceKey == "" {
				if !hasChildren(run, node.NodeID) {
					node.State = Pending
				}
				continue
			}
			if node.SubmitStarted {
				node.State = Waiting
			} else {
				node.State = Pending
			}
		}
		data.Runs[runID] = run
		return nil
	})
}

func (store *Store) update(change func(*storeData) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	data, err := store.load()
	if err != nil {
		return err
	}
	if err := change(&data); err != nil {
		return err
	}
	return store.save(data)
}

func (store *Store) load() (storeData, error) {
	if store.path == "" {
		return storeData{}, fmt.Errorf("workflow store path is empty")
	}
	payload, err := os.ReadFile(store.path)
	if os.IsNotExist(err) {
		return storeData{Runs: map[string]Run{}, Receipts: map[string]time.Time{}, Inbox: map[string]callbackEvent{}}, nil
	}
	if err != nil {
		return storeData{}, err
	}

	data := storeData{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return storeData{}, err
	}
	if data.Runs == nil {
		data.Runs = map[string]Run{}
	}
	if data.Receipts == nil {
		data.Receipts = map[string]time.Time{}
	}
	if data.Inbox == nil {
		data.Inbox = map[string]callbackEvent{}
	}
	return data, nil
}

func (store *Store) save(data storeData) error {
	if err := os.MkdirAll(filepath.Dir(store.path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	temporaryPath := store.path + ".tmp"
	if err := os.WriteFile(temporaryPath, payload, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func consumeCallbackInbox(data *storeData, node *NodeRun) {
	if data == nil || node == nil || node.Provider == "" || node.JobID == "" {
		return
	}
	for receiptKey, event := range data.Inbox {
		if event.Provider != node.Provider || event.JobID != node.JobID {
			continue
		}
		if node.State == Waiting {
			node.State = Running
		}
		data.Receipts[receiptKey] = event.At
		delete(data.Inbox, receiptKey)
		return
	}
}

func newCommand(run Run, node NodeRun) Command {
	return Command{RunID: run.ID, Input: run.Input, NodeRun: node, Inputs: inputArtifacts(run, node)}
}

func providerFor(kind NodeKind) string {
	switch kind {
	case PromptTTSNode:
		return "tts"
	case PreviewNode, FinalVideoNode:
		return "video"
	default:
		return "image"
	}
}

func ready(run Run, node NodeRun) bool {
	if node.State != Pending {
		return false
	}
	if node.InstanceKey != "" {
		for _, controller := range run.NodeRuns {
			if controller.NodeID == node.NodeID && controller.InstanceKey == "" {
				return controller.State == Running
			}
		}
		return false
	}
	for _, edge := range run.Workflow.Edges {
		if edge.ToNodeID == node.NodeID && !controllerSucceeded(run, edge.FromNodeID) {
			return false
		}
	}
	return true
}

func inputArtifacts(run Run, node NodeRun) []Artifact {
	artifacts := make([]Artifact, 0)
	for _, edge := range run.Workflow.Edges {
		if edge.ToNodeID != node.NodeID || !controllerSucceeded(run, edge.FromNodeID) {
			continue
		}
		for _, dependency := range run.NodeRuns {
			if dependency.NodeID != edge.FromNodeID {
				continue
			}
			for _, artifact := range dependency.Artifacts {
				if artifact.Status == string(Succeeded) {
					artifacts = append(artifacts, artifact)
				}
			}
		}
	}
	return artifacts
}

func controllerSucceeded(run Run, nodeID string) bool {
	for _, node := range run.NodeRuns {
		if node.NodeID == nodeID && node.InstanceKey == "" {
			return node.State == Succeeded
		}
	}
	return false
}

func findNodeRun(run Run, target NodeRun) int {
	for index, node := range run.NodeRuns {
		if node.NodeID == target.NodeID && node.InstanceKey == target.InstanceKey {
			return index
		}
	}
	return -1
}

func nodeRunKey(node NodeRun) string {
	return node.NodeID + ":" + node.InstanceKey
}

func hasChildren(run Run, nodeID string) bool {
	for _, node := range run.NodeRuns {
		if node.NodeID == nodeID && node.InstanceKey != "" {
			return true
		}
	}
	return false
}

func settleResourceController(run *Run, nodeID string) {
	if run == nil {
		return
	}
	controllerIndex := -1
	children := make([]NodeRun, 0)
	for index, node := range run.NodeRuns {
		if node.NodeID != nodeID {
			continue
		}
		if node.InstanceKey == "" {
			controllerIndex = index
		} else {
			children = append(children, node)
		}
	}
	if controllerIndex < 0 || !run.NodeRuns[controllerIndex].Kind.resource() || len(children) == 0 {
		return
	}
	for _, child := range children {
		if !child.State.terminal() {
			return
		}
	}
	run.NodeRuns[controllerIndex].State = Succeeded
}
