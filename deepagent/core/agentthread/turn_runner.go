package agentthread

import (
	"context"
	"fmt"
	"io"
	"maps"
	"sync"
	"time"

	"eino-cli/deepagent/core"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
	agentgraph "eino-cli/deepagent/core/graph"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/plan"
	"eino-cli/deepagent/core/middleware/skill"
	"eino-cli/deepagent/core/middleware/subagent"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/core/types"

	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/lang/gg/choose"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	cbutils "github.com/cloudwego/eino/utils/callbacks"
	"github.com/google/uuid"
)

type TurnRunnerConfig struct {
	// 本轮对话使用的聊天模型（支持工具调用）
	ChatModel model.ToolCallingChatModel

	// 可选工具列表（占位，不实现具体逻辑）
	Tools []tool.BaseTool

	// ToolMask 控制最终对 model 可见且可由服务端执行的工具集合。
	ToolMask deeptools.Mask

	// 是否启用流式工具调用执行
	EnableStreamToolCall bool

	// 是否启用规划能力
	EnablePlanning bool

	// MaxSteps controls the maximum graph run steps for one DeepAgent turn.
	// Leave it <= 0 to use the DeepAgent default.
	MaxSteps int

	// MaxModelCalls limits ChatModel invocations for one logical turn.
	// Leave it <= 0 to disable. The consumed count is stored in DeepAgent
	// GraphState so HITL resume continues with the remaining budget.
	MaxModelCalls int

	// EnablePlan enables the lightweight update_plan progress checklist.
	//
	// It injects PlanMiddleware and emits EventPlanUpdated whenever the model
	// successfully updates the checklist.
	EnablePlan bool

	// 是否启用文件系统访问
	EnableFilesystem bool

	// FilesystemConfig controls filesystem middleware behavior when
	// EnableFilesystem is true.
	FilesystemConfig *deepagents.FilesystemConfig

	// 工作目录
	WorkDir string

	// SandboxBackend（同时支持文件操作与命令执行）
	SandboxBackend backends.SandboxBackend

	// Eino 原生 checkpoint 存储，用于图运行中断与恢复。
	CheckpointStore compose.CheckPointStore

	// 是否启用 Web 工具
	EnableWeb bool

	// SkillLoader（为空则不启用技能中间件）
	SkillLoader skill.Loader

	// EventIDProvider: 外部提供的事件ID生成器（可选）。
	// 形参：ctx、threadID、turnID；返回下一个 EventID（string）。
	// 若为空，则直接使用 uuid.New().String() 作为 EventID。
	EventIDProvider func(ctx context.Context, threadID, turnID string) string

	// HITL 配置（人工干预配置）
	HITLConfig *deepagents.HITLConfig

	// sub agent
	SubAgents []*subagent.SubAgent

	// InterruptAfterNodes: 在这些节点执行后允许图中断（用于外部中断的表面化）
	InterruptAfterNodes []string

	// Middlewares: 额外的自定义中间件（静态），在每轮 DeepAgent 构建时注入。
	Middlewares []middleware.Middleware

	// Callbacks: 额外的 Eino callback handlers，在每轮 DeepAgent 运行时注入。
	// 它只用于观测和埋点，不应该承载业务状态变更。
	Callbacks []callbacks.Handler

	// MiddlewaresProvider: 每轮构造 DeepAgent 时调用的中间件提供者（动态）。
	// 形参：ctx、turnID；返回需要注入的中间件切片。
	// 如果同时配置了 Middlewares 与 MiddlewaresProvider，两者都会注入，Provider 返回的优先添加。
	MiddlewaresProvider func(ctx context.Context, turnID string) []middleware.Middleware

	// CustomStateBuilder: 自定义状态构建器（可选）。
	// 形参：ctx、threadID、turnID；返回自定义状态的 map[string]types.RunTimeStateful。
	// CustomStateBuilder  会在每次 TurnRunner 构建时执行，将结果写入 graphState，随 graphState 一同保存和恢复
	// map[string]types.RunTimeStateful，key 是需要保存的状态的名字，记住唯一，否则会覆盖
	// types.RunTimeStateful 则定义相关的状态要如何序列化和反序列化
	CustomStateBuilder func(ctx context.Context, threadID, turnID string) map[string]types.RunTimeStateful

	// TurnCompleted observes a successfully persisted logical turn. Hosts use
	// this boundary for best-effort side effects such as memory extraction.
	TurnCompleted func(ctx context.Context, threadID, turnID string, chatModel model.ToolCallingChatModel, history []*schema.Message)
}

