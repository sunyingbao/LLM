package videoagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type storeData struct {
	Revision      int64                      `json:"revision" bson:"revision"`
	Runs          map[string]Run             `json:"runs" bson:"runs"`
	Projects      map[string]Project         `json:"projects" bson:"projects"`
	Conversations map[string]Conversation    `json:"conversations" bson:"conversations"`
	Messages      map[string]Message         `json:"messages" bson:"messages"`
	Operations    map[string]CanvasOperation `json:"operations" bson:"operations"`
	OperationKeys map[string]string          `json:"operation_keys" bson:"operation_keys"`
	Receipts      map[string]time.Time       `json:"receipts" bson:"receipts"`
	MediaImports  map[string]StoredVideo     `json:"media_imports" bson:"media_imports"`
	Submissions   map[string]SubmittedJob    `json:"submissions" bson:"submissions"`
}

// Store keeps workflow state behind either the local JSON backend or MongoDB.
type Store struct {
	path    string
	backend stateBackend
	mu      sync.Mutex
}

const (
	nodeClaimTTL               = 15 * time.Minute
	mongoStoreOperationTimeout = time.Minute
)

var ErrProjectNotFound = errors.New("project not found")

func NewStore(path string) *Store {
	return &Store{path: path}
}

type stateBackend interface {
	Load() (storeData, error)
	Save(storeData) error
}

// NewMongoStore stores each workflow entity in its own Mongo collection.
func NewMongoStore(client *mongo.Client, database, collection string) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("mongo client is nil")
	}
	if database == "" || collection == "" {
		return nil, fmt.Errorf("mongo database and collection are required")
	}
	db := client.Database(database)
	backend := &mongoStateBackend{
		runs:          db.Collection(collection + "_runs"),
		projects:      db.Collection(collection + "_projects"),
		conversations: db.Collection(collection + "_conversations"),
		messages:      db.Collection(collection + "_messages"),
		operations:    db.Collection(collection + "_operations"),
		receipts:      db.Collection(collection + "_receipts"),
		mediaImports:  db.Collection(collection + "_media_imports"),
		submissions:   db.Collection(collection + "_submissions"),
	}
	if err := backend.init(); err != nil {
		return nil, err
	}
	return &Store{backend: backend}, nil
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

func (store *Store) SaveProject(_ context.Context, project Project) error {
	return store.update(func(data *storeData) error {
		data.Projects[project.ID] = project
		return nil
	})
}

func (store *Store) updateProject(projectID string, create bool, change func(*Project) error) (project Project, err error) {
	err = store.update(func(data *storeData) error {
		current, exists := data.Projects[projectID]
		if !exists {
			if !create {
				return fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
			}
			current = Project{ID: projectID, Name: projectID}
		}
		if err := change(&current); err != nil {
			return err
		}
		if current.ID != projectID {
			return fmt.Errorf("project id changed from %s to %s", projectID, current.ID)
		}
		data.Projects[projectID] = current
		project = current
		return nil
	})
	return
}

func (store *Store) GetProject(_ context.Context, projectID string) (Project, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := store.load()
	if err != nil {
		return Project{}, err
	}
	project, ok := data.Projects[projectID]
	if !ok {
		return Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
	}
	return project, nil
}

// CreateOrGetOperation makes operation creation idempotent when the caller supplies a key.
func (store *Store) CreateOrGetOperation(_ context.Context, operation CanvasOperation) (existing CanvasOperation, reused bool, err error) {
	err = store.update(func(data *storeData) error {
		existing, reused = CanvasOperation{}, false
		if operation.ID == "" {
			return fmt.Errorf("operation id is empty")
		}
		if operation.IdempotencyKey != "" {
			key := operation.ProjectID + ":" + operation.IdempotencyKey
			if operationID := data.OperationKeys[key]; operationID != "" {
				stored, ok := data.Operations[operationID]
				if !ok {
					return fmt.Errorf("idempotent operation is missing: %s", operationID)
				}
				existing, reused = stored, true
				return nil
			}
			data.OperationKeys[key] = operation.ID
		}
		if _, exists := data.Operations[operation.ID]; exists {
			return fmt.Errorf("operation already exists: %s", operation.ID)
		}
		data.Operations[operation.ID] = operation
		existing = operation
		return nil
	})
	return
}

func (store *Store) SaveConversation(_ context.Context, conversation Conversation) error {
	return store.update(func(data *storeData) error {
		if conversation.ID == "" || conversation.ProjectID == "" {
			return fmt.Errorf("conversation id and project id are required")
		}
		data.Conversations[conversation.ID] = conversation
		return nil
	})
}

