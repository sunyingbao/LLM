//go:build !windows

package toolmw

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"code.byted.org/capcut-business/model/kitex_gen/capcut/business/common/sandbox_model"
	"code.byted.org/lang/gg/gptr"
	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/pippit/sandbox/gateway"
	"eino-cli/deepagent/core/metrics"
	"github.com/bytedance/mockey"
	"github.com/cloudwego/eino/compose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	metricsInitMock := mockey.Mock(metrics.MustInitOnce).Return().Build()
	metricsEmitMock := mockey.Mock(metrics.SandboxFileWatchEmit).To(
		func(context.Context, string, string, int32, int, time.Duration) {},
	).Build()
	code := m.Run()
	metricsEmitMock.UnPatch()
	metricsInitMock.UnPatch()
	os.Exit(code)
}

func mockSandboxFileWatchDeps(t *testing.T) {
	oldCreate, oldPoll, oldDelete := fileWatchCreate, fileWatchPoll, fileWatchDelete

	t.Cleanup(func() {
		fileWatchCreate, fileWatchPoll, fileWatchDelete = oldCreate, oldPoll, oldDelete
	})
}

func TestSandboxFileWatchMW_SuccessWithDedup(t *testing.T) {
	mockSandboxFileWatchDeps(t)

	var (
		createReq  *gateway.FileWatchCreateRequest
		deleteReq  *gateway.FileWatchDeleteRequest
		pollCalled int
	)

	fileWatchCreate = func(ctx context.Context, req *gateway.FileWatchCreateRequest) (*gateway.FileWatchCreateResponse, error) {
		createReq = req
		return &gateway.FileWatchCreateResponse{WatcherID: gptr.Of("watcher-1")}, nil
	}
	fileWatchPoll = func(ctx context.Context, req *gateway.FileWatchPollRequest) (*gateway.FileWatchPollResponse, error) {
		pollCalled++
		return &gateway.FileWatchPollResponse{
			Events: []*sandbox_model.FileEvent{
				{Seq: gptr.Of(int64(1)), Type: gptr.Of("write"), Path: gptr.Of("/workspace/a.txt"), Inode: gptr.Of(int64(11)), IsDir: gptr.Of(false)},
				{Seq: gptr.Of(int64(2)), Type: gptr.Of("write"), Path: gptr.Of("/workspace/a.txt"), Inode: gptr.Of(int64(11)), IsDir: gptr.Of(false)},
				{Seq: gptr.Of(int64(3)), Type: gptr.Of("write"), Path: gptr.Of("/workspace/b.txt"), Inode: gptr.Of(int64(22)), IsDir: gptr.Of(false)},
			},
			HasMore: gptr.Of(false),
		}, nil
	}
	fileWatchDelete = func(ctx context.Context, req *gateway.FileWatchDeleteRequest) (*gateway.FileWatchDeleteResponse, error) {
		deleteReq = req
		return &gateway.FileWatchDeleteResponse{}, nil
	}

	ctx := context.Background()
	var handledEvents []*sandbox_model.FileEvent
	var nextCalled bool

	opts := &FileWatchMWOptions{
		EnableDedupAdjacentEvent: true,
		BuildBizMetaFunc: func(ctx context.Context) (*sandbox_model.BizMeta, error) {
			return &sandbox_model.BizMeta{BizType: "ut_biz"}, nil
		},
		HandleFileEventFunc: func(ctx context.Context, input *compose.ToolInput, output *compose.ToolOutput, err error, fileEvents []*sandbox_model.FileEvent) error {
			handledEvents = fileEvents
			assert.NoError(t, err)
			assert.Equal(t, "ok", output.Result)
			return nil
		},
	}

	mw := SandboxFileWatchMW(opts)
	endpoint := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		nextCalled = true
		return &compose.ToolOutput{Result: "ok"}, nil
	})

	out, err := endpoint(ctx, &compose.ToolInput{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.True(t, nextCalled)
	assert.Equal(t, "ok", out.Result)

	require.NotNil(t, createReq)
	assert.Equal(t, WorkspaceAbsDir, createReq.GetPath())
	assert.True(t, createReq.GetRecursive())
	assert.Equal(t, int32(500), createReq.GetDebounceMS())

	assert.Equal(t, 1, pollCalled)
	require.NotNil(t, deleteReq)
	assert.Equal(t, "watcher-1", deleteReq.GetWatcherID())

	require.Len(t, handledEvents, 2)
	assert.Equal(t, int64(2), handledEvents[0].GetSeq())
	assert.Equal(t, "/workspace/a.txt", handledEvents[0].GetPath())
	assert.Equal(t, int64(3), handledEvents[1].GetSeq())
}

func TestSandboxFileWatchMW_CreateErrorCanSkip(t *testing.T) {
	mockSandboxFileWatchDeps(t)

	fileWatchCreate = func(ctx context.Context, req *gateway.FileWatchCreateRequest) (*gateway.FileWatchCreateResponse, error) {
		return nil, errors.New("create failed")
	}

	var (
		nextCalled   bool
		handleCalled bool
	)

	opts := &FileWatchMWOptions{
		SkipErrOnFileWatchCreate: true,
		BuildBizMetaFunc: func(ctx context.Context) (*sandbox_model.BizMeta, error) {
			return &sandbox_model.BizMeta{BizType: "ut_biz"}, nil
		},
		HandleFileEventFunc: func(ctx context.Context, input *compose.ToolInput, output *compose.ToolOutput, err error, fileEvents []*sandbox_model.FileEvent) error {
			handleCalled = true
			assert.NoError(t, err)
			assert.Empty(t, fileEvents)
			return nil
		},
	}

	mw := SandboxFileWatchMW(opts)
	endpoint := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		nextCalled = true
		return &compose.ToolOutput{Result: "ok"}, nil
	})

	out, err := endpoint(context.Background(), &compose.ToolInput{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "ok", out.Result)
	assert.True(t, nextCalled)
	assert.True(t, handleCalled)
}

