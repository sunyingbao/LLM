package graph

import (
	"context"
	"errors"
	"io"
	"testing"

	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// TestCreateBranchNode_NonStreamRoutesByToolCalls
// 主要验证:
// 1. 非流式分支模式下，CreateBranchNode(false) 会根据完整 message 中是否包含 tool calls 选择后续节点。
// 2. 有 tool call 时会进入 tools 节点；没有 tool call 时直接结束，不会误触发工具执行。
//
// 验证思路:
// - 搭一个最小 workflow，executor 输出原始 message，branch 挂在 executor 后；
// - tools 节点只返回固定值，便于观察它是否被执行；
// - 分别喂入“有 tool call”和“无 tool call”的 message，检查最终 output map 里是否出现 tools 结果。
func TestCreateBranchNode_NonStreamRoutesByToolCalls(t *testing.T) {
	ctx := context.Background()

	wf := compose.NewWorkflow[*schema.Message, map[string]any]()
	wf.AddPassthroughNode("executor").AddInput(compose.START)
	wf.AddLambdaNode(constant.NodeKeyTools, compose.InvokableLambda(func(ctx context.Context, in *schema.Message) (string, error) {
		return "tools", nil
	})).AddInput("executor")
	wf.AddBranch("executor", CreateBranchNode(false))
	wf.End().
		AddInput("executor", compose.ToField("executor")).
		AddInput(constant.NodeKeyTools, compose.ToField(constant.NodeKeyTools))

	runner, err := wf.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile err: %v", err)
	}

	out, err := runner.Invoke(ctx, &schema.Message{Role: schema.Assistant, Content: "plain"})
	if err != nil {
		t.Fatalf("Invoke without tool call err: %v", err)
	}
	if _, ok := out[constant.NodeKeyTools]; ok {
		t.Fatalf("tools node should not run when there is no tool call, got %+v", out)
	}

	out, err = runner.Invoke(ctx, &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID: "call_1",
			Function: schema.FunctionCall{
				Name:      "echo",
				Arguments: `{"value":"hello"}`,
			},
		}},
	})
	if err != nil {
		t.Fatalf("Invoke with tool call err: %v", err)
	}
	if got := out[constant.NodeKeyTools]; got != "tools" {
		t.Fatalf("tools node should run when tool call exists, got %+v", out)
	}
}

// TestCreateBranchNode_StreamRoutesAndPreservesStream
// 主要验证:
// 1. 流式分支模式下，CreateBranchNode(true) 能在 stream 中发现 tool call 后正确路由到 tools 节点。
// 2. branch 判定不会破坏下游 tools 节点对原始 stream 的消费，下游仍能收到完整 message stream。
//
// 验证思路:
// - executor 节点输出一个 message stream，其中一部分场景包含 tool call，一部分不包含；
// - tools 节点以 collectable 方式把整个 stream 收完，统计内容拼接结果和 tool call 数量；
// - 分别验证“有 tool call 时 tools 节点执行且拿到完整 stream”和“无 tool call 时 tools 节点不执行”。
func TestCreateBranchNode_StreamRoutesAndPreservesStream(t *testing.T) {
	ctx := context.Background()

	streamInputs := map[string][]*schema.Message{
		"no_tool": {
			{Role: schema.Assistant, Content: "hello"},
			{Role: schema.Assistant, Content: " world"},
		},
		"with_tool": {
			{Role: schema.Assistant, Content: "hello"},
			{
				Role:    schema.Assistant,
				Content: " world",
				ToolCalls: []schema.ToolCall{{
					ID: "call_1",
					Function: schema.FunctionCall{
						Name:      "echo",
						Arguments: `{"value":"ok"}`,
					},
				}},
			},
		},
	}

	wf := compose.NewWorkflow[string, map[string]any]()
	wf.AddLambdaNode("executor", compose.StreamableLambda(func(ctx context.Context, in string) (*schema.StreamReader[*schema.Message], error) {
		return schema.StreamReaderFromArray(streamInputs[in]), nil
	})).AddInput(compose.START)
	wf.AddLambdaNode(constant.NodeKeyTools, compose.CollectableLambda(func(ctx context.Context, in *schema.StreamReader[*schema.Message]) (string, error) {
		defer in.Close()

		var mergedContent string
		toolCallCount := 0
		for {
			chunk, err := in.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return "", err
			}
			mergedContent += chunk.Content
			toolCallCount += len(chunk.ToolCalls)
		}

		return mergedContent + "|" + string(rune('0'+toolCallCount)), nil
	})).AddInput("executor")
	wf.AddBranch("executor", CreateBranchNode(true))
	wf.End().AddInput(constant.NodeKeyTools, compose.ToField(constant.NodeKeyTools))

	runner, err := wf.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile err: %v", err)
	}

	out, err := runner.Invoke(ctx, "no_tool")
	if err != nil {
		t.Fatalf("Invoke without tool call err: %v", err)
	}
	if _, ok := out[constant.NodeKeyTools]; ok {
		t.Fatalf("tools node should not run when stream has no tool call, got %+v", out)
	}

	out, err = runner.Invoke(ctx, "with_tool")
	if err != nil {
		t.Fatalf("Invoke with tool call err: %v", err)
	}
	if got := out[constant.NodeKeyTools]; got != "hello world|1" {
		t.Fatalf("tools node should receive full stream after branch, got %+v", out)
	}
}

