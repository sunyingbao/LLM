package deepagents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"eino-cli/deepagent/core/constant"
	graph_lib "eino-cli/deepagent/core/graph"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/core/types"
	serialiser "eino-cli/deepagent/serialiser"

	"code.byted.org/gopkg/lang/sets"
	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/lang/gg/choose"
	"code.byted.org/lang/gg/gmap"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// einoGraphConfig 构建图的配置
type einoGraphConfig struct {
	chatModel            model.ToolCallingChatModel
	tools                []tool.BaseTool
	maxSteps             int
	checkpointStore      compose.CheckPointStore
	interruptBeforeNodes []string
	interruptAfterNodes  []string
	middlewareChain      *middleware.MiddlewareChain
	enableStreamToolCall bool
	// toolNodePreHandler/toolNodePostHandler 仅作用于非流式 tools 节点。
	toolNodePreHandler  ToolNodePreHandler
	toolNodePostHandler ToolNodePostHandler
	// reactLoopBranchPolicy 控制 model/tools 节点后的分支；为空时保持默认 React loop。
	reactLoopBranchPolicy graph_lib.ReactLoopBranchPolicy
}

func buildGraphWithConfig(
	ctx context.Context,
	cfg *einoGraphConfig,
) (compose.Runnable[[]*schema.Message, *schema.Message], error) {
	// 阶段 1：将工具描述绑定到模型，供模型生成合法的 tool call。
	modelWithTools, err := bindToolsToModel(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// 阶段 2：创建 Eino Graph，并为每次运行准备局部状态。
	graph := compose.NewGraph[[]*schema.Message, *schema.Message](
		compose.WithGenLocalState(func(ctx context.Context) *types.GraphLocalState {
			return &types.GraphLocalState{}
		}))

	// 阶段 3：添加模型节点、工具节点和可选的 React Loop 策略节点。
	err = addModelNode(graph, modelWithTools, cfg)
	if err != nil {
		return nil, err
	}

	err = addToolsNode(ctx, graph, cfg)
	if err != nil {
		return nil, err
	}

	err = addReactLoopPolicyNodes(graph, cfg)
	if err != nil {
		return nil, err
	}

	// 阶段 4：连接图的入口、模型分支、工具分支和结束节点。
	err = connectGraph(graph, cfg)
	if err != nil {
		return nil, err
	}

	// 阶段 5：应用运行步数、checkpoint 和中断配置，编译为 Runnable。
	return compileGraph(ctx, graph, cfg)
}

func bindToolsToModel(ctx context.Context, cfg *einoGraphConfig) (model.ToolCallingChatModel, error) {
	if len(cfg.tools) == 0 {
		return cfg.chatModel, nil
	}

	toolInfos := make([]*schema.ToolInfo, 0, len(cfg.tools))
	for _, t := range cfg.tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, err
		}
		toolInfos = append(toolInfos, info)
	}

	return cfg.chatModel.WithTools(toolInfos)
}

func addModelNode(
	graph *compose.Graph[[]*schema.Message, *schema.Message],
	modelWithTools model.ToolCallingChatModel,
	cfg *einoGraphConfig,
) error {
	return graph.AddChatModelNode(constant.NodeKeyModel, modelWithTools,
		compose.WithStatePreHandler(createModelPreHandler(cfg.middlewareChain)),
		choose.If(cfg.enableStreamToolCall,
			compose.WithStreamStatePostHandler(createModelStreamPostHandler(cfg.middlewareChain)),
			compose.WithStatePostHandler(createModelPostHandler(cfg.middlewareChain))),
		compose.WithNodeName(constant.NodeKeyModel),
	)
}

