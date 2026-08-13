//go:build !windows

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"code.byted.org/gopkg/logs/v2"
	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	cloudbackend "eino-cli/deepagent/cloud/backend"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/worker/cloud"
	"eino-cli/deepagent/worker/tasktool"
)

const defaultTaskRolesDescription = `
- explorer: use for read-only investigation, codebase questions, logs/config inspection, and concise findings. Explorer task threads have read-only file tools and read-only shell policy; do not assign implementation work to them.
- worker: use for bounded implementation tasks such as creating or editing files and running focused validation commands. Worker task threads can write project files and run project-local commands; keep commands narrow and avoid unrelated system changes.
`

// collabMiddlewares wires task/sub-agent tools into a turn. It is enabled only
// when the host provides MessageWaitObserver, because waiting for task results
// requires reading session events.
func (b *threadBuilder) collabMiddlewares(ctx context.Context, threadInfo *ac.Thread, threadProfile ResolvedThreadProfile) []middleware.Middleware {
	taskTool := b.collabTaskTool(ctx, threadInfo, threadProfile)
	if taskTool == nil {
		return nil
	}
	return []middleware.Middleware{
		tasktool.NewCollabMiddleware(tasktool.CollabMiddlewareConfig{
			TaskTool:         taskTool,
			RolesDescription: threadProfile.Collaboration.TaskRolesDescription,
		}),
	}
}

func (b *threadBuilder) collabTaskTool(ctx context.Context, threadInfo *ac.Thread, threadProfile ResolvedThreadProfile) *tasktool.TaskTool {
	if b.deps.MessageWaitObserver == nil {
		return nil
	}
	sessionID := ""
	threadID := int64(0)
	userID := int64(0)
	if threadInfo != nil {
		sessionID = threadInfo.GetSessionId()
		threadID = threadInfo.GetThreadId()
		userID = threadInfo.GetUserId()
	}
	currentRef, err := b.currentThreadRef(ctx, threadInfo, userID, sessionID, threadID)
	if err != nil {
		logs.CtxError(ctx, "[cloudagent] resolve current thread ref failed: 对话流ID=%s thread_id=%d err=%v", sessionID, threadID, err)
	}
	b.registerKnownThreadRefs(ctx, threadInfo)

	metadata := map[string]string{}
	if threadID != 0 {
		parentThreadID := strconv.FormatInt(threadID, 10)
		rootThreadID := threadInfo.GetMetadata()[MetadataRootThreadID]
		if rootThreadID == "" {
			rootThreadID = parentThreadID
		}
		metadata[MetadataThreadRole] = ThreadRoleChild
		metadata[MetadataParentThreadID] = parentThreadID
		metadata[MetadataRootThreadID] = rootThreadID
		metadata[MetadataProjectName] = resolvedThreadProjectName(threadInfo, threadProfile)
	}
	spawnInitialMetadata := map[string]string{}
	if currentRef != "" {
		spawnInitialMetadata[MetadataFromThreadRef] = currentRef
	}
	return &tasktool.TaskTool{
		Host: cloud.CoordinatorTaskHost{
			Namespace: b.cfg.Host.Namespace,
			Env:       b.cfg.Host.Env,
			Client:    b.deps.CoordinatorClient.rawClient(),
			UserID:    userID,
		},
		ThreadID:                    strconv.FormatInt(threadID, 10),
		SessionID:                   sessionID,
		UserID:                      userID,
		WorkerConcurrency:           b.cfg.Host.Concurrency,
		Metadata:                    metadata,
		SpawnProfile:                tasktool.ThreadProfile{Cwd: threadProfile.WorkDir},
		SpawnInitialMessageMetadata: spawnInitialMetadata,
		SpawnMetadataDescription:    threadProfile.Collaboration.SpawnMetadataDescription,
		ResolveTarget: func(ctx context.Context, target string) (string, error) {
			threadID, ok, err := b.resolveThreadRef(ctx, userID, sessionID, target)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", fmt.Errorf("unknown thread target %q", target)
			}
			return strconv.FormatInt(threadID, 10), nil
		},
		OnThreadSpawned: func(ctx context.Context, spawned tasktool.SpawnedThread) (string, error) {
			if b.deps.ThreadRefs == nil {
				return "", nil
			}
			threadID, err := strconv.ParseInt(spawned.ThreadID, 10, 64)
			if err != nil {
				return "", fmt.Errorf("invalid spawned thread id %q: %w", spawned.ThreadID, err)
			}
			return b.deps.ThreadRefs.Allocate(ctx, userID, sessionID, threadID)
		},
		FormatOutbound: func(ctx context.Context, msg tasktool.OutboundMessage) (*tasktool.FormattedOutboundMessage, error) {
			if currentRef == "" {
				return nil, fmt.Errorf("thread ref not found for current thread")
			}
			payload, err := json.Marshal(protoinput.UserMessage{
				Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: msg.Content}},
			})
			if err != nil {
				return nil, fmt.Errorf("marshal outbound message: %w", err)
			}
			return &tasktool.FormattedOutboundMessage{
				MessageType: protoinput.MessageTypeInput,
				Payload:     payload,
				Metadata:    map[string]string{MetadataFromThreadRef: currentRef},
			}, nil
		},
		InputValidator:      b.deps.TaskToolInputValidator,
		TaskResultModifier:  b.deps.TaskResultModifier,
		MessageWaitObserver: b.deps.MessageWaitObserver,
	}
}

