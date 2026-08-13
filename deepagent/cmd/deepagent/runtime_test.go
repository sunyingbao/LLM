package main

import (
	"encoding/json"
	"errors"
	"testing"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/middleware/planmode"
	"eino-cli/deepagent/worker"
)

func TestLocalThreadRuntimeActiveTurnTracksSetAndClear(t *testing.T) {
	runtime := &localThreadRuntime{}
	runtime.setActiveTurn("turn_1", "msg_1")
	runtime.setActiveTurn("turn_1", "msg_2")

	active := runtime.ActiveTurn()
	if active == nil || active.TurnID != "turn_1" {
		t.Fatalf("ActiveTurn() = %+v, want turn_1", active)
	}
	if len(active.ConsumedMessageIDs) != 2 || active.ConsumedMessageIDs[0] != "msg_1" || active.ConsumedMessageIDs[1] != "msg_2" {
		t.Fatalf("ActiveTurn().ConsumedMessageIDs = %+v", active.ConsumedMessageIDs)
	}

	runtime.clearActiveTurn("other_turn")
	if runtime.ActiveTurn() == nil {
		t.Fatal("ActiveTurn cleared for a different turn")
	}
	runtime.clearActiveTurn("turn_1")
	if runtime.ActiveTurn() != nil {
		t.Fatalf("ActiveTurn() = %+v, want nil", runtime.ActiveTurn())
	}
}

func TestLocalThreadRuntimeResumeWhileActivePreservesActiveTurn(t *testing.T) {
	runtime := &localThreadRuntime{}
	runtime.setActiveTurn("turn_active", "msg_active")
	payload, err := json.Marshal(localResumePayload{
		TurnID:       "turn_active",
		CheckpointID: "ckpt_1",
		InterruptID:  "interrupt_1",
		RequestUserInput: &planmode.RequestUserInputResponse{
			Answers: map[string]planmode.RequestUserInputAnswer{
				"q1": {Answers: []string{"yes"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal resume payload: %v", err)
	}

	_, err = runtime.resumeTurn(nil, &agentworker.Message{ID: "msg_resume", Payload: payload})
	if !errors.Is(err, agentthread.ErrThreadRunning) {
		t.Fatalf("resumeTurn() error = %v, want %v", err, agentthread.ErrThreadRunning)
	}
	active := runtime.ActiveTurn()
	if active == nil || active.TurnID != "turn_active" || len(active.ConsumedMessageIDs) != 1 || active.ConsumedMessageIDs[0] != "msg_active" {
		t.Fatalf("ActiveTurn() after failed resume = %+v, want original active turn", active)
	}
}

func TestLocalThreadRuntimeTurnRunnerConfigForMessage(t *testing.T) {
	runtime := &localThreadRuntime{
		baseRunnerConfig: &agentthread.TurnRunnerConfig{MaxSteps: 3},
		planRunnerConfig: func() *agentthread.TurnRunnerConfig {
			return &agentthread.TurnRunnerConfig{MaxSteps: 7}
		},
	}

	base := runtime.turnRunnerConfigForMessage(&agentworker.Message{})
	if base == nil || base.MaxSteps != 3 {
		t.Fatalf("base runner config=%+v, want MaxSteps=3", base)
	}
	base.MaxSteps = 4
	again := runtime.turnRunnerConfigForMessage(&agentworker.Message{})
	if again == nil || again.MaxSteps != 3 {
		t.Fatalf("base runner config mutated: %+v", again)
	}

	plan := runtime.turnRunnerConfigForMessage(&agentworker.Message{Metadata: map[string]string{localTurnModeKey: localTurnModePlan}})
	if plan == nil || plan.MaxSteps != 7 {
		t.Fatalf("plan runner config=%+v, want MaxSteps=7", plan)
	}
}
