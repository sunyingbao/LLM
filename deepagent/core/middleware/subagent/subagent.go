package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"code.byted.org/gopkg/logs/v2"
	deeptools "eino-cli/deepagent/core/tools"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
)

// SubAgent 定义子代理规范
type SubAgent struct {
	// Name 子代理名称，用于在 task 工具中引用
	Name string

	// Description 子代理描述，告诉主代理何时使用
	Description string

	// SystemPrompt 子代理的系统提示词
	SystemPrompt string

	// Tools 子代理可用的工具（手动指定）
	Tools []tool.BaseTool

	// ToolMask 用于对子代理最终可见工具做过滤。
	// 仅作用于当前子代理，不继承父 agent 的 ToolMask。
	ToolMask deeptools.Mask

	// EnableFilesystem 是否启用文件系统工具（自动从父代理继承 Backend 构建）
	// 设置为 true 时，子代理会自动获得文件系统工具，无需手动设置 Tools
	EnableFilesystem bool

	// ReadOnly 文件系统只读模式（仅在 EnableFilesystem=true 时生效）
	// true: 只有 ls/read_file/glob/grep
	// false: 全部文件工具（包括 write_file/edit_file/execute 等）
	ReadOnly bool

	// EnableWeb 是否启用 Web 工具（搜索、HTTP 请求、URL 抓取）
	// 设置为 true 时，子代理会自动获得 Web 工具
	EnableWeb bool

	// EnableSkill 是否启用技能中间件
	// 设置为 true 时，子代理会继承主 agent 的 skill middleware 构造逻辑，并为当前子代理重新创建一个实例
	EnableSkill bool

	// Model 子代理使用的模型（可选，默认使用主代理的模型）
	Model model.ToolCallingChatModel

	// MaxSteps 最大执行步数
	MaxSteps int
}

// SubAgentFactory 子代理工厂函数类型
// 用于创建子代理的执行器，避免循环依赖
// 接收完整的 SubAgent 定义，工厂可根据 EnableFilesystem/ReadOnly/EnableWeb 等字段自动配置
type SubAgentFactory func(
	ctx context.Context,
	chatModel model.ToolCallingChatModel,
	subAgent *SubAgent,
	defaultTools []tool.BaseTool,
	defaultMiddleware []middleware.Middleware,
) (SubAgentRunner, error)

// SubAgentRunner 子代理执行器接口
type SubAgentRunner interface {
	// Run 执行子代理
	Run(ctx context.Context, messages []*schema.Message, opts ...SubAgentRunOption) (*schema.Message, error)
	// Stream 流式执行子代理，输出子代理最终 assistant message 的 chunk。
	Stream(ctx context.Context, messages []*schema.Message, opts ...SubAgentRunOption) (*schema.StreamReader[*schema.Message], error)
	// Close 关闭子代理
	Close(ctx context.Context) error

	Depth() int
}

// SubAgentRunOptions 描述一次子代理执行的运行时配置。
type SubAgentRunOptions struct {
	CheckpointID string
}

// SubAgentRunOption 修改一次子代理执行的运行时配置。
type SubAgentRunOption func(*SubAgentRunOptions)

type SubAgentContextInjector interface {
	LoadContext(ctx context.Context, agentName string) ([]*schema.Message, error)
}

// WithSubAgentRunCheckpointID 设置子代理执行使用的 checkpoint ID。
func WithSubAgentRunCheckpointID(checkpointID string) SubAgentRunOption {
	return func(o *SubAgentRunOptions) {
		o.CheckpointID = checkpointID
	}
}

// ApplySubAgentRunOptions 合并子代理执行参数。
func ApplySubAgentRunOptions(opts ...SubAgentRunOption) *SubAgentRunOptions {
	ro := &SubAgentRunOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(ro)
		}
	}
	return ro
}

