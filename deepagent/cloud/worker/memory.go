//go:build !windows

package worker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/gopkg/thrift"
	ac "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	acsvc "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator/agentcoordinatorservice"
	acbase "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/base"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	runtimethread "eino-cli/deepagent/cloud/worker/thread"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/memory"
	"github.com/bytedance/sonic"
)

func (b *threadBuilder) buildMemoryTurnObserver(threadInfo *ac.Thread) runtimethread.TurnFinishedObserver {
	if !b.cfg.Memory.Enabled {
		return nil
	}
	if b.deps.MemoryStore == nil {
		logs.Warnf("[cloudagent] memory turn observer disabled: memory store is nil")
		return nil
	}
	if threadInfo == nil {
		logs.Warnf("[cloudagent] memory turn observer disabled: thread info is nil")
		return nil
	}
	if memory.IsConsolidationThreadMetadata(threadInfo.GetMetadata()) {
		logs.Infof("[cloudagent] memory turn observer skipped for consolidation thread: thread_id=%d user_id=%d", threadInfo.GetThreadId(), threadInfo.GetUserId())
		return nil
	}
	userID := strconv.FormatInt(threadInfo.GetUserId(), 10)
	sourceThreadID := strconv.FormatInt(threadInfo.GetThreadId(), 10)
	idleWindow := b.cfg.Memory.Stage1IdleWindow
	if idleWindow <= 0 {
		idleWindow = 10 * time.Minute
	}
	logs.Infof("[cloudagent] memory turn observer enabled: user_id=%s source_thread_id=%s idle_window=%s", userID, sourceThreadID, idleWindow)
	return func(ctx context.Context, ev agentthread.Event) {
		go func() {
			sourceUpdatedAt := ev.TS
			if sourceUpdatedAt.IsZero() {
				sourceUpdatedAt = time.Now()
			}
			eligibleAt := sourceUpdatedAt.Add(idleWindow)
			logs.CtxInfo(ctx, "[cloudagent] memory turn observer fired: user_id=%s source_thread_id=%s event_thread_id=%s event_id=%s turn_id=%s event_type=%s source_updated_at=%s eligible_at=%s",
				userID, sourceThreadID, ev.ThreadID, ev.ID, ev.TurnID, ev.Type, sourceUpdatedAt.Format(time.RFC3339Nano), eligibleAt.Format(time.RFC3339Nano))
			if err := b.deps.MemoryStore.TouchSource(context.WithoutCancel(ctx), memory.TouchSourceRequest{
				Memory: memory.UserMemoryContext{
					UserID:       userID,
					WriteEnabled: true,
				},
				SourceThreadID:  sourceThreadID,
				SourceUpdatedAt: sourceUpdatedAt,
				EligibleAt:      eligibleAt,
				Mode:            memory.SourceModeEnabled,
			}); err != nil {
				logs.CtxWarn(ctx, "[cloudagent] memory touch source failed: user_id=%s source_thread_id=%s event_thread_id=%s event_id=%s turn_id=%s err=%v",
					userID, sourceThreadID, ev.ThreadID, ev.ID, ev.TurnID, err)
				return
			}
			logs.CtxInfo(ctx, "[cloudagent] memory touch source succeeded: user_id=%s source_thread_id=%s event_thread_id=%s event_id=%s turn_id=%s source_updated_at=%s eligible_at=%s",
				userID, sourceThreadID, ev.ThreadID, ev.ID, ev.TurnID, sourceUpdatedAt.Format(time.RFC3339Nano), eligibleAt.Format(time.RFC3339Nano))
		}()
	}
}

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
		Stage2ThreadHost:         cloudStage2ThreadHost{cfg: cfg, client: deps.CoordinatorClient.rawClient()},
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
	client acsvc.Client
}

