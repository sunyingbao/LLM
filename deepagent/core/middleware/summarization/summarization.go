package summarization

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/types"
	"eino-cli/deepagent/core/utils"
	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// TriggerType 触发条件类型
type TriggerType string

const (
	// TriggerTypeFraction 基于模型容量百分比触发
	TriggerTypeFraction TriggerType = "fraction"
	// TriggerTypeTokens 基于固定 token 数触发
	TriggerTypeTokens TriggerType = "tokens"
	// TriggerTypeMessages 基于消息数量触发
	TriggerTypeMessages TriggerType = "messages"
)

// ==================== Arguments 压缩配置 ====================

// ArgsCompressMode Arguments 压缩模式
type ArgsCompressMode string

const (
	// ArgsCompressModeKeepFields 保留指定字段，其余压缩
	ArgsCompressModeKeepFields ArgsCompressMode = "keep_fields"
	// ArgsCompressModeCompressFields 压缩指定字段，其余保留
	ArgsCompressModeCompressFields ArgsCompressMode = "compress_fields"
	// ArgsCompressModeFull 全量压缩（默认）
	ArgsCompressModeFull ArgsCompressMode = "full"
)

// ArgsCompressRule Arguments 压缩规则
type ArgsCompressRule struct {
	// Mode 压缩模式
	Mode ArgsCompressMode `json:"mode"`

	// Fields 字段列表（语义取决于 Mode）
	// 支持嵌套路径如 "options.timeout"
	Fields []string `json:"fields,omitempty"`

	// Hint 字段压缩提示，默认 "[compressed]"
	Hint string `json:"hint,omitempty"`
}

// ==================== ToolResult 压缩配置 ====================

// ResultCompressMode ToolResult 压缩模式
type ResultCompressMode string

const (
	// ResultCompressModeFull 全量压缩（默认）
	ResultCompressModeFull ResultCompressMode = "full"
	// ResultCompressModeJSONFields JSON 格式时按字段压缩
	ResultCompressModeJSONFields ResultCompressMode = "json_fields"
	// ResultCompressModeRegex 正则提取保留内容
	ResultCompressModeRegex ResultCompressMode = "regex"
	// ResultCompressModePrefix 保留前 N 个字符
	ResultCompressModePrefix ResultCompressMode = "prefix"
)

// ResultCompressRule ToolResult 压缩规则
type ResultCompressRule struct {
	// Mode 压缩模式
	Mode ResultCompressMode `json:"mode"`

	// Fields JSON 字段列表（Mode=json_fields 时使用）
	Fields []string `json:"fields,omitempty"`

	// Pattern 正则表达式（Mode=regex 时使用）
	// 匹配到的内容会被保留
	Pattern string `json:"pattern,omitempty"`

	// PrefixLen 保留的前缀长度（Mode=prefix 时使用）
	PrefixLen int `json:"prefix_len,omitempty"`

	// Hint 压缩提示，默认 "[result compressed]"
	Hint string `json:"hint,omitempty"`
}

// ==================== 工具压缩规则 ====================

// ToolCompressRule 单个工具的压缩规则
type ToolCompressRule struct {
	// ToolName 工具名称，"*" 表示默认规则
	ToolName string `json:"tool_name"`

	// Args Arguments 压缩规则（nil 表示使用默认全量压缩）
	Args *ArgsCompressRule `json:"args,omitempty"`

	// Result ToolResult 压缩规则（nil 表示使用默认全量压缩）
	Result *ResultCompressRule `json:"result,omitempty"`
}

// TriggerConfig 触发条件配置
type TriggerConfig struct {
	Type  TriggerType `json:"type"`
	Value float64     `json:"value"` // fraction: 0.90, tokens: 170000, messages: 20
}

// ToolCompressConfig 工具内容压缩配置
type ToolCompressConfig struct {
	// CompressedHint 压缩后替换的提示文本
	// 为空时使用 DefaultToolContentCompressedHint
	CompressedHint string `json:"compressed_hint,omitempty"`

	// Trigger 独立触发条件
	// nil 时使用默认值 DefaultToolCompressTriggerFraction
	Trigger *TriggerConfig `json:"trigger,omitempty"`

	// KeepBudgetFraction 工具参数保留预算占可用窗口的百分比
	// 0 时使用默认值 DefaultToolCompressKeepFraction
	KeepBudgetFraction float64 `json:"keep_budget_fraction,omitempty"`

	// CustomToolCompressRules 自定义工具压缩规则（仅用于非内置工具，如 MCP 工具）
	// 内置工具使用 BuiltinToolCompressRules，不可覆盖
	CustomToolCompressRules []*ToolCompressRule `json:"custom_tool_compress_rules,omitempty"`
}

// NewFractionTriggerConfig 创建按容量百分比触发的配置
func NewFractionTriggerConfig(fraction float64) *TriggerConfig {
	return &TriggerConfig{
		Type:  TriggerTypeFraction,
		Value: fraction,
	}
}

// NewTokensTriggerConfig 创建按固定 token 数触发的配置
func NewTokensTriggerConfig(tokens int) *TriggerConfig {
	return &TriggerConfig{
		Type:  TriggerTypeTokens,
		Value: float64(tokens),
	}
}

// NewMessagesTriggerConfig 创建按消息数量触发的配置
func NewMessagesTriggerConfig(messages int) *TriggerConfig {
	return &TriggerConfig{
		Type:  TriggerTypeMessages,
		Value: float64(messages),
	}
}

// NewToolCompressConfig 创建工具内容压缩配置
func NewToolCompressConfig() *ToolCompressConfig {
	return &ToolCompressConfig{}
}

// SummarizationRuntimeConfig 运行时计算的配置
// 由 graph.go 在工具收集完成后计算并填充
type SummarizationRuntimeConfig struct {
	// AvailableTokens 可用窗口 = modelContextWindow - toolDefinitionsTokens
	// SP token 已包含在消息列表的 currentTokens 中，不再单独扣除
	AvailableTokens int `json:"available_tokens"`
}

// SummarizationConfig 摘要中间件配置
type SummarizationConfig struct {
	// ══════ 静态配置（用户传入） ══════

	// Model 用于生成摘要的模型
	Model model.ToolCallingChatModel `json:"-"`

	// ModelMaxInputTokens 用户可直接指定模型上下文窗口大小
	ModelMaxInputTokens int `json:"model_max_input_tokens,omitempty"`

	// ModelName 模型名称，用于从注册表查找上下文窗口
	// 如果 ModelMaxInputTokens > 0，此字段被忽略
	ModelName string `json:"model_name,omitempty"`

	// Trigger 摘要触发配置
	Trigger *TriggerConfig `json:"trigger,omitempty"`

	// Keep 摘要保留配置
	Keep *TriggerConfig `json:"keep,omitempty"`

	// ToolCompressSettings 工具内容压缩配置
	ToolCompressSettings *ToolCompressConfig `json:"tool_compress_settings,omitempty"`

	// MaxTokens 触发摘要的令牌阈值
	// Deprecated: 使用 Trigger 代替
	MaxTokens int `json:"max_tokens,omitempty"`

	// KeepLastN 保留最近 N 条消息不被摘要
	// Deprecated: 使用 Keep 代替
	KeepLastN int `json:"keep_last_n,omitempty"`

	// SummaryPrompt 自定义摘要提示词
	SummaryPrompt string `json:"summary_prompt,omitempty"`

	// TokenCounter 自定义令牌计数器
	// 如果为 nil，使用默认的简单计数器
	TokenCounter func(messages []*schema.Message) int `json:"-"`

	// OnCompressCallback 统一的压缩事件回调
	// 在工具压缩和全量摘要的 start/complete 阶段调用
	// complete 阶段 Data 中携带持久化数据（Updates），业务方负责持久化
	OnCompressCallback OnCompressCallbackFunc `json:"-"`

	// ══════ 运行时配置（graph.go 计算填充） ══════

	// RuntimeConfig 运行时计算的配置，用户无需设置
	// graph.go 在创建 Agent 时自动填充
	RuntimeConfig *SummarizationRuntimeConfig `json:"runtime_config,omitempty"`
}

