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

type testReactLoopPolicy struct {
	afterModel func(context.Context, ReactLoopAfterModelInput) (ReactLoopBranchDecision, error)
	afterTools func(context.Context, ReactLoopAfterToolsInput) (ReactLoopBranchDecision, error)
}

func newContinueNodeForTest() (*compose.Lambda, error) {
	type noOption struct{}
	return compose.AnyLambda[*schema.Message, []*schema.Message, noOption](
		func(context.Context, *schema.Message, ...noOption) ([]*schema.Message, error) {
			return []*schema.Message{}, nil
		},
		nil,
		func(_ context.Context, in *schema.StreamReader[*schema.Message], _ ...noOption) ([]*schema.Message, error) {
			if in != nil {
				defer in.Close()
				for {
					if _, err := in.Recv(); err != nil {
						break
					}
				}
			}
			return []*schema.Message{}, nil
		},
		nil,
	)
}

func newToolResultTerminalNodeForTest() *compose.Lambda {
	return compose.InvokableLambda(func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i] != nil {
				return CopyMessage(messages[i]), nil
			}
		}
		return &schema.Message{Role: schema.Assistant}, nil
	})
}

func newModelBranchForTest(enableStream bool, policy ReactLoopBranchPolicy) *compose.GraphBranch {
	endNodes := map[string]bool{
		constant.NodeKeyContinue: true,
		compose.END:              true,
	}
	selectNextNode := func(ctx context.Context, message *schema.Message, hasToolCall bool) (string, error) {
		decision := ReactLoopBranchToEnd
		override, err := policy.AfterModel(ctx, ReactLoopAfterModelInput{
			Message:     CopyMessage(message),
			Default:     decision,
			HasToolCall: hasToolCall,
		})
		if err != nil {
			return "", err
		}
		if override != ReactLoopBranchDefault {
			decision = override
		}
		if decision == ReactLoopBranchToExecutor {
			return constant.NodeKeyContinue, nil
		}
		if decision == ReactLoopBranchToEnd {
			return compose.END, nil
		}
		return "", errors.New("unexpected model branch decision in test")
	}

	if !enableStream {
		return compose.NewGraphBranch(func(ctx context.Context, message *schema.Message) (string, error) {
			hasToolCall := message != nil && len(message.ToolCalls) > 0
			return selectNextNode(ctx, message, hasToolCall)
		}, endNodes)
	}
	return compose.NewStreamGraphBranch(func(ctx context.Context, input *schema.StreamReader[*schema.Message]) (string, error) {
		hasToolCall, err := StreamHasToolCall(ctx, input)
		if err != nil {
			return "", err
		}
		return selectNextNode(ctx, nil, hasToolCall)
	}, endNodes)
}

func (p testReactLoopPolicy) AfterModel(ctx context.Context, input ReactLoopAfterModelInput) (ReactLoopBranchDecision, error) {
	if p.afterModel == nil {
		return ReactLoopBranchDefault, nil
	}
	return p.afterModel(ctx, input)
}

func (p testReactLoopPolicy) AfterTools(ctx context.Context, input ReactLoopAfterToolsInput) (ReactLoopBranchDecision, error) {
	if p.afterTools == nil {
		return ReactLoopBranchDefault, nil
	}
	return p.afterTools(ctx, input)
}