func addToolsNode(
	ctx context.Context,
	graph *compose.Graph[[]*schema.Message, *schema.Message],
	cfg *einoGraphConfig,
) error {
	if len(cfg.tools) == 0 {
		return nil
	}

	toolsConfig := compose.ToolsNodeConfig{
		Tools:               cfg.tools,
		UnknownToolsHandler: unknownToolHandler(toolNames(ctx, cfg.tools)),
		ExecuteSequentially: false,
		ToolCallMiddlewares: cfg.middlewareChain.ToolCallMiddlewares(),
	}
	if cfg.enableStreamToolCall {
		streamingToolsNode, err := graph_lib.CreateStreamingToolLambda(ctx, &toolsConfig)
		if err != nil {
			return err
		}
		return graph.AddLambdaNode(constant.NodeKeyTools, streamingToolsNode,
			compose.WithNodeName(constant.NodeKeyTools),
		)
	}

	toolsNode, err := compose.NewToolNode(ctx, &toolsConfig)
	if err != nil {
		return err
	}
	toolNodeOpts := []compose.GraphAddNodeOpt{
		compose.WithNodeName(constant.NodeKeyTools),
	}
	if cfg.toolNodePreHandler != nil {
		toolNodeOpts = append(toolNodeOpts, compose.WithStatePreHandler(adaptToolNodePreHandler(cfg.toolNodePreHandler)))
	}
	if cfg.toolNodePostHandler != nil {
		toolNodeOpts = append(toolNodeOpts, compose.WithStatePostHandler(adaptToolNodePostHandler(cfg.toolNodePostHandler)))
	}
	return graph.AddToolsNode(constant.NodeKeyTools, toolsNode, toolNodeOpts...)
}

func addReactLoopPolicyNodes(
	graph *compose.Graph[[]*schema.Message, *schema.Message],
	cfg *einoGraphConfig,
) error {
	if cfg.reactLoopBranchPolicy == nil {
		return nil
	}

	continueNode, err := compose.AnyLambda[*schema.Message, []*schema.Message, struct{}](
		func(context.Context, *schema.Message, ...struct{}) ([]*schema.Message, error) {
			return []*schema.Message{}, nil
		},
		nil,
		func(_ context.Context, input *schema.StreamReader[*schema.Message], _ ...struct{}) ([]*schema.Message, error) {
			if input != nil {
				defer input.Close()
				for {
					if _, err := input.Recv(); err != nil {
						break
					}
				}
			}
			return []*schema.Message{}, nil
		},
		nil,
	)
	if err != nil {
		return err
	}
	if err := graph.AddLambdaNode(constant.NodeKeyContinue, continueNode,
		compose.WithNodeName(constant.NodeKeyContinue),
	); err != nil {
		return err
	}

	if len(cfg.tools) == 0 {
		return nil
	}

	terminalToolMessage := func(messages []*schema.Message) *schema.Message {
		/*
			从 Tools 节点输出的多条消息中，选出最后一条非nil的tool 消息作为整个 Graph 的最终结果
		*/
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i] != nil {
				return graph_lib.CopyMessage(messages[i])
			}
		}
		return &schema.Message{Role: schema.Assistant}
	}

	toolResultTerminal, err := compose.AnyLambda[[]*schema.Message, *schema.Message, struct{}](
		func(_ context.Context, messages []*schema.Message, _ ...struct{}) (*schema.Message, error) {
			return terminalToolMessage(messages), nil
		},
		nil,
		func(_ context.Context, input *schema.StreamReader[[]*schema.Message], _ ...struct{}) (*schema.Message, error) {
			if input == nil {
				return terminalToolMessage(nil), nil
			}
			defer input.Close()

			var messages []*schema.Message
			for {
				chunk, recvErr := input.Recv()
				if errors.Is(recvErr, io.EOF) {
					return terminalToolMessage(messages), nil
				}
				if recvErr != nil {
					return nil, recvErr
				}
				for _, message := range chunk {
					if message != nil {
						messages = append(messages, graph_lib.CopyMessage(message))
					}
				}
			}
		},
		nil,
	)
	if err != nil {
		return err
	}
	return graph.AddLambdaNode(constant.NodeKeyToolResultTerminal, toolResultTerminal,
		compose.WithNodeName(constant.NodeKeyToolResultTerminal),
	)
}

func connectGraph(
	graph *compose.Graph[[]*schema.Message, *schema.Message],
	cfg *einoGraphConfig,
) error {
	if err := graph.AddEdge(compose.START, constant.NodeKeyModel); err != nil {
		return err
	}
	if err := connectPolicyNodeRoutes(graph, cfg); err != nil {
		return err
	}
	if err := connectModelRoute(graph, cfg); err != nil {
		return err
	}
	return connectToolsRoute(graph, cfg)
}

func connectPolicyNodeRoutes(
	graph *compose.Graph[[]*schema.Message, *schema.Message],
	cfg *einoGraphConfig,
) error {
	if cfg.reactLoopBranchPolicy == nil {
		return nil
	}
	if err := graph.AddEdge(constant.NodeKeyContinue, constant.NodeKeyModel); err != nil {
		return err
	}
	if len(cfg.tools) == 0 {
		return nil
	}
	return graph.AddEdge(constant.NodeKeyToolResultTerminal, compose.END)
}