// SummarizationMiddleware 上下文摘要中间件
// 当消息历史过长时自动进行摘要压缩
type SummarizationMiddleware struct {
	middleware.BaseMiddleware
	config        *SummarizationConfig
	summaryCache  string
	originalCount int
	toolCallCache map[string]string // ToolCallID -> ToolName 缓存
	mu            sync.RWMutex
}

// New 创建摘要中间件
func New(config *SummarizationConfig) *SummarizationMiddleware {
	if config == nil {
		config = &SummarizationConfig{}
	}
	// 向后兼容：如果用户设置了旧字段，继续使用
	// 如果都没设置 → 使用新的百分比默认策略
	if config.MaxTokens == 0 && config.Trigger == nil {
		config.Trigger = NewFractionTriggerConfig(constant.DefaultSummarizationTriggerFraction)
	}
	if config.KeepLastN == 0 && config.Keep == nil {
		config.Keep = NewFractionTriggerConfig(constant.DefaultSummarizationKeepFraction)
	}

	// ToolCompress 默认策略
	if config.ToolCompressSettings == nil {
		config.ToolCompressSettings = &ToolCompressConfig{}
	}
	if config.ToolCompressSettings.Trigger == nil {
		config.ToolCompressSettings.Trigger = NewFractionTriggerConfig(constant.DefaultToolCompressTriggerFraction)
	}

	if config.SummaryPrompt == "" {
		config.SummaryPrompt = defaultSummaryPrompt()
	}
	if config.TokenCounter == nil {
		config.TokenCounter = utils.SimpleTokenCounter
	}

	return &SummarizationMiddleware{
		config: config,
	}
}

// emitCompressEvent 触发压缩事件回调
func (m *SummarizationMiddleware) emitCompressEvent(ctx context.Context, event CompressEvent) {
	if m.config.OnCompressCallback == nil {
		return
	}
	if err := m.config.OnCompressCallback(ctx, event); err != nil {
		logs.CtxError(ctx, "[emitCompressEvent] callback error, phase: %s, type: %s, err: %v",
			event.Phase, event.Type, err)
	}
}

// Name 返回中间件名称
func (m *SummarizationMiddleware) Name() string {
	return constant.MiddlewareSummarization
}

// Tools 返回中间件提供的工具
func (m *SummarizationMiddleware) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	return nil, nil
}

// ModifyModelRequest applies context compression before each LLM call.
// Compresses tool content and summarizes old messages to stay within token limits.
func (m *SummarizationMiddleware) ModifyModelRequest(ctx context.Context, messages []*schema.Message, _ *types.GraphState) ([]*schema.Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	compressed, _, err := m.ProcessMessages(ctx, messages)
	if err != nil {
		logs.CtxError(ctx, "[SummarizationMiddleware.ModifyModelRequest] context compression failed: %v", err)
		// Don't fail the request, continue with original messages
		return messages, nil
	}
	return compressed, nil
}

// isContextOverflowError checks if an error indicates a context/token limit overflow.
// Covers common error patterns from various model providers (OpenAI, Anthropic, Ark/Doubao).
func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	// Common patterns across providers (OpenAI, Anthropic, Ark/Doubao)
	// Note: "max_tokens" is intentionally excluded as it's ambiguous —
	// it could refer to output token limits, not input context overflow.
	patterns := []string{
		"context_length_exceeded",
		"context length",
		"maximum context length",
		"token limit",
		"too many tokens",
		"exceeds the model's maximum",
		"input too long",
		"request too large",
		"prompt is too long",
	}
	for _, p := range patterns {
		if strings.Contains(errMsg, p) {
			return true
		}
	}
	return false
}

// ProcessMessages 处理消息列表，执行两阶段压缩
// Phase 1: 工具内容压缩（轻量、高频）
// Phase 2: 消息摘要（重量、低频）
// 返回值: (压缩后的消息列表：可能会进行截断, 是否进行了压缩, 错误)
func (m *SummarizationMiddleware) ProcessMessages(ctx context.Context, messages []*schema.Message) ([]*schema.Message, bool, error) {
	if len(messages) == 0 {
		return messages, false, nil
	}

	availableTokens := m.getAvailableTokens(ctx)

	// 渐进式统计 token：如果消息中已有摘要，只统计从摘要消息开始的 token
	// 因为摘要之前的消息已被压缩，不会发送给模型
	currentTokens, summaryStartIndex := m.CountTokensProgressive(messages, WithTokenCountStopFunc(stopAtSummaryMessage()))
	if summaryStartIndex > 0 {
		logs.CtxInfo(ctx, "[ProcessMessages] found summary at index %d, only counting tokens from there", summaryStartIndex)
		// 截取从摘要开始的消息，之前的消息不再需要
		messages = messages[summaryStartIndex:]
	}

	logs.CtxInfo(ctx, "[ProcessMessages] start, messages: %d, tokens: %d, available: %d, usage: %.1f%%, summarize_trigger: %s, summarize_keep: %s, tool_compress: %s",
		len(messages), currentTokens, availableTokens, (float64(currentTokens)/float64(availableTokens))*100,
		formatTriggerConfig(m.getTriggerConfig()), formatTriggerConfig(m.getKeepConfig()),
		formatToolCompressConfig(m.config.ToolCompressSettings))

	// Phase 1: 工具内容压缩
	messages, didCompress := m.compressToolContentPhase(ctx, messages, availableTokens, currentTokens)
	if didCompress {
		newTokens := m.config.TokenCounter(messages)
		logs.CtxInfo(ctx, "[ProcessMessages] Phase1 tool_compress completed, tokens_before: %d, tokens_after: %d, saved: %d",
			currentTokens, newTokens, currentTokens-newTokens)
		currentTokens = newTokens

		m.logPhaseStatus(ctx, "Phase1 tool_compress", messages, currentTokens, availableTokens)
	}

	// Phase 2: 消息摘要
	result, didSummarize, err := m.summarizeMessages(ctx, messages, availableTokens, currentTokens)
	if didSummarize {
		newTokens := m.config.TokenCounter(result)
		logs.CtxInfo(ctx, "[ProcessMessages] Phase2 summarize completed, tokens_before: %d, tokens_after: %d, saved: %d",
			currentTokens, newTokens, currentTokens-newTokens)
		m.logPhaseStatus(ctx, "Phase2 summarize", result, newTokens, availableTokens)
	}
	return result, didCompress || didSummarize, err
}