func (store *Store) GetConversation(_ context.Context, conversationID string) (Conversation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := store.load()
	if err != nil {
		return Conversation{}, err
	}
	conversation, ok := data.Conversations[conversationID]
	if !ok {
		return Conversation{}, fmt.Errorf("conversation not found: %s", conversationID)
	}
	return conversation, nil
}

func (store *Store) GetProjectSession(_ context.Context, projectID string) (ProjectSession, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := store.load()
	if err != nil {
		return ProjectSession{}, err
	}
	result := ProjectSession{}
	for _, conversation := range data.Conversations {
		if conversation.ProjectID != projectID || (result.Conversation != nil && !conversation.CreatedAt.After(result.Conversation.CreatedAt)) {
			continue
		}
		current := conversation
		result.Conversation = &current
	}
	var latestOperation time.Time
	for _, operation := range data.Operations {
		if operation.ProjectID == projectID && operation.RunID != "" && operation.CreatedAt.After(latestOperation) {
			result.RunID = operation.RunID
			latestOperation = operation.CreatedAt
		}
	}
	return result, nil
}

func (store *Store) SaveMessage(_ context.Context, message Message) error {
	return store.update(func(data *storeData) error {
		if message.ID == "" || message.ConversationID == "" {
			return fmt.Errorf("message id and conversation id are required")
		}
		data.Messages[message.ID] = message
		return nil
	})
}

func (store *Store) FindAgentChatReply(_ context.Context, projectID, idempotencyKey string) (AgentChatReply, string, bool, error) {
	if idempotencyKey == "" {
		return AgentChatReply{}, "", false, nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := store.load()
	if err != nil {
		return AgentChatReply{}, "", false, err
	}
	conversations := make(map[string]Conversation)
	for id, conversation := range data.Conversations {
		if conversation.ProjectID == projectID {
			conversations[id] = conversation
		}
	}
	var userText string
	var assistant *Message
	for _, message := range data.Messages {
		if message.IdempotencyKey != idempotencyKey {
			continue
		}
		if _, exists := conversations[message.ConversationID]; !exists {
			continue
		}
		switch message.Role {
		case "user":
			userText = messageText(message)
		case "assistant":
			current := message
			assistant = &current
		}
	}
	if assistant == nil {
		return AgentChatReply{}, userText, false, nil
	}
	reply := AgentChatReply{Conversation: conversations[assistant.ConversationID], Messages: []Message{*assistant}}
	for _, part := range assistant.Parts {
		if part.OperationID == "" {
			continue
		}
		operation, exists := data.Operations[part.OperationID]
		if !exists {
			return AgentChatReply{}, userText, false, fmt.Errorf("chat operation not found: %s", part.OperationID)
		}
		reply.Operation = &operation
		break
	}
	return reply, userText, true, nil
}

func (store *Store) GetImportedVideo(_ context.Context, jobID string) (StoredVideo, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := store.load()
	if err != nil {
		return StoredVideo{}, false, err
	}
	video, ok := data.MediaImports[jobID]
	return video, ok, nil
}

func (store *Store) SaveImportedVideo(_ context.Context, jobID string, video StoredVideo) error {
	if jobID == "" {
		return fmt.Errorf("video import job id is empty")
	}
	return store.update(func(data *storeData) error {
		if existing, ok := data.MediaImports[jobID]; ok && existing != video {
			return fmt.Errorf("video import result changed for job: %s", jobID)
		}
		data.MediaImports[jobID] = video
		return nil
	})
}

func (store *Store) SaveSubmission(_ context.Context, submitKey string, job SubmittedJob) error {
	if submitKey == "" || (job.JobID == "" && job.Status == nil) {
		return fmt.Errorf("submit key and job result are required")
	}
	return store.update(func(data *storeData) error {
		if existing, ok := data.Submissions[submitKey]; ok && !reflect.DeepEqual(existing, job) {
			return fmt.Errorf("submission result changed for key: %s", submitKey)
		}
		data.Submissions[submitKey] = job
		return nil
	})
}

func (store *Store) GetSubmission(_ context.Context, submitKey string) (SubmittedJob, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := store.load()
	if err != nil {
		return SubmittedJob{}, false, err
	}
	job, ok := data.Submissions[submitKey]
	return job, ok, nil
}

func (store *Store) ListMessages(_ context.Context, conversationID string) ([]Message, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := store.load()
	if err != nil {
		return nil, err
	}
	messages := make([]Message, 0)
	for _, message := range data.Messages {
		if message.ConversationID == conversationID {
			messages = append(messages, message)
		}
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].CreatedAt.Before(messages[j].CreatedAt) })
	return messages, nil
}

