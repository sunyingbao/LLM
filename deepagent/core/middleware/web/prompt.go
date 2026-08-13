package web

import (
	"strings"

	"eino-cli/deepagent/core/constant"
)

type webPromptCatalog struct {
	header string
	intro  string
	lines  map[string]string
}

var webPromptCatalogZH = webPromptCatalog{
	header: constant.WebSystemPromptHeader,
	intro:  constant.WebSystemPromptIntro,
	lines: map[string]string{
		constant.ToolWebSearch:   constant.WebSearchSystemPromptLine,
		constant.ToolHTTPRequest: constant.HTTPRequestSystemPromptLine,
		constant.ToolFetchURL:    constant.FetchURLSystemPromptLine,
	},
}

var webPromptCatalogEN = webPromptCatalog{
	header: "## Web Tools",
	intro:  "You can use the following web tools to retrieve network information:",
	lines: map[string]string{
		constant.ToolWebSearch: `- ` + "`web_search`" + `: Search the web for current information.
  Usage rules:
  1. Synthesize search results into a clear natural-language answer; do not show raw JSON
  2. When citing sources, mention the page title or URL
  3. Combine information from multiple sources into a coherent answer
  4. Use 1-2 searches for simple questions and avoid excessive searching`,
		constant.ToolHTTPRequest: "- `http_request`: Send HTTP requests to APIs or web services.",
		constant.ToolFetchURL:    "- `fetch_url`: Fetch web page content and convert it to Markdown.",
	},
}

func webPromptText() webPromptCatalog {
	if constant.IsEnglishPromptLang() {
		return webPromptCatalogEN
	}
	return webPromptCatalogZH
}

func buildWebPromptLines(visible map[string]bool) string {
	catalog := webPromptText()
	var parts []string
	if visible[constant.ToolWebSearch] || visible[constant.ToolHTTPRequest] || visible[constant.ToolFetchURL] {
		parts = append(parts, catalog.header, catalog.intro)
	}
	for _, toolName := range []string{constant.ToolWebSearch, constant.ToolHTTPRequest, constant.ToolFetchURL} {
		if visible[toolName] {
			parts = append(parts, catalog.lines[toolName])
		}
	}
	return strings.Join(parts, "\n")
}