// logPhaseStatus 打印阶段完成后的状态日志
func (m *SummarizationMiddleware) logPhaseStatus(ctx context.Context, phase string, messages []*schema.Message, currentTokens, availableTokens int) {
	logs.CtxInfo(ctx, "[ProcessMessages] logPhaseStatus, %s completed, messages: %d, tokens: %d, available: %d, usage: %.1f%%, summarize_trigger: %s, summarize_keep: %s, tool_compress: %s",
		phase, len(messages), currentTokens, availableTokens, (float64(currentTokens)/float64(availableTokens))*100,
		formatTriggerConfig(m.getTriggerConfig()), formatTriggerConfig(m.getKeepConfig()),
		formatToolCompressConfig(m.config.ToolCompressSettings))
}

// getAvailableTokens 获取可用窗口 token 数
func (m *SummarizationMiddleware) getAvailableTokens(ctx context.Context) int {
	// 优先级 1: 运行时计算的 AvailableTokens（已扣除 SP + Tools，最精确）
	if m.config.RuntimeConfig != nil && m.config.RuntimeConfig.AvailableTokens > 0 {
		return m.config.RuntimeConfig.AvailableTokens
	}
	// 优先级 2: 用户显式配置的 ModelMaxInputTokens
	if m.config.ModelMaxInputTokens > 0 {
		return m.config.ModelMaxInputTokens
	}
	// 优先级 3: 根据 ModelName 查注册表
	if m.config.ModelName != "" {
		return constant.LookupModelContextWindow(ctx, m.config.ModelName)
	}
	// 优先级 4: 保守默认值
	return constant.DefaultModelContextWindow
}

// compressToolContentPhase Phase 1: 工具内容压缩
// 压缩工具调用参数（Assistant 消息的 ToolCalls）和工具返回结果（Tool 消息的 Content）
// 从尾部往前累计 token，在预算内的保留原样，超出预算的进行压缩
// 已压缩的消息会被跳过，不会重复压缩
// 压缩完成后会调用 OnSummarize 回调持久化压缩状态
func (m *SummarizationMiddleware) compressToolContentPhase(ctx context.Context, messages []*schema.Message, availableTokens, currentTokens int) ([]*schema.Message, bool) {
	settings := m.config.ToolCompressSettings
	if settings == nil {
		return messages, false
	}

	// 构建 ToolCall 缓存，用于通过 ToolCallID 查找工具名
	m.buildToolCallCache(messages)

	// 计算保留预算（token 数）
	keepBudget := settings.KeepBudgetFraction
	if keepBudget == 0 {
		keepBudget = constant.DefaultToolCompressKeepFraction
	}
	keepBudgetTokens := int(float64(availableTokens) * keepBudget)

	// 累计所有消息的工具内容 token，用于触发判断
	// 注意：已压缩的消息也需要统计，因为 prefix 和占位符仍占用 token
	totalToolContentTokens := 0
	for _, msg := range messages {
		totalToolContentTokens += utils.CountToolContentTokens(msg)
	}

	// 检查触发条件：使用所有工具内容的累计 token 数进行判断
	if !m.checkToolCompressTrigger(settings.Trigger, availableTokens, totalToolContentTokens) {
		return messages, false
	}

	// 从尾部往前累计所有消息的工具内容 token，确定保留分界线
	// 注意：已压缩的消息也参与累计，因为 prefix 和占位符仍占用 token
	cumulativeToolContentTokens := 0
	cutoffIndex := -1

	for i := len(messages) - 1; i >= 0; i-- {
		toolContentTokens := utils.CountToolContentTokens(messages[i])
		if toolContentTokens == 0 {
			continue
		}
		cumulativeToolContentTokens += toolContentTokens
		if cumulativeToolContentTokens > keepBudgetTokens {
			cutoffIndex = i
			break
		}
	}

	if cutoffIndex < 0 {
		return messages, false
	}

	// 对 cutoffIndex 及之前的工具消息执行压缩或恢复
	result := make([]*schema.Message, len(messages))
	var details []ToolCompressDetail
	hasChanges := false

	// 通知：工具内容压缩开始
	m.emitCompressEvent(ctx, CompressEvent{
		Phase: CompressPhaseStart,
		Type:  CompressTypeToolCompress,
		Data: &CompressEventData{
			ToolCompress: &ToolCompressEventData{
				TotalMessageCount: len(messages),
				ToCompressCount:   cutoffIndex + 1,
			},
		},
	})
	// 确保 start 之后一定有 complete（即使没有实际压缩发生）
	defer func() {
		m.emitCompressEvent(ctx, CompressEvent{
			Phase: CompressPhaseComplete,
			Type:  CompressTypeToolCompress,
			Data: &CompressEventData{
				ToolCompress: &ToolCompressEventData{
					TotalMessageCount: len(messages),
					ToCompressCount:   len(details),
					Details:           details,
				},
			},
		})
	}()

	for i, msg := range messages {
		if i <= cutoffIndex {
			newMsg, needPersist := m.compressToolContent(msg)
			if newMsg != msg {
				hasChanges = true
			}
			if needPersist {
				// 新压缩的消息需要持久化（仅当消息有 ID 时）
				if msgID := GetMessageID(newMsg); msgID != "" {
					details = append(details, ToolCompressDetail{
						MessageID:         msgID,
						ToolCompressState: GetToolCompressExtra(newMsg),
					})
				}
			}
			result[i] = newMsg
		} else {
			result[i] = msg
		}
	}

	if !hasChanges {
		return messages, false
	}

	return result, true
}