func (store *Store) GetOperation(_ context.Context, operationID string) (CanvasOperation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := store.load()
	if err != nil {
		return CanvasOperation{}, err
	}
	operation, ok := data.Operations[operationID]
	if !ok {
		return CanvasOperation{}, fmt.Errorf("operation not found: %s", operationID)
	}
	return operation, nil
}

func (store *Store) claimOperation(_ context.Context, operationID, runID string) (CanvasOperation, error) {
	var operation CanvasOperation
	err := store.update(func(data *storeData) error {
		operation = CanvasOperation{}
		current, ok := data.Operations[operationID]
		if !ok {
			return fmt.Errorf("operation not found: %s", operationID)
		}
		if current.Status != OperationPending {
			return fmt.Errorf("operation is not pending: %s", current.Status)
		}
		current.Status = OperationConfirmed
		current.RunID = runID
		data.Operations[operationID] = current
		operation = current
		return nil
	})
	return operation, err
}

func (store *Store) applyOperation(_ context.Context, operationID, expected, status string) error {
	return store.update(func(data *storeData) error {
		operation, ok := data.Operations[operationID]
		if !ok {
			return fmt.Errorf("operation not found: %s", operationID)
		}
		if operation.Status != expected {
			return fmt.Errorf("operation status changed: %s", operation.Status)
		}
		operation.Status = status
		data.Operations[operationID] = operation
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
		command, claimed = Command{}, false
		run, exists := data.Runs[runID]
		if !exists {
			return fmt.Errorf("run not found: %s", runID)
		}
		if run.CancelRequested {
			return nil
		}
		for index := range run.NodeRuns {
			if !ready(run, run.NodeRuns[index]) {
				continue
			}
			run.NodeRuns[index].State = Running
			claimNode(&run.NodeRuns[index])
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
		if run.CancelRequested || run.Canceled {
			return errRunCancelRequested
		}
		index := findNodeRun(run, command.NodeRun)
		if index < 0 || run.NodeRuns[index].State != Running {
			return fmt.Errorf("node is not ready to submit: %s/%s", command.NodeRun.NodeID, command.NodeRun.InstanceKey)
		}
		if err := checkNodeClaim(run.NodeRuns[index], command); err != nil {
			return err
		}
		run.NodeRuns[index].SubmitStarted = true
		data.Runs[command.RunID] = run
		return nil
	})
}

func (store *Store) renewClaim(command Command) error {
	return store.update(func(data *storeData) error {
		run, exists := data.Runs[command.RunID]
		if !exists {
			return fmt.Errorf("run not found: %s", command.RunID)
		}
		index := findNodeRun(run, command.NodeRun)
		if index < 0 || run.NodeRuns[index].State != Running {
			return fmt.Errorf("node is not running: %s/%s", command.NodeRun.NodeID, command.NodeRun.InstanceKey)
		}
		if err := checkNodeClaim(run.NodeRuns[index], command); err != nil {
			return err
		}
		now := time.Now().UTC()
		run.NodeRuns[index].ClaimedAt = &now
		data.Runs[command.RunID] = run
		return nil
	})
}

// claimSubmitted claims a submission that may have crossed a process crash.
// Refresh must query by job ID or submit key; it must never submit again.
func (store *Store) claimSubmitted(runID string) (command Command, claimed bool, err error) {
	err = store.update(func(data *storeData) error {
		command, claimed = Command{}, false
		run, exists := data.Runs[runID]
		if !exists {
			return fmt.Errorf("run not found: %s", runID)
		}
		now := time.Now().UTC()
		for index := range run.NodeRuns {
			node := &run.NodeRuns[index]
			if node.InstanceKey != "" && node.State == Running && node.SubmitStarted && claimAvailable(*node, now) {
				claimNode(node)
				command = newCommand(run, *node)
				data.Runs[runID] = run
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
		command, claimed = Command{}, false
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
			claimNode(node)
			command = newCommand(run, *node)
			data.Runs[runID] = run
			claimed = true
			return nil
		}
		return nil
	})
	return
}

// claimCallback claims a durable job without completing callback deduplication.
func (store *Store) claimCallback(message CallbackMessage) (command Command, claimed, refresh, duplicate bool, err error) {
	err = store.update(func(data *storeData) error {
		command = Command{}
		claimed, refresh, duplicate = false, false, false
		receiptKey := callbackReceiptKey(message)
		if _, duplicate = data.Receipts[receiptKey]; duplicate {
			return nil
		}
		for runID, run := range data.Runs {
			for index := range run.NodeRuns {
				node := &run.NodeRuns[index]
				matchesJob := message.JobID != "" && node.JobID == message.JobID
				matchesSubmit := message.SubmitKey != "" && node.SubmitKey == message.SubmitKey
				if !matchesJob && !matchesSubmit {
					continue
				}
				if matchesJob && !matchesSubmit && node.Provider != message.Provider {
					continue
				}
				if run.Canceled {
					data.Receipts[receiptKey] = time.Now().UTC()
					duplicate = true
					return nil
				}
				refresh = node.State == Waiting || node.State == Running
				if refresh {
					if node.State == Running && !claimAvailable(*node, time.Now().UTC()) {
						// 同一任务已由另一条 callback 处理，当前消息可以直接确认。
						duplicate = true
						return nil
					}
					node.State = Running
					claimNode(node)
				}
				command = newCommand(run, *node)
				data.Runs[runID] = run
				claimed = true
				return nil
			}
		}
		return nil
	})
	return
}

func (store *Store) completeCallback(message CallbackMessage) error {
	return store.update(func(data *storeData) error {
		data.Receipts[callbackReceiptKey(message)] = time.Now().UTC()
		return nil
	})
}

func callbackReceiptKey(message CallbackMessage) string {
	identity := message.JobID
	if identity == "" {
		identity = "submit:" + message.SubmitKey
	}
	return message.Provider + ":" + identity + ":" + message.EventID
}

func (store *Store) apply(command Command, result Result) error {
	return store.update(func(data *storeData) error {
		run, exists := data.Runs[command.RunID]
		if !exists {
			return fmt.Errorf("run not found: %s", command.RunID)
		}
		if run.Canceled {
			return nil
		}
		index := findNodeRun(run, command.NodeRun)
		if index < 0 {
			return fmt.Errorf("node run not found: %s/%s", command.NodeRun.NodeID, command.NodeRun.InstanceKey)
		}

		node := &run.NodeRuns[index]
		if err := checkNodeClaim(*node, command); err != nil {
			return err
		}
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
		node.SubmissionUnknown = result.SubmissionUnknown
		if result.Message != "" {
			node.Message = result.Message
		}
		clearNodeClaim(node)
		for _, child := range result.Children {
			if findNodeRun(run, child) >= 0 {
				return fmt.Errorf("duplicate node instance: %s/%s", child.NodeID, child.InstanceKey)
			}
			run.NodeRuns = append(run.NodeRuns, child)
		}
		settleResourceController(&run, nodeID)
		cancelBlockedNodes(&run)
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
		if err := checkNodeClaim(run.NodeRuns[index], command); err != nil {
			return err
		}
		if run.NodeRuns[index].SubmitStarted {
			run.NodeRuns[index].State = Waiting
		} else {
			run.NodeRuns[index].State = Pending
		}
		clearNodeClaim(&run.NodeRuns[index])
		data.Runs[command.RunID] = run
		return nil
	})
}

func (store *Store) releaseClaim(command Command) error {
	return store.update(func(data *storeData) error {
		run, exists := data.Runs[command.RunID]
		if !exists {
			return fmt.Errorf("run not found: %s", command.RunID)
		}
		index := findNodeRun(run, command.NodeRun)
		if index < 0 {
			return fmt.Errorf("node run not found: %s/%s", command.NodeRun.NodeID, command.NodeRun.InstanceKey)
		}
		if run.NodeRuns[index].ClaimToken != command.NodeRun.ClaimToken {
			return nil
		}
		clearNodeClaim(&run.NodeRuns[index])
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
		now := time.Now().UTC()
		for index := range run.NodeRuns {
			node := &run.NodeRuns[index]
			if node.State != Running || !claimAvailable(*node, now) {
				continue
			}
			clearNodeClaim(node)
			if node.InstanceKey == "" {
				if !hasChildren(run, node.NodeID) {
					node.State = Pending
				}
				continue
			}
			if !node.SubmitStarted {
				node.State = Pending
			}
		}
		cancelBlockedNodes(&run)
		data.Runs[runID] = run
		return nil
	})
}

func claimNode(node *NodeRun) {
	node.ClaimToken = newID("claim")
	now := time.Now().UTC()
	node.ClaimedAt = &now
}

func claimAvailable(node NodeRun, now time.Time) bool {
	return node.ClaimToken == "" || node.ClaimedAt == nil || now.Sub(*node.ClaimedAt) >= nodeClaimTTL
}

func checkNodeClaim(node NodeRun, command Command) error {
	if node.ClaimToken == command.NodeRun.ClaimToken {
		return nil
	}
	return fmt.Errorf("node claim is no longer owned: %s/%s", node.NodeID, node.InstanceKey)
}

func clearNodeClaim(node *NodeRun) {
	node.ClaimToken = ""
	node.ClaimedAt = nil
}

func (store *Store) retry(runID string) error {
	return store.update(func(data *storeData) error {
		run, exists := data.Runs[runID]
		if !exists {
			return fmt.Errorf("run not found: %s", runID)
		}
		if run.Canceled {
			return fmt.Errorf("run is canceled: %s", runID)
		}
		for _, node := range run.NodeRuns {
			if node.State == Failed && node.SubmissionUnknown {
				return fmt.Errorf("retry is unsafe because submission outcome is unknown: %s/%s", node.NodeID, node.InstanceKey)
			}
		}
		resumeNodes := make(map[string]bool)
		for index := range run.NodeRuns {
			node := &run.NodeRuns[index]
			if node.State != Failed && node.State != Canceled {
				continue
			}
			if node.InstanceKey != "" {
				resumeNodes[node.NodeID] = true
			}
			node.State = Pending
			node.JobID = ""
			node.Provider = ""
			node.Message = ""
			node.SubmissionUnknown = false
			node.FallbackSubmitted = false
			if node.InstanceKey == "" {
				node.Output = nil
			} else {
				node.Attempt++
				node.SubmitKey = fmt.Sprintf("%s:attempt-%d", newSubmitKey(run.ID, node.NodeID, node.InstanceKey), node.Attempt)
			}
			node.Artifacts = nil
			node.SubmitStarted = false
			clearNodeClaim(node)
		}
		for index := range run.NodeRuns {
			node := &run.NodeRuns[index]
			if node.InstanceKey == "" && resumeNodes[node.NodeID] {
				node.State = Running
				node.Message = ""
			}
		}
		data.Runs[runID] = run
		return nil
	})
}

func (store *Store) requestCancel(runID string) error {
	return store.update(func(data *storeData) error {
		run, exists := data.Runs[runID]
		if !exists {
			return fmt.Errorf("run not found: %s", runID)
		}
		if run.Canceled || run.CancelRequested {
			return nil
		}
		if run.terminal() {
			return fmt.Errorf("run is already finished: %s", runID)
		}
		run.CancelRequested = true
		for index := range run.NodeRuns {
			if run.NodeRuns[index].State.terminal() {
				continue
			}
			run.NodeRuns[index].Message = "cancel requested"
		}
		data.Runs[runID] = run
		return nil
	})
}

func (store *Store) completeCancel(runID string) error {
	return store.update(func(data *storeData) error {
		run, exists := data.Runs[runID]
		if !exists {
			return fmt.Errorf("run not found: %s", runID)
		}
		completeCanceledRun(&run)
		data.Runs[runID] = run
		return nil
	})
}

func (store *Store) completeCancelIfIdle(runID string, canceledJobs map[string]string) error {
	return store.update(func(data *storeData) error {
		run, exists := data.Runs[runID]
		if !exists {
			return fmt.Errorf("run not found: %s", runID)
		}
		if run.Canceled || !run.CancelRequested {
			return nil
		}
		for _, node := range run.NodeRuns {
			if node.State.terminal() || !node.SubmitStarted {
				continue
			}
			if node.JobID == "" || canceledJobs[nodeRunKey(node)] != node.JobID {
				return nil
			}
		}
		completeCanceledRun(&run)
		data.Runs[runID] = run
		return nil
	})
}

func completeCanceledRun(run *Run) {
	run.CancelRequested = false
	run.Canceled = true
	for index := range run.NodeRuns {
		if run.NodeRuns[index].State.terminal() {
			continue
		}
		run.NodeRuns[index].State = Canceled
		run.NodeRuns[index].Message = "run canceled"
		clearNodeClaim(&run.NodeRuns[index])
	}
}

func (store *Store) update(change func(*storeData) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	for attempt := 0; attempt < 3; attempt++ {
		data, err := store.load()
		if err != nil {
			return err
		}
		if err := change(&data); err != nil {
			return err
		}
		if err := store.save(data); err != nil {
			if store.backend != nil && strings.Contains(err.Error(), "modified concurrently") && attempt < 2 {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("workflow state changed concurrently after retries")
}

func (store *Store) load() (storeData, error) {
	if store.backend != nil {
		return store.backend.Load()
	}
	if store.path == "" {
		return storeData{}, fmt.Errorf("workflow store path is empty")
	}
	payload, err := os.ReadFile(store.path)
	if os.IsNotExist(err) {
		return emptyStoreData(), nil
	}
	if err != nil {
		return storeData{}, err
	}

	data := storeData{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return storeData{}, err
	}
	return normalizeStoreData(data), nil
}

func emptyStoreData() storeData {
	return storeData{
		Runs:          map[string]Run{},
		Projects:      map[string]Project{},
		Conversations: map[string]Conversation{},
		Messages:      map[string]Message{},
		Operations:    map[string]CanvasOperation{},
		OperationKeys: map[string]string{},
		Receipts:      map[string]time.Time{},
		MediaImports:  map[string]StoredVideo{},
		Submissions:   map[string]SubmittedJob{},
	}
}

func normalizeStoreData(data storeData) storeData {
	if data.Runs == nil {
		data.Runs = map[string]Run{}
	}
	if data.Projects == nil {
		data.Projects = map[string]Project{}
	}
	if data.Conversations == nil {
		data.Conversations = map[string]Conversation{}
	}
	if data.Messages == nil {
		data.Messages = map[string]Message{}
	}
	if data.Operations == nil {
		data.Operations = map[string]CanvasOperation{}
	}
	if data.OperationKeys == nil {
		data.OperationKeys = map[string]string{}
	}
	if data.Receipts == nil {
		data.Receipts = map[string]time.Time{}
	}
	if data.MediaImports == nil {
		data.MediaImports = map[string]StoredVideo{}
	}
	if data.Submissions == nil {
		data.Submissions = map[string]SubmittedJob{}
	}
	return data
}

func (store *Store) save(data storeData) error {
	if store.backend != nil {
		return store.backend.Save(data)
	}
	data.Revision++
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

type mongoStateBackend struct {
	runs                  *mongo.Collection
	projects              *mongo.Collection
	conversations         *mongo.Collection
	messages              *mongo.Collection
	operations            *mongo.Collection
	receipts              *mongo.Collection
	mediaImports          *mongo.Collection
	submissions           *mongo.Collection
	loaded                storeData
	runRevisions          map[string]int64
	projectRevisions      map[string]int64
	conversationRevisions map[string]int64
	messageRevisions      map[string]int64
	operationRevisions    map[string]int64
	receiptRevisions      map[string]int64
	mediaImportRevisions  map[string]int64
	submissionRevisions   map[string]int64
}

type mongoValueDocument[T any] struct {
	ID       string `bson:"_id"`
	Revision int64  `bson:"revision"`
	Value    T      `bson:"value"`
}

type mongoOperationDocument struct {
	ID             string          `bson:"_id"`
	Revision       int64           `bson:"revision"`
	ProjectID      string          `bson:"project_id"`
	IdempotencyKey string          `bson:"idempotency_key,omitempty"`
	Value          CanvasOperation `bson:"value"`
}

type mongoReceiptDocument struct {
	ID        string    `bson:"_id"`
	Revision  int64     `bson:"revision"`
	CreatedAt time.Time `bson:"created_at"`
}

func (backend *mongoStateBackend) init() error {
	ctx, cancel := context.WithTimeout(context.Background(), mongoStoreOperationTimeout)
	defer cancel()
	_, err := backend.operations.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "project_id", Value: 1}, {Key: "idempotency_key", Value: 1}},
		Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{
			"idempotency_key": bson.M{"$exists": true},
		}),
	})
	return err
}

func (backend *mongoStateBackend) Load() (storeData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mongoStoreOperationTimeout)
	defer cancel()
	data := emptyStoreData()
	var err error
	if data.Runs, backend.runRevisions, err = loadMongoValues[Run](ctx, backend.runs); err != nil {
		return storeData{}, err
	}
	if data.Projects, backend.projectRevisions, err = loadMongoValues[Project](ctx, backend.projects); err != nil {
		return storeData{}, err
	}
	if data.Conversations, backend.conversationRevisions, err = loadMongoValues[Conversation](ctx, backend.conversations); err != nil {
		return storeData{}, err
	}
	if data.Messages, backend.messageRevisions, err = loadMongoValues[Message](ctx, backend.messages); err != nil {
		return storeData{}, err
	}
	if data.Operations, data.OperationKeys, backend.operationRevisions, err = backend.loadOperations(ctx); err != nil {
		return storeData{}, err
	}
	if data.Receipts, backend.receiptRevisions, err = backend.loadReceipts(ctx); err != nil {
		return storeData{}, err
	}
	if data.MediaImports, backend.mediaImportRevisions, err = loadMongoValues[StoredVideo](ctx, backend.mediaImports); err != nil {
		return storeData{}, err
	}
	if data.Submissions, backend.submissionRevisions, err = loadMongoValues[SubmittedJob](ctx, backend.submissions); err != nil {
		return storeData{}, err
	}
	backend.loaded, err = cloneStoreData(data)
	if err != nil {
		return storeData{}, err
	}
	return data, nil
}

