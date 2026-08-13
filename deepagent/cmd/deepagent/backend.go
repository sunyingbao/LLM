package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"eino-cli/deepagent/core/middleware/planmode"
	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
	"github.com/google/uuid"
)

type ThreadBinding struct {
	Thread        *inprocess.ThreadState
	Threads       []*inprocess.ThreadState
	Events        []*agentworker.Event
	SidecarEvents []*agentworker.Event
}

type localUpdate struct {
	Event  *agentworker.Event
	Thread *inprocess.ThreadState
}

type LocalAgentService struct {
	ctx            context.Context
	cfg            AppConfig
	worker         *inprocess.Worker
	refs           *ThreadRefRegistry
	toolExecPolicy *ToolExecPolicy

	mu              sync.Mutex
	watchedSessions map[string]struct{}
	updates         chan localUpdate
}

func NewLocalAgentService(ctx context.Context, cfg AppConfig, worker *inprocess.Worker, refs *ThreadRefRegistry, toolExecPolicy *ToolExecPolicy) *LocalAgentService {
	return &LocalAgentService{
		ctx:             ctx,
		cfg:             cfg,
		worker:          worker,
		refs:            refs,
		toolExecPolicy:  toolExecPolicy,
		watchedSessions: make(map[string]struct{}),
		updates:         make(chan localUpdate, 256),
	}
}

func (s *LocalAgentService) CreateRootThread(ctx context.Context, firstMessage string) (*inprocess.ThreadState, error) {
	title := chatTitleFromMessage(firstMessage)
	if title == "" {
		title = "New chat"
	}
	thread, err := s.worker.CreateThread(ctx, inprocess.CreateThreadSpec{
		UserID:    s.cfg.UserID,
		SessionID: "sess_" + uuid.NewString(),
		Title:     title,
		Profile:   inprocess.ThreadProfile{Cwd: s.cfg.WorkDir},
		Metadata:  map[string]string{"thread_role": "main"},
	})
	if err != nil {
		return nil, err
	}
	s.refs.Register(thread)
	return thread, nil
}

func (s *LocalAgentService) BindThread(ctx context.Context, threadID string) (*ThreadBinding, error) {
	thread, err := s.worker.GetThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if err := s.requireUserThread(thread); err != nil {
		return nil, err
	}
	threads, err := s.SessionThreads(ctx, thread.SessionID)
	if err != nil {
		return nil, err
	}
	events, err := s.worker.ListEvents(ctx, thread.ID, inprocess.ListEventsOptions{Limit: 200})
	if err != nil {
		return nil, err
	}
	sidecarEvents := make([]*agentworker.Event, 0)
	for _, sessionThread := range threads {
		if sessionThread == nil || sessionThread.ID == thread.ID {
			continue
		}
		events, err := s.worker.ListEvents(ctx, sessionThread.ID, inprocess.ListEventsOptions{Limit: 100})
		if err != nil {
			return nil, err
		}
		sidecarEvents = append(sidecarEvents, events...)
	}
	if err := s.WatchSession(ctx, thread.SessionID); err != nil {
		return nil, err
	}
	return &ThreadBinding{Thread: thread, Threads: threads, Events: events, SidecarEvents: sidecarEvents}, nil
}

func (s *LocalAgentService) ResolveThreadTarget(ctx context.Context, sessionID, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("missing thread target")
	}
	if s.refs != nil {
		if id, ok := s.refs.Resolve(sessionID, target); ok {
			return id, nil
		}
	}
	if sessionID != "" {
		threads, err := s.SessionThreads(ctx, sessionID)
		if err != nil {
			return "", err
		}
		matches := make([]string, 0, 1)
		for _, thread := range threads {
			if thread == nil {
				continue
			}
			if thread.ID == target {
				return thread.ID, nil
			}
			if strings.HasPrefix(thread.ID, target) {
				matches = append(matches, thread.ID)
			}
		}
		switch len(matches) {
		case 1:
			return matches[0], nil
		case 0:
		default:
			return "", fmt.Errorf("thread target %q is ambiguous", target)
		}
	} else if thread, err := s.worker.GetThread(ctx, target); err == nil {
		if err := s.requireUserThread(thread); err != nil {
			return "", err
		}
		return thread.ID, nil
	}
	return "", fmt.Errorf("thread target %q not found", target)
}