// summarizeMessages 对消息进行摘要（链式摘要）
// 以 UserMessage 为边界进行压缩，确保保留的消息以 UserMessage 开头
// 返回值: (压缩后的消息列表, 是否进行了摘要, 错误)
func (m *SummarizationMiddleware) summarizeMessages(ctx context.Context, messages []*schema.Message, availableTokens, currentTokens int) ([]*schema.Message, bool, error) {
	if m.config.Model == nil || !m.checkTrigger(messages, m.config.Trigger, availableTokens, currentTokens) {
		return messages, false, nil
	}

	// Step 1: 根据 token 数计算初步的保留消息
	initialToKeep := m.getMessagesToKeep(messages, availableTokens)
	initialKeepStartIndex := len(messages) - len(initialToKeep)

	// Step 2: 找到保留区域的第一个 UserMessage 作为边界
	// 从 initialKeepStartIndex 开始往前找（包括 initialKeepStartIndex 位置）
	userMessageBoundaryIndex := -1
	for i := initialKeepStartIndex; i >= 0; i-- {
		if messages[i].Role == schema.User {
			userMessageBoundaryIndex = i
			break
		}
	}

	// 如果没找到 UserMessage，说明整个历史都没有 UserMessage，不进行压缩
	if userMessageBoundaryIndex < 0 {
		logs.CtxError(ctx, "[summarizeMessages] no UserMessage found in history, skip summarization")
		return messages, false, nil
	}

	// 如果 UserMessage 边界就是第一条消息，没有可压缩的内容
	if userMessageBoundaryIndex == 0 {
		logs.CtxInfo(ctx, "[summarizeMessages] UserMessage boundary is at index 0, nothing to summarize")
		return messages, false, nil
	}

	// Step 3: 以 UserMessage 为边界重新划分
	toSummarize := messages[:userMessageBoundaryIndex]
	toKeep := messages[userMessageBoundaryIndex:]
	keepCount := len(toKeep)

	logs.CtxInfo(ctx, "[summarizeMessages] initial_keep_start: %d, user_boundary: %d, to_summarize: %d, to_keep: %d",
		initialKeepStartIndex, userMessageBoundaryIndex, len(toSummarize), keepCount)

	if len(toSummarize) == 0 {
		return messages, false, nil
	}

	// 分离历史摘要和待摘要的新消息
	prevSummary, newMessages := splitByLastSummary(toSummarize)
	logs.CtxInfo(ctx, "[summarizeMessages] total_message_count: %d, to_keep_message_count: %d, to_summarize_message_count: %d, ready_to_summary_message_count: %d",
		len(messages), keepCount, len(toSummarize), len(newMessages))

	// 如果没有新消息需要摘要（都已经被之前的摘要覆盖了）
	if len(newMessages) == 0 {
		logs.CtxInfo(ctx, "[summarizeMessages] no new messages to summarize after splitting by last summary")
		return messages, false, nil
	}

	// 对新消息截断工具参数
	truncatedMessages := make([]*schema.Message, len(newMessages))
	for i, msg := range newMessages {
		truncatedMessages[i], _ = m.compressToolContent(msg)
	}

	// 生成摘要
	chainMode := "first"
	if prevSummary != "" {
		chainMode = "chain"
	}
	logs.CtxInfo(ctx, "[summarizeMessages] Phase2 generating summary, mode: %s, summarize_messages: %d, keep_messages: %d",
		chainMode, len(newMessages), keepCount)

	// 通知：全量摘要开始
	var summaryDetails []SummaryCompressDetail
	m.emitCompressEvent(ctx, CompressEvent{
		Phase: CompressPhaseStart,
		Type:  CompressTypeSummarize,
		Data: &CompressEventData{
			Summarize: &SummarizeEventData{
				ToSummarizeCount: len(toSummarize),
				ToKeepCount:      keepCount,
			},
		},
	})
	// 确保 start 之后一定有 complete
	defer func() {
		m.emitCompressEvent(ctx, CompressEvent{
			Phase: CompressPhaseComplete,
			Type:  CompressTypeSummarize,
			Data: &CompressEventData{
				Summarize: &SummarizeEventData{
					ToSummarizeCount: len(toSummarize),
					ToKeepCount:      keepCount,
					Details:          summaryDetails,
				},
			},
		})
	}()

	summary, err := m.generateSummary(ctx, prevSummary, truncatedMessages)
	if err != nil {
		logs.CtxError(ctx, "[summarizeMessages] Phase2 summary generation failed, err: %v", err)
		return messages, false, nil
	}

	logs.CtxInfo(ctx, "[summarizeMessages] generateSummary success")

	// 缓存摘要
	m.mu.Lock()
	m.summaryCache = summary
	m.originalCount = len(toSummarize)
	m.mu.Unlock()

	// 把摘要绑定到第一条保留消息上（现在一定是 UserMessage）
	result := make([]*schema.Message, keepCount)
	firstKeptMsg := attachSummaryToMessage(toKeep[0], len(toSummarize), summary)
	result[0] = firstKeptMsg
	copy(result[1:], toKeep[1:])

	// 构建持久化更新（defer 中的 complete 事件会携带此数据）
	summaryDetails = m.buildSummaryDetails(ctx, firstKeptMsg)

	logs.CtxInfo(ctx, "[summarizeMessages] Phase2 completed, original_messages: %d, result_messages: %d",
		len(messages), len(result))

	return result, true, nil
}

// buildSummaryDetails 构建摘要持久化详情
// 返回需要持久化的摘要详情，由调用方通过 CompressEvent 传递给业务层
func (m *SummarizationMiddleware) buildSummaryDetails(ctx context.Context, summaryMsg *schema.Message) []SummaryCompressDetail {
	summaryMsgID := GetMessageID(summaryMsg)
	if summaryMsgID == "" {
		logs.CtxWarn(ctx, "[buildSummaryDetails] summaryMsg has no MessageID")
		return nil
	}
	summaryExtra := GetSummaryExtra(summaryMsg)
	if summaryExtra == nil {
		logs.CtxWarn(ctx, "[buildSummaryDetails] summaryMsg has no SummaryExtra")
		return nil
	}
	return []SummaryCompressDetail{
		{
			MessageID:    summaryMsgID,
			SummaryState: summaryExtra,
		},
	}
}

// generateSummary 生成摘要，支持链式摘要
func (m *SummarizationMiddleware) generateSummary(ctx context.Context, prevSummary string, messages []*schema.Message) (string, error) {
	// 构建消息文本
	var builder strings.Builder
	for i, msg := range messages {
		if i > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(formatMessageForSummary(msg))
	}

	// 构建摘要请求
	var prompt string
	if prevSummary != "" {
		prompt = fmt.Sprintf(chainSummaryPrompt(), prevSummary, builder.String())
	} else {
		prompt = fmt.Sprintf(m.config.SummaryPrompt, builder.String())
	}

	summaryMessages := []*schema.Message{
		schema.UserMessage(prompt),
	}

	resp, err := m.config.Model.Generate(ctx, summaryMessages)
	if err != nil {
		logs.CtxError(ctx, "[summarizeSummary] model generation failed, err: %v", err)
		return "", fmt.Errorf("generate summary failed: %w", err)
	}

	return resp.Content, nil
}

// shouldTrigger 判断是否需要触发摘要
func (m *SummarizationMiddleware) shouldTrigger(messages []*schema.Message, availableTokens, currentTokens int) bool {
	return m.checkTrigger(messages, m.getTriggerConfig(), availableTokens, currentTokens)
}

// checkTrigger 通用触发检查
func (m *SummarizationMiddleware) checkTrigger(messages []*schema.Message, trigger *TriggerConfig, availableTokens, currentTokens int) bool {
	if trigger == nil {
		return false
	}

	switch trigger.Type {
	case TriggerTypeFraction:
		threshold := int(float64(availableTokens) * trigger.Value)
		return currentTokens >= threshold

	case TriggerTypeTokens:
		return currentTokens >= int(trigger.Value)

	case TriggerTypeMessages:
		return len(messages) >= int(trigger.Value)
	}
	return false
}

// checkToolCompressTrigger 检查工具内容压缩的触发条件
// 使用工具参数累计 token 数进行判断，而非总消息 token 数
func (m *SummarizationMiddleware) checkToolCompressTrigger(trigger *TriggerConfig, availableTokens, toolArgTokens int) bool {
	if trigger == nil {
		return false
	}

	switch trigger.Type {
	case TriggerTypeFraction:
		// 工具参数占可用窗口的比例超过阈值时触发
		threshold := int(float64(availableTokens) * trigger.Value)
		return toolArgTokens >= threshold

	case TriggerTypeTokens:
		// 工具参数累计超过固定 token 数时触发
		return toolArgTokens >= int(trigger.Value)

	case TriggerTypeMessages:
		// 消息数触发对工具内容压缩无意义，不支持
		return false
	}
	return false
}

// getMessagesToKeep 根据保留策略获取需要保留的消息
func (m *SummarizationMiddleware) getMessagesToKeep(messages []*schema.Message, availableTokens int) []*schema.Message {
	keep := m.getKeepConfig()

	switch keep.Type {
	case TriggerTypeFraction:
		targetTokens := int(float64(availableTokens) * keep.Value)
		return m.trimToTokens(messages, targetTokens)

	case TriggerTypeTokens:
		return m.trimToTokens(messages, int(keep.Value))

	case TriggerTypeMessages:
		n := int(keep.Value)
		if len(messages) <= n {
			return messages
		}
		return messages[len(messages)-n:]
	}
	return messages
}