func TestSandboxFileWatchMW_PollErrorReturnsAndDeleteStillRuns(t *testing.T) {
	mockSandboxFileWatchDeps(t)

	fileWatchCreate = func(ctx context.Context, req *gateway.FileWatchCreateRequest) (*gateway.FileWatchCreateResponse, error) {
		return &gateway.FileWatchCreateResponse{WatcherID: gptr.Of("watcher-2")}, nil
	}
	fileWatchPoll = func(ctx context.Context, req *gateway.FileWatchPollRequest) (*gateway.FileWatchPollResponse, error) {
		return nil, errors.New("poll failed")
	}

	var deleteCalled bool
	fileWatchDelete = func(ctx context.Context, req *gateway.FileWatchDeleteRequest) (*gateway.FileWatchDeleteResponse, error) {
		deleteCalled = true
		return &gateway.FileWatchDeleteResponse{}, nil
	}

	var handleCalled bool
	opts := &FileWatchMWOptions{
		BuildBizMetaFunc: func(ctx context.Context) (*sandbox_model.BizMeta, error) {
			return &sandbox_model.BizMeta{BizType: "ut_biz"}, nil
		},
		HandleFileEventFunc: func(ctx context.Context, input *compose.ToolInput, output *compose.ToolOutput, err error, fileEvents []*sandbox_model.FileEvent) error {
			handleCalled = true
			return nil
		},
	}

	mw := SandboxFileWatchMW(opts)
	endpoint := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: "ok"}, nil
	})

	out, err := endpoint(context.Background(), &compose.ToolInput{})
	require.Error(t, err)
	require.NotNil(t, out)
	assert.Contains(t, err.Error(), "FileWatchPoll")
	assert.True(t, deleteCalled)
	assert.False(t, handleCalled)
}

func TestSandboxFileWatchMW_HandleErrorOverridesOrigin(t *testing.T) {
	mockSandboxFileWatchDeps(t)

	fileWatchCreate = func(ctx context.Context, req *gateway.FileWatchCreateRequest) (*gateway.FileWatchCreateResponse, error) {
		return &gateway.FileWatchCreateResponse{WatcherID: gptr.Of("watcher-3")}, nil
	}
	fileWatchPoll = func(ctx context.Context, req *gateway.FileWatchPollRequest) (*gateway.FileWatchPollResponse, error) {
		return &gateway.FileWatchPollResponse{HasMore: gptr.Of(false)}, nil
	}
	fileWatchDelete = func(ctx context.Context, req *gateway.FileWatchDeleteRequest) (*gateway.FileWatchDeleteResponse, error) {
		return &gateway.FileWatchDeleteResponse{}, nil
	}

	originErr := errors.New("origin err")
	handleErr := errors.New("handle err")

	opts := &FileWatchMWOptions{
		BuildBizMetaFunc: func(ctx context.Context) (*sandbox_model.BizMeta, error) {
			return &sandbox_model.BizMeta{BizType: "ut_biz"}, nil
		},
		HandleFileEventFunc: func(ctx context.Context, input *compose.ToolInput, output *compose.ToolOutput, err error, fileEvents []*sandbox_model.FileEvent) error {
			assert.ErrorIs(t, err, originErr)
			return handleErr
		},
	}

	mw := SandboxFileWatchMW(opts)
	endpoint := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: "ok"}, originErr
	})

	out, err := endpoint(context.Background(), &compose.ToolInput{})
	require.Error(t, err)
	require.NotNil(t, out)
	assert.ErrorIs(t, err, handleErr)
}

func TestDedupAdjacentFileEvents(t *testing.T) {
	events := []*sandbox_model.FileEvent{
		{Seq: gptr.Of(int64(10)), Type: gptr.Of("write"), Path: gptr.Of("/workspace/a.txt"), Inode: gptr.Of(int64(1)), IsDir: gptr.Of(false)},
		{Seq: gptr.Of(int64(11)), Type: gptr.Of("write"), Path: gptr.Of("/workspace/a.txt"), Inode: gptr.Of(int64(1)), IsDir: gptr.Of(false)},
		{Seq: gptr.Of(int64(13)), Type: gptr.Of("write"), Path: gptr.Of("/workspace/a.txt"), Inode: gptr.Of(int64(1)), IsDir: gptr.Of(false)},
	}

	result := dedupAdjacentFileEvents(events)
	require.Len(t, result, 2)
	assert.Equal(t, int64(11), result[0].GetSeq())
	assert.Equal(t, int64(13), result[1].GetSeq())
}
