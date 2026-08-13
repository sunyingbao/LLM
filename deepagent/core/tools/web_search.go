// Package tools 提供工具包装器和辅助函数
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"eino-cli/deepagent/core/constant"
)

func asciiURL(b ...byte) string {
	return string(b)
}

// WebSearchInput Web 搜索输入参数
type WebSearchInput struct {
	Query             string `json:"query" jsonschema:"description=搜索查询词，要具体详细"`
	MaxResults        int    `json:"max_results,omitempty" jsonschema:"description=返回结果数量,默认5"`
	Topic             string `json:"topic,omitempty" jsonschema:"description=搜索主题类型: general/news/finance,默认general"`
	IncludeRawContent bool   `json:"include_raw_content,omitempty" jsonschema:"description=是否包含完整页面内容"`
}

// WebSearchResult Web 搜索结果
type WebSearchResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// WebSearchResponse Web 搜索响应
type WebSearchResponse struct {
	Query   string            `json:"query"`
	Results []WebSearchResult `json:"results,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// TavilySearchResponse Tavily API 响应
type TavilySearchResponse struct {
	Query   string `json:"query"`
	Results []struct {
		Title   string  `json:"title"`
		URL     string  `json:"url"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
	} `json:"results"`
}

// WebSearchConfig Web 搜索配置
type WebSearchConfig struct {
	APIKey     string        // Tavily API Key
	Timeout    time.Duration // 请求超时
	MaxResults int           // 默认最大结果数
}

// NewWebSearchTool 创建 Web 搜索工具
func NewWebSearchTool(config *WebSearchConfig) tool.BaseTool {
	if config == nil {
		config = &WebSearchConfig{}
	}
	if config.APIKey == "" {
		config.APIKey = os.Getenv("TAVILY_API_KEY")
	}
	if config.Timeout == 0 {
		config.Timeout = constant.DefaultHTTPTimeout
	}
	if config.MaxResults == 0 {
		config.MaxResults = constant.DefaultWebSearchMaxResults
	}

	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolWebSearch,
		constant.ToolWebSearchDesc,
		func(ctx context.Context, input *WebSearchInput) (string, error) {
			return executeWebSearch(ctx, config, input)
		},
		webToolOptions(constant.ToolWebSearch)...,
	)
	return t
}

// executeWebSearch 执行 Web 搜索
func executeWebSearch(ctx context.Context, config *WebSearchConfig, input *WebSearchInput) (string, error) {
	if config.APIKey == "" {
		response := WebSearchResponse{
			Query: input.Query,
			Error: "Tavily API key not configured. Please set TAVILY_API_KEY environment variable.",
		}
		return marshalResponse(response)
	}

	maxResults := input.MaxResults
	if maxResults == 0 {
		maxResults = config.MaxResults
	}

	topic := input.Topic
	if topic == "" {
		topic = constant.DefaultWebSearchTopic
	}

	// 构建 Tavily API 请求
	reqBody := map[string]interface{}{
		"api_key":             config.APIKey,
		"query":               input.Query,
		"max_results":         maxResults,
		"topic":               topic,
		"include_raw_content": input.IncludeRawContent,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		// 不返回 error，将错误信息作为字符串返回，避免图停止运行
		response := WebSearchResponse{
			Query: input.Query,
			Error: fmt.Sprintf("Failed to marshal request: %v", err),
		}
		return marshalResponse(response)
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "POST", asciiURL(104, 116, 116, 112, 115, 58, 47, 47, 97, 112, 105, 46, 116, 97, 118, 105, 108, 121, 46, 99, 111, 109, 47, 115, 101, 97, 114, 99, 104), strings.NewReader(string(reqJSON)))
	if err != nil {
		// 不返回 error，将错误信息作为字符串返回，避免图停止运行
		response := WebSearchResponse{
			Query: input.Query,
			Error: fmt.Sprintf("Failed to create request: %v", err),
		}
		return marshalResponse(response)
	}
	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	client := &http.Client{Timeout: config.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		response := WebSearchResponse{
			Query: input.Query,
			Error: fmt.Sprintf("Web search error: %v", err),
		}
		return marshalResponse(response)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// 不返回 error，将错误信息作为字符串返回，避免图停止运行
		response := WebSearchResponse{
			Query: input.Query,
			Error: fmt.Sprintf("Failed to read response: %v", err),
		}
		return marshalResponse(response)
	}

	if resp.StatusCode != http.StatusOK {
		response := WebSearchResponse{
			Query: input.Query,
			Error: fmt.Sprintf("Tavily API error: %s", string(body)),
		}
		return marshalResponse(response)
	}

	// 解析响应
	var tavilyResp TavilySearchResponse
	if err := json.Unmarshal(body, &tavilyResp); err != nil {
		// 不返回 error，将错误信息作为字符串返回，避免图停止运行
		response := WebSearchResponse{
			Query: input.Query,
			Error: fmt.Sprintf("Failed to parse response: %v", err),
		}
		return marshalResponse(response)
	}

	// 转换为统一格式
	results := make([]WebSearchResult, len(tavilyResp.Results))
	for i, r := range tavilyResp.Results {
		results[i] = WebSearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Content: r.Content,
			Score:   r.Score,
		}
	}

	response := WebSearchResponse{
		Query:   input.Query,
		Results: results,
	}

	return marshalResponse(response)
}

