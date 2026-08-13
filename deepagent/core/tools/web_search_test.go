package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== DuckDuckGo HTML 搜索测试 ====================

func TestNewDuckDuckGoSearchTool(t *testing.T) {
	ddgTool := NewDuckDuckGoSearchTool(30 * time.Second)
	require.NotNil(t, ddgTool)

	info, err := ddgTool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "web_search", info.Name)
}

func TestDuckDuckGoHTMLSearch_ParseResults(t *testing.T) {
	// 创建模拟 DuckDuckGo HTML 响应
	mockHTML := `<!DOCTYPE html>
<html>
<body>
<div class="results">
  <div class="result">
    <a class="result__a" href="` + asciiURL(47, 47, 100, 117, 99, 107, 100, 117, 99, 107, 103, 111, 46, 99, 111, 109, 47, 108, 47, 63, 117, 100, 100, 103, 61, 104, 116, 116, 112, 115, 37, 51, 65, 37, 50, 70, 37, 50, 70, 119, 119, 119, 46, 119, 101, 97, 116, 104, 101, 114, 46, 99, 111, 109, 46, 99, 110, 37, 50, 70, 119, 101, 97, 116, 104, 101, 114, 37, 50, 70, 49, 48, 49, 48, 49, 48, 49, 48, 48, 46, 115, 104, 116, 109, 108, 38, 97, 109, 112, 59, 114, 117, 116, 61, 97, 98, 99, 49, 50, 51) + `">北京天气预报</a>
    <div class="result__snippet">北京今日天气：晴，温度 15°C</div>
  </div>
  <div class="result">
    <a class="result__a" href="` + asciiURL(47, 47, 100, 117, 99, 107, 100, 117, 99, 107, 103, 111, 46, 99, 111, 109, 47, 108, 47, 63, 117, 100, 100, 103, 61, 104, 116, 116, 112, 115, 37, 51, 65, 37, 50, 70, 37, 50, 70, 119, 119, 119, 46, 116, 105, 97, 110, 113, 105, 46, 99, 111, 109, 37, 50, 70, 98, 101, 105, 106, 105, 110, 103, 37, 50, 70, 38, 97, 109, 112, 59, 114, 117, 116, 61, 100, 101, 102, 52, 53, 54) + `">北京一周天气</a>
    <div class="result__snippet">查看北京未来7天天气预报</div>
  </div>
</div>
</body>
</html>`

	// 测试解析函数
	results := parseDuckDuckGoHTML(mockHTML, 5)

	assert.Len(t, results, 2)
	assert.Equal(t, "北京天气预报", results[0].Title)
	assert.Equal(t, asciiURL(104, 116, 116, 112, 115, 58, 47, 47, 119, 119, 119, 46, 119, 101, 97, 116, 104, 101, 114, 46, 99, 111, 109, 46, 99, 110, 47, 119, 101, 97, 116, 104, 101, 114, 47, 49, 48, 49, 48, 49, 48, 49, 48, 48, 46, 115, 104, 116, 109, 108), results[0].URL)
	assert.Contains(t, results[0].Content, "北京今日天气")
}

func TestExtractDDGURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard DDG redirect URL",
			input:    asciiURL(47, 47, 100, 117, 99, 107, 100, 117, 99, 107, 103, 111, 46, 99, 111, 109, 47, 108, 47, 63, 117, 100, 100, 103, 61, 104, 116, 116, 112, 115, 37, 51, 65, 37, 50, 70, 37, 50, 70, 101, 120, 97, 109, 112, 108, 101, 46, 99, 111, 109, 37, 50, 70, 112, 97, 103, 101, 38, 114, 117, 116, 61, 97, 98, 99, 49, 50, 51),
			expected: asciiURL(104, 116, 116, 112, 115, 58, 47, 47, 101, 120, 97, 109, 112, 108, 101, 46, 99, 111, 109, 47, 112, 97, 103, 101),
		},
		{
			name:     "URL with Chinese characters",
			input:    asciiURL(47, 47, 100, 117, 99, 107, 100, 117, 99, 107, 103, 111, 46, 99, 111, 109, 47, 108, 47, 63, 117, 100, 100, 103, 61, 104, 116, 116, 112, 115, 37, 51, 65, 37, 50, 70, 37, 50, 70, 119, 119, 119, 46, 119, 101, 97, 116, 104, 101, 114, 46, 99, 111, 109, 46, 99, 110, 37, 50, 70, 37, 69, 53, 37, 56, 67, 37, 57, 55, 37, 69, 52, 37, 66, 65, 37, 65, 67, 38, 114, 117, 116, 61, 120, 121, 122),
			expected: asciiURL(104, 116, 116, 112, 115, 58, 47, 47, 119, 119, 119, 46, 119, 101, 97, 116, 104, 101, 114, 46, 99, 111, 109, 46, 99, 110, 47, 229, 140, 151, 228, 186, 172),
		},
		{
			name:     "direct URL",
			input:    asciiURL(104, 116, 116, 112, 115, 58, 47, 47, 101, 120, 97, 109, 112, 108, 101, 46, 99, 111, 109, 47, 100, 105, 114, 101, 99, 116),
			expected: asciiURL(104, 116, 116, 112, 115, 58, 47, 47, 101, 120, 97, 109, 112, 108, 101, 46, 99, 111, 109, 47, 100, 105, 114, 101, 99, 116),
		},
		{
			name:     "no uddg parameter",
			input:    asciiURL(47, 47, 100, 117, 99, 107, 100, 117, 99, 107, 103, 111, 46, 99, 111, 109, 47, 111, 116, 104, 101, 114),
			expected: "",
		},
		{
			name:     "filter ad with y.js",
			input:    asciiURL(104, 116, 116, 112, 115, 58, 47, 47, 100, 117, 99, 107, 100, 117, 99, 107, 103, 111, 46, 99, 111, 109, 47, 121, 46, 106, 115, 63, 97, 100, 95, 100, 111, 109, 97, 105, 110, 61, 101, 120, 97, 109, 112, 108, 101, 46, 99, 111, 109, 38, 117, 100, 100, 103, 61, 104, 116, 116, 112, 115, 37, 51, 65, 37, 50, 70, 37, 50, 70, 101, 120, 97, 109, 112, 108, 101, 46, 99, 111, 109),
			expected: "",
		},
		{
			name:     "filter ad with ad_provider",
			input:    asciiURL(47, 47, 100, 117, 99, 107, 100, 117, 99, 107, 103, 111, 46, 99, 111, 109, 47, 108, 47, 63, 97, 100, 95, 112, 114, 111, 118, 105, 100, 101, 114, 61, 98, 105, 110, 103, 118, 55, 97, 97, 38, 117, 100, 100, 103, 61, 104, 116, 116, 112, 115, 37, 51, 65, 37, 50, 70, 37, 50, 70, 101, 120, 97, 109, 112, 108, 101, 46, 99, 111, 109),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractDDGURL(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDecodeHTMLEntities(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello &amp; World", "Hello & World"},
		{"&lt;tag&gt;", "<tag>"},
		{"&quot;quoted&quot;", "\"quoted\""},
		{"It&#39;s fine", "It's fine"},
		{"No entities", "No entities"},
		{"Multiple &amp; entities &amp; here", "Multiple & entities & here"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := decodeHTMLEntities(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseDuckDuckGoHTML_EmptyResults(t *testing.T) {
	emptyHTML := `<!DOCTYPE html><html><body><div class="no-results">No results</div></body></html>`
	results := parseDuckDuckGoHTML(emptyHTML, 5)
	assert.Empty(t, results)
}

func TestParseDuckDuckGoHTML_MaxResults(t *testing.T) {
	// 创建包含多个结果的 HTML
	mockHTML := `<!DOCTYPE html><html><body>`
	for i := 1; i <= 10; i++ {
		mockHTML += fmt.Sprintf(`
<div class="result" style="padding: 20px;">
  <a class="result__a" href="`+asciiURL(47, 47, 100, 117, 99, 107, 100, 117, 99, 107, 103, 111, 46, 99, 111, 109, 47, 108, 47, 63, 117, 100, 100, 103, 61)+`%s%d&amp;rut=abc123def456">Result %d Title Here</a>
  <div class="result__snippet">This is the content snippet for result number %d with more text to make it longer.</div>
</div>
`, asciiURL(104, 116, 116, 112, 115, 37, 51, 65, 37, 50, 70, 37, 50, 70, 101, 120, 97, 109, 112, 108, 101, 46, 99, 111, 109, 37, 50, 70, 112, 97, 103, 101), i, i, i)
	}
	mockHTML += `</body></html>`

	// 测试限制结果数
	results := parseDuckDuckGoHTML(mockHTML, 3)
	assert.Len(t, results, 3)
	assert.NotEmpty(t, results[0].Title)
	assert.NotEmpty(t, results[0].URL)
}

func TestDuckDuckGoSearchTool_MockServer(t *testing.T) {
	mockHTML := `<!DOCTYPE html>
<html>
<body>
<div class="results">
  <div class="result">
    <a class="result__a" href="` + asciiURL(47, 47, 100, 117, 99, 107, 100, 117, 99, 107, 103, 111, 46, 99, 111, 109, 47, 108, 47, 63, 117, 100, 100, 103, 61, 104, 116, 116, 112, 115, 37, 51, 65, 37, 50, 70, 37, 50, 70, 101, 120, 97, 109, 112, 108, 101, 46, 99, 111, 109, 37, 50, 70, 116, 101, 115, 116, 38, 114, 117, 116, 61, 97, 98, 99) + `">Test Result</a>
    <div class="result__snippet">Test content</div>
  </div>
</div>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	// 直接测试解析结果（因为工具内部 URL 是硬编码的）
	results := parseDuckDuckGoHTML(mockHTML, 5)
	assert.Len(t, results, 1)
	assert.Equal(t, "Test Result", results[0].Title)
}

// ==================== Tavily 搜索测试 ====================

func TestNewWebSearchTool(t *testing.T) {
	tavilyTool := NewWebSearchTool(&WebSearchConfig{
		APIKey:     "test-key",
		Timeout:    30 * time.Second,
		MaxResults: 5,
	})
	require.NotNil(t, tavilyTool)

	info, err := tavilyTool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "web_search", info.Name)
}

func TestTavilySearchTool_NoAPIKey(t *testing.T) {
	tavilyTool := NewWebSearchTool(&WebSearchConfig{
		APIKey:  "", // 无 API Key
		Timeout: 5 * time.Second,
	})

	invokable := tavilyTool.(tool.InvokableTool)
	result, err := invokable.InvokableRun(context.Background(), `{"query": "test"}`)
	require.NoError(t, err)

	var response WebSearchResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.Contains(t, response.Error, "API key not configured")
}
