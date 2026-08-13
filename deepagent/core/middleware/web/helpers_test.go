package web

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	jsonschema "github.com/eino-contrib/jsonschema"
	"github.com/stretchr/testify/require"
)

func findTool(t *testing.T, tools []tool.BaseTool, name string) tool.BaseTool {
	t.Helper()
	for _, candidate := range tools {
		info, _ := candidate.Info(context.Background())
		if info.Name == name {
			return candidate
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func toolNames(tools []tool.BaseTool) []string {
	names := make([]string, len(tools))
	for i, candidate := range tools {
		info, _ := candidate.Info(context.Background())
		names[i] = info.Name
	}
	return names
}

func toolJSONSchema(t *testing.T, base tool.BaseTool) *jsonschema.Schema {
	t.Helper()
	info, err := base.Info(context.Background())
	require.NoError(t, err)
	require.NotNil(t, info.ParamsOneOf)
	js, err := info.ParamsOneOf.ToJSONSchema()
	require.NoError(t, err)
	require.NotNil(t, js)
	return js
}

func containsHan(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

func schemaContainsHanDescription(js *jsonschema.Schema) bool {
	if js == nil {
		return false
	}
	if containsHan(js.Description) {
		return true
	}
	if js.Items != nil && schemaContainsHanDescription(js.Items) {
		return true
	}
	if js.AdditionalProperties != nil && schemaContainsHanDescription(js.AdditionalProperties) {
		return true
	}
	if js.Properties != nil {
		for pair := js.Properties.Oldest(); pair != nil; pair = pair.Next() {
			if schemaContainsHanDescription(pair.Value) {
				return true
			}
		}
	}
	for _, children := range [][]*jsonschema.Schema{js.OneOf, js.AnyOf, js.AllOf} {
		for _, child := range children {
			if schemaContainsHanDescription(child) {
				return true
			}
		}
	}
	return false
}