// DuckDuckGoSearchTool 创建 DuckDuckGo 搜索工具（不需要 API Key）
// 使用 DuckDuckGo HTML 搜索，支持中文等多语言查询
func NewDuckDuckGoSearchTool(timeout time.Duration) tool.BaseTool {
	if timeout == 0 {
		timeout = constant.DefaultHTTPTimeout
	}

	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolWebSearch,
		constant.ToolWebSearchDuckDuckGoDesc,
		func(ctx context.Context, input *WebSearchInput) (string, error) {
			return executeDuckDuckGoHTMLSearch(ctx, timeout, input)
		},
		webToolOptions(constant.ToolWebSearch)...,
	)
	return t
}

// executeDuckDuckGoHTMLSearch 执行 DuckDuckGo HTML 搜索
// 使用 HTML 搜索端点而非 Instant Answer API，以获得更好的多语言支持
func executeDuckDuckGoHTMLSearch(ctx context.Context, timeout time.Duration, input *WebSearchInput) (string, error) {
	// DuckDuckGo HTML 搜索端点（支持中文等多语言）
	apiURL := fmt.Sprintf(asciiURL(104, 116, 116, 112, 115, 58, 47, 47, 104, 116, 109, 108, 46, 100, 117, 99, 107, 100, 117, 99, 107, 103, 111, 46, 99, 111, 109, 47, 104, 116, 109, 108, 47, 63, 113, 61, 37, 115),
		url.QueryEscape(input.Query))

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		response := WebSearchResponse{
			Query: input.Query,
			Error: fmt.Sprintf("Failed to create request: %v", err),
		}
		return marshalResponse(response)
	}
	// 使用浏览器 User-Agent 以避免被拒绝
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		response := WebSearchResponse{
			Query: input.Query,
			Error: fmt.Sprintf("Search error: %v", err),
		}
		return marshalResponse(response)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		response := WebSearchResponse{
			Query: input.Query,
			Error: fmt.Sprintf("Failed to read response: %v", err),
		}
		return marshalResponse(response)
	}

	// 解析 HTML 响应
	results := parseDuckDuckGoHTML(string(body), input.MaxResults)

	response := WebSearchResponse{
		Query:   input.Query,
		Results: results,
	}

	if len(results) == 0 {
		response.Error = "No results found. Try rephrasing your query or use Tavily API (set TAVILY_API_KEY) for more comprehensive results."
	}

	return marshalResponse(response)
}