func (s *LocalAgentService) SendUserMessage(ctx context.Context, threadID, content string, metadata ...map[string]string) (string, error) {
	thread, err := s.worker.GetThread(ctx, threadID)
	if err != nil {
		return "", err
	}
	if err := s.requireUserThread(thread); err != nil {
		return "", err
	}
	messageID := newMessageID()
	var meta map[string]string
	if len(metadata) > 0 {
		meta = maps.Clone(metadata[0])
	}
	err = s.worker.PostMessage(ctx, threadID, &agentworker.Message{
		ID:       messageID,
		Sender:   &agentworker.Sender{Type: agentworker.SenderTypeUser, ID: fmt.Sprintf("%d", s.cfg.UserID)},
		Type:     agentworker.MessageTypeText,
		Payload:  []byte(content),
		Metadata: meta,
	})
	return messageID, err
}

func (s *LocalAgentService) Approve(ctx context.Context, pending pendingApproval, decision localApprovalDecision) error {
	if pending.ThreadID == "" {
		return fmt.Errorf("missing pending thread")
	}
	if err := s.resumeFromBlockWithRetry(ctx, pending.ThreadID, pending.InterruptID); err != nil {
		return err
	}
	if decision.AllowInSession && decision.Approved && pending.ToolName != "" {
		thread, err := s.worker.GetThread(ctx, pending.ThreadID)
		if err != nil {
			return err
		}
		s.toolExecPolicy.AllowSession(thread.SessionID, pending.ToolName, pending.ArgumentsInJSON)
	}
	payload, err := json.Marshal(localResumePayload{
		TurnID:          pending.TurnID,
		CheckpointID:    pending.CheckpointID,
		InterruptID:     pending.InterruptID,
		ToolName:        pending.ToolName,
		ArgumentsInJSON: pending.ArgumentsInJSON,
		Approval:        &decision,
	})
	if err != nil {
		return err
	}
	return s.worker.PostMessage(ctx, pending.ThreadID, &agentworker.Message{
		ID:      newMessageID(),
		Sender:  &agentworker.Sender{Type: agentworker.SenderTypeUser, ID: fmt.Sprintf("%d", s.cfg.UserID)},
		Type:    agentworker.MessageTypeText,
		Payload: payload,
		Metadata: map[string]string{
			"message_type": localMessageTypeResume,
		},
	})
}

func (s *LocalAgentService) SubmitPlanInput(ctx context.Context, pending pendingPlanInput, response *planmode.RequestUserInputResponse) error {
	if pending.ThreadID == "" {
		return fmt.Errorf("missing pending thread")
	}
	if response == nil {
		return fmt.Errorf("missing plan input response")
	}
	if err := s.resumeFromBlockWithRetry(ctx, pending.ThreadID, pending.InterruptID); err != nil {
		return err
	}
	payload, err := json.Marshal(localResumePayload{
		TurnID:           pending.TurnID,
		CheckpointID:     pending.CheckpointID,
		InterruptID:      pending.InterruptID,
		RequestUserInput: response,
	})
	if err != nil {
		return err
	}
	return s.worker.PostMessage(ctx, pending.ThreadID, &agentworker.Message{
		ID:      newMessageID(),
		Sender:  &agentworker.Sender{Type: agentworker.SenderTypeUser, ID: fmt.Sprintf("%d", s.cfg.UserID)},
		Type:    agentworker.MessageTypeText,
		Payload: payload,
		Metadata: map[string]string{
			"message_type":   localMessageTypeResume,
			localTurnModeKey: localTurnModePlan,
		},
	})
}

func (s *LocalAgentService) CancelRunningThread(ctx context.Context, threadID, reason string) (*inprocess.InterruptThreadResult, error) {
	thread, err := s.worker.GetThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if err := s.requireUserThread(thread); err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "user_cancel"
	}
	return s.worker.InterruptThread(ctx, inprocess.InterruptThreadRequest{
		ThreadID: thread.ID,
		ThreadInterruptRequest: agentworker.ThreadInterruptRequest{
			Kind:   agentworker.ThreadInterruptKindCancelInput,
			Reason: reason,
		},
	})
}

