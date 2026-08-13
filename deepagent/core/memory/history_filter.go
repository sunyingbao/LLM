package memory

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/utils"
	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

const (
	defaultHistoryFilterMaxRecords       = 200
	defaultHistoryFilterMaxContentBytes  = 200 * 1024
	defaultStage1HistoryHeadRecords      = 40
	defaultStage1HistoryTailRecords      = 160
	defaultStage1HistoryMaxInputTokens   = 50_000
	defaultStage1HistoryMaxMessageTokens = 4_000
)

type HistoryFilterConfig struct {
	MaxRecords      int
	MaxContentBytes int
}

type HistoryRecordFilter func(*agentthread.HistoryRecord) bool

type Stage1HistoryInputConfig struct {
	HeadRecords      int                 `json:"head_records" yaml:"head_records"`
	TailRecords      int                 `json:"tail_records" yaml:"tail_records"`
	MaxInputTokens   int                 `json:"max_input_tokens" yaml:"max_input_tokens"`
	MaxMessageTokens int                 `json:"max_message_tokens" yaml:"max_message_tokens"`
	KeepRecord       HistoryRecordFilter `json:"-" yaml:"-"`
}

type Stage1HistoryInput struct {
	Contents        string
	SourceUpdatedAt time.Time
	EstimatedTokens int
	RecordsRead     int
	RecordsKept     int
}

