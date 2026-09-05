//go:build !windows

package worker

import (
	"context"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/coordinator"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/memory"
	workercloud "eino-cli/deepagent/worker/cloud"
	"fmt"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
)

func newMemoryJobLoopFromConfig(ctx context.Context, cfg Config, deps Deps) (*memory.MemoryJobLoop, error) {
	if !cfg.Memory.Enabled || deps.MemoryStore == nil {
		return nil, nil
	}
	deps = resolveMemoryDeps(cfg.Memory, deps)
	if deps.MemoryWorkspace == nil {
		return nil, fmt.Errorf("cloudagent: memory workspace is required")
	}
	if deps.MemoryWorkspace.AgentBackend() == nil {
		return nil, fmt.Errorf("cloudagent: memory workspace backend must support sandbox tools")
	}
	defaultRole := cfg.Turn.Roles[DefaultRoleID]
	modelProfile := cfg.Turn.Models[defaultRole.Model.Default]
	if modelProfile.ChatModel == nil {
		return nil, fmt.Errorf("cloudagent: memory model is required")
	}
	if deps.HistoryStore == nil {
		return nil, fmt.Errorf("cloudagent: memory history store provider is required")
	}
	engine, err := memory.NewEngine(memory.Config{
		Store:              deps.MemoryStore,
		History:            memoryHistoryStore{ctx: ctx, provider: deps.HistoryStore},
		Workspace:          deps.MemoryWorkspace,
		Extract:            memory.NewModelStage1ExtractFunc(modelProfile.ChatModel),
		Stage1HistoryInput: cfg.Memory.Stage1HistoryInput,
	})
	if err != nil {
		return nil, err
	}
	return memory.NewMemoryJobLoop(memory.MemoryJobLoopConfig{
		Store:                    deps.MemoryStore,
		Engine:                   engine,
		Stage2ThreadHost:         cloudStage2ThreadHost{cfg: cfg, client: deps.Coordinator},
		Owner:                    cfg.Host.LeaseOwnerHint,
		ScanInterval:             cfg.Memory.ScanInterval,
		WakeupDebounce:           cfg.Memory.WakeupDebounce,
		Stage1LeaseTTL:           cfg.Memory.Stage1LeaseTTL,
		Stage1MaxClaimedPerScan:  cfg.Memory.Stage1MaxClaimedPerScan,
		Stage2LeaseTTL:           cfg.Memory.Stage2LeaseTTL,
		Stage2SuccessCooldown:    cfg.Memory.Stage2SuccessCooldown,
		Stage2ScanInterval:       cfg.Memory.Stage2ScanInterval,
		Stage2MaxUsersPerScan:    cfg.Memory.Stage2MaxUsersPerScan,
		Stage2OutputLimitPerUser: cfg.Memory.Stage2OutputLimitPerUser,
	}), nil
}

func resolveMemoryDeps(cfg MemoryConfig, deps Deps) Deps {
	if !cfg.Enabled || deps.MemoryWorkspace != nil {
		return deps
	}
	deps.MemoryWorkspace = resolveMemoryWorkspace(cfg)
	return deps
}

func resolveMemoryWorkspace(cfg MemoryConfig) *memory.Workspace {
	root := strings.TrimSpace(cfg.WorkspaceRoot)
	if root == "" {
		return nil
	}
	return memory.NewWorkspace(backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     root,
		VirtualMode: true,
	}), "memory")
}

type cloudStage2ThreadHost struct {
	cfg    Config
	client workercloud.CoordinatorClient
}

func (h cloudStage2ThreadHost) CreateStage2Thread(ctx context.Context, req memory.Stage2CreateThreadRequest) (memory.Stage2CreatedThread, error) {
	if h.client == nil {
		return memory.Stage2CreatedThread{}, fmt.Errorf("cloudagent: memory stage2 coordinator client is required")
	}
	createReq, err := buildMemoryStage2CreateThreadRequest(h.cfg, req.Spec)
	if err != nil {
		return memory.Stage2CreatedThread{}, err
	}
	result, err := h.client.CreateThread(ctx, createReq)
	if err = memoryCoordinatorError("CreateMemoryStage2Thread", err); err != nil {
		return memory.Stage2CreatedThread{}, err
	}
	if result.Thread == nil {
		return memory.Stage2CreatedThread{}, fmt.Errorf("cloudagent: CreateMemoryStage2Thread returned empty thread")
	}
	return memory.Stage2CreatedThread{ThreadID: strconv.FormatInt(result.Thread.ThreadID, 10)}, nil
}

