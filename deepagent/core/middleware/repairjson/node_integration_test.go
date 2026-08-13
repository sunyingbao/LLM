package repairjson

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	agenttools "eino-cli/deepagent/core/tools"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	cbutils "github.com/cloudwego/eino/utils/callbacks"
)

func TestDefaultToolsNodeRecoveredStreamableErrorDoesNotEmitToolErrorCallback(t *testing.T) {
	type taskArgs struct {
		WaitForDone bool `json:"wait_for_done"`
	}

	streamTool, err := toolutils.InferStreamTool[taskArgs, string](
		"task",
		"test task",
		func(ctx context.Context, input taskArgs) (*schema.StreamReader[string], error) {
			return schema.StreamReaderFromArray([]string{"unexpected"}), nil
		},
	)
	if err != nil {
		t.Fatalf("InferStreamTool err: %v", err)
	}

	wrappedTools := agenttools.WrapToolsWithConfig([]tool.BaseTool{streamTool}, &agenttools.WrapToolsConfig{})
	node, err := compose.NewToolNode(context.Background(), &compose.ToolsNodeConfig{
		Tools:               wrappedTools,
		ToolCallMiddlewares: []compose.ToolMiddleware{New().WrapToolCall()},
	})
	if err != nil {
		t.Fatalf("NewToolNode err: %v", err)
	}

	var gotToolError bool
	var gotToolEndChunk string
	handler := cbutils.NewHandlerHelper().
		Tool(&cbutils.ToolCallbackHandler{
			OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
				gotToolError = true
				return ctx
			},
			OnEndWithStreamOutput: func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[*tool.CallbackOutput]) context.Context {
				for {
					chunk, err := output.Recv()
					if err != nil {
						if !errors.Is(err, io.EOF) {
							t.Fatalf("callback stream Recv() error = %v", err)
						}
						return ctx
					}
					gotToolEndChunk += chunk.Response
				}
			},
		}).Handler()
	ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{
		Name:      "task",
		Component: components.ComponentOfTool,
	}, handler)

	out, err := node.Stream(ctx, schema.AssistantMessage("calling task", []schema.ToolCall{{
		ID: "call_bad_task",
		Function: schema.FunctionCall{
			Name:      "task",
			Arguments: `{"wait_for_done":"true"}`,
		},
	}}))
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	msgs, err := out.Recv()
	if err != nil {
		t.Fatalf("Recv err: %v", err)
	}
	if len(msgs) != 1 || msgs[0] == nil {
		t.Fatalf("expected one tool message, got %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "Tool invocation failed") {
		t.Fatalf("tool message content = %q", msgs[0].Content)
	}
	if gotToolError {
		t.Fatal("recovered streamable argument error should not emit tool OnError callback")
	}
	if gotToolEndChunk != msgs[0].Content {
		t.Fatalf("callback chunk = %q, want %q", gotToolEndChunk, msgs[0].Content)
	}
}

func TestDefaultToolsNodeRecoveredEnhancedErrorDoesNotEmitToolErrorCallback(t *testing.T) {
	type taskArgs struct {
		WaitForDone bool `json:"wait_for_done"`
	}

	enhancedTool, err := toolutils.InferEnhancedTool[taskArgs](
		"task",
		"test task",
		func(ctx context.Context, input taskArgs) (*schema.ToolResult, error) {
			return &schema.ToolResult{Parts: []schema.ToolOutputPart{{
				Type: schema.ToolPartTypeText,
				Text: "unexpected",
			}}}, nil
		},
	)
	if err != nil {
		t.Fatalf("InferEnhancedTool err: %v", err)
	}

	wrappedTools := agenttools.WrapToolsWithConfig([]tool.BaseTool{enhancedTool}, &agenttools.WrapToolsConfig{})
	node, err := compose.NewToolNode(context.Background(), &compose.ToolsNodeConfig{
		Tools:               wrappedTools,
		ToolCallMiddlewares: []compose.ToolMiddleware{New().WrapToolCall()},
	})
	if err != nil {
		t.Fatalf("NewToolNode err: %v", err)
	}

	var gotToolError bool
	var gotToolEndText string
	handler := cbutils.NewHandlerHelper().
		Tool(&cbutils.ToolCallbackHandler{
			OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
				gotToolError = true
				return ctx
			},
			OnEnd: func(ctx context.Context, info *callbacks.RunInfo, output *tool.CallbackOutput) context.Context {
				for _, part := range output.ToolOutput.Parts {
					gotToolEndText += part.Text
				}
				return ctx
			},
		}).Handler()
	ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{
		Name:      "task",
		Component: components.ComponentOfTool,
	}, handler)

	msgs, err := node.Invoke(ctx, schema.AssistantMessage("calling task", []schema.ToolCall{{
		ID: "call_bad_task",
		Function: schema.FunctionCall{
			Name:      "task",
			Arguments: `{"wait_for_done":"true"}`,
		},
	}}))
	if err != nil {
		t.Fatalf("Invoke err: %v", err)
	}
	if len(msgs) != 1 || msgs[0] == nil {
		t.Fatalf("expected one tool message, got %+v", msgs)
	}
	if !strings.Contains(gotToolEndText, "Tool invocation failed") {
		t.Fatalf("tool callback end text = %q", gotToolEndText)
	}
	if gotToolError {
		t.Fatal("recovered enhanced argument error should not emit tool OnError callback")
	}
}