func connectModelRoute(
	graph *compose.Graph[[]*schema.Message, *schema.Message],
	cfg *einoGraphConfig,
) error {
	hasTools := len(cfg.tools) > 0
	if !hasTools && cfg.reactLoopBranchPolicy == nil {
		return graph.AddEdge(constant.NodeKeyModel, compose.END)
	}

	endNodes := map[string]bool{compose.END: true}
	if hasTools {
		endNodes[constant.NodeKeyTools] = true
	}
	if cfg.reactLoopBranchPolicy != nil {
		endNodes[constant.NodeKeyContinue] = true
	}

	selectNextNode := func(ctx context.Context, message *schema.Message, hasToolCall bool) (string, error) {
		decision := graph_lib.ReactLoopBranchToEnd
		if hasTools && hasToolCall {
			decision = graph_lib.ReactLoopBranchToTools
		}
		if cfg.reactLoopBranchPolicy != nil {
			override, err := cfg.reactLoopBranchPolicy.AfterModel(ctx, graph_lib.ReactLoopAfterModelInput{
				Message:     graph_lib.CopyMessage(message),
				Default:     decision,
				HasToolCall: hasToolCall,
			})
			if err != nil {
				return "", err
			}
			if override != graph_lib.ReactLoopBranchDefault {
				decision = override
			}
		}

		switch decision {
		case graph_lib.ReactLoopBranchToTools:
			if !hasTools {
				return "", errors.New("react loop branch decision ToTools requires tools node")
			}
			return constant.NodeKeyTools, nil
		case graph_lib.ReactLoopBranchToExecutor:
			return constant.NodeKeyContinue, nil
		case graph_lib.ReactLoopBranchToEnd:
			return compose.END, nil
		default:
			return "", errors.New("unknown react loop branch decision")
		}
	}

	if !cfg.enableStreamToolCall {
		return graph.AddBranch(constant.NodeKeyModel, compose.NewGraphBranch(
			func(ctx context.Context, message *schema.Message) (string, error) {
				hasToolCall := message != nil && len(message.ToolCalls) > 0
				return selectNextNode(ctx, message, hasToolCall)
			},
			endNodes,
		))
	}

	return graph.AddBranch(constant.NodeKeyModel, compose.NewStreamGraphBranch(
		func(ctx context.Context, input *schema.StreamReader[*schema.Message]) (string, error) {
			hasToolCall, err := graph_lib.StreamHasToolCall(ctx, input)
			if err != nil {
				return "", err
			}
			return selectNextNode(ctx, nil, hasToolCall)
		},
		endNodes,
	))
}

func connectToolsRoute(
	graph *compose.Graph[[]*schema.Message, *schema.Message],
	cfg *einoGraphConfig,
) error {
	if len(cfg.tools) == 0 {
		return nil
	}
	if cfg.reactLoopBranchPolicy == nil {
		return graph.AddEdge(constant.NodeKeyTools, constant.NodeKeyModel)
	}
	return graph.AddBranch(constant.NodeKeyTools, graph_lib.CreateReactLoopToolsBranch(
		cfg.enableStreamToolCall,
		cfg.reactLoopBranchPolicy,
	))
}

func compileGraph(
	ctx context.Context,
	graph *compose.Graph[[]*schema.Message, *schema.Message],
	cfg *einoGraphConfig,
) (compose.Runnable[[]*schema.Message, *schema.Message], error) {
	compileOpts := []compose.GraphCompileOption{
		compose.WithGraphName(constant.GraphName),
		compose.WithNodeTriggerMode(compose.AnyPredecessor),
		compose.WithMaxRunSteps(cfg.maxSteps),
	}

	if cfg.checkpointStore != nil {
		compileOpts = append(compileOpts, compose.WithCheckPointStore(cfg.checkpointStore))
	}

	if len(cfg.interruptBeforeNodes) > 0 {
		compileOpts = append(compileOpts, compose.WithInterruptBeforeNodes(cfg.interruptBeforeNodes))
	}
	if len(cfg.interruptAfterNodes) > 0 {
		compileOpts = append(compileOpts, compose.WithInterruptAfterNodes(cfg.interruptAfterNodes))
	}

	return graph.Compile(ctx, compileOpts...)
}