// SubAgentConfig 子代理中间件配置
type SubAgentConfig struct {
	// SubAgents 预定义的子代理列表（来自 YAML 等外部配置）
	SubAgents []*SubAgent

	// DefaultModel 默认模型，用于没有指定模型的子代理
	DefaultModel model.ToolCallingChatModel

	// DefaultTools 默认工具（手动指定的额外工具）
	DefaultTools []tool.BaseTool

	// Factory 子代理工厂函数（必需）
	// 用于创建完整的 DeepAgent 来执行子代理
	Factory SubAgentFactory

	// SubAgentSkillMiddlewareFactory 用于为启用 skill 的子代理创建新的 skill middleware 实例
	// 每次执行子代理时都会重新调用，避免与主 agent 共享 middleware 内部状态
	SubAgentSkillMiddlewareFactory func() middleware.Middleware

	// Deprecated: use SubAgentSkillMiddlewareFactory.
	SkillMiddlewareFactory func() middleware.Middleware

	// ContextInjector 用于给子 agent 注入上下文
	ContextInjector SubAgentContextInjector

	// ToolMask 控制该中间件自身工具和工具提示词的可见性。
	ToolMask deeptools.Mask

	// EnableTaskStreaming 使 task 工具实现 StreamableTool。
	// 当前仅流式输出子 agent 最终 assistant response，不转发子 agent 内部事件。
	EnableTaskStreaming bool
}

// SubAgentMiddleware 提供子代理委托能力
type SubAgentMiddleware struct {
	middleware.BaseMiddleware
	mu                             sync.RWMutex
	subAgents                      map[string]*SubAgent
	model                          model.ToolCallingChatModel
	defaultTools                   []tool.BaseTool
	factory                        SubAgentFactory
	subAgentSkillMiddlewareFactory func() middleware.Middleware
	contextInjector                SubAgentContextInjector
	results                        map[string]string
	toolMask                       deeptools.Mask
	enableTaskStreaming            bool
}

// ExplorerSubAgent 是只读调研型子代理的便捷预设。
var ExplorerSubAgent = &SubAgent{
	Name:             constant.ExplorerName,
	Description:      constant.DefaultExplorerDescription,
	SystemPrompt:     constant.DefaultExplorerPrompt,
	MaxSteps:         constant.DefaultExplorerMaxSteps,
	EnableFilesystem: true,
	ReadOnly:         true,
	EnableWeb:        true,
}

// ExecutorSubAgent 是完整执行型子代理的便捷预设。
var ExecutorSubAgent = &SubAgent{
	Name:             constant.ExecutorName,
	Description:      constant.DefaultExecutorDescription,
	SystemPrompt:     constant.DefaultExecutorPrompt,
	MaxSteps:         constant.DefaultExecutorMaxSteps,
	EnableFilesystem: true,
	ReadOnly:         false,
	EnableWeb:        true,
}

// New 创建子代理中间件
// 子代理列表完全由调用方通过 cfg.SubAgents 显式指定。
func New(cfg *SubAgentConfig) *SubAgentMiddleware {
	subAgentSkillMiddlewareFactory := cfg.SubAgentSkillMiddlewareFactory
	if subAgentSkillMiddlewareFactory == nil {
		subAgentSkillMiddlewareFactory = cfg.SkillMiddlewareFactory
	}

	m := &SubAgentMiddleware{
		subAgents:                      make(map[string]*SubAgent),
		model:                          cfg.DefaultModel,
		defaultTools:                   cfg.DefaultTools,
		factory:                        cfg.Factory,
		subAgentSkillMiddlewareFactory: subAgentSkillMiddlewareFactory,
		results:                        make(map[string]string),
		toolMask:                       cfg.ToolMask,
		enableTaskStreaming:            cfg.EnableTaskStreaming,
	}

	m.contextInjector = cfg.ContextInjector

	// 注册调用方显式声明的子代理
	for _, sa := range cfg.SubAgents {
		m.subAgents[sa.Name] = sa
	}

	return m
}

func (m *SubAgentMiddleware) Name() string {
	return constant.MiddlewareSubAgent
}

func (m *SubAgentMiddleware) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	tools := []tool.BaseTool{
		m.newTaskTool(),
		m.newListSubAgentsTool(),
	}
	return deeptools.FilterToolsByMask(ctx, tools, m.toolMask, "SubAgentMiddleware::Tools"), nil
}

// buildPrompt 构建子代理系统提示词
func (m *SubAgentMiddleware) buildPrompt(ctx context.Context) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.toolMask == nil {
		return m.buildDefaultPromptLocked()
	}

	visibleTools, err := m.Tools(ctx)
	if err != nil {
		return ""
	}
	visible := deeptools.ToolNameSet(ctx, visibleTools, "SubAgentMiddleware::buildPrompt")
	if !visible[constant.ToolTask] && !visible[constant.ToolListSubAgents] {
		return ""
	}
	return m.buildMaskedPromptLocked(visible)
}