func resolvedThreadProjectName(threadInfo *ac.Thread, threadProfile ResolvedThreadProfile) string {
	if name, err := cloudbackend.CleanProjectName(threadProfile.Project); err == nil {
		return name
	}
	return threadProjectName(threadInfo, tasktool.ThreadProfile{Cwd: threadProfile.WorkDir})
}

func (b *threadBuilder) currentThreadRef(ctx context.Context, threadInfo *ac.Thread, userID int64, sessionID string, threadID int64) (string, error) {
	if isMainThread(threadInfo) {
		return ThreadRoleMain, nil
	}
	if b.deps.ThreadRefs == nil {
		return "", nil
	}
	ref, ok, err := b.deps.ThreadRefs.RefForThread(ctx, userID, sessionID, threadID)
	if err != nil || !ok {
		return ref, err
	}
	return ref, nil
}

func (b *threadBuilder) resolveThreadRef(ctx context.Context, userID int64, sessionID string, ref string) (int64, bool, error) {
	if b.deps.ThreadRefs != nil {
		return b.deps.ThreadRefs.Resolve(ctx, userID, sessionID, ref)
	}
	threadID, err := strconv.ParseInt(strings.TrimSpace(ref), 10, 64)
	return threadID, err == nil && threadID != 0, nil
}

func (b *threadBuilder) registerKnownThreadRefs(ctx context.Context, threadInfo *ac.Thread) {
	if b.deps.ThreadRefs == nil || threadInfo == nil {
		return
	}
	sessionID := threadInfo.GetSessionId()
	threadID := threadInfo.GetThreadId()
	if sessionID == "" || threadID == 0 {
		return
	}
	metadata := threadInfo.GetMetadata()
	if isMainThread(threadInfo) {
		_ = b.deps.ThreadRefs.Register(ctx, threadInfo.GetUserId(), sessionID, ThreadRoleMain, threadID)
	}
	if parentThreadID := metadata[MetadataParentThreadID]; parentThreadID != "" {
		if parsed, err := strconv.ParseInt(parentThreadID, 10, 64); err == nil {
			_ = b.deps.ThreadRefs.Register(ctx, threadInfo.GetUserId(), sessionID, "parent", parsed)
		}
	}
	if rootThreadID := metadata[MetadataRootThreadID]; rootThreadID != "" {
		if parsed, err := strconv.ParseInt(rootThreadID, 10, 64); err == nil {
			_ = b.deps.ThreadRefs.Register(ctx, threadInfo.GetUserId(), sessionID, ThreadRoleMain, parsed)
		}
	}
}

func isMainThread(threadInfo *ac.Thread) bool {
	if threadInfo == nil {
		return false
	}
	metadata := threadInfo.GetMetadata()
	return metadata[MetadataThreadRole] == ThreadRoleMain ||
		(metadata[MetadataThreadRole] == "" && metadata[MetadataParentThreadID] == "")
}