// trimToTokens 从后往前保留消息，直到 token 数达到目标值
func (m *SummarizationMiddleware) trimToTokens(messages []*schema.Message, targetTokens int) []*schema.Message {
	if len(messages) == 0 {
		return messages
	}

	currentTokens := 0
	startIndex := len(messages)

	for i := len(messages) - 1; i >= 0; i-- {
		msgTokens := m.config.TokenCounter([]*schema.Message{messages[i]})
		if currentTokens+msgTokens > targetTokens && startIndex < len(messages) {
			break
		}
		currentTokens += msgTokens
		startIndex = i
	}

	return messages[startIndex:]
}

// compressToolContent 将工具相关内容替换为压缩提示
// 处理两种情况：
// 1. Assistant 消息的 ToolCall.Arguments → 按规则压缩
// 2. Tool 角色消息的 Content（ToolResult）→ 按规则压缩
// compressToolContent 压缩或恢复工具消息内容
// 返回值：(处理后的消息, 是否需要持久化)
// - 已压缩的消息：恢复内容，返回 (newMsg, false) 不需要持久化
// - 未压缩的消息：执行压缩，返回 (newMsg, true) 需要持久化
func (m *SummarizationMiddleware) compressToolContent(msg *schema.Message) (*schema.Message, bool) {
	hasToolCalls := len(msg.ToolCalls) > 0
	isToolResult := msg.Role == schema.Tool

	if !hasToolCalls && !isToolResult {
		return msg, false
	}

	// 检查是否已经被压缩（从存储恢复的消息可能已有压缩状态但内容未替换）
	existingExtra := GetToolCompressExtra(msg)
	if existingExtra != nil {
		// 已有压缩状态，用存储的 hint 恢复压缩内容，不需要再次持久化
		return m.restoreCompressedContent(msg, existingExtra), false
	}

	newMsg := *msg

	// 准备压缩信息
	compressExtra := &ToolCompressExtra{}

	// 替换 ToolCall.Arguments（原始内容保留在原始消息中）
	if hasToolCalls {
		compressExtra.ToolCallArgsHints = make(map[string]string)
		newToolCalls := make([]schema.ToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			rule := m.findToolCompressRule(tc.Function.Name)
			compressedArgs, _ := m.compressArgs(tc.Function.Arguments, rule.Args)

			// 存储压缩后的完整内容，用于从数据库恢复时替换原始内容
			compressExtra.ToolCallArgsHints[tc.ID] = compressedArgs
			compressExtra.ToolCallArgsCompress = true
			newToolCalls[i] = tc
			newToolCalls[i].Function.Arguments = compressedArgs
		}
		newMsg.ToolCalls = newToolCalls
	}

	// 替换 Tool 角色消息的 Content（原始内容保留在原始消息中）
	if isToolResult && len(msg.Content) > 0 {
		// Tool 消息需要通过 ToolCallID 找到对应的工具名
		toolName := m.findToolNameByCallID(msg.ToolCallID)
		rule := m.findToolCompressRule(toolName)
		compressedResult, _ := m.compressResult(msg.Content, rule.Result)

		// 存储压缩后的完整内容，用于从数据库恢复时替换原始内容
		compressExtra.ToolResultHint = compressedResult
		compressExtra.ToolResultCompress = true
		newMsg.Content = compressedResult
	}

	// 记录压缩状态到 Extra
	if newMsg.Extra == nil {
		newMsg.Extra = make(map[string]any)
	} else {
		// 复制 Extra map 避免修改原消息
		newExtra := make(map[string]any, len(msg.Extra)+1)
		for k, v := range msg.Extra {
			newExtra[k] = v
		}
		newMsg.Extra = newExtra
	}
	newMsg.Extra[ToolCompressExtraKey] = compressExtra

	return &newMsg, true
}

// restoreCompressedContent 从存储的压缩状态恢复压缩内容
// 用于从数据库加载的消息：原始内容 + 压缩状态 -> 压缩后的内容
func (m *SummarizationMiddleware) restoreCompressedContent(msg *schema.Message, extra *ToolCompressExtra) *schema.Message {
	newMsg := *msg

	// 恢复 ToolCall Arguments
	if extra.ToolCallArgsCompress && len(msg.ToolCalls) > 0 {
		newToolCalls := make([]schema.ToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			newToolCalls[i] = tc
			if hint, ok := extra.ToolCallArgsHints[tc.ID]; ok && hint != "" {
				// 兼容历史数据：旧版本可能存储了非法 JSON 的 hint
				newToolCalls[i].Function.Arguments = ensureValidJSONArgs(hint)
			}
		}
		newMsg.ToolCalls = newToolCalls
	}

	// 恢复 Tool Result Content
	if extra.ToolResultCompress && extra.ToolResultHint != "" {
		newMsg.Content = extra.ToolResultHint
	}

	return &newMsg
}

// wrapCompressedHint 将 hint 包装为合法 JSON 对象 {"_compressed":"<escaped>"}
func wrapCompressedHint(hint string) string {
	escaped, err := sonic.MarshalString(hint)
	if err != nil {
		return `{"_compressed":""}`
	}
	return `{"_compressed":` + escaped + `}`
}

// ensureValidJSONArgs 确保 tool_call arguments 是合法 JSON
// 兼容旧版本存储的纯文本 hint（如 "[content removed to save context]"）
func ensureValidJSONArgs(args string) string {
	if args == "" {
		return args
	}
	if sonic.Valid([]byte(args)) {
		return args
	}
	return wrapCompressedHint(args)
}

// findToolCompressRule 查找匹配的工具压缩规则
// 优先级：内置规则 > 用户自定义规则 > 默认全量压缩
func (m *SummarizationMiddleware) findToolCompressRule(toolName string) *ToolCompressRule {
	// 1. 优先查找内置工具规则（不可覆盖）
	if rule, ok := BuiltinToolCompressRules[toolName]; ok {
		return rule
	}

	// 2. 查找用户自定义规则（仅用于非内置工具）
	if m.config.ToolCompressSettings != nil {
		// 先精确匹配
		for _, rule := range m.config.ToolCompressSettings.CustomToolCompressRules {
			if rule.ToolName == toolName {
				return rule
			}
		}
		// 再查找通配符规则
		for _, rule := range m.config.ToolCompressSettings.CustomToolCompressRules {
			if rule.ToolName == "*" {
				return rule
			}
		}
	}

	// 3. 默认：全量压缩
	return &ToolCompressRule{
		Args:   &ArgsCompressRule{Mode: ArgsCompressModeFull},
		Result: &ResultCompressRule{Mode: ResultCompressModeFull},
	}
}

