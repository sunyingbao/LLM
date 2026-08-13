package main

import (
	"context"
	"fmt"

	"eino-cli/deepagent/core/middleware"
	inprocess "eino-cli/deepagent/worker/inprocess"
	"eino-cli/deepagent/worker/tasktool"
)

type CollabBuilder struct {
	Refs             *ThreadRefRegistry
	RolesDescription string
}

func (b *CollabBuilder) Middlewares(state *inprocess.ThreadState, worker *inprocess.Worker) []middleware.Middleware {
	if b == nil || b.Refs == nil || state == nil || worker == nil {
		return nil
	}
	b.Refs.Register(state)
	metadata := map[string]string{
		"parent_thread_id": state.ID,
		"root_thread_id":   firstNonEmpty(state.RootThreadID, state.ID),
	}
	currentRef := b.Refs.Ref(state)
	spawnInitialMetadata := map[string]string{}
	if currentRef != "" {
		spawnInitialMetadata["from_thread_ref"] = currentRef
	}
	taskTool := &tasktool.TaskTool{
		Host: inprocess.TaskToolHostAdapter{
			Worker: worker,
			UserID: state.UserID,
		},
		ThreadID:                    state.ID,
		SessionID:                   state.SessionID,
		WorkerConcurrency:           8,
		Metadata:                    metadata,
		SpawnProfile:                tasktool.ThreadProfile{Cwd: state.Profile.Cwd},
		SpawnInitialMessageMetadata: spawnInitialMetadata,
		ResolveTarget: func(ctx context.Context, target string) (string, error) {
			if id, ok := b.Refs.Resolve(state.SessionID, target); ok {
				return id, nil
			}
			thread, err := worker.GetThread(ctx, target)
			if err != nil {
				return "", err
			}
			if thread.UserID != state.UserID || thread.SessionID != state.SessionID {
				return "", fmt.Errorf("target %q is outside current user/session", target)
			}
			return thread.ID, nil
		},
		OnThreadSpawned: func(ctx context.Context, spawned tasktool.SpawnedThread) (string, error) {
			ref := b.Refs.AllocateChild(state.SessionID, spawned.ThreadID)
			return ref, nil
		},
		FormatOutbound: func(ctx context.Context, msg tasktool.OutboundMessage) (*tasktool.FormattedOutboundMessage, error) {
			ref := b.Refs.Ref(state)
			if ref == "" {
				ref = state.ID
			}
			return &tasktool.FormattedOutboundMessage{
				Payload:  []byte(msg.Content),
				Metadata: map[string]string{"from_thread_ref": ref},
			}, nil
		},
		MessageWaitObserver: localMessageWaitObserver,
	}
	return []middleware.Middleware{
		tasktool.NewCollabMiddleware(tasktool.CollabMiddlewareConfig{
			TaskTool:         taskTool,
			RolesDescription: b.RolesDescription,
		}),
	}
}