// BuildInitialContext 构建初始上下文
func (m *SubAgentMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	if prompt := m.buildPrompt(ctx); prompt != "" {
		return []*schema.Message{schema.SystemMessage(prompt)}, nil
	}
	return nil, nil
}

// HasAgent 检查指定名称的子代理是否已注册
func (m *SubAgentMiddleware) HasAgent(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.subAgents[name]
	return exists
}

// ListAgentNames 返回所有已注册的子代理名称
func (m *SubAgentMiddleware) ListAgentNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.subAgents))
	for name := range m.subAgents {
		names = append(names, name)
	}
	return names
}

// RegisterSubAgent 注册子代理
func (m *SubAgentMiddleware) RegisterSubAgent(sa *SubAgent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subAgents[sa.Name] = sa
}

// ==================== task 工具 ====================

type taskInput struct {
	SubAgent    string `json:"subagent" jsonschema:"description=子代理名称，使用 list_subagents 查看可用的子代理"`
	Task        string `json:"task" jsonschema:"description=任务描述，提供足够的上下文信息"`
	Context     string `json:"context,omitempty" jsonschema:"description=额外的上下文信息"`
	WaitForDone *bool  `json:"wait_for_done,omitempty" jsonschema:"description=是否等待任务完成，默认true"`
	ForkContext bool   `json:"fork_context,omitempty" jsonschema:"description=是否继承主 agent 上下文, 默认为 false"`
}

func (m *SubAgentMiddleware) newTaskTool() tool.BaseTool {
	if m.enableTaskStreaming {
		return m.newStreamTaskTool()
	}
	return m.newInvokableTaskTool()
}

func (m *SubAgentMiddleware) newInvokableTaskTool() tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolTask,
		subAgentToolDesc(constant.ToolTask),
		func(ctx context.Context, input *taskInput) (string, error) {
			return m.runTask(ctx, input)
		},
		subAgentToolOptions(constant.ToolTask)...,
	)
	return t
}

func (m *SubAgentMiddleware) newStreamTaskTool() tool.BaseTool {
	t, _ := utils.InferStreamTool(
		constant.ToolTask,
		subAgentToolDesc(constant.ToolTask),
		func(ctx context.Context, input *taskInput) (*schema.StreamReader[string], error) {
			return m.streamTask(ctx, input)
		},
		subAgentToolOptions(constant.ToolTask)...,
	)
	return t
}

type taskExecution struct {
	input        *taskInput
	sa           *SubAgent
	agentModel   model.ToolCallingChatModel
	waitForDone  bool
	checkpointID string
}

func (m *SubAgentMiddleware) prepareTaskExecution(ctx context.Context, input *taskInput) (*taskExecution, string, error) {
	logs.CtxInfo(ctx, "[task] start, subagent: %s, task_len: %d", input.SubAgent, len(input.Task))

	m.mu.RLock()
	sa, exists := m.subAgents[input.SubAgent]
	m.mu.RUnlock()

	if !exists {
		logs.CtxError(ctx, "[task] subagent not found, subagent: %s", input.SubAgent)
		return nil, fmt.Sprintf("[Error] task failed: %s: %s", constant.ErrMsgSubAgentNotFound, input.SubAgent), nil
	}

	agentModel := sa.Model
	if agentModel == nil {
		agentModel = m.model
	}
	if agentModel == nil {
		logs.CtxError(ctx, "[task] no model available, subagent: %s", input.SubAgent)
		return nil, fmt.Sprintf("[Error] task failed: %s: %s", constant.ErrMsgSubAgentNoModel, input.SubAgent), nil
	}

	exec := &taskExecution{
		input:       input,
		sa:          sa,
		agentModel:  agentModel,
		waitForDone: input.WaitForDone == nil || *input.WaitForDone,
	}
	if exec.waitForDone {
		wasInterrupted, hasState, checkpointID := tool.GetInterruptState[string](ctx)
		if wasInterrupted && !hasState {
			return nil, "", fmt.Errorf("subagent task resumed without checkpoint state")
		}
		if checkpointID == "" {
			checkpointID = subAgentCheckpointID(input.SubAgent, input.Task)
		}
		exec.checkpointID = checkpointID
	}
	return exec, "", nil
}

