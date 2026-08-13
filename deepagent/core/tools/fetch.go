package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"eino-cli/deepagent/core/constant"
)

// FetchURLInput URL 获取输入参数
type FetchURLInput struct {
	URL     string `json:"url" jsonschema:"description=要获取的 URL（必须是有效的 HTTP/HTTPS URL）"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"description=请求超时秒数,默认30"`
}

// FetchURLResponse URL 获取响应
type FetchURLResponse struct {
	Success         bool   `json:"success"`
	URL             string `json:"url"`
	MarkdownContent string `json:"markdown_content,omitempty"`
	StatusCode      int    `json:"status_code"`
	ContentLength   int    `json:"content_length"`
	Error           string `json:"error,omitempty"`
}

// FetchURLConfig URL 获取工具配置
type FetchURLConfig struct {
	DefaultTimeout time.Duration
	MaxBodySize    int64
	UserAgent      string
}

// NewFetchURLTool 创建 URL 获取工具
func NewFetchURLTool(config *FetchURLConfig) tool.BaseTool {
	if config == nil {
		config = &FetchURLConfig{}
	}
	if config.DefaultTimeout == 0 {
		config.DefaultTimeout = constant.DefaultHTTPTimeout
	}
	if config.MaxBodySize == 0 {
		config.MaxBodySize = constant.DefaultMaxBodySize
	}
	if config.UserAgent == "" {
		config.UserAgent = constant.DefaultUserAgent
	}

	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolFetchURL,
		constant.ToolFetchURLDesc,
		func(ctx context.Context, input *FetchURLInput) (string, error) {
			return executeFetchURL(ctx, config, input)
		},
		webToolOptions(constant.ToolFetchURL)...,
	)
	return t
}

// executeFetchURL 执行 URL 获取
func executeFetchURL(ctx context.Context, config *FetchURLConfig, input *FetchURLInput) (string, error) {
	timeout := time.Duration(input.Timeout) * time.Second
	if timeout == 0 {
		timeout = config.DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", input.URL, nil)
	if err != nil {
		return marshalFetchResponse(FetchURLResponse{
			Success: false,
			URL:     input.URL,
			Error:   fmt.Sprintf("Failed to create request: %v", err),
		})
	}

	req.Header.Set("User-Agent", config.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= constant.DefaultMaxRedirects {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return marshalFetchResponse(FetchURLResponse{
			Success: false,
			URL:     input.URL,
			Error:   fmt.Sprintf("Fetch URL error: %v", err),
		})
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return marshalFetchResponse(FetchURLResponse{
			Success:    false,
			URL:        resp.Request.URL.String(),
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("HTTP error: %d", resp.StatusCode),
		})
	}

	// 读取响应体
	body, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxBodySize))
	if err != nil {
		return marshalFetchResponse(FetchURLResponse{
			Success:    false,
			URL:        resp.Request.URL.String(),
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("Failed to read response: %v", err),
		})
	}

	// 转换 HTML 到 Markdown
	markdown := htmlToMarkdown(string(body))

	return marshalFetchResponse(FetchURLResponse{
		Success:         true,
		URL:             resp.Request.URL.String(),
		MarkdownContent: markdown,
		StatusCode:      resp.StatusCode,
		ContentLength:   len(markdown),
	})
}

