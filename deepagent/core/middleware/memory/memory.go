package memory

import (
	"context"
	deeptools "eino-cli/deepagent/core/tools"
	"github.com/cloudwego/eino/schema"
	"strings"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	"github.com/cloudwego/eino/components/tool"
)

// MemoryMiddleware 提供持久化记忆能力
// 通过 AGENTS.md 文件存储长期记忆
type MemoryMiddleware struct {
	middleware.BaseMiddleware
	backend     backends.Backend
	sources     []string          // 记忆文件路径列表
	memoryCache map[string]string // 路径 -> 内容
	enableLearn bool              // 是否启用学习模式
	toolMask    deeptools.Mask
}

// MemoryConfig 记忆中间件配置
type MemoryConfig struct {
	// Backend 后端（用于读取记忆文件）
	Backend backends.Backend

	// Sources 记忆文件路径列表
	// 支持多个路径，所有内容会被合并
	// 示例: ["~/.deepagents/AGENTS.md", "./AGENTS.md"]
	Sources []string

	// EnableLearn 是否启用学习模式
	// 启用后，会在系统提示词中添加学习指南
	EnableLearn bool

	// ToolMask 用于判断学习指南引用的编辑工具是否可见。
	ToolMask deeptools.Mask
}

// New 创建记忆中间件
func New(cfg *MemoryConfig) *MemoryMiddleware {
	return &MemoryMiddleware{
		backend:     cfg.Backend,
		sources:     cfg.Sources,
		memoryCache: make(map[string]string),
		enableLearn: cfg.EnableLearn,
		toolMask:    cfg.ToolMask,
	}
}

func (m *MemoryMiddleware) Name() string {
	return constant.MiddlewareMemory
}

func (m *MemoryMiddleware) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	// 记忆中间件不提供工具，通过文件系统中间件的 edit_file 工具来更新记忆
	return nil, nil
}

// buildMemoryPrompt constructs the memory system prompt from cache
func (m *MemoryMiddleware) buildMemoryPrompt(ctx context.Context) string {
	var allMemory strings.Builder

	for _, content := range m.memoryCache {
		if content != "" {
			if allMemory.Len() > 0 {
				allMemory.WriteString("\n\n---\n\n")
			}
			allMemory.WriteString(content)
		}
	}

	if allMemory.Len() == 0 {
		return ""
	}

	catalog := memoryPromptText()
	var sb strings.Builder
	sb.WriteString(catalog.prefix)
	sb.WriteString(allMemory.String())
	sb.WriteString(catalog.suffix)

	if m.enableLearn && m.memoryEditToolVisible(ctx) {
		sb.WriteString(formatMemoryLearningGuide(m.sources))
	}

	return sb.String()
}

func (m *MemoryMiddleware) memoryEditToolVisible(ctx context.Context) bool {
	return deeptools.ToolNameVisible(ctx, m.toolMask, constant.ToolEditFile, "MemoryMiddleware::buildMemoryPrompt") ||
		deeptools.ToolNameVisible(ctx, m.toolMask, constant.ToolApplyPatch, "MemoryMiddleware::buildMemoryPrompt") ||
		deeptools.ToolNameVisible(ctx, m.toolMask, constant.ToolWriteFile, "MemoryMiddleware::buildMemoryPrompt")
}

// BeforeAgent 每次 Run/Stream 前重新加载记忆文件
func (m *MemoryMiddleware) BeforeAgent(ctx context.Context) error {
	// Reload all memory files (they may have been modified since last invocation)
	for _, source := range m.sources {
		content, err := m.loadMemory(ctx, source)
		if err != nil {
			continue // File not found, skip
		}
		m.memoryCache[source] = content
	}
	return nil
}

func (m *MemoryMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	prompt := m.buildMemoryPrompt(ctx)
	if prompt != "" {
		return []*schema.Message{schema.SystemMessage(prompt)}, nil
	}
	return nil, nil
}

// loadMemory 加载记忆文件
func (m *MemoryMiddleware) loadMemory(ctx context.Context, source string) (string, error) {
	// 使用后端读取
	content, err := m.backend.Read(ctx, source, nil, nil)
	if err != nil {
		return "", err
	}

	// 清理行号前缀
	return m.cleanContent(content), nil
}

// cleanContent 清理内容（去除行号前缀）
func (m *MemoryMiddleware) cleanContent(content string) string {
	lines := strings.Split(content, "\n")
	var cleaned []string

	for _, line := range lines {
		// 去除行号前缀（格式：空格+数字+|）
		if idx := strings.Index(line, "|"); idx != -1 && idx < 10 {
			line = line[idx+1:]
			if len(line) > 0 && line[0] == ' ' {
				line = line[1:]
			}
		}
		cleaned = append(cleaned, line)
	}

	return strings.Join(cleaned, "\n")
}

// GetMemory 获取当前记忆内容
func (m *MemoryMiddleware) GetMemory() string {
	var allMemory strings.Builder
	for _, content := range m.memoryCache {
		if content != "" {
			if allMemory.Len() > 0 {
				allMemory.WriteString("\n\n")
			}
			allMemory.WriteString(content)
		}
	}
	return allMemory.String()
}

// DefaultAgentsMD 返回默认的 AGENTS.md 模板
func DefaultAgentsMD() string {
	return constant.DefaultAgentsMDTemplate
}
