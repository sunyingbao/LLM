package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/utils"
	serialiser "eino-cli/deepagent/serialiser"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

/*
def run_compact(history: []*Message) -> ContextManager:
    if history.get_total_token_usage() < AUTO_COMPACT_LIMIT:
        return
    tmp_history = history.clone() // 拷贝一份历史，这样不对原数据造成污染

	unHandlesUueQrery = None
	if history[-1].is_user_message():
		unHandlesUueQrery = history.pop_back()

    summary_text = call_llm(tmp_history + SUMMARIZATION_PROMPT) // 历史上下文进行压缩
    summary_text = SUMMARY_PREFIX + summary_text // 在压缩的上下文前面加一段提示词标记后面是压缩的内容
    new_history = []*Message{}

    select_use_msg = []
    for msg in history.rev():  // 倒序遍历旧历史上下文
        if user_msg_used_token > cfg.kept_user_msg_token_limit:
            break
        if msg.is_user_message(): // 只保留 user message
            select_use_msg.append(msg)
            user_msg_used_token += count_token(msg)
    new_history.append(select_use_msg)
    new_history.append(summary_text)
	if unHandlesUueQrery not None:
		new_history.append(unHandlesUueQrery)
*/

const (
	summarizationPrompt = "You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM that will resume the task.\n" +
		"Include:\n" +
		"- Current progress and key decisions made\n" +
		"- Completed work, including concrete files, commands, tool results, and user-facing conclusions\n" +
		"- Important context, constraints, or user preferences\n" +
		"- What remains to be done (clear next steps)\n" +
		"- Any critical data, examples, or references needed to continue\n" +
		"\n" +
		"Clearly distinguish completed work from pending work. Be concise, structured, and focused on helping the next LLM seamlessly continue the work.\n"

	summarizationUserPrompt = "Summarize the conversation above now. Return only the handoff summary. Do not call tools."

	summarizationPrefix = "Another language model started to solve this problem and produced a summary of its thinking process. You also have access to the state of the tools that were used by that language model. Use this to build on the work that has already been done and avoid duplicating work. Here is the summary produced by the other language model, use the information in this summary to assist with your own analysis:"

	postCompactUserMessagesBeginMarker = "<aic_agent_sdk_post_compact_user_messages_json>"
	postCompactUserMessagesEndMarker   = "</aic_agent_sdk_post_compact_user_messages_json>"
)

type CodexStrategy struct {
	// 用于生成摘要的聊天模型
	Model model.ToolCallingChatModel
	// 触发压缩的总 token 阈值（成员变量）
	AutoCompactLimitTokens int
	// 保留尾部用户消息的预算（成员变量）
	KeptUserMsgTokenLimit int
	// 令牌计数器（可选，nil 则使用 utils.SimpleTokenCounter）
	TokenCounter func([]*schema.Message) int
	// PromptAppend is optional host-provided guidance appended to the compact
	// summarization prompt. It must stay generic at the SDK boundary.
	PromptAppend string
}

type CodexStrategyOption func(*CodexStrategy)

func WithPromptAppend(prompt string) CodexStrategyOption {
	return func(c *CodexStrategy) {
		c.PromptAppend = strings.TrimSpace(prompt)
	}
}

// NewCodexStrategy 构造函数，供上层注入模型与阈值
func NewCodexStrategy(m model.ToolCallingChatModel, autoLimit, keptLimit int, counter func([]*schema.Message) int, opts ...CodexStrategyOption) *CodexStrategy {
	if counter == nil {
		counter = utils.SimpleTokenCounter
	}
	strategy := &CodexStrategy{
		Model:                  m,
		AutoCompactLimitTokens: autoLimit,
		KeptUserMsgTokenLimit:  keptLimit,
		TokenCounter:           counter,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(strategy)
		}
	}
	return strategy
}

func (c CodexStrategy) ID() string {
	return "codex"
}

func (c CodexStrategy) AutoCompactTokenLimit() int64 {
	if c.AutoCompactLimitTokens <= 0 {
		return 16000
	}
	return int64(c.AutoCompactLimitTokens)
}