// TurnRunner 使用 DeepAgent 执行单轮对话。
// 回调仅采集运行信号并交给 DeepAgentThread 原子方法处理。
type TurnRunner struct {
	cfg             *TurnRunnerConfig
	cm              ContextManager
	threadID        string
	turnID          string
	eventMu         sync.RWMutex
	eventClosed     bool
	eventIDProvider func(ctx context.Context, threadID, turnID string) string
	eventBus        chan Event
	mu              sync.Mutex
	agent           *deepagents.DeepAgent
	interruptOpts   InterruptOptions
	interruptActive bool
	cbWg            sync.WaitGroup
	toolEventMw     *toolEventMiddleware
	drainer         pendingInputDrainer
	branchPolicy    deepagents.ReactLoopBranchPolicy
	llmResponseID   string
	modelBarrierMu  sync.Mutex
	modelBarrier    *modelEventBarrier
}

const eventEnqueueWarnThreshold = 50 * time.Millisecond

func NewTurnRunner(cfg *TurnRunnerConfig, threadID, turnID string, cm ContextManager, eventBus chan Event) *TurnRunner {
	a := &TurnRunner{cfg: cfg, threadID: threadID, turnID: turnID, cm: cm, eventBus: eventBus}
	a.eventIDProvider = choose.If(cfg.EventIDProvider != nil, cfg.EventIDProvider, func(ctx context.Context, threadID, turnID string) string {
		return uuid.New().String()
	})
	a.toolEventMw = newToolEventMiddleware()

	return a
}

func (r *TurnRunner) Init(ctx context.Context) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.agent != nil {
		return nil
	}

	opts := []deepagents.Option{
		deepagents.WithModel(r.cfg.ChatModel),
		deepagents.WithTools(r.cfg.Tools...),
		deepagents.WithContextManager(r.buildCtxMngMiddleware()),
	}
	appendMiddlewares := func(mws []middleware.Middleware) {
		for _, mw := range mws {
			if mw != nil {
				opts = append(opts, deepagents.WithMiddleware(mw))
			}
		}
	}
	if r.cfg.MaxSteps > 0 {
		opts = append(opts, deepagents.WithMaxSteps(r.cfg.MaxSteps))
	}
	if r.cfg.MaxModelCalls > 0 {
		opts = append(opts, deepagents.WithMaxModelCalls(r.cfg.MaxModelCalls))
	}
	if r.branchPolicy != nil {
		opts = append(opts, deepagents.WithReactLoopBranchPolicy(r.branchPolicy))
	}
	if r.cfg.ToolMask != nil {
		opts = append(opts, deepagents.WithToolMask(r.cfg.ToolMask))
	}
	if r.cfg.EnablePlanning {
		opts = append(opts, deepagents.WithPlanning())
	}
	if r.cfg.EnablePlan {
		opts = append(opts, deepagents.WithMiddleware(r.buildPlanMiddleware()))
	}
	if r.cfg.EnableStreamToolCall {
		opts = append(opts, deepagents.WithStreamToolCall())
	}
	if r.cfg.EnableFilesystem {
		if r.cfg.FilesystemConfig != nil {
			fsCfg := *r.cfg.FilesystemConfig
			if fsCfg.WorkDir == "" {
				fsCfg.WorkDir = r.cfg.WorkDir
			}
			opts = append(opts, deepagents.WithFilesystemConfig(&fsCfg))
		} else {
			opts = append(opts, deepagents.WithFilesystem())
			if r.cfg.WorkDir != "" {
				opts = append(opts, deepagents.WithWorkDir(r.cfg.WorkDir))
			}
		}
	}
	// Web 能力
	if r.cfg.EnableWeb {
		opts = append(opts, deepagents.WithWeb())
	}
	if r.cfg.SkillLoader != nil {
		opts = append(opts, deepagents.WithSkillLoader(r.cfg.SkillLoader))
	}
	if r.cfg.SandboxBackend != nil {
		opts = append(opts, deepagents.WithSandboxBackend(r.cfg.SandboxBackend))
	}
	if r.cfg.CheckpointStore != nil {
		opts = append(opts, deepagents.WithCheckpointStore(r.cfg.CheckpointStore))
	}
	if r.cfg.HITLConfig != nil {
		opts = append(opts, deepagents.WithHITLConfig(r.cfg.HITLConfig))
	}
	if len(r.cfg.InterruptAfterNodes) > 0 {
		opts = append(opts, deepagents.WithInterruptAfterNodes(normalizeInterruptAfterNodes(r.cfg.InterruptAfterNodes)...))
	}

	if len(r.cfg.SubAgents) != 0 {
		opts = append(opts, deepagents.WithSubAgents(
			r.cfg.SubAgents...,
		))
	}

	// 注入工具事件去重中间件（走标准 BuildStateHandler 通道持久化）
	opts = append(opts, deepagents.WithMiddleware(r.toolEventMw))

	// 注入外部自定义中间件（动态 Provider 优先，其次静态 Middlewares）
	if r.cfg.MiddlewaresProvider != nil {
		appendMiddlewares(r.cfg.MiddlewaresProvider(ctx, r.turnID))
	}
	appendMiddlewares(r.cfg.Middlewares)

	// 构建自定义 GraphState（仅外部 CustomStateBuilder）
	if r.cfg.CustomStateBuilder != nil {
		customStates := r.cfg.CustomStateBuilder(ctx, r.threadID, r.turnID)
		if len(customStates) > 0 {
			opts = append(opts, deepagents.WithCustomGraphState(customStates))
		}
	}

	logs.CtxInfo(ctx, "[ComposeRunner] ensure agent: planning=%v max_steps=%d max_model_calls=%d fs=%v web=%v workdir=%s skills_loader=%v hitl=%v", r.cfg.EnablePlanning, r.cfg.MaxSteps, r.cfg.MaxModelCalls, r.cfg.EnableFilesystem, r.cfg.EnableWeb, r.cfg.WorkDir, r.cfg.SkillLoader != nil, r.cfg.HITLConfig != nil)
	agent, err := deepagents.New(ctx, opts...)
	if err != nil {
		return err
	}
	r.agent = agent
	return nil
}

