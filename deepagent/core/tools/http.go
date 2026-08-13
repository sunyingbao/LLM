package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"eino-cli/deepagent/core/constant"
)

// HTTPRequestInput HTTP 请求输入参数
type HTTPRequestInput struct {
	URL     string            `json:"url" jsonschema:"description=目标 URL"`
	Method  string            `json:"method,omitempty" jsonschema:"description=HTTP 方法(GET/POST/PUT/DELETE等),默认GET"`
	Headers map[string]string `json:"headers,omitempty" jsonschema:"description=HTTP 请求头"`
	Body    string            `json:"body,omitempty" jsonschema:"description=请求体数据"`
	Params  map[string]string `json:"params,omitempty" jsonschema:"description=URL 查询参数"`
	Timeout int               `json:"timeout,omitempty" jsonschema:"description=请求超时秒数,默认30"`
}

// HTTPResponse HTTP 响应
type HTTPResponse struct {
	Success    bool              `json:"success"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Content    interface{}       `json:"content"`
	URL        string            `json:"url"`
	Error      string            `json:"error,omitempty"`
}

// HTTPRequestConfig HTTP 请求工具配置
type HTTPRequestConfig struct {
	DefaultTimeout time.Duration
	MaxBodySize    int64
	UserAgent      string
}

// NewHTTPRequestTool 创建 HTTP 请求工具
func NewHTTPRequestTool(config *HTTPRequestConfig) tool.BaseTool {
	if config == nil {
		config = &HTTPRequestConfig{}
	}
	if config.DefaultTimeout == 0 {
		config.DefaultTimeout = constant.DefaultHTTPTimeout
	}
	if config.MaxBodySize == 0 {
		config.MaxBodySize = constant.DefaultHTTPMaxBodySize
	}
	if config.UserAgent == "" {
		config.UserAgent = constant.DefaultUserAgent
	}

	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolHTTPRequest,
		constant.ToolHTTPRequestDesc,
		func(ctx context.Context, input *HTTPRequestInput) (string, error) {
			return executeHTTPRequest(ctx, config, input)
		},
		webToolOptions(constant.ToolHTTPRequest)...,
	)
	return t
}

// executeHTTPRequest 执行 HTTP 请求
func executeHTTPRequest(ctx context.Context, config *HTTPRequestConfig, input *HTTPRequestInput) (string, error) {
	// 设置默认值
	method := strings.ToUpper(input.Method)
	if method == "" {
		method = "GET"
	}

	timeout := time.Duration(input.Timeout) * time.Second
	if timeout == 0 {
		timeout = config.DefaultTimeout
	}

	// 构建 URL 参数
	targetURL := input.URL
	if len(input.Params) > 0 {
		if strings.Contains(targetURL, "?") {
			targetURL += "&"
		} else {
			targetURL += "?"
		}
		var params []string
		for k, v := range input.Params {
			params = append(params, fmt.Sprintf("%s=%s", k, v))
		}
		targetURL += strings.Join(params, "&")
	}

	// 创建请求
	var body io.Reader
	if input.Body != "" {
		body = bytes.NewBufferString(input.Body)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return marshalHTTPResponse(HTTPResponse{
			Success:    false,
			StatusCode: 0,
			Headers:    map[string]string{},
			Content:    fmt.Sprintf("Failed to create request: %v", err),
			URL:        input.URL,
		})
	}

	// 设置请求头
	req.Header.Set("User-Agent", config.UserAgent)
	for k, v := range input.Headers {
		req.Header.Set(k, v)
	}

	// 如果有 JSON body，设置 Content-Type
	if input.Body != "" && req.Header.Get("Content-Type") == "" {
		// 尝试检测是否为 JSON
		var js json.RawMessage
		if json.Unmarshal([]byte(input.Body), &js) == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}

	// 发送请求
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
		errMsg := fmt.Sprintf("Request error: %v", err)
		if ctx.Err() == context.DeadlineExceeded {
			errMsg = fmt.Sprintf("Request timed out after %v", timeout)
		}
		return marshalHTTPResponse(HTTPResponse{
			Success:    false,
			StatusCode: 0,
			Headers:    map[string]string{},
			Content:    errMsg,
			URL:        input.URL,
			Error:      errMsg,
		})
	}
	defer resp.Body.Close()

	// 读取响应体
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxBodySize))
	if err != nil {
		return marshalHTTPResponse(HTTPResponse{
			Success:    false,
			StatusCode: resp.StatusCode,
			Headers:    map[string]string{},
			Content:    fmt.Sprintf("Failed to read response: %v", err),
			URL:        resp.Request.URL.String(),
			Error:      err.Error(),
		})
	}

	// 解析响应头
	headers := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	// 尝试解析为 JSON
	var content interface{}
	if err := json.Unmarshal(respBody, &content); err != nil {
		content = string(respBody)
	}

	return marshalHTTPResponse(HTTPResponse{
		Success:    resp.StatusCode < 400,
		StatusCode: resp.StatusCode,
		Headers:    headers,
		Content:    content,
		URL:        resp.Request.URL.String(),
	})
}

// marshalHTTPResponse 序列化 HTTP 响应
// 注意：此函数不应返回 error，因为工具返回 error 会导致图停止运行
func marshalHTTPResponse(resp HTTPResponse) (string, error) {
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		// 如果序列化失败，返回简单的错误字符串
		return fmt.Sprintf("[Error] Failed to serialize response: %v", err), nil
	}
	return string(data), nil
}