func (c CodexStrategy) Compact(ctx context.Context, current []*agentthread.Message) (*agentthread.CompactionResult, error) {
	keptUserMsgTokenLimit := c.KeptUserMsgTokenLimit
	if keptUserMsgTokenLimit <= 0 {
		keptUserMsgTokenLimit = 4000
	}

	counter := c.TokenCounter
	if counter == nil {
		counter = utils.SimpleTokenCounter
	}

	total := counter(current)
	request := make([]*schema.Message, 0, len(current)+2)
	request = append(request, schema.SystemMessage(c.summarizationPrompt()))
	request = append(request, current...)
	request = append(request, schema.UserMessage(summarizationUserPrompt))

	start := time.Now()
	logs.CtxInfo(ctx,
		"[CodexStrategy] compact model request start: messages=%d request_messages=%d original_tokens=%d auto_limit=%d kept_user_limit=%d",
		len(current), len(request), total, c.AutoCompactTokenLimit(), keptUserMsgTokenLimit,
	)
	resp, err := c.Model.Generate(ctx, request)
	if err != nil {
		logs.CtxError(ctx, "[CodexStrategy] compact model request failed: elapsed=%s err=%v", time.Since(start), err)
		return nil, err
	}
	if resp == nil {
		logs.CtxError(ctx, "[CodexStrategy] compact model request returned nil: elapsed=%s", time.Since(start))
		return nil, fmt.Errorf("codex compact: model returned nil response")
	}
	summaryText := strings.TrimSpace(resp.Content)
	if summaryText == "" {
		logs.CtxError(ctx, "[CodexStrategy] compact model request returned empty summary: elapsed=%s", time.Since(start))
		return nil, fmt.Errorf("codex compact: model returned empty summary")
	}
	logs.CtxInfo(ctx,
		"[CodexStrategy] compact model request done: elapsed=%s summary_chars=%d",
		time.Since(start), len(summaryText),
	)

	// 5) 从后往前保留用户消息，直到达到预算
	var selected []*schema.Message
	used := 0
selectLoop:
	for i := len(current) - 1; i >= 0; i-- {
		msg := current[i]
		if msg.Role != schema.User {
			continue
		}
		candidates := []*schema.Message{msg}
		if isCompactionSummaryMessage(msg) {
			candidates = embeddedPostCompactUserMessages(msg)
			if len(candidates) == 0 {
				continue
			}
		}
		for j := len(candidates) - 1; j >= 0; j-- {
			candidate := candidates[j]
			if candidate == nil || candidate.Role != schema.User || strings.TrimSpace(candidate.Content) == "" {
				continue
			}
			tok := counter([]*schema.Message{candidate})
			if used+tok > keptUserMsgTokenLimit {
				break selectLoop
			}
			selected = append(selected, candidate)
			used += tok
		}
	}
	// 为保持时间顺序：上述选择是从尾到头收集，这里反转为从旧到新
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	summaryMsg := schema.UserMessage(buildSummaryContent(summaryText, selected))

	// 6) 构建新的上下文窗口：用一条摘要消息承载压缩上下文和保留的用户意图。
	// 保留用户消息不能直接作为多条 user message 拼在摘要前，否则部分模型会拒绝
	// 连续 user 消息；如果 compact 边界切在 assistant tool_call 和 tool result
	// 中间，还会产生孤儿 tool 消息。
	rebuilt := []*schema.Message{summaryMsg}

	// 7) 返回压缩记录
	compact := &agentthread.CompactRecord{
		Summary:           summaryMsg,
		CompactStrategyID: c.ID(),
		CompactStrategyPayload: serialiser.ToString(&codexStrategyPayload{
			OriginalTokens: total,
			NewTokens:      used,
			KeptUserMsgs:   selected,
		}),
	}

	return &agentthread.CompactionResult{Compact: compact, Rebuilt: rebuilt}, nil
}

func (c CodexStrategy) Resume(ctx context.Context, compact *agentthread.CompactRecord, postCompactMessages []*agentthread.Message) (*agentthread.ResumeResult, error) {
	rebuilt := make([]*schema.Message, 0, len(postCompactMessages)+1)
	var csp codexStrategyPayload
	if err := serialiser.FromString(compact.CompactStrategyPayload, &csp); err != nil {
		return nil, err
	}

	summary := compact.Summary
	if summary != nil && len(csp.KeptUserMsgs) > 0 && !strings.Contains(summary.Content, "Recent user messages preserved before compaction:") {
		copied := *summary
		copied.Content = appendKeptUserMessages(copied.Content, csp.KeptUserMsgs)
		summary = &copied
	}
	rebuilt = append(rebuilt, summary)
	rebuilt = append(rebuilt, sanitizePostCompactMessages(postCompactMessages)...)
	rebuilt = mergeAdjacentUserMessages(rebuilt)
	// 8) 返回恢复结果
	return &agentthread.ResumeResult{
		Rebuilt: rebuilt,
	}, nil
}

func (c CodexStrategy) summarizationPrompt() string {
	if strings.TrimSpace(c.PromptAppend) == "" {
		return summarizationPrompt
	}
	return strings.TrimRight(summarizationPrompt, "\n") + "\n\n" + strings.TrimSpace(c.PromptAppend) + "\n"
}

func buildSummaryContent(summaryText string, keptUserMsgs []*schema.Message) string {
	var sb strings.Builder
	sb.WriteString(summarizationPrefix)
	sb.WriteString("\n\n")
	sb.WriteString(strings.TrimSpace(summaryText))
	if len(keptUserMsgs) == 0 {
		return sb.String()
	}
	sb.WriteString("\n\nRecent user messages preserved before compaction:")
	for _, msg := range keptUserMsgs {
		if msg == nil || msg.Role != schema.User || strings.TrimSpace(msg.Content) == "" || isCompactionSummaryMessage(msg) {
			continue
		}
		sb.WriteString("\n- ")
		sb.WriteString(strings.TrimSpace(msg.Content))
	}
	return sb.String()
}