func cloneStoreData(data storeData) (storeData, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return storeData{}, err
	}
	cloned := storeData{}
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return storeData{}, err
	}
	return normalizeStoreData(cloned), nil
}

func (backend *mongoStateBackend) Save(data storeData) error {
	ctx, cancel := context.WithTimeout(context.Background(), mongoStoreOperationTimeout)
	defer cancel()
	var err error
	if backend.runRevisions, err = syncMongoValues(ctx, backend.runs, backend.loaded.Runs, data.Runs, backend.runRevisions); err != nil {
		return err
	}
	if backend.projectRevisions, err = syncMongoValues(ctx, backend.projects, backend.loaded.Projects, data.Projects, backend.projectRevisions); err != nil {
		return err
	}
	if backend.conversationRevisions, err = syncMongoValues(ctx, backend.conversations, backend.loaded.Conversations, data.Conversations, backend.conversationRevisions); err != nil {
		return err
	}
	if backend.messageRevisions, err = syncMongoValues(ctx, backend.messages, backend.loaded.Messages, data.Messages, backend.messageRevisions); err != nil {
		return err
	}
	if err := backend.syncOperations(ctx, backend.loaded.Operations, data.Operations); err != nil {
		return err
	}
	if err := backend.syncReceipts(ctx, backend.loaded.Receipts, data.Receipts); err != nil {
		return err
	}
	if backend.mediaImportRevisions, err = syncMongoValues(ctx, backend.mediaImports, backend.loaded.MediaImports, data.MediaImports, backend.mediaImportRevisions); err != nil {
		return err
	}
	if backend.submissionRevisions, err = syncMongoValues(ctx, backend.submissions, backend.loaded.Submissions, data.Submissions, backend.submissionRevisions); err != nil {
		return err
	}
	backend.loaded = data
	return nil
}