// htmlToMarkdown 简单的 HTML 到 Markdown 转换
// 注意：这是一个简化实现，完整实现应使用专门的库如 html-to-markdown
func htmlToMarkdown(html string) string {
	// 移除 script 和 style 标签
	scriptRe := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = scriptRe.ReplaceAllString(html, "")

	styleRe := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = styleRe.ReplaceAllString(html, "")

	// 移除 HTML 注释
	commentRe := regexp.MustCompile(`(?s)<!--.*?-->`)
	html = commentRe.ReplaceAllString(html, "")

	// 转换标题
	for i := 6; i >= 1; i-- {
		hRe := regexp.MustCompile(fmt.Sprintf(`(?is)<h%d[^>]*>(.*?)</h%d>`, i, i))
		prefix := strings.Repeat("#", i)
		html = hRe.ReplaceAllString(html, "\n"+prefix+" $1\n")
	}

	// 转换段落
	pRe := regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
	html = pRe.ReplaceAllString(html, "\n$1\n")

	// 转换换行
	brRe := regexp.MustCompile(`(?i)<br\s*/?>`)
	html = brRe.ReplaceAllString(html, "\n")

	// 转换粗体
	boldRe := regexp.MustCompile(`(?is)<(strong|b)[^>]*>(.*?)</(strong|b)>`)
	html = boldRe.ReplaceAllString(html, "**$2**")

	// 转换斜体
	italicRe := regexp.MustCompile(`(?is)<(em|i)[^>]*>(.*?)</(em|i)>`)
	html = italicRe.ReplaceAllString(html, "*$2*")

	// 转换链接
	linkRe := regexp.MustCompile(`(?is)<a[^>]*href=["']([^"']*)["'][^>]*>(.*?)</a>`)
	html = linkRe.ReplaceAllString(html, "[$2]($1)")

	// 转换图片
	imgRe := regexp.MustCompile(`(?is)<img[^>]*src=["']([^"']*)["'][^>]*alt=["']([^"']*)["'][^>]*/?>`)
	html = imgRe.ReplaceAllString(html, "![$2]($1)")
	imgRe2 := regexp.MustCompile(`(?is)<img[^>]*src=["']([^"']*)["'][^>]*/?>`)
	html = imgRe2.ReplaceAllString(html, "![]($1)")

	// 转换无序列表
	ulRe := regexp.MustCompile(`(?is)<ul[^>]*>(.*?)</ul>`)
	html = ulRe.ReplaceAllString(html, "\n$1\n")
	liRe := regexp.MustCompile(`(?is)<li[^>]*>(.*?)</li>`)
	html = liRe.ReplaceAllString(html, "- $1\n")

	// 转换有序列表
	olRe := regexp.MustCompile(`(?is)<ol[^>]*>(.*?)</ol>`)
	html = olRe.ReplaceAllString(html, "\n$1\n")

	// 转换代码块
	preRe := regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`)
	html = preRe.ReplaceAllString(html, "\n```\n$1\n```\n")

	// 转换行内代码
	codeRe := regexp.MustCompile(`(?is)<code[^>]*>(.*?)</code>`)
	html = codeRe.ReplaceAllString(html, "`$1`")

	// 转换引用块
	blockquoteRe := regexp.MustCompile(`(?is)<blockquote[^>]*>(.*?)</blockquote>`)
	html = blockquoteRe.ReplaceAllString(html, "\n> $1\n")

	// 转换水平线
	hrRe := regexp.MustCompile(`(?i)<hr[^>]*/?>`)
	html = hrRe.ReplaceAllString(html, "\n---\n")

	// 移除剩余的 HTML 标签
	tagRe := regexp.MustCompile(`<[^>]+>`)
	html = tagRe.ReplaceAllString(html, "")

	// 解码常见的 HTML 实体
	html = strings.ReplaceAll(html, "&nbsp;", " ")
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&quot;", "\"")
	html = strings.ReplaceAll(html, "&#39;", "'")
	html = strings.ReplaceAll(html, "&apos;", "'")

	// 清理多余的空白
	multiNewlineRe := regexp.MustCompile(`\n{3,}`)
	html = multiNewlineRe.ReplaceAllString(html, "\n\n")

	multiSpaceRe := regexp.MustCompile(`[ \t]+`)
	html = multiSpaceRe.ReplaceAllString(html, " ")

	// 清理每行开头和结尾的空白
	lines := strings.Split(html, "\n")
	var cleanLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}

	return strings.Join(cleanLines, "\n")
}

// marshalFetchResponse 序列化响应
// 注意：此函数不应返回 error，因为工具返回 error 会导致图停止运行
func marshalFetchResponse(resp FetchURLResponse) (string, error) {
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		// 如果序列化失败，返回简单的错误字符串
		return fmt.Sprintf("[Error] Failed to serialize response: %v", err), nil
	}
	return string(data), nil
}