// ===== Middleware assembly =====

func (r *TurnRunner) buildPlanMiddleware() middleware.Middleware {
	return plan.New(&plan.PlanMiddlewareConfig{
		ToolMask: r.cfg.ToolMask,
		OnPlanUpdate: func(ctx context.Context, update plan.PlanUpdate) error {
			r.emitEvent(ctx, EventPlanUpdated, PlanUpdatedPayload{
				Explanation: update.Explanation,
				Plan:        planStepsFromMiddleware(update.Plan),
			})
			return nil
		},
	})
}
func (r *TurnRunner) buildCtxMngMiddleware() middleware.Middleware {
	return &ctxMngMiddleware{
		core:    r.cm,
		turnID:  r.turnID,
		drainer: r.drainer,
		emit:    r.emitEvent,
	}
}
func (r *TurnRunner) setPendingInputDrainer(drainer pendingInputDrainer) {
	r.drainer = drainer
}
func (r *TurnRunner) setReactLoopBranchPolicy(policy deepagents.ReactLoopBranchPolicy) {
	r.branchPolicy = policy
}

// ===== Turn execution and interrupt translation =====

// RunTurn 拆分为 3 步：初始化 agent → 执行（带 callbacks） → 消耗 stream
func (r *TurnRunner) RunTurn(ctx context.Context, input *Message, opts *TurnRunOptions) (err error) {
	checkpointID := fmt.Sprintf("%s:%s", r.threadID, r.turnID)
	isResume := false

	defer func() {
		err = r.handleRunTurnError(ctx, checkpointID, err)
	}()

	runOpts := []deepagents.RunOptionFunc{
		deepagents.WithCallbacks(r.buildRunCallbacks()...),
	}
	if opts != nil {
		if opts.CheckpointID != "" {
			checkpointID = opts.CheckpointID
		}
		isResume = len(opts.ResumeData) > 0 || len(opts.ResumeInterruptIDs) > 0
		if opts.WriteToCheckpointID != "" {
			runOpts = append(runOpts, deepagents.WithWriteToCheckpointID(opts.WriteToCheckpointID))
		}
		if opts.ForceNewRun {
			runOpts = append(runOpts, deepagents.WithForceNewRun())
		}
		if len(opts.ResumeData) > 0 {
			runOpts = append(runOpts, deepagents.WithResumeData(opts.ResumeData))
		} else if len(opts.ResumeInterruptIDs) > 0 {
			runOpts = append(runOpts, deepagents.WithResume(opts.ResumeInterruptIDs...))
		}
	}
	runOpts = append(runOpts, deepagents.WithCheckpointID(checkpointID))

	if !isResume {
		r.emitEvent(ctx, EventTurnStart, TurnStartPayload{Input: agentgraph.CopyMessage(input)})
	}

	var msgs []*schema.Message
	if input != nil {
		msgs = []*schema.Message{input}
	}

	var stream *schema.StreamReader[*schema.Message]
	stream, err = r.agent.Stream(ctx, msgs, runOpts...)
	if err != nil {
		logs.CtxError(ctx, "[TurnRunner::RunTurn] stream start failed: thread_id=%s turn_id=%s checkpoint_id=%s err=%v",
			r.threadID, r.turnID, checkpointID, err)
		return
	}

	if err = r.waitStreamGraphComplete(stream); err != nil {
		logs.CtxError(ctx, "[TurnRunner::RunTurn] wait graph stream failed: thread_id=%s turn_id=%s err=%v",
			r.threadID, r.turnID, err)
		return
	}
	// 等待模型流式回调消费完成，避免 goroutine 泄漏或事件顺序错乱
	r.cbWg.Wait()
	if r.cfg.TurnCompleted != nil {
		history := append([]*schema.Message(nil), r.cm.History(ctx)...)
		r.cfg.TurnCompleted(ctx, r.threadID, r.turnID, r.cfg.ChatModel, history)
	}
	r.emitEvent(ctx, EventTurnEnd, TurnEndPayload{Usage: 0.0})

	return
}