func (m *SubAgentMiddleware) startAsyncTask(ctx context.Context, exec *taskExecution) (string, error) {
	task := exec.input.Task
	subAgentName := exec.input.SubAgent
	saCopy := *exec.sa
	messages, err := m.buildSubAgentMessages(ctx, exec.sa.Name, exec.input.Context, task, exec.input.ForkContext)
	if err != nil {
		logs.CtxError(ctx, "[task] failed to build subagent context, subagent: %s, err: %v", exec.input.SubAgent, err)
		return "", err
	}
	runCtx := context.WithoutCancel(ctx)
	go m.runSubAgentAsync(runCtx, &saCopy, exec.agentModel, subAgentName, task, messages)
	return fmt.Sprintf("subagent %s started asynchronously", subAgentName), nil
}

// task 工具使用调用时收到的 ctx 启动子 agent。
// 该 ctx 中包含父 agent 本轮 Run/Stream 注入的 Eino callbacks；子 agent Run/Stream
// 会复用这些 handlers，同时在自身 prepareRun 阶段把 deepagents.GetDeepAgent(ctx)
// 覆盖为当前子 agent。因此 callback 内需要区分主/子 agent 时，应读取
// deepagents.GetDeepAgent(ctx).Name()/Depth()，而不是假定 handler 只属于父 agent。
func (m *SubAgentMiddleware) runTask(ctx context.Context, input *taskInput) (string, error) {
	exec, directOutput, err := m.prepareTaskExecution(ctx, input)
	if err != nil || directOutput != "" {
		return directOutput, err
	}
	if !exec.waitForDone {
		return m.startAsyncTask(ctx, exec)
	}

	result, err := m.executeSubAgent(ctx, exec.sa, exec.agentModel, input.Task, input.Context, input.ForkContext,
		WithSubAgentRunCheckpointID(exec.checkpointID))
	if err != nil {
		logs.CtxError(ctx, "[task] subagent execution failed, subagent: %s, err: %v", input.SubAgent, err)
		if _, ok := compose.ExtractInterruptInfo(err); ok {
			return "", tool.CompositeInterrupt(ctx, "subagent task interrupted", exec.checkpointID, err)
		}
		return "", err
	}

	logs.CtxInfo(ctx, "[task] subagent execution completed, subagent: %s, result_len: %d", input.SubAgent, len(result))
	m.storeResult(input.SubAgent, input.Task, result)
	return result, nil
}

func (m *SubAgentMiddleware) streamTask(ctx context.Context, input *taskInput) (*schema.StreamReader[string], error) {
	exec, directOutput, err := m.prepareTaskExecution(ctx, input)
	if err != nil {
		return nil, err
	}
	if directOutput != "" {
		return schema.StreamReaderFromArray([]string{directOutput}), nil
	}
	if !exec.waitForDone {
		out, err := m.startAsyncTask(ctx, exec)
		if err != nil {
			return nil, err
		}
		return schema.StreamReaderFromArray([]string{out}), nil
	}

	stream, err := m.executeSubAgentStream(ctx, exec.sa, exec.agentModel, input.Task, input.Context, input.ForkContext,
		WithSubAgentRunCheckpointID(exec.checkpointID))
	if err != nil {
		logs.CtxError(ctx, "[task] subagent execution failed, subagent: %s, err: %v", input.SubAgent, err)
		if _, ok := compose.ExtractInterruptInfo(err); ok {
			return nil, tool.CompositeInterrupt(ctx, "subagent task interrupted", exec.checkpointID, err)
		}
		return nil, err
	}

	sr, sw := schema.Pipe[string](1)
	go func() {
		defer sw.Close()
		defer stream.Close()
		var result string
		for {
			msg, recvErr := stream.Recv()
			if errors.Is(recvErr, io.EOF) {
				logs.CtxInfo(ctx, "[task] subagent execution completed, subagent: %s, result_len: %d", input.SubAgent, len(result))
				m.storeResult(input.SubAgent, input.Task, result)
				return
			}
			if recvErr != nil {
				logs.CtxError(ctx, "[task] subagent execution failed, subagent: %s, err: %v", input.SubAgent, recvErr)
				if _, ok := compose.ExtractInterruptInfo(recvErr); ok {
					recvErr = tool.CompositeInterrupt(ctx, "subagent task interrupted", exec.checkpointID, recvErr)
				}
				sw.Send("", recvErr)
				return
			}
			text := subAgentResultText(msg)
			if text == "" {
				continue
			}
			result += text
			if sw.Send(text, nil) {
				return
			}
		}
	}()
	return sr, nil
}