// TestStreamMessageMerger_MergeContentAndCompleteToolCalls
// 主要验证:
// 1. StreamMessageMerger 会把所有 chunk 的 content 顺序拼接成最终 message.Content。
// 2. 分块输出的同一个 tool call 会被正确合并成完整 tool call。
// 3. onChunk 回调会对每个 chunk 都触发一次。
//
// 验证思路:
// - 构造一个 assistant stream，content 和 tool call arguments 都分两段输出；
// - 调用 Merge 收敛成单条 message；
// - 检查最终 content、tool call arguments、以及 onChunk 命中次数是否符合预期。
func TestStreamMessageMerger_MergeContentAndCompleteToolCalls(t *testing.T) {
	ctx := context.Background()
	onChunkCount := 0

	merger := NewStreamMessageMerger(func(ctx context.Context, chunk *schema.Message) {
		onChunkCount++
	})

	idx := 0
	merged, err := merger.Merge(ctx, schema.StreamReaderFromArray([]*schema.Message{
		{
			Role:             schema.Assistant,
			Content:          "hel",
			ReasoningContent: "think-",
			ToolCalls: []schema.ToolCall{{
				ID:    "call_1",
				Index: &idx,
				Function: schema.FunctionCall{
					Name:      "echo",
					Arguments: `{"value":"wo`,
				},
			}},
		},
		{
			Role:             schema.Assistant,
			Content:          "lo!",
			ReasoningContent: "done",
			ToolCalls: []schema.ToolCall{{
				ID:    "call_1",
				Index: &idx,
				Function: schema.FunctionCall{
					Arguments: `rld"}`,
				},
			}},
		},
	}))
	if err != nil {
		t.Fatalf("Merge err: %v", err)
	}
	if merged == nil {
		t.Fatal("expected merged message, got nil")
	}
	if merged.Role != schema.Assistant {
		t.Fatalf("unexpected merged role: %s", merged.Role)
	}
	if merged.Content != "hello!" {
		t.Fatalf("unexpected merged content: %s", merged.Content)
	}
	if merged.ReasoningContent != "think-done" {
		t.Fatalf("unexpected merged reasoning content: %s", merged.ReasoningContent)
	}
	if onChunkCount != 2 {
		t.Fatalf("expected onChunk to be called twice, got %d", onChunkCount)
	}
	if len(merged.ToolCalls) != 1 {
		t.Fatalf("expected 1 merged tool call, got %d", len(merged.ToolCalls))
	}
	if merged.ToolCalls[0].Function.Name != "echo" {
		t.Fatalf("unexpected tool name: %s", merged.ToolCalls[0].Function.Name)
	}
	if merged.ToolCalls[0].Function.Arguments != `{"value":"world"}` {
		t.Fatalf("unexpected merged tool arguments: %s", merged.ToolCalls[0].Function.Arguments)
	}
}