// ===== Callback adapters and stream helpers =====

func (r *TurnRunner) buildRunCallbacks() []callbacks.Handler {
	handlers := []callbacks.Handler{r.buildCallbacks()}
	for _, handler := range r.cfg.Callbacks {
		if handler != nil {
			handlers = append(handlers, handler)
		}
	}
	return handlers
}
func (r *TurnRunner) handleRunTurnError(ctx context.Context, checkpointID string, err error) error {
	if err == nil {
		return nil
	}

	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		r.emitEvent(ctx, EventError, ErrorPayload{Message: err.Error()})
		return err
	}

	defer r.emitEvent(ctx, EventInterruptInfo, info)

	if payload, ok := r.consumeExternalInterruptPayload(checkpointID); ok {
		r.emitEvent(ctx, EventInterrupted, payload)
		return nil
	}

	if len(info.InterruptContexts) == 0 {
		r.emitEvent(ctx, EventInterrupted, InterruptedPayload{Source: "custom", CheckpointID: checkpointID})
		return nil
	}

	if len(info.InterruptContexts) == 1 {
		r.emitInterruptContext(ctx, checkpointID, info.InterruptContexts[0])
		return nil
	}

	items := make([]InterruptBatchItem, 0, len(info.InterruptContexts))
	for _, ictx := range info.InterruptContexts {
		items = append(items, buildInterruptBatchItem(ictx))
	}
	r.emitEvent(ctx, EventInterruptBatchRequested, InterruptBatchPayload{
		CheckpointID: checkpointID,
		Items:        items,
	})

	return nil
}
func (r *TurnRunner) consumeExternalInterruptPayload(checkpointID string) (InterruptedPayload, bool) {
	r.mu.Lock()
	opts := r.interruptOpts
	active := r.interruptActive
	r.interruptActive = false
	r.mu.Unlock()
	if !active {
		return InterruptedPayload{}, false
	}
	payload := InterruptedPayload{
		Source:       "external",
		CheckpointID: checkpointID,
		Metadata:     maps.Clone(opts.Metadata),
	}
	if opts.Timeout != nil {
		payload.TimeoutMS = opts.Timeout.Milliseconds()
	}
	return payload, true
}
func (r *TurnRunner) emitInterruptContext(ctx context.Context, checkpointID string, ictx *compose.InterruptCtx) {
	if ictx == nil {
		payload := InterruptedPayload{Source: "custom", CheckpointID: checkpointID}
		r.emitEvent(ctx, EventInterrupted, payload)
		return
	}

	switch x := ictx.Info.(type) {
	case *deeptools.FollowUpInfo:
		r.emitEvent(ctx, EventFollowUpRequested, FollowUpRequestedPayload{
			InterruptID:  ictx.ID,
			CheckpointID: checkpointID,
			Info:         x,
		})
	case *deeptools.ApprovalInfo:
		r.emitEvent(ctx, EventApproveRequested, ApprovalRequiredPayload{
			InterruptID:  ictx.ID,
			CheckpointID: checkpointID,
			ApprovalInfo: x,
		})
	case *deeptools.ReviewEditInfo:
		r.emitEvent(ctx, EventApproveRequested, ApprovalRequiredPayload{
			InterruptID:    ictx.ID,
			CheckpointID:   checkpointID,
			ReviewEditInfo: x,
		})
	default:
		r.emitEvent(ctx, EventInterrupted, InterruptedPayload{
			Source:       "custom",
			InterruptID:  ictx.ID,
			CheckpointID: checkpointID,
			InfoType:     fmt.Sprintf("%T", ictx.Info),
			Info:         ictx.Info,
		})
	}
}

