package tools

import (
	"reflect"

	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/components/tool/utils"
	jsonschema "github.com/eino-contrib/jsonschema"
)

var webToolParamDescriptionsEN = map[string]map[string]string{
	constant.ToolWebSearch: {
		"query":               "Search query. Be specific and detailed.",
		"max_results":         "Number of results to return. Defaults to 5.",
		"topic":               "Search topic type: general, news, or finance. Defaults to general.",
		"include_raw_content": "Whether to include full page content.",
	},
	constant.ToolHTTPRequest: {
		"url":     "Target URL.",
		"method":  "HTTP method such as GET, POST, PUT, or DELETE. Defaults to GET.",
		"headers": "HTTP request headers.",
		"body":    "Request body data.",
		"params":  "URL query parameters.",
		"timeout": "Request timeout in seconds. Defaults to 30.",
	},
	constant.ToolFetchURL: {
		"url":     "URL to fetch. Must be a valid HTTP or HTTPS URL.",
		"timeout": "Request timeout in seconds. Defaults to 30.",
	},
}

func webToolOptions(toolName string) []utils.Option {
	if !constant.IsEnglishPromptLang() {
		return nil
	}
	return []utils.Option{utils.WithSchemaModifier(webEnglishSchemaModifier(toolName))}
}

func webEnglishSchemaModifier(toolName string) utils.SchemaModifierFn {
	return func(jsonTagName string, _ reflect.Type, _ reflect.StructTag, schema *jsonschema.Schema) {
		if schema == nil {
			return
		}
		if fields := webToolParamDescriptionsEN[toolName]; fields != nil {
			if desc := fields[jsonTagName]; desc != "" {
				schema.Description = desc
			}
		}
	}
}