func toolNames(ctx context.Context, tools []tool.BaseTool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil || info == nil || info.Name == "" {
			continue
		}
		names = append(names, info.Name)
	}
	return names
}

func unknownToolHandler(available []string) func(ctx context.Context, name, input string) (string, error) {
	return func(ctx context.Context, name, input string) (string, error) {
		if len(available) == 0 {
			return fmt.Sprintf("Tool %q is not available in this runtime. Continue without this tool or answer with the available context.", name), nil
		}
		return fmt.Sprintf("Tool %q is not available in this runtime. Use one of the available tools instead: %s.", name, strings.Join(available, ", ")), nil
	}
}

// createModelPreHandler creates the PreHandler for the Executor node.
//
// Flow:
//  1. Accumulate input messages into graph state
//  2. chain.ModifyModelRequest — pipeline: each middleware transforms messages
//     (including BasePromptMiddleware which prepends the system prompt)
func createModelPreHandler(
	chain *middleware.MiddlewareChain,
) compose.StatePreHandler[[]*schema.Message, *types.GraphLocalState] {
	return func(ctx context.Context, messages []*schema.Message, _ *types.GraphLocalState) ([]*schema.Message, error) {
		if chain == nil {
			return messages, nil
		}

		prompts, err := chain.BuildPrompts(ctx)
		if err != nil {
			return nil, fmt.Errorf("middleware BuildPrompts failed: %w", err)
		}

		state := types.StateFromContext(ctx)
		messages, err = chain.ModifyModelRequest(ctx, prompts, messages, state)
		if err != nil {
			return nil, fmt.Errorf("middleware ModifyModelRequest failed: %w", err)
		}

		return messages, nil
	}
}

func createModelPostHandler(chain *middleware.MiddlewareChain) compose.StatePostHandler[*schema.Message, *types.GraphLocalState] {
	return func(ctx context.Context, message *schema.Message, _ *types.GraphLocalState) (*schema.Message, error) {
		if chain == nil {
			return message, nil
		}

		state := types.StateFromContext(ctx)
		message, err := chain.ModifyModelResponse(ctx, message, state)
		if err != nil {
			return nil, fmt.Errorf("middleware ModifyModelResponse failed: %w", err)
		}
		return message, nil
	}
}

func createModelStreamPostHandler(chain *middleware.MiddlewareChain) compose.StreamStatePostHandler[*schema.Message, *types.GraphLocalState] {
	return func(ctx context.Context, out *schema.StreamReader[*schema.Message], _ *types.GraphLocalState) (*schema.StreamReader[*schema.Message], error) {
		if chain == nil {
			return out, nil
		}

		state := types.StateFromContext(ctx)
		out, err := chain.ModifyModelStreamResponse(ctx, out, state)
		if err != nil {
			return nil, fmt.Errorf("middleware ModifyModelStreamResponse failed: %w", err)
		}
		return out, nil
	}
}

func adaptToolNodePreHandler(handler ToolNodePreHandler) compose.StatePreHandler[*schema.Message, *types.GraphLocalState] {
	return func(ctx context.Context, input *schema.Message, _ *types.GraphLocalState) (*schema.Message, error) {
		return handler(ctx, input)
	}
}

func adaptToolNodePostHandler(handler ToolNodePostHandler) compose.StatePostHandler[[]*schema.Message, *types.GraphLocalState] {
	return func(ctx context.Context, output []*schema.Message, _ *types.GraphLocalState) ([]*schema.Message, error) {
		return handler(ctx, output)
	}
}

func buildAgentState(ctx context.Context, chain *middleware.MiddlewareChain, customGraphState map[string]types.RunTimeStateful, store compose.CheckPointStore) (*types.GraphState, error) {
	agentState := types.NewGraphState(store)
	if chain != nil {
		// 为每个 middleware 构建状态处理器
		handlers := chain.BuildStateHandlers()

		if len(handlers) != 0 && store == nil {
			needCheckpointStoreMiddlewares := strings.Join(gmap.Keys(handlers), ",")

			logs.CtxError(ctx, "[DeepAgent::buildAgentState] check point store is required by %s", needCheckpointStoreMiddlewares)
			return nil, fmt.Errorf("check point store is required by %s", needCheckpointStoreMiddlewares)
		}

		for k, h := range handlers {
			agentState.RegisterStateful(k, h)
		}
	}
	// 注册自定义状态ful
	for k, v := range customGraphState {
		agentState.RegisterStateful(k, v)
	}
	return agentState, nil
}