// compressArgs 按规则压缩 Arguments
// 返回压缩后的内容和提示信息
// 兜底策略：任何解析/序列化错误都会回退到全量压缩，确保不会因压缩失败导致线上问题
// 注意：返回的 compressed 必须是合法 JSON，因为 tool_call.arguments 字段会被下游 API 解析
func (m *SummarizationMiddleware) compressArgs(args string, rule *ArgsCompressRule) (compressed, hint string) {
	defaultHint := m.config.ToolCompressSettings.CompressedHint
	if defaultHint == "" {
		defaultHint = constant.DefaultToolContentCompressedHint
	}
	// 全量压缩时返回合法 JSON（下游 API 要求 arguments 必须是合法 JSON）
	jsonHint := wrapCompressedHint(defaultHint)

	// 兜底：无规则或全量压缩模式，直接返回
	if rule == nil || rule.Mode == ArgsCompressModeFull {
		return jsonHint, defaultHint
	}

	// 兜底：空参数直接返回
	if args == "" {
		return args, ""
	}

	// 尝试解析 JSON，非 JSON 格式回退到全量压缩
	var data map[string]any
	if err := sonic.Unmarshal([]byte(args), &data); err != nil {
		// 兜底：非 JSON 格式（可能是模型输出的非标准格式），回退到全量压缩
		return jsonHint, defaultHint
	}

	fieldHint := rule.Hint
	if fieldHint == "" {
		fieldHint = "[content omitted to save tokens]"
	}

	switch rule.Mode {
	case ArgsCompressModeKeepFields:
		// 保留指定字段，其余压缩
		keepFields := make(map[string]bool)
		for _, f := range rule.Fields {
			keepFields[f] = true
		}
		m.compressMapFields(data, keepFields, fieldHint, true)

	case ArgsCompressModeCompressFields:
		// 压缩指定字段，其余保留
		compressFields := make(map[string]bool)
		for _, f := range rule.Fields {
			compressFields[f] = true
		}
		m.compressMapFields(data, compressFields, fieldHint, false)

	default:
		// 兜底：未知模式，回退到全量压缩
		return jsonHint, defaultHint
	}

	// 序列化回 JSON
	result, err := sonic.Marshal(data)
	if err != nil {
		// 兜底：序列化失败，回退到全量压缩
		return jsonHint, defaultHint
	}

	return string(result), fieldHint
}

// compressMapFields 压缩 map 中的字段
// keepMode=true: 保留 fields 中的字段，压缩其余
// keepMode=false: 压缩 fields 中的字段，保留其余
func (m *SummarizationMiddleware) compressMapFields(data map[string]any, fields map[string]bool, hint string, keepMode bool) {
	for key := range data {
		_, inFields := fields[key]
		shouldCompress := (keepMode && !inFields) || (!keepMode && inFields)

		if shouldCompress {
			// 检查是否是嵌套路径的前缀
			hasNestedField := false
			for f := range fields {
				if strings.HasPrefix(f, key+".") {
					hasNestedField = true
					break
				}
			}

			if hasNestedField {
				// 如果有嵌套字段需要处理，递归处理
				if nested, ok := data[key].(map[string]any); ok {
					// 提取针对这个 key 的嵌套字段
					nestedFields := make(map[string]bool)
					prefix := key + "."
					for f := range fields {
						if strings.HasPrefix(f, prefix) {
							nestedFields[strings.TrimPrefix(f, prefix)] = true
						}
					}
					m.compressMapFields(nested, nestedFields, hint, keepMode)
				}
			} else {
				// 直接压缩该字段
				data[key] = hint
			}
		}
	}

	// 处理嵌套路径（如 "options.timeout"）
	for f := range fields {
		if strings.Contains(f, ".") {
			parts := strings.SplitN(f, ".", 2)
			rootKey := parts[0]
			if nested, ok := data[rootKey].(map[string]any); ok {
				nestedFields := map[string]bool{parts[1]: true}
				m.compressMapFields(nested, nestedFields, hint, keepMode)
			}
		}
	}
}

// compressResult 按规则压缩 ToolResult
// 返回压缩后的内容和提示信息
// 兜底策略：任何解析/序列化/正则错误都会回退到全量压缩，确保不会因压缩失败导致线上问题
func (m *SummarizationMiddleware) compressResult(result string, rule *ResultCompressRule) (compressed, hint string) {
	defaultHint := m.config.ToolCompressSettings.CompressedHint
	if defaultHint == "" {
		defaultHint = constant.DefaultToolContentCompressedHint
	}

	// 兜底：无规则或全量压缩模式，直接返回
	if rule == nil || rule.Mode == ResultCompressModeFull {
		return defaultHint, defaultHint
	}

	// 兜底：空结果直接返回
	if result == "" {
		return result, ""
	}

	resultHint := rule.Hint
	if resultHint == "" {
		resultHint = "[... truncated to save context]"
	}

	switch rule.Mode {
	case ResultCompressModePrefix:
		// 保留前 N 个字符（安全模式，不依赖 JSON 解析）
		if rule.PrefixLen > 0 && len(result) > rule.PrefixLen {
			return result[:rule.PrefixLen] + "\n" + resultHint, resultHint
		}
		return result, "" // 内容较短，不需要压缩

	case ResultCompressModeJSONFields:
		// JSON 格式时按字段压缩
		// 注意：此模式要求返回结果为 JSON 格式，非 JSON 将回退到全量压缩
		var data map[string]any
		if err := sonic.Unmarshal([]byte(result), &data); err != nil {
			// 兜底：非 JSON 格式，回退到全量压缩
			return defaultHint, defaultHint
		}

		// 只保留指定字段
		newData := make(map[string]any)
		for _, f := range rule.Fields {
			if val, ok := getNestedField(data, f); ok {
				setNestedField(newData, f, val)
			}
		}
		newData["_compressed"] = true

		jsonResult, err := sonic.Marshal(newData)
		if err != nil {
			// 兜底：序列化失败，回退到全量压缩
			return defaultHint, defaultHint
		}
		return string(jsonResult), resultHint

	case ResultCompressModeRegex:
		// 正则提取保留内容
		if rule.Pattern == "" {
			// 兜底：空正则，回退到全量压缩
			return defaultHint, defaultHint
		}

		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			// 兜底：正则编译失败，回退到全量压缩
			return defaultHint, defaultHint
		}

		matches := re.FindAllString(result, -1)
		if len(matches) == 0 {
			// 无匹配时返回提示
			return resultHint, resultHint
		}

		return strings.Join(matches, " ") + " " + resultHint, resultHint

	default:
		// 兜底：未知模式，回退到全量压缩
		return defaultHint, defaultHint
	}
}

// findToolNameByCallID 通过 ToolCallID 查找工具名
// 需要从 middleware 的缓存或历史消息中查找
// 由于我们在处理消息时是按顺序处理的，这里使用一个简单的缓存机制
func (m *SummarizationMiddleware) findToolNameByCallID(callID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.toolCallCache == nil {
		return ""
	}

	if name, ok := m.toolCallCache[callID]; ok {
		return name
	}
	return ""
}

// buildToolCallCache 从消息列表构建 ToolCall 缓存
// 用于 findToolNameByCallID 查找
func (m *SummarizationMiddleware) buildToolCallCache(messages []*schema.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.toolCallCache = make(map[string]string)
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			m.toolCallCache[tc.ID] = tc.Function.Name
		}
	}
}

// ==================== 嵌套字段操作 ====================

// getNestedField 获取嵌套字段值，支持点号分隔路径
// 例如：getNestedField(data, "options.timeout") 获取 data["options"]["timeout"]
func getNestedField(data map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	current := data

	for i, part := range parts {
		val, ok := current[part]
		if !ok {
			return nil, false
		}
		if i == len(parts)-1 {
			return val, true
		}
		// 继续深入
		if nested, ok := val.(map[string]any); ok {
			current = nested
		} else {
			return nil, false
		}
	}
	return nil, false
}

// setNestedField 设置嵌套字段值（自动创建中间层）
func setNestedField(data map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	current := data

	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
			return
		}
		// 创建或获取中间层
		if next, ok := current[part].(map[string]any); ok {
			current = next
		} else {
			next := make(map[string]any)
			current[part] = next
			current = next
		}
	}
}

// deleteNestedField 删除嵌套字段
func deleteNestedField(data map[string]any, path string) {
	parts := strings.Split(path, ".")
	current := data

	for i, part := range parts {
		if i == len(parts)-1 {
			delete(current, part)
			return
		}
		if next, ok := current[part].(map[string]any); ok {
			current = next
		} else {
			return
		}
	}
}

