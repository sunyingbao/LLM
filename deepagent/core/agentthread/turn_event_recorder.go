package agentthread

import (
	"context"
	"io"
	"sync"
	"time"

	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/plan"
	deeptools "eino-cli/deepagent/core/tools"

	"code.byted.org/gopkg/logs/v2"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	cbutils "github.com/cloudwego/eino/utils/callbacks"
	"github.com/google/uuid"
)

const eventEnqueueWarnThreshold = 50 * time.Millisecond

// turnEventRecorder owns the callback-to-event protocol for one turn.
// It keeps event ordering, asynchronous callback completion, and tool-event
// deduplication out of the runner execution lifecycle.
type turnEventRecorder struct {
	threadID              string
	turnID                string
	contextManager        ContextManager
	eventIDProvider       func(ctx context.Context, threadID, turnID string) string
	eventBus              chan Event
	eventMu               sync.RWMutex
	eventClosed           bool
	asyncCallbacks        sync.WaitGroup
	toolEvents            *toolEventMiddleware
	enableStreamToolCalls bool
	llmResponseID         string
	modelBarrierMu        sync.Mutex
	modelBarrier          *modelEventBarrier
}

func newTurnEventRecorder(
	cfg *TurnConfig,
	threadID string,
	turnID string,
	contextManager ContextManager,
	eventBus chan Event,
) (recorder *turnEventRecorder) {
	eventIDProvider := cfg.EventIDProvider
	if eventIDProvider == nil {
		eventIDProvider = func(context.Context, string, string) string {
			return uuid.NewString()
		}
	}
	return &turnEventRecorder{
		threadID:              threadID,
		turnID:                turnID,
		contextManager:        contextManager,
		eventIDProvider:       eventIDProvider,
		eventBus:              eventBus,
		toolEvents:            newToolEventMiddleware(),
		enableStreamToolCalls: cfg.Agent.EnableStreamToolCall,
	}
}

func (r *turnEventRecorder) middlewares(enablePlan bool, toolMask deeptools.Mask) (middlewares []middleware.Middleware) {
	if enablePlan {
		middlewares = append(middlewares, plan.New(&plan.PlanMiddlewareConfig{
			ToolMask: toolMask,
			OnPlanUpdate: func(ctx context.Context, update plan.PlanUpdate) error {
				var steps []PlanStep
				if update.Plan != nil {
					steps = make([]PlanStep, len(update.Plan))
					for i, step := range update.Plan {
						steps[i] = PlanStep{Step: step.Step, Status: PlanStepStatus(step.Status)}
					}
				}
				r.emit(ctx, EventPlanUpdated, PlanUpdatedPayload{
					Explanation: update.Explanation,
					Plan:        steps,
				})
				return nil
			},
		}))
	}
	middlewares = append(middlewares, r.toolEvents)
	return middlewares
}