func TestReactLoopModelBranchPolicyCanContinueWithEmptyExecutorInput(t *testing.T) {
	ctx := context.Background()
	modelBranchCalls := 0
	executorInputLens := make([]int, 0, 2)

	policy := testReactLoopPolicy{
		afterModel: func(ctx context.Context, input ReactLoopAfterModelInput) (ReactLoopBranchDecision, error) {
			modelBranchCalls++
			if input.Default != ReactLoopBranchToEnd {
				t.Fatalf("expected default end decision, got %q", input.Default)
			}
			if modelBranchCalls == 1 {
				return ReactLoopBranchToExecutor, nil
			}
			return ReactLoopBranchDefault, nil
		},
	}

	continueNode, err := newContinueNodeForTest()
	if err != nil {
		t.Fatalf("newContinueNodeForTest err: %v", err)
	}

	g := compose.NewGraph[[]*schema.Message, *schema.Message]()
	err = g.AddLambdaNode(constant.NodeKeyModel, compose.InvokableLambda(func(ctx context.Context, in []*schema.Message) (*schema.Message, error) {
		executorInputLens = append(executorInputLens, len(in))
		return &schema.Message{Role: schema.Assistant, Content: "executor"}, nil
	}))
	if err != nil {
		t.Fatalf("Add executor err: %v", err)
	}
	err = g.AddLambdaNode(constant.NodeKeyContinue, continueNode)
	if err != nil {
		t.Fatalf("Add continue err: %v", err)
	}
	if err = g.AddEdge(compose.START, constant.NodeKeyModel); err != nil {
		t.Fatalf("Add start edge err: %v", err)
	}
	if err = g.AddBranch(constant.NodeKeyModel, newModelBranchForTest(false, policy)); err != nil {
		t.Fatalf("Add executor branch err: %v", err)
	}
	if err = g.AddEdge(constant.NodeKeyContinue, constant.NodeKeyModel); err != nil {
		t.Fatalf("Add continue edge err: %v", err)
	}

	runner, err := g.Compile(ctx, compose.WithMaxRunSteps(8))
	if err != nil {
		t.Fatalf("Compile err: %v", err)
	}
	if _, err = runner.Invoke(ctx, []*schema.Message{{Role: schema.User, Content: "first input"}}); err != nil {
		t.Fatalf("Invoke err: %v", err)
	}

	if modelBranchCalls != 2 {
		t.Fatalf("expected model branch to run twice, got %d", modelBranchCalls)
	}
	if len(executorInputLens) != 2 {
		t.Fatalf("expected executor to run twice, got lens %v", executorInputLens)
	}
	if executorInputLens[0] != 1 {
		t.Fatalf("expected first executor input length 1, got %d", executorInputLens[0])
	}
	if executorInputLens[1] != 0 {
		t.Fatalf("continue node should feed empty input into executor, got %d", executorInputLens[1])
	}
}

func TestReactLoopModelBranchPolicyCanContinueWithEmptyExecutorInputInStreamMode(t *testing.T) {
	ctx := context.Background()
	modelBranchCalls := 0
	executorInputLens := make([]int, 0, 2)

	policy := testReactLoopPolicy{
		afterModel: func(ctx context.Context, input ReactLoopAfterModelInput) (ReactLoopBranchDecision, error) {
			modelBranchCalls++
			if modelBranchCalls == 1 {
				return ReactLoopBranchToExecutor, nil
			}
			return ReactLoopBranchDefault, nil
		},
	}

	continueNode, err := newContinueNodeForTest()
	if err != nil {
		t.Fatalf("newContinueNodeForTest err: %v", err)
	}

	g := compose.NewGraph[[]*schema.Message, *schema.Message]()
	err = g.AddLambdaNode(constant.NodeKeyModel, compose.StreamableLambda(func(ctx context.Context, in []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
		executorInputLens = append(executorInputLens, len(in))
		return schema.StreamReaderFromArray([]*schema.Message{{
			Role:    schema.Assistant,
			Content: "executor",
		}}), nil
	}))
	if err != nil {
		t.Fatalf("Add executor err: %v", err)
	}
	err = g.AddLambdaNode(constant.NodeKeyContinue, continueNode)
	if err != nil {
		t.Fatalf("Add continue err: %v", err)
	}
	if err = g.AddEdge(compose.START, constant.NodeKeyModel); err != nil {
		t.Fatalf("Add start edge err: %v", err)
	}
	if err = g.AddBranch(constant.NodeKeyModel, newModelBranchForTest(true, policy)); err != nil {
		t.Fatalf("Add executor branch err: %v", err)
	}
	if err = g.AddEdge(constant.NodeKeyContinue, constant.NodeKeyModel); err != nil {
		t.Fatalf("Add continue edge err: %v", err)
	}

	runner, err := g.Compile(ctx, compose.WithMaxRunSteps(8))
	if err != nil {
		t.Fatalf("Compile err: %v", err)
	}
	out, err := runner.Stream(ctx, []*schema.Message{{Role: schema.User, Content: "first input"}})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	defer out.Close()
	for {
		_, err = out.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("Recv err: %v", err)
			}
			break
		}
	}

	if modelBranchCalls != 2 {
		t.Fatalf("expected model branch to run twice, got %d", modelBranchCalls)
	}
	if len(executorInputLens) != 2 {
		t.Fatalf("expected executor to run twice, got lens %v", executorInputLens)
	}
	if executorInputLens[0] != 1 {
		t.Fatalf("expected first executor input length 1, got %d", executorInputLens[0])
	}
	if executorInputLens[1] != 0 {
		t.Fatalf("continue node should feed empty input into executor, got %d", executorInputLens[1])
	}
}