func (h cloudStage2ThreadHost) CreateStage2Thread(ctx context.Context, req memory.Stage2CreateThreadRequest) (memory.Stage2CreatedThread, error) {
	if h.client == nil {
		return memory.Stage2CreatedThread{}, fmt.Errorf("cloudagent: memory stage2 coordinator client is required")
	}
	createReq, err := buildMemoryStage2CreateThreadRequest(h.cfg, req.Spec)
	if err != nil {
		return memory.Stage2CreatedThread{}, err
	}
	resp, err := h.client.CreateThread(ctx, createReq)
	if err := memoryRPCError("CreateMemoryStage2Thread", resp, err); err != nil {
		return memory.Stage2CreatedThread{}, err
	}
	if resp.GetThread() == nil {
		return memory.Stage2CreatedThread{}, fmt.Errorf("cloudagent: CreateMemoryStage2Thread returned empty thread")
	}
	return memory.Stage2CreatedThread{ThreadID: strconv.FormatInt(resp.GetThread().GetThreadId(), 10)}, nil
}

func buildMemoryStage2CreateThreadRequest(cfg Config, spec memory.Stage2ThreadSpec) (*ac.CreateThreadRequest, error) {
	userID, err := strconv.ParseInt(strings.TrimSpace(spec.UserID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("cloudagent: invalid memory user id %q: %w", spec.UserID, err)
	}
	payload, err := sonic.Marshal(protoinput.UserMessage{
		Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: spec.InitialPrompt}},
	})
	if err != nil {
		return nil, fmt.Errorf("cloudagent: marshal memory stage2 prompt: %w", err)
	}
	metadata, err := memory.BuildStage2Metadata(spec.Metadata, spec)
	if err != nil {
		return nil, fmt.Errorf("cloudagent: build memory stage2 metadata: %w", err)
	}
	createReq := &ac.CreateThreadRequest{
		Namespace: cfg.Host.Namespace,
		UserId:    userID,
		SessionId: thrift.StringPtr(memorySessionID(spec.UserID)),
		Title:     thrift.StringPtr("Memory consolidation"),
		Metadata:  metadata,
		Profile: &ac.ThreadProfile{
			Role: DefaultRoleID,
		},
		InitialMessage: &ac.InitialMessage{
			MessageType: protoinput.MessageTypeInput,
			Payload:     payload,
			Metadata:    cloneMemoryMetadata(metadata),
		},
	}
	if cfg.Host.Env != "" {
		createReq.Env = thrift.StringPtr(cfg.Host.Env)
	}
	createReq.GetOrSetBase()
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
	req := &ac.CloseThreadRequest{
		Namespace: h.cfg.Host.Namespace,
		ThreadId:  id,
		Reason:    thrift.StringPtr(reason),
	}
	req.GetOrSetBase()
	resp, err := h.client.CloseThread(ctx, req)
	return memoryRPCError("CloseMemoryStage2Thread", resp, err)
}

type memoryBaseRespGetter interface {
	GetBaseResp() *acbase.BaseResp
}

func memoryRPCError(op string, resp memoryBaseRespGetter, err error) error {
	if resp != nil {
		if baseResp := resp.GetBaseResp(); baseResp != nil && baseResp.GetStatusCode() != 0 {
			msg := fmt.Sprintf("%s status_code=%d status_message=%q", op, baseResp.GetStatusCode(), baseResp.GetStatusMessage())
			if err != nil {
				return fmt.Errorf("%s: %w", msg, err)
			}
			return fmt.Errorf("%s", msg)
		}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

func (b *threadBuilder) stage2RetryDelay() time.Duration {
	if b.cfg.Memory.Stage2ScanInterval > 0 {
		return b.cfg.Memory.Stage2ScanInterval
	}
	return 5 * time.Minute
}

func (b *threadBuilder) stage2LeaseTTL() time.Duration {
	if b.cfg.Memory.Stage2LeaseTTL > 0 {
		return b.cfg.Memory.Stage2LeaseTTL
	}
	return time.Hour
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