func (r *turnEventRecorder) callbackHandler() (handler callbacks.Handler) {
	return cbutils.NewHandlerHelper().
		Tool(&cbutils.ToolCallbackHandler{
			OnStart: func(ctx context.Context, runInfo *callbacks.RunInfo, input *tool.CallbackInput) context.Context {
				if !r.waitModelEventBarrier(ctx) {
					return ctx
				}
				callID := toolCallbackCallID(ctx, input, nil)
				state := r.toolState(callID, runInfo.Name)
				args := callbackInputArgs(input)
				if !state.markStartEmitted(time.Now(), args) {
					return ctx
				}
				r.emit(ctx, EventToolStart, ToolStartPayload{Name: runInfo.Name, CallID: callID, Args: args})
				return ctx
			},
			OnEnd: func(ctx context.Context, runInfo *callbacks.RunInfo, output *tool.CallbackOutput) context.Context {
				callID := toolCallbackCallID(ctx, nil, output)
				state := r.lookupOrCreateToolState(callID, runInfo.Name)
				if state.markStreamSeen() {
					return ctx
				}
				r.emitToolEnd(ctx, state, callbackOutputText(output))
				return ctx
			},
			OnEndWithStreamOutput: func(ctx context.Context, runInfo *callbacks.RunInfo, output *schema.StreamReader[*tool.CallbackOutput]) context.Context {
				if output == nil {
					return ctx
				}
				callID := toolCallbackCallID(ctx, nil, nil)
				state := r.lookupOrCreateToolState(callID, runInfo.Name)
				state.markStreamStarted()
				r.asyncCallbacks.Add(1)
				go func() {
					defer r.asyncCallbacks.Done()
					defer output.Close()
					for {
						chunk, err := output.Recv()
						if err != nil {
							if err == io.EOF {
								r.emitToolEnd(ctx, state, state.snapshotResult())
							} else {
								logs.CtxError(ctx, "[agentthread::toolStream] recv failed: tool=%s call_id=%s err=%v",
									state.name, state.callID, err)
							}
							return
						}
						if chunkCallID := toolCallbackExtraCallID(chunk.Extra); chunkCallID != "" {
							state.setCallID(chunkCallID)
						}
						text := callbackOutputText(chunk)
						state.appendResult(text)
						r.emit(ctx, EventToolCallOutputChunk, ToolCallOutputChunkPayload{
							Name: state.name, CallID: state.callID, Chunk: text,
						})
					}
				}()
				return ctx
			},
		}).
		ChatModel(&cbutils.ModelCallbackHandler{
			OnStart: func(ctx context.Context, runInfo *callbacks.RunInfo, input *model.CallbackInput) context.Context {
				runName := callbackRunName(runInfo)
				r.llmResponseID = uuid.NewString()
				r.beginModelEventBarrier(ctx)
				logs.CtxInfo(ctx, "[agentthread::model] request start: thread_id=%s turn_id=%s run_name=%s input_present=%t",
					r.threadID, r.turnID, runName, input != nil)
				if input == nil {
					r.emit(ctx, EventLLMRequesting, (*model.CallbackInput)(nil))
					return ctx
				}
				r.emit(ctx, EventLLMRequesting, input)
				return ctx
			},
			OnEnd: func(ctx context.Context, runInfo *callbacks.RunInfo, output *model.CallbackOutput) context.Context {
				barrier := r.currentModelEventBarrier()
				defer r.releaseModelEventBarrier(barrier)
				contentLength, toolCalls := modelOutputSize(output)
				logs.CtxInfo(ctx, "[agentthread::model] response end: thread_id=%s turn_id=%s run_name=%s content_len=%d tool_calls=%d usage_present=%t",
					r.threadID, r.turnID, callbackRunName(runInfo), contentLength, toolCalls, output != nil && output.TokenUsage != nil)
				responseID := r.currentLLMResponseID()
				if output != nil {
					r.recordModelUsage(ctx, output.TokenUsage)
				}
				r.emit(ctx, EventLLMEnd, llmEndFromCallbackOutput(output, responseID))
				return ctx
			},
			OnEndWithStreamOutput: func(ctx context.Context, runInfo *callbacks.RunInfo, output *schema.StreamReader[*model.CallbackOutput]) context.Context {
				barrier := r.currentModelEventBarrier()
				if output == nil {
					r.releaseModelEventBarrier(barrier)
					return ctx
				}
				runName := callbackRunName(runInfo)
				responseID := r.currentLLMResponseID()
				r.asyncCallbacks.Add(1)
				go r.consumeModelStream(ctx, output, barrier, runName, responseID)
				return ctx
			},
		}).Handler()
}

func (r *turnEventRecorder) consumeModelStream(
	ctx context.Context,
	output *schema.StreamReader[*model.CallbackOutput],
	barrier *modelEventBarrier,
	runName string,
	responseID string,
) {
	defer r.asyncCallbacks.Done()
	defer r.releaseModelEventBarrier(barrier)
	logs.CtxInfo(ctx, "[agentthread::modelStream] stream merge begin: thread_id=%s turn_id=%s run_name=%s",
		r.threadID, r.turnID, runName)
	llmEnd, chunks, err := mergeModelCallbackStream(ctx, output, responseID, func(ctx context.Context, chunk *schema.Message) {
		if chunk == nil || (chunk.Content == "" && chunk.ReasoningContent == "") {
			return
		}
		r.emit(ctx, EventLLMToken, LLMTokenChunk{
			Text: chunk.Content, ReasoningText: chunk.ReasoningContent, LLMResponseID: responseID,
		})
	})
	if err != nil {
		logs.CtxError(ctx, "[agentthread::modelStream] recv failed: err=%v", err)
		return
	}
	contentLength, toolCalls := modelOutputSize(&llmEnd.CallbackOutput)
	logs.CtxInfo(ctx, "[agentthread::modelStream] stream merge done: thread_id=%s turn_id=%s run_name=%s chunks=%d content_len=%d tool_calls=%d usage_present=%t",
		r.threadID, r.turnID, runName, chunks, contentLength, toolCalls, llmEnd.TokenUsage != nil)
	r.recordModelUsage(ctx, llmEnd.TokenUsage)
	r.emit(ctx, EventLLMEnd, llmEnd)
}

func callbackRunName(runInfo *callbacks.RunInfo) (name string) {
	if runInfo != nil {
		return runInfo.Name
	}
	return ""
}

func modelOutputSize(output *model.CallbackOutput) (contentLength int, toolCalls int) {
	if output == nil || output.Message == nil {
		return 0, 0
	}
	return len(output.Message.Content), len(output.Message.ToolCalls)
}

func (r *turnEventRecorder) currentLLMResponseID() (responseID string) {
	if r.llmResponseID != "" {
		return r.llmResponseID
	}
	return uuid.NewString()
}

func (r *turnEventRecorder) recordModelUsage(ctx context.Context, usage *model.TokenUsage) {
	if usage == nil || r.contextManager == nil {
		return
	}
	r.contextManager.RecordModelUsage(ctx, usage)
}

func (r *turnEventRecorder) waitForCallbacks() {
	r.asyncCallbacks.Wait()
}

