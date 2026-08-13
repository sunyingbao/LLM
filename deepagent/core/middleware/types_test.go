package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	cbutils "github.com/cloudwego/eino/utils/callbacks"
)

// testInputModifierMW records modified arguments for verification
type testInputModifierMW struct {
	BaseMiddleware
	prefix string
}

func (m *testInputModifierMW) Name() string { return "test_input_modifier" }

func (m *testInputModifierMW) WrapToolCall() compose.ToolMiddleware {
	return WrapAllToolInputs(func(ctx context.Context, input *compose.ToolInput) (string, bool) {
		input.Arguments = m.prefix + input.Arguments
		return "", false
	})
}

// testNoopMW doesn't override WrapToolCall (uses BaseMiddleware default)
type testNoopMW struct {
	BaseMiddleware
}

func (m *testNoopMW) Name() string { return "test_noop" }

func TestToolCallMiddlewares_CollectsNonNil(t *testing.T) {
	chain := NewMiddlewareChain(
		&testNoopMW{},
		&testInputModifierMW{prefix: "a:"},
		&testNoopMW{},
		&testInputModifierMW{prefix: "b:"},
	)

	mws := chain.ToolCallMiddlewares()
	if len(mws) != 2 {
		t.Fatalf("expected 2 non-nil middlewares, got %d", len(mws))
	}
}

func TestToolCallMiddlewares_EmptyChain(t *testing.T) {
	chain := NewMiddlewareChain()
	mws := chain.ToolCallMiddlewares()
	if len(mws) != 0 {
		t.Fatalf("expected 0 middlewares for empty chain, got %d", len(mws))
	}
}

func TestToolCallMiddlewares_AllNoop(t *testing.T) {
	chain := NewMiddlewareChain(&testNoopMW{}, &testNoopMW{})
	mws := chain.ToolCallMiddlewares()
	if len(mws) != 0 {
		t.Fatalf("expected 0 middlewares when all noop, got %d", len(mws))
	}
}

func TestWrapAllToolInputs_Invokable(t *testing.T) {
	mw := WrapAllToolInputs(func(ctx context.Context, input *compose.ToolInput) (string, bool) {
		input.Arguments = "fixed:" + input.Arguments
		return "", false
	})

	if mw.Invokable == nil {
		t.Fatal("Invokable should not be nil")
	}

	called := false
	inner := func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		called = true
		if input.Arguments != "fixed:raw" {
			t.Fatalf("expected 'fixed:raw', got '%s'", input.Arguments)
		}
		return &compose.ToolOutput{Result: "ok"}, nil
	}

	wrapped := mw.Invokable(inner)
	out, err := wrapped(context.Background(), &compose.ToolInput{Arguments: "raw"})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("inner handler not called")
	}
	if out.Result != "ok" {
		t.Fatalf("expected 'ok', got '%s'", out.Result)
	}
}

func TestWrapAllToolInputs_Streamable(t *testing.T) {
	mw := WrapAllToolInputs(func(ctx context.Context, input *compose.ToolInput) (string, bool) {
		input.Arguments = "fixed:" + input.Arguments
		return "", false
	})

	if mw.Streamable == nil {
		t.Fatal("Streamable should not be nil")
	}

	called := false
	inner := func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
		called = true
		if input.Arguments != "fixed:raw" {
			t.Fatalf("expected 'fixed:raw', got '%s'", input.Arguments)
		}
		return &compose.StreamToolOutput{}, nil
	}

	wrapped := mw.Streamable(inner)
	_, err := wrapped(context.Background(), &compose.ToolInput{Arguments: "raw"})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("inner handler not called")
	}
}

func TestWrapAllToolInputs_EnhancedInvokable(t *testing.T) {
	mw := WrapAllToolInputs(func(ctx context.Context, input *compose.ToolInput) (string, bool) {
		input.Arguments = "fixed:" + input.Arguments
		return "", false
	})

	if mw.EnhancedInvokable == nil {
		t.Fatal("EnhancedInvokable should not be nil")
	}

	called := false
	inner := func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
		called = true
		if input.Arguments != "fixed:raw" {
			t.Fatalf("expected 'fixed:raw', got '%s'", input.Arguments)
		}
		return &compose.EnhancedInvokableToolOutput{}, nil
	}

	wrapped := mw.EnhancedInvokable(inner)
	_, err := wrapped(context.Background(), &compose.ToolInput{Arguments: "raw"})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("inner handler not called")
	}
}

func TestWrapAllToolInputs_EnhancedStreamable(t *testing.T) {
	mw := WrapAllToolInputs(func(ctx context.Context, input *compose.ToolInput) (string, bool) {
		input.Arguments = "fixed:" + input.Arguments
		return "", false
	})

	if mw.EnhancedStreamable == nil {
		t.Fatal("EnhancedStreamable should not be nil")
	}

	called := false
	inner := func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedStreamableToolOutput, error) {
		called = true
		if input.Arguments != "fixed:raw" {
			t.Fatalf("expected 'fixed:raw', got '%s'", input.Arguments)
		}
		return &compose.EnhancedStreamableToolOutput{}, nil
	}

	wrapped := mw.EnhancedStreamable(inner)
	_, err := wrapped(context.Background(), &compose.ToolInput{Arguments: "raw"})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("inner handler not called")
	}
}