func appendKeptUserMessages(content string, keptUserMsgs []*schema.Message) string {
	content = strings.TrimSpace(content)
	if len(keptUserMsgs) == 0 {
		return content
	}
	var sb strings.Builder
	sb.WriteString(content)
	sb.WriteString("\n\nRecent user messages preserved before compaction:")
	for _, msg := range keptUserMsgs {
		if msg == nil || msg.Role != schema.User || strings.TrimSpace(msg.Content) == "" || isCompactionSummaryMessage(msg) {
			continue
		}
		sb.WriteString("\n- ")
		sb.WriteString(strings.TrimSpace(msg.Content))
	}
	return sb.String()
}

func sanitizePostCompactMessages(messages []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if isCompactionSummaryMessage(msg) {
			continue
		}
		if msg.Role == schema.Tool && !hasPendingToolCall(out, msg.ToolCallID) {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func hasPendingToolCall(messages []*schema.Message, callID string) bool {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil {
			continue
		}
		if msg.Role == schema.Tool {
			if msg.ToolCallID == callID {
				return false
			}
			continue
		}
		if msg.Role != schema.Assistant {
			return false
		}
		for _, call := range msg.ToolCalls {
			if call.ID == callID {
				return true
			}
		}
		return false
	}
	return false
}

func mergeAdjacentUserMessages(messages []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if msg.Role == schema.User && len(out) > 0 && out[len(out)-1] != nil && out[len(out)-1].Role == schema.User {
			prevIsSummary := isCompactionSummaryMessage(out[len(out)-1])
			msgIsSummary := isCompactionSummaryMessage(msg)
			if prevIsSummary && !msgIsSummary {
				merged := *out[len(out)-1]
				merged.Content = appendPostCompactUserMessages(merged.Content, []*schema.Message{msg})
				out[len(out)-1] = &merged
				continue
			}
			if !prevIsSummary && !msgIsSummary {
				merged := *out[len(out)-1]
				merged.Content = strings.TrimRight(merged.Content, "\n") + "\n\n" + strings.TrimSpace(msg.Content)
				out[len(out)-1] = &merged
				continue
			}
		}
		out = append(out, msg)
	}
	return out
}

func isCompactionSummaryMessage(msg *schema.Message) bool {
	if msg == nil || msg.Role != schema.User {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(msg.Content), summarizationPrefix)
}

func appendPostCompactUserMessages(content string, messages []*schema.Message) string {
	content = strings.TrimSpace(content)
	if len(messages) == 0 {
		return content
	}
	base, existing, ok := splitPostCompactUserMessagesBlock(content)
	if ok {
		content = base
	}
	payload := make([]string, 0, len(existing)+len(messages))
	for _, msg := range existing {
		if msg != nil && strings.TrimSpace(msg.Content) != "" {
			payload = append(payload, msg.Content)
		}
	}
	for _, msg := range messages {
		if msg == nil || msg.Role != schema.User || strings.TrimSpace(msg.Content) == "" || isCompactionSummaryMessage(msg) {
			continue
		}
		payload = append(payload, msg.Content)
	}
	if len(payload) == 0 {
		return content
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return content
	}
	var sb strings.Builder
	sb.WriteString(content)
	sb.WriteString("\n\n")
	sb.WriteString(postCompactUserMessagesBeginMarker)
	sb.WriteString("\n")
	sb.Write(encoded)
	sb.WriteString("\n")
	sb.WriteString(postCompactUserMessagesEndMarker)
	return sb.String()
}

func embeddedPostCompactUserMessages(msg *schema.Message) []*schema.Message {
	if !isCompactionSummaryMessage(msg) {
		return nil
	}
	_, messages, ok := splitPostCompactUserMessagesBlock(msg.Content)
	if !ok {
		return nil
	}
	return messages
}

func splitPostCompactUserMessagesBlock(content string) (string, []*schema.Message, bool) {
	content = strings.TrimSpace(content)
	endIdx := strings.LastIndex(content, postCompactUserMessagesEndMarker)
	if endIdx < 0 || strings.TrimSpace(content[endIdx+len(postCompactUserMessagesEndMarker):]) != "" {
		return content, nil, false
	}
	prefix := content[:endIdx]
	beginIdx := strings.LastIndex(prefix, postCompactUserMessagesBeginMarker)
	if beginIdx < 0 {
		return content, nil, false
	}
	block := strings.TrimSpace(prefix[beginIdx+len(postCompactUserMessagesBeginMarker):])
	var payload []string
	if err := json.Unmarshal([]byte(block), &payload); err != nil {
		return content, nil, false
	}
	out := make([]*schema.Message, 0, len(payload))
	for _, text := range payload {
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, schema.UserMessage(text))
	}
	return strings.TrimSpace(content[:beginIdx]), out, true
}

type codexStrategyPayload struct {
	OriginalTokens int               `json:"original_tokens"`
	NewTokens      int               `json:"new_tokens"`
	KeptUserMsgs   []*schema.Message `json:"kept_user_msgs"`
}
