package types

import (
	"context"
	"testing"
)

type graphStateMemoryStore struct {
	data map[string][]byte
}

func (s *graphStateMemoryStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	data, ok := s.data[key]
	return append([]byte(nil), data...), ok, nil
}

func (s *graphStateMemoryStore) Set(_ context.Context, key string, data []byte) error {
	s.data[key] = append([]byte(nil), data...)
	return nil
}

type graphStateTestStateful struct {
	value string
}

func (s *graphStateTestStateful) MarshalRuntimeState() string {
	return s.value
}

func (s *graphStateTestStateful) UnmarshalRuntimeState(data string) error {
	s.value = data
	return nil
}

func TestGraphStateSaveResumeWithMemoryStore(t *testing.T) {
	ctx := context.Background()
	store := &graphStateMemoryStore{data: map[string][]byte{}}

	persisted := &graphStateTestStateful{value: "persisted"}
	runtimeOnly := &graphStateTestStateful{value: "runtime-only"}
	state := NewGraphState(store)
	state.RegisterStateful("persisted", persisted)
	state.RegisterRuntimeOnlyStateful("runtime_only", runtimeOnly)

	ctx = NewStateContext(ctx, state)
	if StateFromContext(ctx) != state {
		t.Fatalf("StateFromContext() did not return registered state")
	}
	if got := state.GetStateful("persisted"); got != persisted {
		t.Fatalf("GetStateful() = %v, want persisted state", got)
	}
	if err := state.Save(ctx, "cp_1"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	restoredPersisted := &graphStateTestStateful{}
	restoredRuntimeOnly := &graphStateTestStateful{value: "keep"}
	restored := NewGraphState(store)
	restored.RegisterStateful("persisted", restoredPersisted)
	restored.RegisterRuntimeOnlyStateful("runtime_only", restoredRuntimeOnly)
	if err := restored.Resume(ctx, "cp_1"); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if restoredPersisted.value != "persisted" {
		t.Fatalf("restored persisted value = %q, want persisted", restoredPersisted.value)
	}
	if restoredRuntimeOnly.value != "keep" {
		t.Fatalf("runtime-only state = %q, want keep", restoredRuntimeOnly.value)
	}
}

func TestGraphStateNilStoreNoop(t *testing.T) {
	state := NewGraphState(nil)
	if err := state.Save(context.Background(), "cp"); err != nil {
		t.Fatalf("Save(nil store) error = %v", err)
	}
	if err := state.Resume(context.Background(), "cp"); err != nil {
		t.Fatalf("Resume(nil store) error = %v", err)
	}
}
