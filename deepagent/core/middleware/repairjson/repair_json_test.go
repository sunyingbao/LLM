package repairjson

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/compose"
)

func TestRepairJSONMiddleware_RepairsOnlyInvalidJSON(t *testing.T) {
	mw := New().WrapToolCall()

	var gotArgs string
	wrapped := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		gotArgs = input.Arguments
		return &compose.ToolOutput{Result: "ok"}, nil
	})

	out, err := wrapped(context.Background(), &compose.ToolInput{Arguments: `{"a": 1,}`})
	if err != nil {
		t.Fatal(err)
	}
	if out.Result != "ok" {
		t.Fatalf("result = %q", out.Result)
	}
	if gotArgs != `{"a": 1}` {
		t.Fatalf("arguments = %q", gotArgs)
	}
}

func TestRepairJSONMiddleware_ReturnsToolResultForUnrepairableInput(t *testing.T) {
	mw := New().WrapToolCall()

	called := false
	wrapped := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		called = true
		return &compose.ToolOutput{Result: "unexpected"}, nil
	})

	out, err := wrapped(context.Background(), &compose.ToolInput{Arguments: `plain text without json`})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("inner tool should not be called for unrepairable JSON")
	}
	if !strings.Contains(out.Result, "[Error] input is invalid json") {
		t.Fatalf("result = %q", out.Result)
	}
}