func TestReactLoopToolsBranchPolicyCanEndWithoutReturningToExecutor(t *testing.T) {
	ctx := context.Background()
	executorCalls := 0
	afterToolsCalls := 0

	policy := testReactLoopPolicy{
		afterTools: func(ctx context.Context, input ReactLoopAfterToolsInput) (ReactLoopBranchDecision, error) {
			afterToolsCalls++
			if input.Default != ReactLoopBranchToExecutor {
				t.Fatalf("expected default executor decision, got %q", input.Default)
			}
			if len(input.Messages) != 1 || input.Messages[0].Content != "tool-result" {
				t.Fatalf("unexpected tool branch input: %+v", input.Messages)
			}
			return ReactLoopBranchToEnd, nil
		},
	}

	terminalNode := newToolResultTerminalNodeForTest()

	g := compose.NewGraph[[]*schema.Message, *schema.Message]()
	if err := g.AddPassthroughNode(constant.NodeKeyTools); err != nil {
		t.Fatalf("Add tools err: %v", err)
	}
	if err := g.AddLambdaNode(constant.NodeKeyModel, compose.InvokableLambda(func(ctx context.Context, in []*schema.Message) (*schema.Message, error) {
		executorCalls++
		return &schema.Message{Role: schema.Assistant, Content: "executor"}, nil
	})); err != nil {
		t.Fatalf("Add executor err: %v", err)
	}
	if err := g.AddLambdaNode(constant.NodeKeyToolResultTerminal, terminalNode); err != nil {
		t.Fatalf("Add terminal err: %v", err)
	}
	if err := g.AddEdge(compose.START, constant.NodeKeyTools); err != nil {
		t.Fatalf("Add start edge err: %v", err)
	}
	if err := g.AddBranch(constant.NodeKeyTools, CreateReactLoopToolsBranch(false, policy)); err != nil {
		t.Fatalf("Add tools branch err: %v", err)
	}
	if err := g.AddEdge(constant.NodeKeyToolResultTerminal, compose.END); err != nil {
		t.Fatalf("Add terminal edge err: %v", err)
	}

	runner, err := g.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile err: %v", err)
	}
	out, err := runner.Invoke(ctx, []*schema.Message{{Role: schema.Tool, Content: "tool-result"}})
	if err != nil {
		t.Fatalf("Invoke err: %v", err)
	}
	if afterToolsCalls != 1 {
		t.Fatalf("expected AfterTools to run once, got %d", afterToolsCalls)
	}
	if executorCalls != 0 {
		t.Fatalf("executor should not run after tools policy chooses end, got %d calls", executorCalls)
	}
	if out == nil || out.Role != schema.Tool || out.Content != "tool-result" {
		t.Fatalf("unexpected terminal output: %+v", out)
	}
}

func TestReactLoopToolsBranchCanReturnToCustomModelNode(t *testing.T) {
	ctx := context.Background()
	const modelNodeName = "custom_model"
	modelCalls := 0

	g := compose.NewGraph[[]*schema.Message, *schema.Message]()
	if err := g.AddPassthroughNode(constant.NodeKeyTools); err != nil {
		t.Fatalf("Add tools err: %v", err)
	}
	terminalNode := newToolResultTerminalNodeForTest()
	if err := g.AddLambdaNode(constant.NodeKeyToolResultTerminal, terminalNode); err != nil {
		t.Fatalf("Add terminal err: %v", err)
	}
	if err := g.AddLambdaNode(modelNodeName, compose.InvokableLambda(func(ctx context.Context, in []*schema.Message) (*schema.Message, error) {
		modelCalls++
		if len(in) != 1 || in[0].Content != "tool-result" {
			t.Fatalf("unexpected model input: %+v", in)
		}
		return &schema.Message{Role: schema.Assistant, Content: "model-done"}, nil
	})); err != nil {
		t.Fatalf("Add model err: %v", err)
	}
	if err := g.AddEdge(compose.START, constant.NodeKeyTools); err != nil {
		t.Fatalf("Add start edge err: %v", err)
	}
	if err := g.AddBranch(constant.NodeKeyTools, CreateReactLoopToolsBranchToModel(false, nil, modelNodeName)); err != nil {
		t.Fatalf("Add tools branch err: %v", err)
	}
	if err := g.AddEdge(constant.NodeKeyToolResultTerminal, compose.END); err != nil {
		t.Fatalf("Add terminal edge err: %v", err)
	}
	if err := g.AddEdge(modelNodeName, compose.END); err != nil {
		t.Fatalf("Add model edge err: %v", err)
	}

	runner, err := g.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile err: %v", err)
	}
	out, err := runner.Invoke(ctx, []*schema.Message{{Role: schema.Tool, Content: "tool-result"}})
	if err != nil {
		t.Fatalf("Invoke err: %v", err)
	}
	if modelCalls != 1 {
		t.Fatalf("expected custom model node to run once, got %d", modelCalls)
	}
	if out == nil || out.Content != "model-done" {
		t.Fatalf("unexpected output: %+v", out)
	}
}
