package web

import (
	"context"
	"os"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/tools"
)

// WebConfig Web 工具中间件配置
type WebConfig struct {
	// TavilyAPIKey Tavily API 密钥
	// 如果设置，将使用 Tavily API 进行搜索
	TavilyAPIKey string

	// UseDuckDuckGo 强制使用 DuckDuckGo（即使有 Tavily API Key）
	UseDuckDuckGo bool

	// Timeout 请求超时
	Timeout time.Duration

	// MaxResults 默认最大搜索结果数
	MaxResults int

	// EnableWebSearch 启用 Web 搜索
	EnableWebSearch bool

	// EnableHTTPRequest 启用 HTTP 请求
	EnableHTTPRequest bool

	// EnableFetchURL 启用 URL 获取
	EnableFetchURL bool

	// ToolMask 控制该中间件自身工具和工具提示词的可见性。
	ToolMask tools.Mask
}

// WebMiddleware Web 工具中间件
type WebMiddleware struct {
	middleware.BaseMiddleware
	config *WebConfig
}

func DefaultConfig() (config *WebConfig) {
	return &WebConfig{
		TavilyAPIKey:      "",
		EnableWebSearch:   true,
		EnableHTTPRequest: true,
		EnableFetchURL:    true,
		Timeout:           time.Second * 30,
		MaxResults:        5,
		UseDuckDuckGo:     true,
	}
}

// mergeWebConfigWithDefault 将用户配置与默认配置合并，零值字段使用默认值
func mergeWebConfigWithDefault(config *WebConfig) (mergedConfig *WebConfig) {
	if config == nil {
		return DefaultConfig()
	}

	defaults := DefaultConfig()

	merged := *config

	if merged.Timeout == 0 {
		merged.Timeout = defaults.Timeout
	}
	if merged.MaxResults == 0 {
		merged.MaxResults = defaults.MaxResults
	}

	return &merged
}

// New 创建 Web 工具中间件
func New(config *WebConfig) (web *WebMiddleware) {
	config = mergeWebConfigWithDefault(config)

	// 尝试从环境变量获取 Tavily API Key
	if config.TavilyAPIKey == "" {
		config.TavilyAPIKey = os.Getenv("TAVILY_API_KEY")
	}

	return &WebMiddleware{
		config: config,
	}
}

// Name 返回中间件名称
func (m *WebMiddleware) Name() string {
	return constant.MiddlewareWeb
}

// Tools 返回中间件提供的工具
func (m *WebMiddleware) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	var webTools []tool.BaseTool

	// Web 搜索工具
	// 优先级：Tavily（如有 API Key 且未强制使用 DuckDuckGo）> DuckDuckGo（默认）
	if m.config.EnableWebSearch {
		if m.config.TavilyAPIKey != "" && !m.config.UseDuckDuckGo {
			webTools = append(webTools, tools.NewWebSearchTool(&tools.WebSearchConfig{
				APIKey:     m.config.TavilyAPIKey,
				Timeout:    m.config.Timeout,
				MaxResults: m.config.MaxResults,
			}))
		} else {
			webTools = append(webTools, tools.NewDuckDuckGoSearchTool(m.config.Timeout))
		}
	}

	// HTTP 请求工具
	if m.config.EnableHTTPRequest {
		webTools = append(webTools, tools.NewHTTPRequestTool(&tools.HTTPRequestConfig{
			DefaultTimeout: m.config.Timeout,
		}))
	}

	// URL 获取工具
	if m.config.EnableFetchURL {
		webTools = append(webTools, tools.NewFetchURLTool(&tools.FetchURLConfig{
			DefaultTimeout: m.config.Timeout,
		}))
	}

	return tools.FilterToolsByMask(ctx, webTools, m.config.ToolMask, "WebMiddleware::Tools"), nil
}

// buildPrompt 构建 Web 工具的系统提示词
func (m *WebMiddleware) buildPrompt(ctx context.Context) string {
	visibleTools, err := m.Tools(ctx)
	if err != nil {
		return ""
	}
	visible := tools.ToolNameSet(ctx, visibleTools, "WebMiddleware::buildPrompt")
	return buildWebPromptLines(visible)
}

func (m *WebMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	prompt := m.buildPrompt(ctx)
	if prompt == "" {
		return nil, nil
	}
	return []*schema.Message{schema.SystemMessage(prompt)}, nil
}