func subAgentResultText(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	if msg.Role != "" && msg.Role != schema.Assistant {
		return ""
	}
	if msg.Content != "" {
		return msg.Content
	}
	if len(msg.MultiContent) == 0 {
		return ""
	}
	var b strings.Builder
	for _, part := range msg.MultiContent {
		if part.Type == schema.ChatMessagePartTypeText && part.Text != "" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func (m *SubAgentMiddleware) runSubAgentAsync(
	ctx context.Context,
	sa *SubAgent,
	chatModel model.ToolCallingChatModel,
	subAgentName string,
	task string,
	messages []*schema.Message,
) {
	logs.CtxInfo(ctx, "[task] async subagent execution started, subagent: %s, task_len: %d", subAgentName, len(task))
	result, err := m.executeSubAgentWithMessages(ctx, sa, chatModel, task, messages)
	if err != nil {
		logs.CtxError(ctx, "[task] async subagent execution failed, subagent: %s, err: %v", subAgentName, err)
		m.storeResult(subAgentName, task, fmt.Sprintf("[Error] task failed: %v", err))
		return
	}
	logs.CtxInfo(ctx, "[task] async subagent execution completed, subagent: %s, result_len: %d", subAgentName, len(result))
	m.storeResult(subAgentName, task, result)
}

func (m *SubAgentMiddleware) storeResult(subAgentName, task, result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[resultKey(subAgentName, task)] = result
}

func resultKey(subAgentName, task string) string {
	return subAgentName + ":" + task[:min(50, len(task))]
}

func subAgentCheckpointID(subAgentName, task string) string {
	return fmt.Sprintf("subagent:%s:%s", subAgentName, uuid.NewString())
}

// executeSubAgent 执行子代理
func (m *SubAgentMiddleware) executeSubAgent(
	ctx context.Context,
	sa *SubAgent,
	chatModel model.ToolCallingChatModel,
	task string,
	extraContext string,
	forkContext bool,
	runOpts ...SubAgentRunOption,
) (string, error) {
	if m.factory == nil {
		logs.CtxError(ctx, "[executeSubAgent] factory not configured, agent_name: %s", sa.Name)
		return "", fmt.Errorf("sub-agent factory not configured")
	}

	messages, err := m.buildSubAgentMessages(ctx, sa.Name, extraContext, task, forkContext)
	if err != nil {
		logs.CtxError(ctx, "[executeSubAgent] failed to build sub-agent messages, agent_name: %s, err: %v", sa.Name, err)
		return "", err
	}
	return m.executeSubAgentWithMessages(ctx, sa, chatModel, task, messages, runOpts...)
}

func (m *SubAgentMiddleware) executeSubAgentStream(
	ctx context.Context,
	sa *SubAgent,
	chatModel model.ToolCallingChatModel,
	task string,
	extraContext string,
	forkContext bool,
	runOpts ...SubAgentRunOption,
) (*schema.StreamReader[*schema.Message], error) {
	if m.factory == nil {
		logs.CtxError(ctx, "[executeSubAgent] factory not configured, agent_name: %s", sa.Name)
		return nil, fmt.Errorf("sub-agent factory not configured")
	}

	messages, err := m.buildSubAgentMessages(ctx, sa.Name, extraContext, task, forkContext)
	if err != nil {
		logs.CtxError(ctx, "[executeSubAgent] failed to build sub-agent messages, agent_name: %s, err: %v", sa.Name, err)
		return nil, err
	}
	return m.executeSubAgentStreamWithMessages(ctx, sa, chatModel, task, messages, runOpts...)
}

func (m *SubAgentMiddleware) buildSubAgentMessages(
	ctx context.Context,
	agentName string,
	extraContext string,
	task string,
	forkContext bool,
) ([]*schema.Message, error) {
	var messages []*schema.Message
	if extraContext != "" {
		messages = append(messages, schema.UserMessage(fmt.Sprintf("上下文信息:\n%s", extraContext)))
	}

	if forkContext && m.contextInjector != nil {
		forkMsg, err := m.contextInjector.LoadContext(ctx, agentName)
		if err != nil {
			return nil, err
		}
		if len(forkMsg) > 0 {
			messages = append(messages, schema.UserMessage(subAgentPromptText().contextBegin))
			messages = append(messages, forkMsg...)
			messages = append(messages, schema.UserMessage(subAgentPromptText().contextEnd))
		}
	}
	if task != "" {
		messages = append(messages, schema.UserMessage(task))
	}

	return messages, nil
}

func (m *SubAgentMiddleware) executeSubAgentWithMessages(
	ctx context.Context,
	sa *SubAgent,
	chatModel model.ToolCallingChatModel,
	task string,
	messages []*schema.Message,
	runOpts ...SubAgentRunOption,
) (string, error) {
	startTime := time.Now()
	defaultMiddleware := m.buildDefaultMiddlewares(sa)
	logs.CtxInfo(ctx, "[executeSubAgent] creating sub-agent, agent_name: %s, filesystem: %v, read_only: %v, web: %v, skill: %v, max_steps: %d, task_len: %d", sa.Name, sa.EnableFilesystem, sa.ReadOnly, sa.EnableWeb, sa.EnableSkill, sa.MaxSteps, len(task))

	// 使用工厂创建子 agent，传入完整的 SubAgent 定义
	runner, err := m.factory(ctx, chatModel, sa, m.defaultTools, defaultMiddleware)
	if err != nil {
		logs.CtxError(ctx, "[executeSubAgent] failed to create sub-agent, agent_name: %s, err: %v", sa.Name, err)
		return "", fmt.Errorf("failed to create sub-agent: %w", err)
	}
	defer runner.Close(ctx)

	logs.CtxInfo(ctx, "[executeSubAgent] sub-agent created, agent_name: %s, elapsed_ms: %d", sa.Name, time.Since(startTime).Milliseconds())

	// 执行子 agent
	logs.CtxInfo(ctx, "[executeSubAgent] running sub-agent, agent_name: %s, message_count: %d", sa.Name, len(messages))
	result, err := runner.Run(ctx, messages, runOpts...)
	elapsed := time.Since(startTime).Milliseconds()

	if err != nil {
		logs.CtxError(ctx, "[executeSubAgent] sub-agent execution failed, agent_name: %s, elapsed_ms: %d, err: %v", sa.Name, elapsed, err)
		return "", err
	}

	resultText := subAgentResultText(result)
	logs.CtxInfo(ctx, "[executeSubAgent] sub-agent execution completed, agent_name: %s, elapsed_ms: %d, result_len: %d", sa.Name, elapsed, len(resultText))
	return resultText, nil
}

func (m *SubAgentMiddleware) executeSubAgentStreamWithMessages(
	ctx context.Context,
	sa *SubAgent,
	chatModel model.ToolCallingChatModel,
	task string,
	messages []*schema.Message,
	runOpts ...SubAgentRunOption,
) (*schema.StreamReader[*schema.Message], error) {
	startTime := time.Now()
	defaultMiddleware := m.buildDefaultMiddlewares(sa)
	logs.CtxInfo(ctx, "[executeSubAgent] creating sub-agent, agent_name: %s, filesystem: %v, read_only: %v, web: %v, skill: %v, max_steps: %d, task_len: %d", sa.Name, sa.EnableFilesystem, sa.ReadOnly, sa.EnableWeb, sa.EnableSkill, sa.MaxSteps, len(task))

	runner, err := m.factory(ctx, chatModel, sa, m.defaultTools, defaultMiddleware)
	if err != nil {
		logs.CtxError(ctx, "[executeSubAgent] failed to create sub-agent, agent_name: %s, err: %v", sa.Name, err)
		return nil, fmt.Errorf("failed to create sub-agent: %w", err)
	}

	logs.CtxInfo(ctx, "[executeSubAgent] sub-agent created, agent_name: %s, elapsed_ms: %d", sa.Name, time.Since(startTime).Milliseconds())
	logs.CtxInfo(ctx, "[executeSubAgent] streaming sub-agent, agent_name: %s, message_count: %d", sa.Name, len(messages))

	childStream, err := runner.Stream(ctx, messages, runOpts...)
	if err != nil {
		_ = runner.Close(ctx)
		elapsed := time.Since(startTime).Milliseconds()
		logs.CtxError(ctx, "[executeSubAgent] sub-agent execution failed, agent_name: %s, elapsed_ms: %d, err: %v", sa.Name, elapsed, err)
		return nil, err
	}

	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()
		defer runner.Close(ctx)
		defer childStream.Close()
		for {
			msg, recvErr := childStream.Recv()
			if errors.Is(recvErr, io.EOF) {
				return
			}
			if recvErr != nil {
				sw.Send(nil, recvErr)
				return
			}
			if sw.Send(msg, nil) {
				return
			}
		}
	}()
	return sr, nil
}

func (m *SubAgentMiddleware) buildDefaultMiddlewares(sa *SubAgent) []middleware.Middleware {
	if sa == nil || !sa.EnableSkill || m.subAgentSkillMiddlewareFactory == nil {
		return nil
	}

	skillMiddleware := m.subAgentSkillMiddlewareFactory()
	if skillMiddleware == nil {
		return nil
	}
	if setter, ok := skillMiddleware.(interface{ SetToolMask(deeptools.Mask) }); ok {
		setter.SetToolMask(sa.ToolMask)
	}

	return []middleware.Middleware{skillMiddleware}
}

// ExecuteSubAgent 公共方法：按名称执行子代理
// 由 PlanningMiddleware.dispatch_tasks 调用
func (m *SubAgentMiddleware) ExecuteSubAgent(ctx context.Context, agentName, task, extraContext string) (string, error) {
	if agentName == "" {
		agentName = constant.ExecutorName
	}

	logs.CtxInfo(ctx, "[ExecuteSubAgent] start, agent_name: %s, task_len: %d", agentName, len(task))

	m.mu.RLock()
	sa, exists := m.subAgents[agentName]
	m.mu.RUnlock()

	if !exists {
		logs.CtxError(ctx, "[ExecuteSubAgent] agent not found, agent_name: %s", agentName)
		return "", fmt.Errorf("%s: %s", constant.ErrMsgSubAgentNotFound, agentName)
	}

	agentModel := sa.Model
	if agentModel == nil {
		agentModel = m.model
	}
	if agentModel == nil {
		logs.CtxError(ctx, "[ExecuteSubAgent] no model available, agent_name: %s", agentName)
		return "", fmt.Errorf("%s: %s", constant.ErrMsgSubAgentNoModel, agentName)
	}

	return m.executeSubAgent(ctx, sa, agentModel, task, extraContext, false)
}

// ==================== list_subagents 工具 ====================

// listSubAgentsInput 输入结构体
// 添加占位参数避免空参数导致 OpenRouter API 500 错误
type listSubAgentsInput struct {
	// Verbose 是否返回详细信息，可选参数
	Verbose bool `json:"verbose,omitempty" jsonschema:"description=是否返回详细信息,可选"`
}

func (m *SubAgentMiddleware) newListSubAgentsTool() tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	t, _ := utils.InferTool(
		constant.ToolListSubAgents,
		subAgentToolDesc(constant.ToolListSubAgents),
		func(ctx context.Context, input *listSubAgentsInput) (string, error) {
			m.mu.RLock()
			defer m.mu.RUnlock()

			if len(m.subAgents) == 0 {
				return constant.MsgNoSubAgents, nil
			}

			type subAgentInfo struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				Tools       []string `json:"tools,omitempty"`
			}

			var agents []subAgentInfo
			for name, sa := range m.subAgents {
				info := subAgentInfo{
					Name:        name,
					Description: sa.Description,
				}

				for _, t := range sa.Tools {
					tInfo, _ := t.Info(ctx)
					if tInfo != nil {
						info.Tools = append(info.Tools, tInfo.Name)
					}
				}

				agents = append(agents, info)
			}

			output, _ := json.MarshalIndent(agents, "", "  ")
			return string(output), nil
		},
		subAgentToolOptions(constant.ToolListSubAgents)...,
	)
	return t
}
