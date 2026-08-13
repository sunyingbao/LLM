package agentdefinition

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		definition Definition
		field      string
	}{
		{
			name:       "minimal valid definition",
			definition: Definition{Name: "assistant", Version: "v1"},
		},
		{
			name:       "empty name",
			definition: Definition{Version: "v1"},
			field:      "name",
		},
		{
			name:       "empty version",
			definition: Definition{Name: "assistant"},
			field:      "version",
		},
		{
			name: "duplicate tool",
			definition: Definition{Name: "assistant", Version: "v1", Tools: []ToolBinding{
				{Name: "search"},
				{Name: "search"},
			}},
			field: "tools[1].name",
		},
		{
			name: "duplicate middleware",
			definition: Definition{Name: "assistant", Version: "v1", Middleware: []MiddlewareBinding{
				{Name: "memory"},
				{Name: "memory"},
			}},
			field: "middleware[1].name",
		},
		{
			name:       "negative max steps",
			definition: Definition{Name: "assistant", Version: "v1", Limits: RuntimeLimits{MaxSteps: -1}},
			field:      "limits.max_steps",
		},
		{
			name:       "negative max model calls",
			definition: Definition{Name: "assistant", Version: "v1", Limits: RuntimeLimits{MaxModelCalls: -1}},
			field:      "limits.max_model_calls",
		},
		{
			name: "non JSON compatible config",
			definition: Definition{Name: "assistant", Version: "v1", Tools: []ToolBinding{
				{Name: "search", Config: map[string]any{"callback": func() {}}},
			}},
			field: "tools[0].config",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := Validate(tt.definition)
			if tt.field == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() error = nil, want field %q", tt.field)
			}
			var fieldErr *FieldError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("Validate() error type = %T, want *FieldError", err)
			}
			if fieldErr.Field != tt.field {
				t.Fatalf("FieldError.Field = %q, want %q", fieldErr.Field, tt.field)
			}
		})
	}
}