func loadMongoValues[T any](ctx context.Context, collection *mongo.Collection) (map[string]T, map[string]int64, error) {
	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		return nil, nil, err
	}
	defer cursor.Close(ctx)
	values := make(map[string]T)
	revisions := make(map[string]int64)
	for cursor.Next(ctx) {
		var document mongoValueDocument[T]
		if err := cursor.Decode(&document); err != nil {
			return nil, nil, err
		}
		values[document.ID] = document.Value
		revisions[document.ID] = document.Revision
	}
	return values, revisions, cursor.Err()
}

func syncMongoValues[T any](ctx context.Context, collection *mongo.Collection, before, after map[string]T, revisions map[string]int64) (map[string]int64, error) {
	next := make(map[string]int64, len(revisions))
	for id, revision := range revisions {
		next[id] = revision
	}
	for id, value := range after {
		if previous, exists := before[id]; exists && reflect.DeepEqual(previous, value) {
			continue
		}
		revision := revisions[id]
		result, err := collection.ReplaceOne(ctx, bson.M{"_id": id, "revision": revision}, mongoValueDocument[T]{ID: id, Revision: revision + 1, Value: value}, options.Replace().SetUpsert(revision == 0))
		if mongo.IsDuplicateKeyError(err) || (err == nil && result.MatchedCount == 0 && result.UpsertedCount == 0) {
			return nil, fmt.Errorf("mongo workflow state was modified concurrently")
		}
		if err != nil {
			return nil, err
		}
		next[id] = revision + 1
	}
	for id := range before {
		if _, exists := after[id]; exists {
			continue
		}
		result, err := collection.DeleteOne(ctx, bson.M{"_id": id, "revision": revisions[id]})
		if err != nil {
			return nil, err
		}
		if result.DeletedCount != 1 {
			return nil, fmt.Errorf("mongo workflow state was modified concurrently")
		}
		delete(next, id)
	}
	return next, nil
}