// getTriggerConfig 获取触发配置（向后兼容）
func (m *SummarizationMiddleware) getTriggerConfig() *TriggerConfig {
	if m.config.Trigger != nil {
		return m.config.Trigger
	}
	return &TriggerConfig{
		Type:  TriggerTypeTokens,
		Value: float64(m.config.MaxTokens),
	}
}

// getKeepConfig 获取保留配置（向后兼容）
func (m *SummarizationMiddleware) getKeepConfig() *TriggerConfig {
	if m.config.Keep != nil {
		return m.config.Keep
	}
	return &TriggerConfig{
		Type:  TriggerTypeMessages,
		Value: float64(m.config.KeepLastN),
	}
}

// ==================== 辅助函数 ====================

// SummaryExtraKey 存储摘要信息的 Extra key
const SummaryExtraKey = "__summary__"

// ToolCompressExtraKey 存储工具压缩信息的 Extra key
const ToolCompressExtraKey = "__tool_compress__"

// MessageIDExtraKey 存储消息 ID 的 Extra key（用于关联持久层）
const MessageIDExtraKey = "__message_id__"

// ToolCompressDetail 工具压缩持久化详情
type ToolCompressDetail struct {
	MessageID         string             `json:"message_id"`
	ToolCompressState *ToolCompressExtra `json:"tool_compress_state,omitempty"`
}

// SummaryCompressDetail 全量摘要持久化详情
type SummaryCompressDetail struct {
	MessageID    string        `json:"message_id"`
	SummaryState *SummaryExtra `json:"summary_state,omitempty"`
}

// ==================== 压缩事件回调 ====================

// CompressPhase 压缩事件阶段
type CompressPhase string

const (
	// CompressPhaseStart 压缩开始
	CompressPhaseStart CompressPhase = "start"
	// CompressPhaseComplete 压缩完成
	CompressPhaseComplete CompressPhase = "complete"
)

// CompressType 压缩类型
type CompressType string

const (
	// CompressTypeToolCompress Phase 1: 工具内容压缩
	CompressTypeToolCompress CompressType = "tool_compress"
	// CompressTypeSummarize Phase 2: 全量消息摘要
	CompressTypeSummarize CompressType = "summarize"
)

// CompressEvent 统一的压缩事件
type CompressEvent struct {
	Phase CompressPhase      // 事件阶段
	Type  CompressType       // 压缩类型
	Data  *CompressEventData // 事件数据（根据 Type 取对应字段）
}

// CompressEventData 压缩事件数据
// 业务方根据 CompressEvent.Type 选择对应的数据指针
type CompressEventData struct {
	ToolCompress *ToolCompressEventData // Type == CompressTypeToolCompress 时有值
	Summarize    *SummarizeEventData    // Type == CompressTypeSummarize 时有值
}

// ToolCompressEventData 工具压缩事件数据
type ToolCompressEventData struct {
	TotalMessageCount int                  // 总消息数
	ToCompressCount   int                  // 将被/已被压缩的消息数
	Details           []ToolCompressDetail // complete 阶段：需要持久化的更新（start 阶段为 nil）
}

// SummarizeEventData 全量摘要事件数据
type SummarizeEventData struct {
	ToSummarizeCount int                     // 将被/已被摘要的消息数
	ToKeepCount      int                     // 保留的消息数
	Details          []SummaryCompressDetail // complete 阶段：需要持久化的更新（start 阶段为 nil）
}

// OnCompressCallbackFunc 压缩事件回调函数
// start 阶段返回值被忽略；complete 阶段返回 error 用于持久化错误日志
type OnCompressCallbackFunc func(ctx context.Context, event CompressEvent) error

// SummaryExtra 摘要信息结构体，存储在消息的 Extra 中
type SummaryExtra struct {
	// Summarized 是否已被摘要（用于标记被摘要掉的消息）
	Summarized bool `json:"summarized,omitempty"`
	// Content 摘要内容（仅摘要消息有）
	Content string `json:"content,omitempty"`
	// Count 被摘要的消息数量（仅摘要消息有）
	Count int `json:"count,omitempty"`
}

// ToolCompressExtra 工具压缩信息结构体，存储在消息的 Extra 中
// 原始内容保留在数据库中，这里存储压缩后的完整内容，用于恢复时替换原始内容
type ToolCompressExtra struct {

	// ToolCallArgsCompress 工具参数是否已压缩
	ToolCallArgsCompress bool `json:"tool_call_args_compress,omitempty"`

	// ToolCallArgsHints 每个 ToolCall 压缩后的完整 Arguments
	// key: ToolCall ID, value: 压缩后的完整 Arguments（如 {"path":"...","content":"[content removed to save context]"}）
	// 用于从数据库恢复消息时替换原始 Arguments
	// 适用于 Assistant 消息
	ToolCallArgsHints map[string]string `json:"tool_call_args_hints,omitempty"`

	// ToolResultCompress 工具结果是否已压缩
	ToolResultCompress bool `json:"tool_result_compress,omitempty"`

	// ToolResultHint 压缩后的完整工具结果内容
	// 用于从数据库恢复消息时替换原始 Content
	// 适用于 Tool 消息
	ToolResultHint string `json:"tool_result_hint,omitempty"`
}

// isSummaryMessage 判断消息是否携带摘要内容
// 注意：仅检查是否有摘要内容，Summarized=true 但无 Content 的消息不算
func isSummaryMessage(msg *schema.Message) bool {
	extra := GetSummaryExtra(msg)
	return extra != nil && extra.Summarized && extra.Content != ""
}

// GetSummaryExtra 从消息中获取摘要信息
func GetSummaryExtra(msg *schema.Message) *SummaryExtra {
	if msg.Extra == nil {
		return nil
	}
	v, ok := msg.Extra[SummaryExtraKey]
	if !ok {
		return nil
	}
	summary, ok := v.(*SummaryExtra)
	if !ok {
		return nil
	}
	return summary
}

// extractSummaryContent 从消息的 Extra 中提取摘要内容
func extractSummaryContent(msg *schema.Message) string {
	summary := GetSummaryExtra(msg)
	if summary == nil {
		return ""
	}
	return summary.Content
}

// GetToolCompressExtra 从消息中获取工具压缩信息
func GetToolCompressExtra(msg *schema.Message) *ToolCompressExtra {
	if msg.Extra == nil {
		return nil
	}
	v, ok := msg.Extra[ToolCompressExtraKey]
	if !ok {
		return nil
	}
	compress, ok := v.(*ToolCompressExtra)
	if !ok {
		return nil
	}
	return compress
}

// SetToolCompressExtra 设置工具压缩状态到消息
// 用于业务方从存储恢复压缩状态
func SetToolCompressExtra(msg *schema.Message, extra *ToolCompressExtra) {
	if msg == nil || extra == nil {
		return
	}
	if msg.Extra == nil {
		msg.Extra = make(map[string]any)
	}
	msg.Extra[ToolCompressExtraKey] = extra
}

// SetSummaryExtra 设置摘要状态到消息
// 用于业务方从存储恢复摘要状态
func SetSummaryExtra(msg *schema.Message, extra *SummaryExtra) {
	if msg == nil || extra == nil {
		return
	}
	if msg.Extra == nil {
		msg.Extra = make(map[string]any)
	}
	msg.Extra[SummaryExtraKey] = extra
}