func TestStreamMessageMerger_MergeToolCallChunks_WhenOnlyFirstChunkCarriesID(t *testing.T) {
	ctx := context.Background()
	merger := NewStreamMessageMerger(nil)

	idx := 0
	merged, err := merger.Merge(ctx, schema.StreamReaderFromArray([]*schema.Message{
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:    "call_kzha3f9sbnnfssc85aol8lom",
				Index: &idx,
				Type:  "function",
				Function: schema.FunctionCall{
					Name: "ls",
				},
			}},
		},
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				Index: &idx,
				Function: schema.FunctionCall{
					Arguments: `{"path": "`,
				},
			}},
		},
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				Index: &idx,
				Function: schema.FunctionCall{
					Arguments: `.`,
				},
			}},
		},
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				Index: &idx,
				Function: schema.FunctionCall{
					Arguments: `"`,
				},
			}},
		},
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				Index: &idx,
				Function: schema.FunctionCall{
					Arguments: `}`,
				},
			}},
		},
	}))
	if err != nil {
		t.Fatalf("Merge err: %v", err)
	}
	if merged == nil {
		t.Fatal("expected merged message, got nil")
	}
	if len(merged.ToolCalls) != 1 {
		t.Fatalf("expected 1 merged tool call, got %d", len(merged.ToolCalls))
	}
	if merged.ToolCalls[0].ID != "call_kzha3f9sbnnfssc85aol8lom" {
		t.Fatalf("unexpected tool call id: %s", merged.ToolCalls[0].ID)
	}
	if merged.ToolCalls[0].Function.Name != "ls" {
		t.Fatalf("unexpected tool name: %s", merged.ToolCalls[0].Function.Name)
	}
	if merged.ToolCalls[0].Function.Arguments != `{"path": "."}` {
		t.Fatalf("unexpected merged tool arguments: %s", merged.ToolCalls[0].Function.Arguments)
	}
}

// TestStreamMessageMerger_RepairsIncompleteToolCallsAtEOF
// 主要验证:
// 1. 如果 stream 在 EOF 时还留有未闭合但可修复的 tool call 参数，StreamMessageMerger 会走 repair 路径。
// 2. repair 后的 tool call 会进入最终 message.ToolCalls，而不是被直接丢弃。
//
// 验证思路:
// - 构造一个只有单个 chunk 的 assistant stream，tool call 参数故意缺少结尾引号和右大括号；
// - Merge 结束后检查 tool call 仍然存在，且参数已经变成完整 JSON。
func TestStreamMessageMerger_RepairsIncompleteToolCallsAtEOF(t *testing.T) {
	ctx := context.Background()
	merger := NewStreamMessageMerger(nil)

	idx := 0
	merged, err := merger.Merge(ctx, schema.StreamReaderFromArray([]*schema.Message{{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:    "call_1",
			Index: &idx,
			Function: schema.FunctionCall{
				Name:      "echo",
				Arguments: `{"value":"repaired`,
			},
		}},
	}}))
	if err != nil {
		t.Fatalf("Merge err: %v", err)
	}
	if merged == nil {
		t.Fatal("expected merged message, got nil")
	}
	if len(merged.ToolCalls) != 1 {
		t.Fatalf("expected 1 repaired tool call, got %d", len(merged.ToolCalls))
	}

	args := merged.ToolCalls[0].Function.Arguments
	if !IsToolArgumentsComplete(args) {
		t.Fatalf("expected repaired arguments to be complete JSON, got %s", args)
	}
	if args == `{"value":"repaired` {
		t.Fatalf("expected arguments to be repaired, got %s", args)
	}
}
