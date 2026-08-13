//go:build !windows

package toolmw

import (
	"context"
	"fmt"
	"time"

	"code.byted.org/capcut-business/model/kitex_gen/capcut/business/common/sandbox_model"
	"code.byted.org/gopkg/jsonx"
	"code.byted.org/gopkg/lang/v2/conv"
	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/lang/gg/choose"
	"code.byted.org/lang/gg/gptr"
	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/pippit/sandbox/gateway"
	"code.byted.org/overpass/pippit_sandbox_gateway/rpc/pippit_sandbox_gateway"
	"github.com/cloudwego/eino/compose"
	uuid2 "github.com/google/uuid"

	"eino-cli/deepagent/core/metrics"
	"eino-cli/deepagent/core/utils/bizerror"
)

const (
	SandboxFileWatchMWName = "sandbox_file_watch_mw"

	// WorkspaceAbsDir sandbox 工作目录
	WorkspaceAbsDir = "/workspace/"
)

var (
	fileWatchCreate = func(ctx context.Context, req *gateway.FileWatchCreateRequest) (*gateway.FileWatchCreateResponse, error) {
		return pippit_sandbox_gateway.RawCall.FileWatchCreate(ctx, req)
	}
	fileWatchPoll = func(ctx context.Context, req *gateway.FileWatchPollRequest) (*gateway.FileWatchPollResponse, error) {
		return pippit_sandbox_gateway.RawCall.FileWatchPoll(ctx, req)
	}
	fileWatchDelete = func(ctx context.Context, req *gateway.FileWatchDeleteRequest) (*gateway.FileWatchDeleteResponse, error) {
		return pippit_sandbox_gateway.RawCall.FileWatchDelete(ctx, req)
	}
)

// FileWatchMWOptions controls sandbox watcher lifecycle and error strategy.
type FileWatchMWOptions struct {
	// EnableLog controls whether middleware enter/exit and watcher lifecycle logs are printed.
	EnableLog bool `json:"enable_log"`
	// EnableDedupAdjacentEvent controls whether adjacent duplicated file events are deduplicated.
	EnableDedupAdjacentEvent bool `json:"enable_dedup_adjacent_event"`
	// SkipErrOnFileWatchCreate allows tool execution to continue when FileWatchCreate fails.
	SkipErrOnFileWatchCreate bool `json:"skip_err_on_file_watch_create"`
	// SkipErrOnFileWatchPoll allows tool execution to continue when FileWatchPoll fails.
	SkipErrOnFileWatchPoll bool `json:"skip_err_on_file_watch_poll"`
	// SkipErrOnHandelFileEvent allows tool execution to continue when HandleFileEvent fails.
	SkipErrOnHandleFileEvent bool `json:"skip_err_on_handle_file_event"`
	// MaxPollLoopCount limits FileWatchPoll loop count; <=0 means no limit.
	MaxPollLoopCount int `json:"max_poll_loop_count"`

	// BuildBizMetaFunc builds the BizMeta from the request context.
	BuildBizMetaFunc func(ctx context.Context) (*sandbox_model.BizMeta, error) `json:"-"`

	// HandleFileEventFunc handles the collected file events after tool invocation.
	HandleFileEventFunc func(ctx context.Context, input *compose.ToolInput, output *compose.ToolOutput, err error, fileEvents []*sandbox_model.FileEvent) error `json:"-"`
}

