package main

import (
	"fmt"
	"sync"

	inprocess "eino-cli/deepagent/worker/inprocess"
)

type ThreadRefRegistry struct {
	mu    sync.Mutex
	byRef map[string]map[string]string
	byID  map[string]map[string]string
}

func NewThreadRefRegistry() *ThreadRefRegistry {
	return &ThreadRefRegistry{
		byRef: make(map[string]map[string]string),
		byID:  make(map[string]map[string]string),
	}
}

func (r *ThreadRefRegistry) Register(thread *inprocess.ThreadState) {
	if thread == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureSession(thread.SessionID)
	ref := thread.ID
	if thread.ParentThreadID == "" {
		ref = "main"
	} else if existing := r.byID[thread.SessionID][thread.ID]; existing != "" {
		ref = existing
	} else {
		r.byRef[thread.SessionID][thread.ID] = thread.ID
		return
	}
	r.byRef[thread.SessionID][ref] = thread.ID
	r.byRef[thread.SessionID][thread.ID] = thread.ID
	r.byID[thread.SessionID][thread.ID] = ref
}

func (r *ThreadRefRegistry) AllocateChild(sessionID, threadID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureSession(sessionID)
	if existing := r.byID[sessionID][threadID]; existing != "" {
		return existing
	}
	ref := fmt.Sprintf("child-%d", len(r.byID[sessionID]))
	for r.byRef[sessionID][ref] != "" {
		ref = fmt.Sprintf("child-%d", len(r.byRef[sessionID])+1)
	}
	r.byRef[sessionID][ref] = threadID
	r.byRef[sessionID][threadID] = threadID
	r.byID[sessionID][threadID] = ref
	return ref
}

func (r *ThreadRefRegistry) Resolve(sessionID, target string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if refs := r.byRef[sessionID]; refs != nil {
		id := refs[target]
		return id, id != ""
	}
	return "", false
}

func (r *ThreadRefRegistry) Ref(thread *inprocess.ThreadState) string {
	if thread == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[thread.SessionID][thread.ID]
}

func (r *ThreadRefRegistry) ensureSession(sessionID string) {
	if r.byRef[sessionID] == nil {
		r.byRef[sessionID] = make(map[string]string)
	}
	if r.byID[sessionID] == nil {
		r.byID[sessionID] = make(map[string]string)
	}
}
