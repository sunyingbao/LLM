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

type storeData struct {
	Runs     map[string]Run       `json:"runs"`
	Receipts map[string]time.Time `json:"receipts"`
}

// Store persists workflow state in one atomically replaced JSON file.
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

func (store *Store) claimUnconfirmed(runID string) (command Command, claimed bool, err error) {
	err = store.update(func(data *storeData) error {
		run, exists := data.Runs[runID]
		if !exists {
			return fmt.Errorf("run not found: %s", runID)
		}
		for _, node := range run.NodeRuns {
			if node.InstanceKey != "" && node.State == Running && node.Provider != "" && node.JobID == "" {
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
			if run.NodeRuns[index].State != Waiting || skipped[nodeRunKey(run.NodeRuns[index])] {
				continue
			}
			run.NodeRuns[index].State = Running
			command = newCommand(run, run.NodeRuns[index])
			data.Runs[runID] = run
			claimed = true
			return nil
		}
		return nil
	})
	return
}

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
		if result.Output != nil {
			node.Output = result.Output
		}
		if result.Artifacts != nil {
			node.Artifacts = result.Artifacts
		}
		if result.FallbackSubmitted {
			node.FallbackSubmitted = true
		}
		if result.Message != "" {
			node.Message = result.Message
		}
		for _, child := range result.Children {
			if findNodeRun(run, child) < 0 {
				run.NodeRuns = append(run.NodeRuns, child)
			}
		}
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
		return storeData{Runs: make(map[string]Run), Receipts: make(map[string]time.Time)}, nil
	}
	if err != nil {
		return storeData{}, err
	}

	data := storeData{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return storeData{}, err
	}
	if data.Runs == nil {
		data.Runs = make(map[string]Run)
	}
	if data.Receipts == nil {
		data.Receipts = make(map[string]time.Time)
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

func newCommand(run Run, node NodeRun) Command {
	return Command{RunID: run.ID, Input: run.Input, NodeRun: node, Inputs: inputArtifacts(run, node)}
}

func providerFor(kind NodeKind) string {
	if kind == PromptTTSNode {
		return "tts"
	}
	return "image"
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
		if edge.To != node.NodeID {
			continue
		}
		for _, dependency := range run.NodeRuns {
			if dependency.NodeID == edge.From && dependency.InstanceKey == "" && dependency.State != Succeeded {
				return false
			}
		}
	}
	return true
}

func inputArtifacts(run Run, node NodeRun) []Artifact {
	artifacts := make([]Artifact, 0)
	for _, edge := range run.Workflow.Edges {
		if edge.To != node.NodeID {
			continue
		}
		for _, dependency := range run.NodeRuns {
			if dependency.NodeID != edge.From || dependency.InstanceKey != "" || dependency.State != Succeeded {
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
			continue
		}
		children = append(children, node)
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