func TestWrapAllToolInputs_AllEndpointsSet(t *testing.T) {
	mw := WrapAllToolInputs(func(ctx context.Context, input *compose.ToolInput) (string, bool) { return "", false })
	if mw.Invokable == nil {
		t.Error("Invokable nil")
	}
	if mw.Streamable == nil {
		t.Error("Streamable nil")
	}
	if mw.EnhancedInvokable == nil {
		t.Error("EnhancedInvokable nil")
	}
	if mw.EnhancedStreamable == nil {
		t.Error("EnhancedStreamable nil")
	}
}

func TestWrapAllToolInputs_ShortCircuitsInvokable(t *testing.T) {
	mw := WrapAllToolInputs(func(ctx context.Context, input *compose.ToolInput) (string, bool) {
		if input.Arguments == "bad" {
			return "[Error] input is invalid json", true
		}
		input.Arguments = "fixed:" + input.Arguments
		return "", false
	})

	called := false
	wrapped := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		called = true
		return &compose.ToolOutput{Result: input.Arguments}, nil
	})

	out, err := wrapped(context.Background(), &compose.ToolInput{Arguments: "bad"})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("inner handler should not be called when middleware handles the input")
	}
	if out.Result != "[Error] input is invalid json" {
		t.Fatalf("result = %q", out.Result)
	}

	out, err = wrapped(context.Background(), &compose.ToolInput{Arguments: "raw"})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("inner handler should be called when middleware does not handle input")
	}
	if out.Result != "fixed:raw" {
		t.Fatalf("result = %q", out.Result)
	}
}

func TestWrapAllToolInputs_ShortCircuitsStreamable(t *testing.T) {
	mw := WrapAllToolInputs(func(ctx context.Context, input *compose.ToolInput) (string, bool) {
		return "[Error] input is invalid json", true
	})

	called := false
	wrapped := mw.Streamable(func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
		called = true
		return &compose.StreamToolOutput{}, nil
	})

	out, err := wrapped(context.Background(), &compose.ToolInput{Arguments: "bad"})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("inner handler should not be called")
	}
	chunk, err := out.Result.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if chunk != "[Error] input is invalid json" {
		t.Fatalf("stream chunk = %q", chunk)
	}
}

func TestWrapAllToolInputs_RecoveredStreamableErrorEmitsToolEndCallback(t *testing.T) {
	mw := WrapAllToolInputs(func(ctx context.Context, input *compose.ToolInput) (string, bool) {
		return "", false
	})

	var gotChunk string
	handler := cbutils.NewHandlerHelper().
		Tool(&cbutils.ToolCallbackHandler{
			OnEndWithStreamOutput: func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[*tool.CallbackOutput]) context.Context {
				chunk, err := output.Recv()
				if err != nil {
					t.Fatalf("callback stream Recv() error = %v", err)
				}
				gotChunk = chunk.Response
				return ctx
			},
		}).Handler()
	ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{
		Name:      "task",
		Component: components.ComponentOfTool,
	}, handler)

	wrapped := mw.Streamable(func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
		return nil, errors.New(`[LocalStreamFunc] failed to unmarshal arguments in json, toolName=task, err=Mismatch type bool with value string`)
	})

	out, err := wrapped(ctx, &compose.ToolInput{Name: "task", CallID: "call_bad_task", Arguments: `{"wait_for_done":"true"}`})
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := out.Result.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chunk, "Tool invocation failed") {
		t.Fatalf("stream chunk = %q", chunk)
	}
	if gotChunk != chunk {
		t.Fatalf("callback chunk = %q, want %q", gotChunk, chunk)
	}
}

func TestBaseMiddleware_WrapToolCall_AllNil(t *testing.T) {
	bm := &BaseMiddleware{}
	tm := bm.WrapToolCall()
	if tm.Invokable != nil || tm.Streamable != nil ||
		tm.EnhancedInvokable != nil || tm.EnhancedStreamable != nil {
		t.Error("BaseMiddleware.WrapToolCall() should return all-nil ToolMiddleware")
	}
}

// ==================== BuildPrompts Tests ====================

// testSystemPromptMW returns a system message from BuildInitialContext
type testSystemPromptMW struct {
	BaseMiddleware
	name    string
	content string
}

func (m *testSystemPromptMW) Name() string { return m.name }

func (m *testSystemPromptMW) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	if m.content == "" {
		return nil, nil
	}
	return []*schema.Message{schema.SystemMessage(m.content)}, nil
}

// testErrorMW returns an error from BuildInitialContext
type testErrorMW struct {
	BaseMiddleware
}

func (m *testErrorMW) Name() string { return "error_mw" }

func (m *testErrorMW) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	return nil, errors.New("build context failed")
}

// testNilMsgMW returns a slice containing nil messages
type testNilMsgMW struct {
	BaseMiddleware
}

func (m *testNilMsgMW) Name() string { return "nil_msg_mw" }

