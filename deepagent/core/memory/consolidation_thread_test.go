package memory

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/mock/mock_model"
	agentworker "eino-cli/deepagent/worker"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"go.uber.org/mock/gomock"
)

func TestConsolidationCloseCancelsRunningModel(t *testing.T) {
	thread, started, stopped := newBlockingConsolidationThread(t, NewInMemoryStore(), nil)
	requireNoError(t, postConsolidationMessage(thread))
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	requireNoError(t, thread.Close(ctx))
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("Close returned without canceling the running model")
	}
	if thread.ActiveTurn() != nil {
		t.Fatal("Close returned before the turn finished")
	}
}

func TestConsolidationLeaseLossCancelsRunningModel(t *testing.T) {
	lost := make(chan struct{})
	store := &consolidationLeaseStore{Store: NewInMemoryStore(), lost: lost}
	thread, started, stopped := newBlockingConsolidationThread(t, store, nil)
	requireNoError(t, postConsolidationMessage(thread))
	<-started
	close(lost)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("lease loss did not cancel the running model")
	}
}

func TestConsolidationCancellationWaitsForExecutionBeforeFinalizing(t *testing.T) {
	release := make(chan struct{})
	thread, started, stopped := newBlockingConsolidationThread(t, NewInMemoryStore(), release)
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := thread.PostMessage(ctx, &agentworker.Message{ID: "msg-1", Payload: []byte(`{"parts":[{"type":"text","text":"consolidate"}]}`)})
	requireNoError(t, err)
	<-started
	cancel()
	<-stopped
	select {
	case item := <-thread.output:
		t.Fatalf("finalized before model execution exited: %+v", item)
	case <-time.After(50 * time.Millisecond):
	}
	if thread.ActiveTurn() == nil {
		t.Fatal("canceled turn must stay active until execution exits")
	}
}

func TestConsolidationHeartbeatCanStopBeforeGoroutineStarts(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	ctx := context.Background()
	store := NewInMemoryStore()
	now := time.Now()
	requireNoError(t, store.EnqueueStage2(ctx, EnqueueStage2Request{UserID: "user", InputWatermark: "w1", Now: now}))
	jobs, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{Now: now, Owner: "test", LeaseTTL: time.Minute, Limit: 1})
	requireNoError(t, err)
	thread := &consolidationAgentThread{store: store, userID: "user", ownershipToken: jobs[0].OwnershipToken,
		heartbeatEvery: time.Minute, leaseTTL: time.Minute, now: time.Now}
	for i := 0; i < 100; i++ {
		thread.mu.Lock()
		thread.startHeartbeatLocked(ctx)
		thread.mu.Unlock()
		thread.stopHeartbeat()
	}
}

type consolidationLeaseStore struct {
	Store
	lost <-chan struct{}
}

func (s *consolidationLeaseStore) HeartbeatStage2(ctx context.Context, req HeartbeatStage2Request) (err error) {
	select {
	case <-s.lost:
		return ErrStage2JobLeaseLost
	default:
		return s.Store.HeartbeatStage2(ctx, req)
	}
}

func newBlockingConsolidationThread(t *testing.T, store Store, release <-chan struct{}) (thread *consolidationAgentThread, started, stopped chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	now := time.Now()
	requireNoError(t, store.EnqueueStage2(ctx, EnqueueStage2Request{UserID: "123", InputWatermark: "w1", Now: now}))
	jobs, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{Now: now, Owner: "test", LeaseTTL: time.Minute, Limit: 1})
	requireNoError(t, err)
	metadata, err := BuildStage2Metadata(nil, Stage2ThreadSpec{UserID: "123", OwnershipToken: jobs[0].OwnershipToken, InputWatermark: "w1"})
	requireNoError(t, err)
	started, stopped = make(chan struct{}), make(chan struct{})
	chatModel := mock_model.NewMockToolCallingChatModel(gomock.NewController(t))
	chatModel.EXPECT().WithTools(gomock.Any()).Return(chatModel, nil).AnyTimes()
	chatModel.EXPECT().Stream(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(runCtx context.Context, _ []*schema.Message, _ ...model.Option) (stream *schema.StreamReader[*schema.Message], err error) {
			close(started)
			select {
			case <-runCtx.Done():
			case <-ctx.Done():
			}
			close(stopped)
			if release != nil {
				<-release
			}
			return nil, errors.New("model stopped")
		})
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: t.TempDir(), VirtualMode: true})
	runtime, err := NewConsolidationAgentThread(ConsolidationAgentThreadConfig{
		ThreadID: "42", Metadata: metadata, ChatModel: chatModel, HistoryStore: &fakeHistoryStore{}, Store: store,
		Workspace: NewWorkspace(backend, "memory"), LeaseTTL: time.Minute, CheckpointStore: checkpointer.NewInMemoryStore(),
	})
	requireNoError(t, err)
	thread = runtime.(*consolidationAgentThread)
	thread.heartbeatEvery = time.Millisecond
	_, err = thread.Init(ctx)
	requireNoError(t, err)
	t.Cleanup(func() { cancel(); _ = thread.Close(context.Background()) })
	return thread, started, stopped
}

