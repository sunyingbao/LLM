package skill

import (
	"context"
	"testing"

	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/components/tool"
	jsonschema "github.com/eino-contrib/jsonschema"
)

func skillToolInfo(t *testing.T, base tool.BaseTool) string {
	t.Helper()
	info, err := base.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info == nil {
		t.Fatalf("Info() = nil")
	}
	return info.Desc
}

func skillToolSchema(t *testing.T, base tool.BaseTool) *jsonschema.Schema {
	t.Helper()
	info, err := base.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("ToJSONSchema() error = %v", err)
	}
	return js
}

func skillHasHan(s string) bool {
	for _, r := range s {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

func skillSchemaHasHanDescription(js *jsonschema.Schema) bool {
	if js == nil {
		return false
	}
	if skillHasHan(js.Description) {
		return true
	}
	if js.Properties != nil {
		for pair := js.Properties.Oldest(); pair != nil; pair = pair.Next() {
			if skillSchemaHasHanDescription(pair.Value) {
				return true
			}
		}
	}
	if js.Items != nil {
		return skillSchemaHasHanDescription(js.Items)
	}
	return false
}

func TestSkillTools_EnglishDescriptionAndSchema(t *testing.T) {
	t.Setenv(constant.PromptLangEnv, "en")

	m := New(&fakeLoader{})
	tools, err := m.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	for _, tl := range tools {
		if skillHasHan(skillToolInfo(t, tl)) {
			t.Fatalf("tool desc contains Chinese")
		}
		if skillSchemaHasHanDescription(skillToolSchema(t, tl)) {
			t.Fatalf("tool schema contains Chinese")
		}
	}
}