// parseDuckDuckGoHTML 解析 DuckDuckGo HTML 搜索结果
func parseDuckDuckGoHTML(html string, maxResults int) []WebSearchResult {
	if maxResults == 0 {
		maxResults = constant.DefaultWebSearchMaxResults
	}

	var results []WebSearchResult

	// 查找所有搜索结果
	// DuckDuckGo HTML 格式: class="result__a" href="redirect link with uddg=ENCODED_URL&..."
	// 结果标题在 <a> 标签内
	resultStart := 0
	for i := 0; i < maxResults*2 && resultStart >= 0; i++ { // 多搜索一些以确保有足够结果
		// 查找结果链接
		linkStart := strings.Index(html[resultStart:], `class="result__a"`)
		if linkStart == -1 {
			break
		}
		linkStart += resultStart

		// 提取 href
		hrefStart := strings.Index(html[linkStart:], `href="`)
		if hrefStart == -1 {
			resultStart = linkStart + 20
			continue
		}
		hrefStart += linkStart + 6

		hrefEnd := strings.Index(html[hrefStart:], `"`)
		if hrefEnd == -1 {
			resultStart = hrefStart
			continue
		}

		rawHref := html[hrefStart : hrefStart+hrefEnd]

		// 提取实际 URL（从 uddg 参数中解码）
		actualURL := extractDDGURL(rawHref)
		if actualURL == "" {
			resultStart = hrefStart + hrefEnd
			continue
		}

		// 提取标题（在 > 和 < 之间）
		titleStart := strings.Index(html[hrefStart+hrefEnd:], ">")
		if titleStart == -1 {
			resultStart = hrefStart + hrefEnd
			continue
		}
		titleStart += hrefStart + hrefEnd + 1

		titleEnd := strings.Index(html[titleStart:], "<")
		if titleEnd == -1 {
			resultStart = titleStart
			continue
		}

		title := strings.TrimSpace(html[titleStart : titleStart+titleEnd])
		title = decodeHTMLEntities(title)

		// 提取摘要（在 result__snippet 类中）
		snippet := ""
		snippetStart := strings.Index(html[titleStart:], `class="result__snippet"`)
		if snippetStart != -1 {
			snippetStart += titleStart
			contentStart := strings.Index(html[snippetStart:], ">")
			if contentStart != -1 {
				contentStart += snippetStart + 1
				contentEnd := strings.Index(html[contentStart:], "<")
				if contentEnd != -1 {
					snippet = strings.TrimSpace(html[contentStart : contentStart+contentEnd])
					snippet = decodeHTMLEntities(snippet)
				}
			}
		}

		if title != "" && actualURL != "" {
			results = append(results, WebSearchResult{
				Title:   title,
				URL:     actualURL,
				Content: snippet,
				Score:   1.0 - float64(len(results))*0.05,
			})

			if len(results) >= maxResults {
				break
			}
		}

		resultStart = hrefStart + hrefEnd + 100
	}

	return results
}

// extractDDGURL 从 DuckDuckGo 重定向 URL 中提取实际 URL
func extractDDGURL(rawHref string) string {
	// 过滤广告链接 (包含 /y.js 或 ad_provider)
	if strings.Contains(rawHref, "/y.js") || strings.Contains(rawHref, "ad_provider") {
		return ""
	}

	// 格式: redirect link with uddg=ENCODED_URL&rut=...
	uddgStart := strings.Index(rawHref, "uddg=")
	if uddgStart == -1 {
		// 可能是直接 URL
		if strings.HasPrefix(rawHref, "http") {
			return rawHref
		}
		return ""
	}

	uddgStart += 5
	uddgEnd := strings.Index(rawHref[uddgStart:], "&")
	if uddgEnd == -1 {
		uddgEnd = len(rawHref) - uddgStart
	}

	encoded := rawHref[uddgStart : uddgStart+uddgEnd]
	decoded, err := url.QueryUnescape(encoded)
	if err != nil {
		return ""
	}

	return decoded
}

// decodeHTMLEntities 解码常见的 HTML 实体
func decodeHTMLEntities(s string) string {
	replacements := map[string]string{
		"&amp;":    "&",
		"&lt;":     "<",
		"&gt;":     ">",
		"&quot;":   "\"",
		"&#39;":    "'",
		"&apos;":   "'",
		"&nbsp;":   " ",
		"&mdash;":  "—",
		"&ndash;":  "–",
		"&hellip;": "…",
	}

	result := s
	for entity, char := range replacements {
		result = strings.ReplaceAll(result, entity, char)
	}
	return result
}

// extractTitle 从文本中提取标题
func extractTitle(text string) string {
	// DuckDuckGo 的文本格式通常是 "Title - Description"
	if idx := strings.Index(text, " - "); idx > 0 {
		return text[:idx]
	}
	if len(text) > 50 {
		return text[:50] + "..."
	}
	return text
}

// marshalResponse 序列化响应
// 注意：此函数不应返回 error，因为工具返回 error 会导致图停止运行
func marshalResponse(v interface{}) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		// 如果序列化失败，返回简单的错误字符串
		return fmt.Sprintf("[Error] Failed to serialize response: %v", err), nil
	}
	return string(data), nil
}