func postConsolidationMessage(thread *consolidationAgentThread) (err error) {
	_, err = thread.PostMessage(context.Background(), &agentworker.Message{ID: "msg-1", Payload: []byte(`{"parts":[{"type":"text","text":"consolidate"}]}`)})
	return err
}

func TestConsolidationTurnConfigIsRestricted(t *testing.T) {
	ctrl := gomock.NewController(t)
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	cfg := newConsolidationTurnConfig(ConsolidationAgentThreadConfig{
		ThreadID:  "42",
		ChatModel: mock_model.NewMockToolCallingChatModel(ctrl),
		Workspace: NewWorkspace(backend, "memory"),
	})

	if len(cfg.Agent.Tools) != 0 {
		t.Fatalf("tools=%d, want no business tools", len(cfg.Agent.Tools))
	}
	if len(cfg.Agent.Middlewares) != 0 {
		t.Fatalf("middlewares=%d, want no normal prompt/collab middleware", len(cfg.Agent.Middlewares))
	}
	if cfg.EnablePlan {
		t.Fatal("EnablePlan=true, want false")
	}
	if cfg.Agent.FilesystemConfig == nil {
		t.Fatalf("filesystem config missing: %+v", cfg)
	}
	if cfg.Agent.FilesystemConfig.WorkDir != "memory" || !cfg.Agent.FilesystemConfig.DisableExecute || !cfg.Agent.FilesystemConfig.DisableUploadDownload {
		t.Fatalf("filesystem config=%+v, want memory root with execute/upload disabled", cfg.Agent.FilesystemConfig)
	}
	if cfg.Agent.SkillLoader != nil {
		t.Fatal("SkillLoader set, want nil")
	}
}

func TestConsolidationAgentThreadHeartbeatsStage2Lease(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	start := time.Now()
	requireNoError(t, store.EnqueueStage2(ctx, EnqueueStage2Request{
		UserID:         "123",
		InputWatermark: "w1",
		Now:            start,
	}))
	claimed, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
		Now:      start,
		Owner:    "worker-a",
		LeaseTTL: 20 * time.Millisecond,
		Limit:    1,
	})
	requireNoError(t, err)
	if len(claimed) != 1 {
		t.Fatalf("claimed=%d, want 1", len(claimed))
	}

	ctrl := gomock.NewController(t)
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	metadata, err := BuildStage2Metadata(nil, Stage2ThreadSpec{
		UserID:         "123",
		OwnershipToken: claimed[0].OwnershipToken,
		InputWatermark: "w1",
	})
	requireNoError(t, err)
	thread, err := NewConsolidationAgentThread(ConsolidationAgentThreadConfig{
		ThreadID:     "42",
		ChatModel:    mock_model.NewMockToolCallingChatModel(ctrl),
		HistoryStore: &fakeHistoryStore{},
		Store:        store,
		Workspace:    NewWorkspace(backend, "memory"),
		Metadata:     metadata,
		LeaseTTL:     90 * time.Millisecond,
	})
	requireNoError(t, err)
	if _, err := thread.Init(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = thread.Close(ctx) }()

	time.Sleep(130 * time.Millisecond)
	deadline := time.After(time.Second)
	for {
		reclaimed, err := store.ClaimStage2Jobs(ctx, ClaimStage2Request{
			Now:      time.Now(),
			Owner:    "worker-b",
			LeaseTTL: time.Minute,
			Limit:    1,
		})
		requireNoError(t, err)
		if len(reclaimed) == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("stage2 lease was reclaimed while consolidation thread heartbeat was running")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestConsolidationAgentThreadStaysActiveDuringFinalization(t *testing.T) {
	thread, started, _ := newBlockingConsolidationThread(t, NewInMemoryStore(), nil)
	requireNoError(t, postConsolidationMessage(thread))
	<-started

	active := thread.ActiveTurn()
	if active == nil {
		t.Fatal("ActiveTurn() nil, want active while finalization is pending")
	}
	if active.TurnID != thread.thread.ActiveTurn().TurnID() || len(active.ConsumedMessageIDs) != 1 || active.ConsumedMessageIDs[0] != "msg-1" {
		t.Fatalf("ActiveTurn() = %+v", active)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
