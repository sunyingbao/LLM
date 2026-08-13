package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/worker"
)

func TestStreamOneShotIgnoresChildTurnEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan *agentworker.Event, 2)
	done := make(chan error, 1)
	go func() {
		done <- streamOneShot(ctx, "main", events)
	}()

	events <- testEvent("ev_child_end", "child", "turn_child", agentworker.EventType(agentthread.EventTurnEnd), localEventPayload{})
	select {
	case err := <-done:
		t.Fatalf("streamOneShot returned after child TurnEnd: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	events <- testEvent("ev_main_end", "main", "turn_main", agentworker.EventType(agentthread.EventTurnEnd), localEventPayload{})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("streamOneShot() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for root TurnEnd")
	}
}

func TestStreamOneShotDoesNotPrintChildOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan *agentworker.Event, 4)

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = oldStdout }()
	done := make(chan error, 1)
	go func() {
		done <- streamOneShot(ctx, "main", events)
	}()

	events <- testEvent("ev_child_token", "child", "turn_child", agentworker.EventType(agentthread.EventLLMToken), localEventPayload{Text: "child token"})
	events <- testEvent("ev_child_tool", "child", "turn_child", agentworker.EventType(agentthread.EventToolStart), localEventPayload{Name: "child_tool"})
	events <- testEvent("ev_main_end", "main", "turn_main", agentworker.EventType(agentthread.EventTurnEnd), localEventPayload{})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("streamOneShot() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for root TurnEnd")
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	if got := string(out); strings.Contains(got, "child token") || strings.Contains(got, "child_tool") {
		t.Fatalf("streamOneShot printed child output: %q", got)
	}
}
