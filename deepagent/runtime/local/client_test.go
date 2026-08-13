package local_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	runtimeclient "eino-cli/deepagent/runtime"
	"eino-cli/deepagent/runtime/clienttest"
	localclient "eino-cli/deepagent/runtime/local"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/inprocess"
	localstore "eino-cli/deepagent/worker/inprocess/store"
)

func TestClientSatisfiesRuntimeClientContract(t *testing.T) {
	clienttest.Run(t, func(t *testing.T) (client runtimeclient.Client, cleanup func()) {
		databasePath := filepath.Join(t.TempDir(), "runtime.db")
		threadStore, err := localstore.OpenSQLiteThreadStateStore(databasePath, "")
		if err != nil {
			t.Fatalf("OpenSQLiteThreadStateStore() error = %v", err)
		}
		eventStore, err := localstore.OpenSQLiteEventStore(databasePath, "")
		if err != nil {
			t.Fatalf("OpenSQLiteEventStore() error = %v", err)
		}
		var sequence atomic.Int64
		worker, err := inprocess.NewWorker(inprocess.Dependencies{
			ThreadStateStore: threadStore,
			EventStore:       eventStore,
			ThreadFactory: func(ctx context.Context, state *inprocess.ThreadState, worker *inprocess.Worker) (runtime agentworker.AgentThread, err error) {
				runtime = newContractRuntime(state.ID, &sequence)
				return runtime, nil
			},
		})
		if err != nil {
			t.Fatalf("NewWorker() error = %v", err)
		}
		adapter, err := localclient.New(worker, localclient.Options{UserID: 1001})
		if err != nil {
			t.Fatalf("local.New() error = %v", err)
		}
		return adapter, func() { _ = worker.Close(context.Background()) }
	})
}

type contractRuntime struct {
	threadID string
	sequence *atomic.Int64
	items    chan agentworker.ThreadOutputItem
}

func newContractRuntime(threadID string, sequence *atomic.Int64) (runtime *contractRuntime) {
	runtime = &contractRuntime{threadID: threadID, sequence: sequence, items: make(chan agentworker.ThreadOutputItem, 8)}
	return runtime
}

func (runtime *contractRuntime) Init(ctx context.Context) (output *agentworker.ThreadOutput, err error) {
	output = &agentworker.ThreadOutput{Items: runtime.items}
	return output, nil
}

func (runtime *contractRuntime) PostMessage(ctx context.Context, message *agentworker.Message) (result *agentworker.PostMessageResult, err error) {
	turnID := ""
	switch string(message.Type) {
	case protoinput.MessageTypeResume:
		var payload protoinput.ResumeTurnPayload
		if err = json.Unmarshal(message.Payload, &payload); err != nil {
			return nil, err
		}
		turnID = payload.TurnID
		runtime.emit(turnID, "TURN_FINISHED")
	default:
		turnID = "turn-local-" + strconv.FormatInt(runtime.sequence.Add(1), 10)
		var input protoinput.UserMessage
		if err = json.Unmarshal(message.Payload, &input); err != nil {
			return nil, err
		}
		if len(input.Parts) > 0 && input.Parts[0].Text == "block" {
			runtime.emit(turnID, "INTERRUPT_REQUIRED")
			runtime.items <- agentworker.ThreadOutputItem{Yield: &agentworker.ThreadYield{Reason: "blocked", Block: &agentworker.PendingBlock{TurnID: turnID, CheckpointID: "checkpoint-1", InterruptID: "interrupt-1"}}}
		} else {
			runtime.emit(turnID, "TURN_FINISHED")
		}
	}
	return &agentworker.PostMessageResult{TurnID: turnID}, nil
}

func (runtime *contractRuntime) emit(turnID string, eventType agentworker.EventType) {
	id := runtime.sequence.Add(1)
	runtime.items <- agentworker.ThreadOutputItem{Event: &agentworker.Event{ID: "event-" + strconv.FormatInt(id, 10), ThreadID: runtime.threadID, TurnID: turnID, Type: eventType, Payload: []byte("null")}}
}

func (runtime *contractRuntime) Interrupt(ctx context.Context, req agentworker.ThreadInterruptRequest) (err error) {
	return nil
}

func (runtime *contractRuntime) ActiveTurn() (turn *agentworker.ActiveTurn) { return nil }

func (runtime *contractRuntime) Close(ctx context.Context) (err error) { return nil }