// SandboxFileWatchMW creates a tool middleware that watches `/workspace/` changes
// during tool execution and forwards collected file events to HandleFileEventFunc.
func SandboxFileWatchMW(opts *FileWatchMWOptions) compose.ToolMiddleware {
	metrics.MustInitOnce()
	invokable := func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (output *compose.ToolOutput, err error) {
			var (
				overallStart = time.Now()
				uuid         = uuid2.NewString()

				bizType                     = "unknown"
				fileEvents                  = []*sandbox_model.FileEvent{}
				fileWatchDuration           time.Duration
				handleFileEventFuncDuration time.Duration
				fileWatchErr                error
				handleFileEventErr          error
			)

			defer func() {
				metrics.SandboxFileWatchEmit(ctx, "overall", bizType, bizerror.ParseErrorCode(err), len(fileEvents), time.Since(overallStart))
				metrics.SandboxFileWatchEmit(ctx, "file_watch", bizType, bizerror.ParseErrorCode(fileWatchErr), len(fileEvents), fileWatchDuration)
				metrics.SandboxFileWatchEmit(ctx, "handle_file_event_func", bizType, bizerror.ParseErrorCode(handleFileEventErr), len(fileEvents), handleFileEventFuncDuration)
			}()

			if opts.EnableLog {
				logs.CtxInfo(ctx, "[FileWatchMW][uuid=%v] BEGIN, opts: %v", uuid, jsonx.ToString(opts))
				defer func() {
					logf := choose.If(err == nil, logs.CtxInfo, logs.CtxWarn)
					logf(ctx, "[FileWatchMW][uuid=%v] END, is_error=[overall:%v,file_watch:%v,handle_file_event_func:%v], err_detail=[%v][%v][%v]",
						uuid, conv.BoolToInteger[int](err != nil), conv.BoolToInteger[int](fileWatchErr != nil), conv.BoolToInteger[int](handleFileEventErr != nil),
						err, fileWatchErr, handleFileEventErr)
				}()
			}

			// create watcher
			watcherID := ""
			var bizMeta *sandbox_model.BizMeta
			bizMeta, fileWatchErr = opts.BuildBizMetaFunc(ctx)
			if fileWatchErr != nil {
				fileWatchErr = bizerror.ParseOrNewError(
					fileWatchErr,
					bizerror.ErrCodeBizFileWatchBuildBizMetaError,
					fmt.Sprintf("[FileWatchMW][uuid=%v] BuildBizMetaFunc fail, err: %v", uuid, fileWatchErr),
				)
			}
			if bizMeta != nil {
				bizType = bizMeta.BizType
				createStart := time.Now()
				var createResp *gateway.FileWatchCreateResponse
				createResp, fileWatchErr = fileWatchCreate(ctx, &gateway.FileWatchCreateRequest{
					BizMeta:         bizMeta,
					Path:            gptr.Of(WorkspaceAbsDir),
					Recursive:       gptr.Of(true),
					DebounceMS:      gptr.Of(int32(500)),
					ExcludePatterns: nil,
					IncludePatterns: nil,
				})
				fileWatchDuration += time.Since(createStart)
				if fileWatchErr == nil {
					watcherID = conv.PtrToValueOrZero(createResp.WatcherID)
				}
			}
			if fileWatchErr != nil {
				fileWatchErr = bizerror.ParseOrNewError(fileWatchErr, bizerror.ErrCodeInternalFileWatchCreateError,
					fmt.Sprintf("FileWatchCreate failed, err: %v", fileWatchErr))
				if opts.SkipErrOnFileWatchCreate {
					logs.CtxWarn(ctx, "[FileWatchMW][uuid=%v] FileWatchCreate fail, continue exec due to SkipErrOnFileWatch, err: %v", uuid, fileWatchErr)
				} else {
					err = fileWatchErr
					return nil, err
				}
			}

			// do next
			output, err = next(ctx, input)

			// poll and delete
			if watcherID != "" {
				// defer do delete watcher
				defer func() {
					deleteStart := time.Now()
					deleteResp, localErr := fileWatchDelete(ctx, &gateway.FileWatchDeleteRequest{
						BizMeta:   bizMeta,
						WatcherID: gptr.Of(watcherID),
					})
					fileWatchDuration += time.Since(deleteStart)
					logf := choose.If(localErr == nil, logs.CtxInfo, logs.CtxWarn)
					logf(ctx, "[FileWatchMW][uuid=%v] FileWatchDelete, resp: %v, err: %v", uuid, jsonx.ToString(deleteResp), localErr)
				}()

				// poll event from watcher
				pollStart := time.Now()
				var cursor *int64
				for i := 1; ; i++ {
					if opts.MaxPollLoopCount > 0 && i > opts.MaxPollLoopCount {
						logs.CtxWarn(ctx, "[FileWatchMW][uuid=%v] FileWatchPoll loop reached max count(%d), break to avoid infinite loop", uuid, opts.MaxPollLoopCount)
						break
					}
					if i%10 == 0 {
						logs.CtxInfo(ctx, "[FileWatchMW][uuid=%v] FileWatchPoll loop iteration=%d, watcherID=%v, cursor=%v", uuid, i, watcherID, conv.PtrToValueOrZero(cursor))
					}
					pollResp, localErr := fileWatchPoll(ctx, &gateway.FileWatchPollRequest{
						BizMeta:        bizMeta,
						WatcherID:      gptr.Of(watcherID),
						Cursor:         cursor,
						Limit:          gptr.Of(int32(100)),
						TimeoutSeconds: gptr.Of(int32(5)),
					})
					if localErr != nil {
						localErr = bizerror.ParseOrNewError(localErr, bizerror.ErrCodeInternalFileWatchPollError, fmt.Sprintf("call FileWatchPoll fail, err: %v", localErr))
						if opts.SkipErrOnFileWatchPoll {
							logs.CtxWarn(ctx, "[FileWatchMW][uuid=%v] FileWatchPoll fail, continue exec due to SkipErrOnFileWatchPoll, err: %v", uuid, localErr)
							break
						} else {
							err = localErr
							fileWatchDuration += time.Since(pollStart)
							return output, err
						}
					}
					cursor = pollResp.NextCursor
					fileEvents = append(fileEvents, pollResp.Events...)
					if !pollResp.GetHasMore() {
						break
					}
				}
				// TODO: maybe sort and uniq fileEvents
				if opts.EnableDedupAdjacentEvent {
					fileEvents = dedupAdjacentFileEvents(fileEvents)
				}
				fileWatchDuration += time.Since(pollStart)
			}

			// exec HandleFileEventFunc
			handleFileEventFuncStart := time.Now()
			defer func() {
				handleFileEventFuncDuration = time.Since(handleFileEventFuncStart)
			}()
			handleFileEventErr = opts.HandleFileEventFunc(ctx, input, output, err, fileEvents)
			if handleFileEventErr != nil {
				isSameWithOld := err != nil && err.Error() == handleFileEventErr.Error()
				if isSameWithOld {
					handleFileEventErr = nil
				} else {
					if opts.SkipErrOnHandleFileEvent {
						logs.CtxWarn(ctx, "[FileWatchMW][uuid=%v] HandleFileEventFunc fail, continue exec due to SkipErrOnHandleFileEvent, err: %v", uuid, handleFileEventErr)
					} else {
						logs.CtxWarn(ctx, "[FileWatchMW][uuid=%v] HandleFileEventFunc fail, use new error instead of origin err, new err: %v, origin err: %v", uuid, handleFileEventErr, err)
						err = handleFileEventErr
					}
				}
			}

			return output, err
		}
	}

	return compose.ToolMiddleware{
		Invokable:  invokable,
		Streamable: InvokableToStreamable(invokable),
	}
}

func dedupAdjacentFileEvents(fileEvents []*sandbox_model.FileEvent) []*sandbox_model.FileEvent {
	if len(fileEvents) <= 1 {
		return fileEvents
	}

	result := make([]*sandbox_model.FileEvent, 0, len(fileEvents))
	for _, event := range fileEvents {
		if len(result) == 0 {
			result = append(result, event)
			continue
		}

		last := result[len(result)-1]
		if isSameOrAdjacentSeqSameFile(last, event) {
			// Keep the later event when two adjacent entries represent the same file change.
			result[len(result)-1] = event
			continue
		}

		result = append(result, event)
	}

	return result
}

func isSameOrAdjacentSeqSameFile(prev, curr *sandbox_model.FileEvent) bool {
	if prev == nil || curr == nil {
		return false
	}

	if prev.GetType() != curr.GetType() ||
		prev.GetPath() != curr.GetPath() ||
		prev.GetInode() != curr.GetInode() ||
		prev.GetIsDir() != curr.GetIsDir() {
		return false
	}

	prevSeq := prev.GetSeq()
	currSeq := curr.GetSeq()
	if prevSeq > currSeq {
		return false
	}

	return currSeq-prevSeq <= 1
}