// consumeStream 消耗流式输出以驱动 Graph 执行
func (r *TurnRunner) waitStreamGraphComplete(stream *schema.StreamReader[*schema.Message]) error {
	defer stream.Close()
	for {
		_, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// buildCallbacks: 仅用于事件观测，不修改上下文与历史。
func (r *TurnRunner) buildCallbacks() callbacks.Handler {
	return cbutils.NewHandlerHelper().
		Tool(&cbutils.ToolCallbackHandler{
			OnStart: func(ctx context.Context, runInfo *callbacks.RunInfo, input *tool.CallbackInput) context.Context {
				if !r.waitModelEventBarrier(ctx) {
					return ctx
				}
				callID := toolCallbackCallID(ctx, input, nil)
				state := r.toolState(callID, runInfo.Name)
				args := callbackInputArgs(input)
				// 该工具调用已经有发送过事件
				if !state.markStartEmitted(time.Now(), args) {
					return ctx
				}
				r.emitEvent(ctx, EventToolStart, ToolStartPayload{Name: runInfo.Name, CallID: callID, Args: args})

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
				r.cbWg.Add(1)
				go func() {
					defer r.cbWg.Done()
					defer output.Close()
					for {
						chunk, err := output.Recv()
						if err != nil {
							if err == io.EOF {
								r.emitToolEnd(ctx, state, state.snapshotResult())
							} else {
								logs.CtxError(ctx, "[TurnRunner::toolStream] recv failed: tool=%s call_id=%s err=%v",
									state.name, state.callID, err)
							}
							return
						}
						if chunkCallID := toolCallbackExtraCallID(chunk.Extra); chunkCallID != "" {
							state.setCallID(chunkCallID)
						}
						text := callbackOutputText(chunk)
						state.appendResult(text)
						r.emitEvent(ctx, EventToolCallOutputChunk, ToolCallOutputChunkPayload{
							Name:   state.name,
							CallID: state.callID,
							Chunk:  text,
						})
					}
				}()
				return ctx
			},
			// OnError 无法识别中断错误，这里 eino 实现有 bug
			// OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			// 	_, ok := compose.ExtractInterruptInfo(err)
			// 	if ok {
			// 		return ctx
			// 	}
			// 	if r.thread != nil {
			// 		_ = r.thread.emitRuntimeEvent(ctx, turnID, EventError, ErrorPayload{Message: err.Error()})
			// 	}
			// 	return ctx
			// },
		}).
		ChatModel(&cbutils.ModelCallbackHandler{
			OnStart: func(ctx context.Context, runInfo *callbacks.RunInfo, input *model.CallbackInput) context.Context {
				runName := ""
				if runInfo != nil {
					runName = runInfo.Name
				}
				r.llmResponseID = uuid.NewString()
				r.beginModelEventBarrier(ctx)
				logs.CtxInfo(ctx, "[TurnRunner::model] request start: thread_id=%s turn_id=%s run_name=%s input_present=%t",
					r.threadID, r.turnID, runName, input != nil)
				if input != nil {
					r.emitEvent(ctx, EventLLMRequesting, input)
				} else {
					r.emitEvent(ctx, EventLLMRequesting, (*model.CallbackInput)(nil))
				}
				return ctx
			},
			OnEnd: func(ctx context.Context, runInfo *callbacks.RunInfo, output *model.CallbackOutput) context.Context {
				barrier := r.currentModelEventBarrier()
				defer r.releaseModelEventBarrier(barrier)
				runName := ""
				if runInfo != nil {
					runName = runInfo.Name
				}
				contentLen := 0
				toolCalls := 0
				if output != nil && output.Message != nil {
					contentLen = len(output.Message.Content)
					toolCalls = len(output.Message.ToolCalls)
				}
				logs.CtxInfo(ctx, "[TurnRunner::model] response end: thread_id=%s turn_id=%s run_name=%s content_len=%d tool_calls=%d usage_present=%t",
					r.threadID, r.turnID, runName, contentLen, toolCalls, output != nil && output.TokenUsage != nil)
				llmResponseID := r.llmResponseID
				if llmResponseID == "" {
					llmResponseID = uuid.NewString()
				}
				if output != nil {
					r.recordModelUsage(ctx, output.TokenUsage)
				}
				r.emitEvent(ctx, EventLLMEnd, llmEndFromCallbackOutput(output, llmResponseID))
				return ctx
			},
			OnEndWithStreamOutput: func(ctx context.Context, runInfo *callbacks.RunInfo, output *schema.StreamReader[*model.CallbackOutput]) context.Context {
				barrier := r.currentModelEventBarrier()
				if output == nil {
					r.releaseModelEventBarrier(barrier)
					return ctx
				}
				runName := ""
				if runInfo != nil {
					runName = runInfo.Name
				}
				llmResponseID := r.llmResponseID
				if llmResponseID == "" {
					llmResponseID = uuid.NewString()
				}
				r.cbWg.Add(1)
				go func() {
					defer r.cbWg.Done()
					defer r.releaseModelEventBarrier(barrier)
					logs.CtxInfo(ctx, "[TurnRunner::modelStream] stream merge begin: thread_id=%s turn_id=%s run_name=%s",
						r.threadID, r.turnID, runName)
					llmEnd, chunks, err := mergeModelCallbackStream(ctx, output, llmResponseID, func(ctx context.Context, chunk *schema.Message) {
						if chunk == nil || (chunk.Content == "" && chunk.ReasoningContent == "") {
							return
						}
						r.emitEvent(ctx, EventLLMToken, LLMTokenChunk{
							Text:          chunk.Content,
							ReasoningText: chunk.ReasoningContent,
							LLMResponseID: llmResponseID,
						})
					})
					if err != nil {
						logs.CtxError(ctx, "[TurnRunner::modelStream] recv failed: err=%v", err)
						return
					}
					contentLen := 0
					toolCalls := 0
					if llmEnd.Message != nil {
						contentLen = len(llmEnd.Message.Content)
						toolCalls = len(llmEnd.Message.ToolCalls)
					}
					logs.CtxInfo(ctx, "[TurnRunner::modelStream] stream merge done: thread_id=%s turn_id=%s run_name=%s chunks=%d content_len=%d tool_calls=%d usage_present=%t",
						r.threadID, r.turnID, runName, chunks, contentLen, toolCalls, llmEnd.TokenUsage != nil)
					r.recordModelUsage(ctx, llmEnd.TokenUsage)
					r.emitEvent(ctx, EventLLMEnd, llmEnd)
				}()
				return ctx
			},
			// OnError Pippit场景下无法识别大模型重试错误，先关闭
			//OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			//	logs.CtxError(ctx, "[TurnRunner::modelError] err=%v", err)
			//	r.emitEvent(ctx, EventError, ErrorPayload{Message: err.Error()})
			//	return ctx
			//},
		}).Handler()
}
func (r *TurnRunner) recordModelUsage(ctx context.Context, usage *model.TokenUsage) {
	if usage == nil || r.cm == nil {
		return
	}
	r.cm.RecordModelUsage(ctx, usage)
}
func normalizeInterruptAfterNodes(nodes []string) (normalized []string) {
	normalized = make([]string, len(nodes))
	for i, node := range nodes {
		if node == constant.ExecutorName {
			normalized[i] = constant.NodeKeyModel
			continue
		}
		normalized[i] = node
	}
	return normalized
}

func planStepsFromMiddleware(steps []plan.PlanStep) []PlanStep {
	if steps == nil {
		return nil
	}
	out := make([]PlanStep, len(steps))
	for i, step := range steps {
		out[i] = PlanStep{
			Step:   step.Step,
			Status: PlanStepStatus(step.Status),
		}
	}
	return out
}

func buildInterruptBatchItem(ictx *compose.InterruptCtx) InterruptBatchItem {
	item := InterruptBatchItem{}
	if ictx == nil {
		item.Kind = InterruptItemCustom
		item.InfoType = "<nil>"
		return item
	}

	item.InterruptID = ictx.ID
	item.InfoType = fmt.Sprintf("%T", ictx.Info)
	item.Info = ictx.Info

	switch x := ictx.Info.(type) {
	case *deeptools.FollowUpInfo:
		item.Kind = InterruptItemFollowUp
		item.FollowUpInfo = x
	case *deeptools.ApprovalInfo:
		item.Kind = InterruptItemApprove
		item.ApprovalInfo = x
	case *deeptools.ReviewEditInfo:
		item.Kind = InterruptItemReviewEdit
		item.ReviewEditInfo = x
	default:
		item.Kind = InterruptItemCustom
	}

	return item
}

// Clone returns a turn-local copy of the runner config.
//
// Dependency objects such as models, backends, stores, loaders, and middleware
// instances are intentionally shared by reference. Container fields are copied
// so turn-local edits cannot mutate the thread-level base config by appending to
// slices or writing maps.
func (c *TurnRunnerConfig) Clone() *TurnRunnerConfig {
	if c == nil {
		return &TurnRunnerConfig{}
	}
	out := *c
	out.Tools = append([]tool.BaseTool(nil), c.Tools...)
	if c.FilesystemConfig != nil {
		fs := *c.FilesystemConfig
		out.FilesystemConfig = &fs
	}
	if c.HITLConfig != nil {
		hitl := *c.HITLConfig
		hitl.NeedApproveTools = maps.Clone(c.HITLConfig.NeedApproveTools)
		hitl.ToolPolicyGates = maps.Clone(c.HITLConfig.ToolPolicyGates)
		hitl.NeedReviewAndEditTools = maps.Clone(c.HITLConfig.NeedReviewAndEditTools)
		out.HITLConfig = &hitl
	}
	out.SubAgents = append([]*subagent.SubAgent(nil), c.SubAgents...)
	out.InterruptAfterNodes = append([]string(nil), c.InterruptAfterNodes...)
	out.Middlewares = append([]middleware.Middleware(nil), c.Middlewares...)
	out.Callbacks = append([]callbacks.Handler(nil), c.Callbacks...)
	return &out
}

// emitEvent is the single producer boundary for events emitted by one turn.
// The event lock keeps closeEventBus from racing with late callbacks.
func (r *TurnRunner) emitEvent(ctx context.Context, typ EventType, payload any) {
	loc := eventLocationFromContext(ctx)
	if loc == (EventLocation{}) && r.agent != nil {
		loc = EventLocation{AgentName: r.agent.Name(), AgentDepth: r.agent.Depth()}
	}
	ev := Event{
		Loc: loc, ID: r.eventIDProvider(ctx, r.threadID, r.turnID), TS: time.Now(),
		ThreadID: r.threadID, TurnID: r.turnID, Type: typ, Payload: payload,
	}
	r.eventMu.RLock()
	defer r.eventMu.RUnlock()
	if r.eventClosed {
		logs.CtxWarn(ctx, "[TurnRunner::emitEvent] drop late event after turn event channel closed: thread_id=%s turn_id=%s event_type=%s",
			r.threadID, r.turnID, typ)
		return
	}
	queueLen, queueCap, startedAt := len(r.eventBus), cap(r.eventBus), time.Now()
	r.eventBus <- ev
	if elapsed := time.Since(startedAt); elapsed > eventEnqueueWarnThreshold {
		logs.CtxWarn(ctx, "[TurnRunner::emitEvent] slow event enqueue: thread_id=%s turn_id=%s event_type=%s elapsed=%s queue_len_before=%d queue_cap=%d",
			r.threadID, r.turnID, typ, elapsed, queueLen, queueCap)
	}
}

// closeEventBus closes the turn-local event stream exactly once. The thread
// waits for the forwarding goroutine before declaring the turn complete.
func (r *TurnRunner) closeEventBus() {
	r.eventMu.Lock()
	defer r.eventMu.Unlock()
	if r.eventClosed {
		return
	}
	r.eventClosed = true
	close(r.eventBus)
}

// Interrupt requests an interruption of the currently running DeepAgent graph.
// A nil timeout uses Eino's default interrupt behavior.
func (r *TurnRunner) Interrupt(timeout *time.Duration) bool {
	return r.InterruptWithOptions(InterruptOptions{Timeout: timeout})
}

// InterruptWithOptions records external interrupt metadata before asking
// DeepAgent to stop. The metadata is consumed when RunTurn translates the
// graph interrupt into an agentthread event.
func (r *TurnRunner) InterruptWithOptions(opts InterruptOptions) bool {
	r.mu.Lock()
	agent := r.agent
	if agent == nil {
		r.mu.Unlock()
		return false
	}
	r.interruptOpts = InterruptOptions{Timeout: opts.Timeout, Metadata: maps.Clone(opts.Metadata)}
	r.interruptActive = true
	r.mu.Unlock()
	if opts.Timeout == nil {
		return agent.Interrupt()
	}
	return agent.Interrupt(compose.WithGraphInterruptTimeout(*opts.Timeout))
}

func (r *TurnRunner) beginModelEventBarrier(ctx context.Context) *modelEventBarrier {
	if r.cfg != nil && r.cfg.EnableStreamToolCall {
		return nil
	}
	barrier := newModelEventBarrier()
	r.modelBarrierMu.Lock()
	if r.modelBarrier != nil {
		r.modelBarrier.release()
		logs.CtxWarn(ctx, "[TurnRunner::modelBarrier] release stale model barrier before new model call: thread_id=%s turn_id=%s", r.threadID, r.turnID)
	}
	r.modelBarrier = barrier
	r.modelBarrierMu.Unlock()
	return barrier
}

func (r *TurnRunner) currentModelEventBarrier() *modelEventBarrier {
	if r.cfg != nil && r.cfg.EnableStreamToolCall {
		return nil
	}
	r.modelBarrierMu.Lock()
	defer r.modelBarrierMu.Unlock()
	return r.modelBarrier
}

func (r *TurnRunner) releaseModelEventBarrier(barrier *modelEventBarrier) {
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

func (r *TurnRunner) waitModelEventBarrier(ctx context.Context) bool {
	barrier := r.currentModelEventBarrier()
	if barrier == nil {
		return true
	}
	startedAt := time.Now()
	select {
	case <-barrier.done:
	case <-ctx.Done():
		logs.CtxWarn(ctx, "[TurnRunner::modelBarrier] stop waiting for llm_end before tool start: thread_id=%s turn_id=%s err=%v", r.threadID, r.turnID, ctx.Err())
		return false
	}
	if elapsed := time.Since(startedAt); elapsed > modelBarrierWaitWarnThreshold {
		logs.CtxWarn(ctx, "[TurnRunner::modelBarrier] waited for llm_end before tool start: thread_id=%s turn_id=%s elapsed=%s", r.threadID, r.turnID, elapsed)
	}
	return true
}

func (r *TurnRunner) toolState(callID, name string) *toolEventState {
	key := toolEventKey(callID, name)
	if state, ok := r.toolEventMw.store.load(key); ok {
		if state.name == "" {
			state.name = name
		}
		if state.callID == "" {
			state.callID = callID
		}
		return state
	}
	return r.toolEventMw.store.loadOrStore(key, &toolEventState{name: name, callID: callID})
}

func (r *TurnRunner) lookupOrCreateToolState(callID, name string) *toolEventState {
	if callID != "" {
		return r.toolState(callID, name)
	}
	if state := r.findPendingToolStateByName(name); state != nil {
		return state
	}
	return r.toolState(callID, name)
}

func (r *TurnRunner) findPendingToolStateByName(name string) *toolEventState {
	var ret *toolEventState
	r.toolEventMw.store.rangeStates(func(_ string, state *toolEventState) bool {
		if state.name == name && state.isPendingStreamBinding() {
			ret = state
			return false
		}
		return true
	})
	return ret
}

func (r *TurnRunner) emitToolEnd(ctx context.Context, state *toolEventState, result string) {
	if state == nil || !state.markEndEmitted() {
		return
	}
	snapshot := state.snapshot()
	r.emitEvent(ctx, EventToolEnd, ToolEndPayload{
		Name: snapshot.name, CallID: snapshot.callID, ToolStartTime: snapshot.toolStartTime,
		ArgumentsInJSON: snapshot.argumentsInJSON, Result: result,
	})
	// Keep state for interrupt resume: persisted start/end flags prevent
	// duplicate tool events when callbacks are replayed.
}