func collectAllTools(ctx context.Context, chain *middleware.MiddlewareChain, cfg *Config) ([]tool.BaseTool, error) {
	// 从中间件收集工具
	middlewareTools, err := chain.Tools(ctx)
	if err != nil {
		return nil, err
	}
	userInjectTools := cfg.Tools
	allTools := make([]tool.BaseTool, 0, 32)
	allTools = append(allTools, middlewareTools...)
	allTools = append(allTools, userInjectTools...)

	hitl := cfg.HITLConfig
	if hitl != nil {
		if hitl.NeedFollowUpTool {
			allTools = append(allTools, tools.GetFollowUpTool())
		}
	}

	allTools = filterToolsByMask(ctx, allTools, cfg.ToolMask)

	if hitl != nil {
		apTools := sets.NewStringSetFromSlice(gmap.Keys(hitl.NeedApproveTools))
		policyTools := sets.NewStringSetFromSlice(gmap.Keys(hitl.ToolPolicyGates))
		reviewEditTools := sets.NewStringSetFromSlice(gmap.Keys(hitl.NeedReviewAndEditTools))

		for i, t := range allTools {
			tInfo, err := t.Info(ctx)
			if err != nil {
				logs.CtxError(ctx, "[DeepAgent::collectAllTools] get tool info failed,  err: %v", err)
				continue
			}

			if apTools.Contains(tInfo.Name) && policyTools.Contains(tInfo.Name) {
				return nil, fmt.Errorf("tool %q cannot be configured in both NeedApproveTools and ToolPolicyGates", tInfo.Name)
			}

			if policyTools.Contains(tInfo.Name) {
				if invokeTool, ok := t.(tool.InvokableTool); ok {
					gate := hitl.ToolPolicyGates[tInfo.Name]
					if gate.Policy == nil {
						return nil, fmt.Errorf("tool %q policy gate requires Policy", tInfo.Name)
					}
					if gate.DenyFormatter == nil {
						return nil, fmt.Errorf("tool %q policy gate requires DenyFormatter", tInfo.Name)
					}
					allTools[i] = tools.NewInvokablePolicyTool(invokeTool, gate)
				}

				continue
			}

			if apTools.Contains(tInfo.Name) {
				if invokeTool, ok := t.(tool.InvokableTool); ok {
					allTools[i] = tools.NewInvokableApprovableTool(invokeTool, hitl.NeedApproveTools[tInfo.Name])
				}

				continue
			}

			if reviewEditTools.Contains(tInfo.Name) {
				if invokeTool, ok := t.(tool.InvokableTool); ok {
					allTools[i] = tools.NewInvokableReviewEditTool(invokeTool, hitl.NeedReviewAndEditTools[tInfo.Name])
				}
				continue
			}
		}
	}

	// 包装所有工具。JSON 修复能力通过 RepairJSONMiddleware 显式接入，
	// 不在 SDK 默认链路里改变工具入参语义。
	allTools = tools.WrapToolsWithConfig(allTools, &tools.WrapToolsConfig{
		InfoRewriter: cfg.ToolInfoRewriter,
	})
	// 后置调用一下打印下日志
	var infos []*schema.ToolInfo
	for _, t := range allTools {
		info, _ := t.Info(ctx)
		infos = append(infos, info)
	}
	logs.CtxInfo(ctx, "[DeepAgent::collectAllTools] allToolsInfo: %v", serialiser.ToString(infos))
	return allTools, nil
}

func filterToolsByMask(ctx context.Context, toolList []tool.BaseTool, mask tools.Mask) []tool.BaseTool {
	if mask == nil || len(toolList) == 0 {
		return toolList
	}

	filtered := make([]tool.BaseTool, 0, len(toolList))
	for _, t := range toolList {
		info, err := t.Info(ctx)
		if err != nil {
			logs.CtxError(ctx, "[DeepAgent::filterToolsByMask] get tool info failed, keep tool by default, err: %v", err)
			filtered = append(filtered, t)
			continue
		}
		if info == nil {
			logs.CtxWarn(ctx, "[DeepAgent::filterToolsByMask] tool info is nil, keep tool by default")
			filtered = append(filtered, t)
			continue
		}
		if mask(ctx, info) {
			filtered = append(filtered, t)
			continue
		}
		logs.CtxInfo(ctx, "[DeepAgent::filterToolsByMask] masked tool: %s", info.Name)
	}

	return filtered
}
