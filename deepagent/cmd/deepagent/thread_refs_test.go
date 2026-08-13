package main

import (
	"testing"

	inprocess "eino-cli/deepagent/worker/inprocess"
)

func TestThreadRefRegistryAllocateChildUpgradesRawChildRegistration(t *testing.T) {
	refs := NewThreadRefRegistry()
	refs.Register(&inprocess.ThreadState{ID: "child_thread", SessionID: "sess", ParentThreadID: "main"})
	if ref := refs.Ref(&inprocess.ThreadState{ID: "child_thread", SessionID: "sess", ParentThreadID: "main"}); ref != "" {
		t.Fatalf("Ref(child) before AllocateChild = %q, want empty canonical ref", ref)
	}
	if id, ok := refs.Resolve("sess", "child_thread"); !ok || id != "child_thread" {
		t.Fatalf("Resolve(raw child) = %q, %t; want raw id", id, ok)
	}

	ref := refs.AllocateChild("sess", "child_thread")
	if ref != "child-0" {
		t.Fatalf("AllocateChild() = %q, want child-0", ref)
	}
	if got := refs.Ref(&inprocess.ThreadState{ID: "child_thread", SessionID: "sess", ParentThreadID: "main"}); got != "child-0" {
		t.Fatalf("Ref(child) = %q, want child-0", got)
	}
}