func (backend *mongoStateBackend) loadOperations(ctx context.Context) (map[string]CanvasOperation, map[string]string, map[string]int64, error) {
	cursor, err := backend.operations.Find(ctx, bson.M{})
	if err != nil {
		return nil, nil, nil, err
	}
	defer cursor.Close(ctx)
	operations := make(map[string]CanvasOperation)
	keys := make(map[string]string)
	revisions := make(map[string]int64)
	for cursor.Next(ctx) {
		var document mongoOperationDocument
		if err := cursor.Decode(&document); err != nil {
			return nil, nil, nil, err
		}
		operations[document.ID] = document.Value
		revisions[document.ID] = document.Revision
		if document.IdempotencyKey != "" {
			keys[document.ProjectID+":"+document.IdempotencyKey] = document.ID
		}
	}
	return operations, keys, revisions, cursor.Err()
}

func (backend *mongoStateBackend) syncOperations(ctx context.Context, before, after map[string]CanvasOperation) error {
	for id, operation := range after {
		if previous, exists := before[id]; exists && reflect.DeepEqual(previous, operation) {
			continue
		}
		revision := backend.operationRevisions[id]
		document := mongoOperationDocument{ID: id, Revision: revision + 1, ProjectID: operation.ProjectID, IdempotencyKey: operation.IdempotencyKey, Value: operation}
		result, err := backend.operations.ReplaceOne(ctx, bson.M{"_id": id, "revision": revision}, document, options.Replace().SetUpsert(revision == 0))
		if mongo.IsDuplicateKeyError(err) || (err == nil && result.MatchedCount == 0 && result.UpsertedCount == 0) {
			return fmt.Errorf("mongo workflow state was modified concurrently")
		}
		if err != nil {
			return err
		}
		backend.operationRevisions[id] = revision + 1
	}
	for id := range before {
		if _, exists := after[id]; !exists {
			if _, err := backend.operations.DeleteOne(ctx, bson.M{"_id": id, "revision": backend.operationRevisions[id]}); err != nil {
				return err
			}
			delete(backend.operationRevisions, id)
		}
	}
	return nil
}