// GetMessageID 从消息的 Extra 中获取消息 ID（用于关联持久层）
func GetMessageID(msg *schema.Message) string {
	if msg.Extra == nil {
		return ""
	}
	v, ok := msg.Extra[MessageIDExtraKey]
	if !ok {
		return ""
	}
	id, ok := v.(string)
	if !ok {
		return ""
	}
	return id
}

// SetMessageID 设置消息 ID 到消息
// 用于业务方关联消息与持久层
func SetMessageID(msg *schema.Message, id string) {
	if msg == nil || id == "" {
		return
	}
	if msg.Extra == nil {
		msg.Extra = make(map[string]any)
	}
	msg.Extra[MessageIDExtraKey] = id
}

// splitByLastSummary 按最后一条摘要消息分割，返回历史摘要内容和待摘要的新消息
// 摘要消息之前的内容已被总结过，不需要再次摘要
func splitByLastSummary(messages []*schema.Message) (prevSummary string, newMessages []*schema.Message) {
	lastSummaryIndex := -1
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if isSummaryMessage(msg) {
			prevSummary = extractSummaryContent(msg)
			lastSummaryIndex = i
			break
		}
	}

	if lastSummaryIndex >= 0 {
		newMessages = messages[lastSummaryIndex:]
	} else {
		newMessages = messages
	}
	return
}

// attachSummaryToMessage 把摘要信息绑定到现有消息上
// 返回一个新的消息副本，不修改原消息
func attachSummaryToMessage(msg *schema.Message, count int, summary string) *schema.Message {
	newMsg := *msg
	if newMsg.Extra == nil {
		newMsg.Extra = make(map[string]any)
	} else {
		// 复制 Extra map 避免修改原消息
		newExtra := make(map[string]any, len(msg.Extra)+1)
		for k, v := range msg.Extra {
			newExtra[k] = v
		}
		newMsg.Extra = newExtra
	}
	newMsg.Extra[SummaryExtraKey] = &SummaryExtra{
		Summarized: true,
		Content:    summary,
		Count:      count,
	}
	return &newMsg
}

// formatMessageForSummary 格式化单条消息用于摘要
func formatMessageForSummary(msg *schema.Message) string {
	role := "Unknown"
	switch msg.Role {
	case schema.User:
		role = "User"
	case schema.Assistant:
		role = "Assistant"
	case schema.System:
		role = "System"
	case schema.Tool:
		role = "Tool"
	}

	content := msg.Content
	if len(msg.ToolCalls) > 0 {
		var toolParts []string
		for _, tc := range msg.ToolCalls {
			toolParts = append(toolParts, fmt.Sprintf("[调用工具: %s]", tc.Function.Name))
		}
		if content != "" {
			content += "\n"
		}
		content += strings.Join(toolParts, ", ")
	}

	return fmt.Sprintf("[%s]: %s", role, content)
}

// formatTriggerConfig 格式化触发配置为可读字符串
func formatTriggerConfig(tc *TriggerConfig) string {
	if tc == nil {
		return "none"
	}
	switch tc.Type {
	case TriggerTypeFraction:
		return fmt.Sprintf("fraction(%.0f%%)", tc.Value*100)
	case TriggerTypeTokens:
		return fmt.Sprintf("tokens(%d)", int(tc.Value))
	case TriggerTypeMessages:
		return fmt.Sprintf("messages(%d)", int(tc.Value))
	default:
		return "unknown"
	}
}

// formatToolCompressConfig 格式化工具内容压缩配置为可读字符串
func formatToolCompressConfig(tac *ToolCompressConfig) string {
	if tac == nil {
		return "disabled"
	}
	trigger := formatTriggerConfig(tac.Trigger)
	keepBudget := tac.KeepBudgetFraction
	if keepBudget == 0 {
		keepBudget = constant.DefaultToolCompressKeepFraction
	}
	return fmt.Sprintf("trigger=%s, keep_budget=%.0f%%", trigger, keepBudget*100)
}

// GetSummary 获取当前缓存的摘要
func (m *SummarizationMiddleware) GetSummary() (string, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.summaryCache, m.originalCount
}

// ClearSummary 清除缓存的摘要
func (m *SummarizationMiddleware) ClearSummary() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.summaryCache = ""
	m.originalCount = 0
}

// ShouldSummarize 检查是否需要进行摘要
func (m *SummarizationMiddleware) ShouldSummarize(ctx context.Context, messages []*schema.Message) bool {
	return m.shouldTrigger(messages, m.getAvailableTokens(ctx), m.config.TokenCounter(messages))
}

// SummarizationStats 摘要统计信息
type SummarizationStats struct {
	TotalSummarizations  int
	TotalMessagesSaved   int
	CurrentSummary       string
	CurrentOriginalCount int
}

// CreateTikTokenCounter 创建基于字符的令牌计数器
func CreateTikTokenCounter(charsPerToken int) func([]*schema.Message) int {
	if charsPerToken <= 0 {
		charsPerToken = 4
	}
	return func(messages []*schema.Message) int {
		total := 0
		for _, msg := range messages {
			total += len(msg.Content)
			for _, tc := range msg.ToolCalls {
				total += len(tc.Function.Name)
				total += len(tc.Function.Arguments)
			}
		}
		return total / charsPerToken
	}
}

// TokenCountStopFunc 渐进式 token 统计的停止条件函数
// 从后往前遍历消息时调用，返回 true 表示停止统计（当前消息不计入）
type TokenCountStopFunc func(index int, msg *schema.Message) bool

// TokenCountOption 渐进式 token 统计的选项
type TokenCountOption func(*tokenCountOptions)

type tokenCountOptions struct {
	stopFunc TokenCountStopFunc
}

// WithTokenCountStopFunc 设置停止条件函数
func WithTokenCountStopFunc(fn TokenCountStopFunc) TokenCountOption {
	return func(opts *tokenCountOptions) {
		opts.stopFunc = fn
	}
}

// stopAtSummaryMessage 创建一个停止条件：遇到带摘要的 UserMessage 时停止
// 这意味着只统计从最近一个摘要消息开始的 token（摘要消息本身会计入）
func stopAtSummaryMessage() TokenCountStopFunc {
	return func(index int, msg *schema.Message) bool {
		// 如果是带摘要的 UserMessage，从这里开始计数（不跳过这条消息）
		// 但要跳过之前的消息
		if msg.Role == schema.User {
			extra := GetSummaryExtra(msg)
			if extra != nil && extra.Content != "" {
				return true // 停止，但当前消息会被计入
			}
		}
		return false
	}
}

// CountTokensProgressive 渐进式 token 统计
// 从后往前遍历消息，逐条累加 token 数，遇到停止条件时返回
// 返回值: (token数, 开始统计的消息索引)
func (m *SummarizationMiddleware) CountTokensProgressive(messages []*schema.Message, opts ...TokenCountOption) (int, int) {
	options := &tokenCountOptions{}
	for _, opt := range opts {
		opt(options)
	}

	totalTokens := 0
	startIndex := 0

	// 从后往前遍历，逐条累加 token
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		// 先计算当前消息的 token 并累加
		msgTokens := m.config.TokenCounter([]*schema.Message{msg})
		totalTokens += msgTokens
		startIndex = i

		// 计入当前消息后检查停止条件
		if options.stopFunc != nil && options.stopFunc(i, msg) {
			return totalTokens, startIndex
		}
	}

	return totalTokens, 0
}