func (m *testNilMsgMW) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	return []*schema.Message{nil, schema.SystemMessage("after-nil")}, nil
}

// testNonSystemMW returns a user message from BuildInitialContext
type testNonSystemMW struct {
	BaseMiddleware
}

func (m *testNonSystemMW) Name() string { return "non_system_mw" }

func (m *testNonSystemMW) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	return []*schema.Message{schema.UserMessage("user-context")}, nil
}

func TestBuildPrompts_MergesSystemMessages(t *testing.T) {
	chain := NewMiddlewareChain(
		&testSystemPromptMW{name: "skill", content: "## Skills System"},
		&testSystemPromptMW{name: "filesystem", content: "## Filesystem Access"},
		&testSystemPromptMW{name: BasePromptMiddlewareName, content: "You are an AI assistant."},
	)

	msgs, err := chain.BuildPrompts(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Should produce exactly 1 message
	if len(msgs) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(msgs))
	}

	msg := msgs[0]
	if msg.Role != schema.System {
		t.Fatalf("expected system role, got %s", msg.Role)
	}

	// base_prompt content should come first
	if !strings.HasPrefix(msg.Content, "You are an AI assistant.") {
		t.Fatalf("base_prompt should be first, got: %s", msg.Content[:50])
	}

	// All parts should be present, separated by \n\n
	parts := strings.Split(msg.Content, "\n\n")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %v", len(parts), parts)
	}
	if parts[0] != "You are an AI assistant." {
		t.Errorf("part[0] = %q, want base_prompt", parts[0])
	}
	if parts[1] != "## Skills System" {
		t.Errorf("part[1] = %q, want skill", parts[1])
	}
	if parts[2] != "## Filesystem Access" {
		t.Errorf("part[2] = %q, want filesystem", parts[2])
	}
}

func TestBuildPrompts_BasePromptFirst(t *testing.T) {
	// BasePromptMiddleware registered last (as in real usage), but content should be first
	chain := NewMiddlewareChain(
		&testSystemPromptMW{name: "mw_a", content: "A"},
		&testSystemPromptMW{name: "mw_b", content: "B"},
		&testSystemPromptMW{name: BasePromptMiddlewareName, content: "IDENTITY"},
	)

	msgs, err := chain.BuildPrompts(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !strings.HasPrefix(msgs[0].Content, "IDENTITY") {
		t.Fatalf("identity should be first, got: %q", msgs[0].Content)
	}
}

func TestBuildPrompts_NoBasePrompt(t *testing.T) {
	// No base_prompt middleware — should still merge other system messages
	chain := NewMiddlewareChain(
		&testSystemPromptMW{name: "skill", content: "Skills"},
		&testSystemPromptMW{name: "filesystem", content: "FS"},
	)

	msgs, err := chain.BuildPrompts(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "Skills\n\nFS" {
		t.Fatalf("unexpected content: %q", msgs[0].Content)
	}
}

func TestBuildPrompts_EmptyChain(t *testing.T) {
	chain := NewMiddlewareChain()
	msgs, err := chain.BuildPrompts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}

func TestBuildPrompts_NilMessageSkipped(t *testing.T) {
	chain := NewMiddlewareChain(&testNilMsgMW{})

	msgs, err := chain.BuildPrompts(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "after-nil" {
		t.Fatalf("expected 'after-nil', got %q", msgs[0].Content)
	}
}

func TestBuildPrompts_EmptyContentSkipped(t *testing.T) {
	chain := NewMiddlewareChain(
		&testSystemPromptMW{name: "empty", content: ""},
		&testSystemPromptMW{name: "valid", content: "Valid"},
	)

	msgs, err := chain.BuildPrompts(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "Valid" {
		t.Fatalf("expected 'Valid', got %q", msgs[0].Content)
	}
}

func TestBuildPrompts_NonSystemMessagesPreserved(t *testing.T) {
	chain := NewMiddlewareChain(
		&testSystemPromptMW{name: BasePromptMiddlewareName, content: "Identity"},
		&testNonSystemMW{},
		&testSystemPromptMW{name: "skill", content: "Skills"},
	)

	msgs, err := chain.BuildPrompts(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// 1 merged system + 1 user
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != schema.System {
		t.Errorf("msgs[0] should be system, got %s", msgs[0].Role)
	}
	if msgs[1].Role != schema.User {
		t.Errorf("msgs[1] should be user, got %s", msgs[1].Role)
	}
	if msgs[1].Content != "user-context" {
		t.Errorf("msgs[1].Content = %q, want 'user-context'", msgs[1].Content)
	}
}

func TestBuildPrompts_ErrorPropagated(t *testing.T) {
	chain := NewMiddlewareChain(
		&testSystemPromptMW{name: "ok", content: "OK"},
		&testErrorMW{},
	)

	_, err := chain.BuildPrompts(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "build context failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildPrompts_AllEmpty(t *testing.T) {
	// All middlewares return nil — no messages
	chain := NewMiddlewareChain(
		&testSystemPromptMW{name: "a", content: ""},
		&testSystemPromptMW{name: "b", content: ""},
	)

	msgs, err := chain.BuildPrompts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}