func (backend *mongoStateBackend) loadReceipts(ctx context.Context) (map[string]time.Time, map[string]int64, error) {
	cursor, err := backend.receipts.Find(ctx, bson.M{})
	if err != nil {
		return nil, nil, err
	}
	defer cursor.Close(ctx)
	receipts := make(map[string]time.Time)
	revisions := make(map[string]int64)
	for cursor.Next(ctx) {
		var document mongoReceiptDocument
		if err := cursor.Decode(&document); err != nil {
			return nil, nil, err
		}
		receipts[document.ID] = document.CreatedAt
		revisions[document.ID] = document.Revision
	}
	return receipts, revisions, cursor.Err()
}

func (backend *mongoStateBackend) syncReceipts(ctx context.Context, before, after map[string]time.Time) error {
	for id, createdAt := range after {
		if previous, exists := before[id]; exists && previous.Equal(createdAt) {
			continue
		}
		revision := backend.receiptRevisions[id]
		document := mongoReceiptDocument{ID: id, Revision: revision + 1, CreatedAt: createdAt}
		result, err := backend.receipts.ReplaceOne(ctx, bson.M{"_id": id, "revision": revision}, document, options.Replace().SetUpsert(revision == 0))
		if mongo.IsDuplicateKeyError(err) || (err == nil && result.MatchedCount == 0 && result.UpsertedCount == 0) {
			return fmt.Errorf("mongo workflow state was modified concurrently")
		}
		if err != nil {
			return err
		}
		backend.receiptRevisions[id] = revision + 1
	}
	return nil
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
				if artifact.Status == string(Succeeded) && (edge.FromPort == "" || artifact.Kind == edge.FromPort) {
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
	if run.NodeRuns[controllerIndex].Kind == PreviewNode || run.NodeRuns[controllerIndex].Kind == FinalVideoNode {
		for _, child := range children {
			if child.State == Failed {
				run.NodeRuns[controllerIndex].State = Failed
				run.NodeRuns[controllerIndex].Message = child.Message
				return
			}
		}
	}
	run.NodeRuns[controllerIndex].State = Succeeded
}

func cancelBlockedNodes(run *Run) {
	if run == nil {
		return
	}
	for changed := true; changed; {
		changed = false
		for index := range run.NodeRuns {
			node := &run.NodeRuns[index]
			if node.InstanceKey != "" || node.State != Pending {
				continue
			}
			for _, edge := range run.Workflow.Edges {
				if edge.ToNodeID != node.NodeID {
					continue
				}
				dependency := controllerNode(run, edge.FromNodeID)
				if dependency == nil || (dependency.State != Failed && dependency.State != Canceled) {
					continue
				}
				node.State = Canceled
				node.Message = fmt.Sprintf("dependency %s did not succeed", edge.FromNodeID)
				changed = true
				break
			}
		}
	}
}

func controllerNode(run *Run, nodeID string) *NodeRun {
	if run == nil {
		return nil
	}
	for index := range run.NodeRuns {
		if run.NodeRuns[index].NodeID == nodeID && run.NodeRuns[index].InstanceKey == "" {
			return &run.NodeRuns[index]
		}
	}
	return nil
}