func buildMemoryStage2CreateThreadRequest(cfg Config, spec memory.Stage2ThreadSpec) (request coordinator.CreateThreadRequest, err error) {
	userID, err := strconv.ParseInt(strings.TrimSpace(spec.UserID), 10, 64)
	if err != nil {
		return coordinator.CreateThreadRequest{}, fmt.Errorf("cloudagent: invalid memory user id %q: %w", spec.UserID, err)
	}
	payload, err := sonic.Marshal(protoinput.UserMessage{
		Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: spec.InitialPrompt}},
	})
	if err != nil {
		return coordinator.CreateThreadRequest{}, fmt.Errorf("cloudagent: marshal memory stage2 prompt: %w", err)
	}
	metadata, err := memory.BuildStage2Metadata(spec.Metadata, spec)
	if err != nil {
		return coordinator.CreateThreadRequest{}, fmt.Errorf("cloudagent: build memory stage2 metadata: %w", err)
	}
	createReq := coordinator.CreateThreadRequest{
		Namespace: cfg.Host.Namespace,
		UserID:    userID,
		SessionID: memorySessionID(spec.UserID),
		Title:     "Memory consolidation",
		Metadata:  metadata,
		Profile: &coordinator.Profile{
			Role: DefaultRoleID,
		},
		InitialMessage: &coordinator.InitialMessage{
			MessageType: protoinput.MessageTypeInput,
			Payload:     payload,
			Metadata:    cloneMemoryMetadata(metadata),
		},
	}
	if cfg.Host.Env != "" {
		createReq.Env = cfg.Host.Env
	}
	return createReq, nil
}

func (h cloudStage2ThreadHost) CloseStage2Thread(ctx context.Context, threadID string, reason string) error {
	if h.client == nil {
		return fmt.Errorf("cloudagent: memory stage2 coordinator client is required")
	}
	id, err := strconv.ParseInt(strings.TrimSpace(threadID), 10, 64)
	if err != nil {
		return fmt.Errorf("cloudagent: invalid memory thread id %q: %w", threadID, err)
	}
	req := coordinator.RequestThreadCloseRequest{
		Namespace: h.cfg.Host.Namespace,
		ThreadID:  id,
		Reason:    reason,
	}
	_, err = h.client.RequestThreadClose(ctx, req)
	return memoryCoordinatorError("CloseMemoryStage2Thread", err)
}

func memoryCoordinatorError(op string, err error) (result error) {
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

func memorySessionID(userID string) string {
	return "memory-" + strings.TrimSpace(userID)
}

func cloneMemoryMetadata(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

type memoryHistoryStore struct {
	ctx      context.Context
	provider HistoryStoreProvider
}

func (s memoryHistoryStore) Append(ctx context.Context, rec *agentthread.HistoryRecord) error {
	if rec == nil {
		return fmt.Errorf("cloudagent: memory history append record is required")
	}
	store, err := s.store(ctx, rec.ThreadID)
	if err != nil {
		return err
	}
	return store.Append(ctx, rec)
}

func (s memoryHistoryStore) List(ctx context.Context, q agentthread.ListQuery) ([]*agentthread.HistoryRecord, error) {
	store, err := s.store(ctx, q.ThreadID)
	if err != nil {
		return nil, err
	}
	return store.List(ctx, q)
}

func (s memoryHistoryStore) store(ctx context.Context, threadID string) (agentthread.HistoryRolloutStore, error) {
	if s.provider == nil {
		return nil, fmt.Errorf("cloudagent: memory history store provider is required")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, fmt.Errorf("cloudagent: memory history thread id is required")
	}
	if ctx == nil {
		ctx = s.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	store, err := s.provider(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, fmt.Errorf("cloudagent: memory history store is nil for thread %s", threadID)
	}
	return store, nil
}