type serializedHistoryMessage struct {
	Type      string `json:"type"`
	ThreadID  string `json:"thread_id"`
	TurnID    string `json:"turn_id"`
	MessageID int64  `json:"message_id"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	CreateAt  int64  `json:"create_at,omitempty"`
}

func buildStage1HistoryInput(ctx context.Context, history agentthread.HistoryRolloutStore, req RunStage1Request, cfg Stage1HistoryInputConfig) (Stage1HistoryInput, error) {
	if history == nil {
		return Stage1HistoryInput{}, nil
	}
	cfg = normalizeStage1HistoryInputConfig(cfg)
	head, err := history.List(ctx, agentthread.ListQuery{
		ThreadID: req.SourceThreadID,
		TurnID:   req.SourceTurnID,
		Order:    agentthread.ListOrderASC,
		Limit:    cfg.HeadRecords,
	})
	if err != nil {
		return Stage1HistoryInput{}, err
	}
	tail, err := history.List(ctx, agentthread.ListQuery{
		ThreadID: req.SourceThreadID,
		TurnID:   req.SourceTurnID,
		Order:    agentthread.ListOrderDESC,
		Limit:    cfg.TailRecords,
	})
	if err != nil {
		return Stage1HistoryInput{}, err
	}
	records := mergeHistoryHeadTail(head, tail)
	items, updated, err := serializeStage1HistoryRecords(ctx, records, cfg)
	if err != nil {
		return Stage1HistoryInput{}, err
	}
	items, contents, tokens, err := fitSerializedHistoryItemsToBudget(items, cfg.MaxInputTokens)
	if err != nil {
		return Stage1HistoryInput{}, err
	}
	return Stage1HistoryInput{
		Contents:        contents,
		SourceUpdatedAt: updated,
		EstimatedTokens: tokens,
		RecordsRead:     len(records),
		RecordsKept:     len(items),
	}, nil
}

func SerializeHistoryForStage1(ctx context.Context, records []*agentthread.HistoryRecord, cfg HistoryFilterConfig) (string, time.Time, error) {
	maxRecords := cfg.MaxRecords
	if maxRecords <= 0 {
		maxRecords = defaultHistoryFilterMaxRecords
	}
	maxContentBytes := cfg.MaxContentBytes
	if maxContentBytes <= 0 {
		maxContentBytes = defaultHistoryFilterMaxContentBytes
	}

	items := make([]serializedHistoryMessage, 0, min(maxRecords, len(records)))
	var updated time.Time
	remainingContentBytes := maxContentBytes
	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			return "", time.Time{}, err
		}
		if rec == nil {
			continue
		}
		if rec.CreateAt > 0 {
			ts := time.Unix(rec.CreateAt, 0)
			if ts.After(updated) {
				updated = ts
			}
		}
		if len(items) >= maxRecords {
			continue
		}
		if rec.Type != agentthread.HistoryRecordMessage || rec.Message == nil {
			continue
		}
		if !shouldKeepHistoryMessage(rec.Message) {
			continue
		}
		content := strings.TrimSpace(rec.Message.Content)
		if content == "" {
			continue
		}
		if remainingContentBytes <= 0 {
			continue
		}
		content = truncateUTF8Bytes(content, remainingContentBytes)
		if content == "" {
			continue
		}
		remainingContentBytes -= len([]byte(content))
		items = append(items, serializedHistoryMessage{
			Type:      string(rec.Type),
			ThreadID:  rec.ThreadID,
			TurnID:    rec.TurnID,
			MessageID: rec.MessageID,
			Role:      string(rec.Message.Role),
			Content:   content,
			CreateAt:  rec.CreateAt,
		})
	}
	b, err := sonic.MarshalIndent(items, "", "  ")
	if err != nil {
		return "", time.Time{}, err
	}
	return string(b), updated, nil
}

func normalizeStage1HistoryInputConfig(cfg Stage1HistoryInputConfig) Stage1HistoryInputConfig {
	if cfg.HeadRecords <= 0 {
		cfg.HeadRecords = defaultStage1HistoryHeadRecords
	}
	if cfg.TailRecords <= 0 {
		cfg.TailRecords = defaultStage1HistoryTailRecords
	}
	if cfg.MaxInputTokens <= 0 {
		cfg.MaxInputTokens = defaultStage1HistoryMaxInputTokens
	}
	if cfg.MaxMessageTokens <= 0 {
		cfg.MaxMessageTokens = defaultStage1HistoryMaxMessageTokens
	}
	return cfg
}

func mergeHistoryHeadTail(head []*agentthread.HistoryRecord, tail []*agentthread.HistoryRecord) []*agentthread.HistoryRecord {
	bySeq := make(map[int64]*agentthread.HistoryRecord, len(head)+len(tail))
	var noID []*agentthread.HistoryRecord
	add := func(records []*agentthread.HistoryRecord) {
		for _, rec := range records {
			if rec == nil {
				continue
			}
			seq := rec.OrderSeq()
			if seq == 0 {
				noID = append(noID, rec)
				continue
			}
			if _, ok := bySeq[seq]; !ok {
				bySeq[seq] = rec
			}
		}
	}
	add(head)
	add(tail)

	out := make([]*agentthread.HistoryRecord, 0, len(bySeq)+len(noID))
	for _, rec := range bySeq {
		out = append(out, rec)
	}
	out = append(out, noID...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].OrderSeq() < out[j].OrderSeq()
	})
	return out
}

func serializeStage1HistoryRecords(ctx context.Context, records []*agentthread.HistoryRecord, cfg Stage1HistoryInputConfig) ([]serializedHistoryMessage, time.Time, error) {
	items := make([]serializedHistoryMessage, 0, len(records))
	var updated time.Time
	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			return nil, time.Time{}, err
		}
		if rec == nil {
			continue
		}
		if rec.CreateAt > 0 {
			ts := time.Unix(rec.CreateAt, 0)
			if ts.After(updated) {
				updated = ts
			}
		}
		if rec.Type != agentthread.HistoryRecordMessage || rec.Message == nil {
			continue
		}
		if cfg.KeepRecord != nil && !cfg.KeepRecord(rec) {
			continue
		}
		if !shouldKeepHistoryMessage(rec.Message) {
			continue
		}
		content := strings.TrimSpace(rec.Message.Content)
		if content == "" || isMemoryExcludedContextualFragment(content) {
			continue
		}
		content = truncateToStage1Tokens(content, cfg.MaxMessageTokens)
		if content == "" {
			continue
		}
		items = append(items, serializedHistoryMessage{
			Type:      string(rec.Type),
			ThreadID:  rec.ThreadID,
			TurnID:    rec.TurnID,
			MessageID: rec.MessageID,
			Role:      string(rec.Message.Role),
			Content:   content,
			CreateAt:  rec.CreateAt,
		})
	}
	return items, updated, nil
}

func fitSerializedHistoryItemsToBudget(items []serializedHistoryMessage, maxTokens int) ([]serializedHistoryMessage, string, int, error) {
	for {
		contents, tokens, err := marshalSerializedHistoryItems(items)
		if err != nil {
			return nil, "", 0, err
		}
		if maxTokens <= 0 || tokens <= maxTokens || len(items) == 0 {
			return items, contents, tokens, nil
		}
		if len(items) == 1 {
			oldContent := items[0].Content
			items[0].Content = truncateToStage1Tokens(items[0].Content, max(1, estimateStage1Tokens(items[0].Content)-1))
			if items[0].Content == oldContent {
				return items, contents, tokens, nil
			}
			continue
		}
		drop := len(items) / 2
		if len(items) <= 2 {
			drop = 0
		}
		items = append(items[:drop], items[drop+1:]...)
	}
}

func marshalSerializedHistoryItems(items []serializedHistoryMessage) (string, int, error) {
	b, err := sonic.MarshalIndent(items, "", "  ")
	if err != nil {
		return "", 0, err
	}
	contents := string(b)
	return contents, estimateStage1Tokens(contents), nil
}

func truncateToStage1Tokens(s string, maxTokens int) string {
	if maxTokens <= 0 || estimateStage1Tokens(s) <= maxTokens {
		return s
	}
	return truncateUTF8Bytes(s, maxTokens*4)
}

func estimateStage1Tokens(s string) int {
	if s == "" {
		return 0
	}
	tokens := utils.EstimateTokens(s)
	if tokens <= 0 {
		return 1
	}
	return tokens
}

func isMemoryExcludedContextualFragment(content string) bool {
	return matchesMarkedStage1Fragment(content, "# AGENTS.md instructions for ", "</INSTRUCTIONS>") ||
		matchesMarkedStage1Fragment(content, "<skill>", "</skill>")
}

func matchesMarkedStage1Fragment(content, startMarker, endMarker string) bool {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) < len(startMarker)+len(endMarker) {
		return false
	}
	start := trimmed[:len(startMarker)]
	end := trimmed[len(trimmed)-len(endMarker):]
	return strings.EqualFold(start, startMarker) && strings.EqualFold(end, endMarker)
}

func shouldKeepHistoryMessage(msg *schema.Message) bool {
	switch msg.Role {
	case schema.User, schema.Assistant:
		return true
	case schema.Tool:
		if strings.TrimSpace(msg.Content) == "" {
			return false
		}
		return !isControlToolName(msg.ToolName)
	case schema.System:
		return false
	default:
		return false
	}
}

func isControlToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case constant.ToolUpdatePlan, constant.ToolWriteTodos, constant.ToolReadTodos, constant.ToolUpdateTodo, constant.ToolDispatchTasks:
		return true
	default:
		return false
	}
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len([]byte(s)) <= maxBytes {
		return s
	}
	for len([]byte(s)) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(s)
		if size <= 0 {
			return ""
		}
		s = s[:len(s)-size]
	}
	return s
}