func (r *turnEventRecorder) emit(ctx context.Context, typ EventType, payload any) {
	location := eventLocationFromContext(ctx)
	if location == (EventLocation{}) {
		location = EventLocation{AgentName: constant.GraphName}
	}
	event := Event{
		Loc: location, ID: r.eventIDProvider(ctx, r.threadID, r.turnID), TS: time.Now(),
		ThreadID: r.threadID, TurnID: r.turnID, Type: typ, Payload: payload,
	}
	r.eventMu.RLock()
	defer r.eventMu.RUnlock()
	if r.eventClosed {
		logs.CtxWarn(ctx, "[agentthread::emitEvent] drop late event after turn event channel closed: thread_id=%s turn_id=%s event_type=%s",
			r.threadID, r.turnID, typ)
		return
	}
	queueLength, queueCapacity, startedAt := len(r.eventBus), cap(r.eventBus), time.Now()
	r.eventBus <- event
	if elapsed := time.Since(startedAt); elapsed > eventEnqueueWarnThreshold {
		logs.CtxWarn(ctx, "[agentthread::emitEvent] slow event enqueue: thread_id=%s turn_id=%s event_type=%s elapsed=%s queue_len_before=%d queue_cap=%d",
			r.threadID, r.turnID, typ, elapsed, queueLength, queueCapacity)
	}
}

func (r *turnEventRecorder) close() {
	r.eventMu.Lock()
	defer r.eventMu.Unlock()
	if r.eventClosed {
		return
	}
	r.eventClosed = true
	close(r.eventBus)
}

func (r *turnEventRecorder) beginModelEventBarrier(ctx context.Context) (barrier *modelEventBarrier) {
	if r.enableStreamToolCalls {
		return nil
	}
	barrier = newModelEventBarrier()
	r.modelBarrierMu.Lock()
	if r.modelBarrier != nil {
		r.modelBarrier.release()
		logs.CtxWarn(ctx, "[agentthread::modelBarrier] release stale model barrier before new model call: thread_id=%s turn_id=%s", r.threadID, r.turnID)
	}
	r.modelBarrier = barrier
	r.modelBarrierMu.Unlock()
	return barrier
}

func (r *turnEventRecorder) currentModelEventBarrier() (barrier *modelEventBarrier) {
	if r.enableStreamToolCalls {
		return nil
	}
	r.modelBarrierMu.Lock()
	defer r.modelBarrierMu.Unlock()
	return r.modelBarrier
}

func (r *turnEventRecorder) releaseModelEventBarrier(barrier *modelEventBarrier) {
	if barrier == nil {
		return
	}
	barrier.release()
	r.modelBarrierMu.Lock()
	if r.modelBarrier == barrier {
		r.modelBarrier = nil
	}
	r.modelBarrierMu.Unlock()
}

func (r *turnEventRecorder) waitModelEventBarrier(ctx context.Context) (completed bool) {
	barrier := r.currentModelEventBarrier()
	if barrier == nil {
		return true
	}
	startedAt := time.Now()
	select {
	case <-barrier.done:
	case <-ctx.Done():
		logs.CtxWarn(ctx, "[agentthread::modelBarrier] stop waiting for llm_end before tool start: thread_id=%s turn_id=%s err=%v", r.threadID, r.turnID, ctx.Err())
		return false
	}
	if elapsed := time.Since(startedAt); elapsed > modelBarrierWaitWarnThreshold {
		logs.CtxWarn(ctx, "[agentthread::modelBarrier] waited for llm_end before tool start: thread_id=%s turn_id=%s elapsed=%s", r.threadID, r.turnID, elapsed)
	}
	return true
}

func (r *turnEventRecorder) toolState(callID string, name string) (state *toolEventState) {
	key := toolEventKey(callID, name)
	state, found := r.toolEvents.store.load(key)
	if found {
		if state.name == "" {
			state.name = name
		}
		if state.callID == "" {
			state.callID = callID
		}
		return state
	}
	return r.toolEvents.store.loadOrStore(key, &toolEventState{name: name, callID: callID})
}

func (r *turnEventRecorder) lookupOrCreateToolState(callID string, name string) (state *toolEventState) {
	if callID != "" {
		return r.toolState(callID, name)
	}
	state = r.findPendingToolStateByName(name)
	if state != nil {
		return state
	}
	return r.toolState(callID, name)
}

func (r *turnEventRecorder) findPendingToolStateByName(name string) (pending *toolEventState) {
	r.toolEvents.store.rangeStates(func(_ string, state *toolEventState) bool {
		if state.name == name && state.isPendingStreamBinding() {
			pending = state
			return false
		}
		return true
	})
	return pending
}

func (r *turnEventRecorder) emitToolEnd(ctx context.Context, state *toolEventState, result string) {
	if state == nil || !state.markEndEmitted() {
		return
	}
	snapshot := state.snapshot()
	r.emit(ctx, EventToolEnd, ToolEndPayload{
		Name: snapshot.name, CallID: snapshot.callID, ToolStartTime: snapshot.toolStartTime,
		ArgumentsInJSON: snapshot.argumentsInJSON, Result: result,
	})
}