func (s *LocalAgentService) resumeFromBlockWithRetry(ctx context.Context, threadID, interruptID string) error {
	var lastErr error
	for i := 0; i < 5; i++ {
		err := s.worker.ResumeFromBlock(ctx, inprocess.ResumeFromBlockRequest{
			ThreadID:    threadID,
			InterruptID: interruptID,
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, inprocess.ErrInvalidResumeRequest) {
			return err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return lastErr
}

func (s *LocalAgentService) ListResumableRoots(ctx context.Context) ([]*inprocess.ThreadState, error) {
	threads, err := s.worker.ListThreads(ctx, inprocess.ListThreadsOptions{
		UserID:        s.cfg.UserID,
		Cwd:           s.cfg.WorkDir,
		RootOnly:      true,
		IncludeClosed: false,
		OrderBy:       inprocess.ListThreadsOrderByUpdatedAt,
		Desc:          true,
		Limit:         30,
	})
	if err != nil {
		return nil, err
	}
	for _, thread := range threads {
		s.refs.Register(thread)
	}
	return threads, nil
}

func (s *LocalAgentService) SessionThreads(ctx context.Context, sessionID string) ([]*inprocess.ThreadState, error) {
	threads, err := s.worker.ListThreadsBySession(ctx, sessionID, inprocess.ListThreadsOptions{UserID: s.cfg.UserID, Limit: 100})
	if err != nil {
		return nil, err
	}
	filtered := threads[:0]
	for _, thread := range threads {
		if thread != nil && thread.UserID == s.cfg.UserID {
			filtered = append(filtered, thread)
			s.refs.Register(thread)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].ParentThreadID == "" && filtered[j].ParentThreadID != "" {
			return true
		}
		if filtered[i].ParentThreadID != "" && filtered[j].ParentThreadID == "" {
			return false
		}
		return filtered[i].UpdatedAt.Before(filtered[j].UpdatedAt)
	})
	return filtered, nil
}

func (s *LocalAgentService) Updates() <-chan localUpdate {
	return s.updates
}

func (s *LocalAgentService) WatchSession(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if ctx == nil {
		ctx = s.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if _, ok := s.watchedSessions[sessionID]; ok {
		s.mu.Unlock()
		return nil
	}
	sub, err := s.worker.SubscribeSessionEvents(ctx, sessionID, 64)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.watchedSessions[sessionID] = struct{}{}
	s.mu.Unlock()
	seenThreads := make(map[string]struct{})
	go func() {
		defer sub.Close()
		for ev := range sub.Events {
			if ev == nil {
				continue
			}
			update := localUpdate{Event: ev}
			if strings.TrimSpace(ev.ThreadID) != "" {
				if _, seen := seenThreads[ev.ThreadID]; !seen {
					seenThreads[ev.ThreadID] = struct{}{}
					update.Thread = s.threadForSessionEvent(ctx, sessionID, ev.ThreadID)
				}
			}
			select {
			case s.updates <- update:
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (s *LocalAgentService) threadForSessionEvent(ctx context.Context, sessionID, threadID string) *inprocess.ThreadState {
	thread, err := s.worker.GetThread(ctx, threadID)
	if err != nil || thread == nil || thread.SessionID != sessionID {
		return nil
	}
	if err := s.requireUserThread(thread); err != nil {
		return nil
	}
	s.refs.Register(thread)
	return thread
}

func (s *LocalAgentService) requireUserThread(thread *inprocess.ThreadState) error {
	if thread == nil {
		return fmt.Errorf("thread not found")
	}
	if thread.UserID != s.cfg.UserID {
		return fmt.Errorf("thread %s belongs to user %d, current user is %d", thread.ID, thread.UserID, s.cfg.UserID)
	}
	return nil
}

func chatTitleFromMessage(message string) string {
	message = strings.TrimSpace(strings.ReplaceAll(message, "\n", " "))
	return truncateRunes(message, 60)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
