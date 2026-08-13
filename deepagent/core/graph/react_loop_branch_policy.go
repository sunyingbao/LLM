package graph

import (
	"context"
	"errors"

	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// ReactLoopBranchDecision 表示 DeepAgent react loop 在一个节点执行完成后的下一跳。
//
// DeepAgent 的默认执行图大致是：
//
//	Executor(model) -> Tools -> Executor(model) -> ... -> END
//
// ReactLoopBranchDecision 只表达这个图里的下一跳，不表达业务输入、不表达
// mailbox message id，也不表达一次用户 query 的生命周期。
type ReactLoopBranchDecision string

const (
	// ReactLoopBranchDefault 表示不覆盖 DeepAgent 默认路由。
	//
	// 默认路由是：
	//   - AfterModel: 有工具调用则去 Tools，否则 END。
	//   - AfterTools: 回到 Executor，让模型读取 tool result 后继续推理。
	ReactLoopBranchDefault ReactLoopBranchDecision = ""

	// ReactLoopBranchToTools 表示下一跳进入 Tools 节点。
	//
	// 只允许在 AfterModel 返回。它要求当前 graph 里确实存在服务端 Tools 节点。
	// 在 AfterTools 返回该值是非法的，因为工具结果不能直接再次进入 Tools。
	ReactLoopBranchToTools ReactLoopBranchDecision = "tools"

	// ReactLoopBranchToExecutor 表示下一跳回到 Executor(model) 节点。
	//
	// 在 AfterModel 返回该值时，实际 graph 形态是 Executor -> Continue -> Executor，
	// 而不是 Executor -> Executor。Continue 节点会丢弃上一次 model output，
	// 给下一次 Executor 提供 empty []*schema.Message，避免旧 assistant response
	// 被当作新输入重复写入 history。
	//
	// 在 AfterTools 返回该值时，等价于旧行为：Tools -> Executor。
	ReactLoopBranchToExecutor ReactLoopBranchDecision = "executor"

	// ReactLoopBranchToEnd 表示当前 react loop 结束。
	//
	// AfterModel 返回该值表示模型没有必要继续工具调用或下一轮模型请求。
	// AfterTools 返回该值表示工具结果已经是最终结果，不再让模型基于 tool result
	// 生成一次总结或后续动作。
	ReactLoopBranchToEnd ReactLoopBranchDecision = "end"
)

// ReactLoopBranchPolicy 是 DeepAgent graph 的分支策略扩展点。
//
// 它只负责回答两个问题：
//  1. model 节点完成后，下一跳去 Tools、Executor 还是 END？
//  2. tools 节点完成后，下一跳回 Executor 还是 END？
//
// 它不应该理解 agentthread、pending input、持久化、mailbox 或外部分布式调度。
// 这些上层能力可以实现这个接口，但不应该把自己的概念反向塞进 DeepAgent graph。
//
// policy 返回 ReactLoopBranchDefault 时，DeepAgent 保持原始行为。返回 error 时，
// 当前 graph run 失败并向调用方返回该错误。
type ReactLoopBranchPolicy interface {
	// AfterModel 在 Executor(model) 节点产出 assistant message 后调用。
	//
	// 常见用途：
	//   - 默认会 END，但上层发现同一个 turn 还有待消费输入，于是返回 ToExecutor。
	//   - 默认会去 Tools，但上层出于策略原因拒绝工具执行，于是返回 ToEnd。
	//
	// 合法返回值：
	//   - Default: 使用 input.Default。
	//   - ToTools: 进入 Tools 节点；要求 graph 存在 Tools 节点。
	//   - ToExecutor: 通过 Continue 节点回到 Executor，下一次 Executor 输入为空。
	//   - ToEnd: 结束当前 react loop。
	AfterModel(ctx context.Context, input ReactLoopAfterModelInput) (ReactLoopBranchDecision, error)

	// AfterTools 在 Tools 节点产出 tool result messages 后调用。
	//
	// 常见用途：
	//   - 工具结果只是中间观察，返回 Default 或 ToExecutor，让模型继续推理。
	//   - 工具结果已经是最终响应，返回 ToEnd，避免模型再生成一次总结。
	//
	// 合法返回值：
	//   - Default: 使用 input.Default，也就是 ToExecutor。
	//   - ToExecutor: 回到 Executor。
	//   - ToEnd: 结束当前 react loop。
	//
	// ToTools 在这里非法。
	AfterTools(ctx context.Context, input ReactLoopAfterToolsInput) (ReactLoopBranchDecision, error)
}

// ReactLoopAfterModelInput 是 AfterModel 的决策输入。
type ReactLoopAfterModelInput struct {
	// Message 是模型刚产出的 assistant message。
	//
	// 非流式模式下该字段是 message 的拷贝，policy 可以读取但不应该修改它。
	// 流式模式下分支判断当前只消费 stream 来判断是否有 tool call，
	// 因此这里暂时为空；需要完整 message 的策略应优先在非流式模式使用。
	Message *schema.Message

	// Default 是 DeepAgent 在没有 policy 覆盖时会选择的默认下一跳。
	//
	// 当前规则：
	//   - graph 有 Tools 节点，且模型产出了 tool call: ToTools。
	//   - 其他情况: ToEnd。
	Default ReactLoopBranchDecision

	// HasToolCall 表示模型响应里是否有 tool call。
	HasToolCall bool
}

// ReactLoopAfterToolsInput 是 AfterTools 的决策输入。
type ReactLoopAfterToolsInput struct {
	// Messages 是 Tools 节点刚产出的 tool result messages。
	//
	// policy 可以用它判断工具是否已经给出最终结果。传入的是拷贝，避免 policy
	// 修改 graph 内部继续流转的消息。
	Messages []*schema.Message

	// Default 是 DeepAgent 在没有 policy 覆盖时会选择的默认下一跳。
	//
	// 当前固定为 ToExecutor，也就是工具执行完后回模型继续推理。
	Default ReactLoopBranchDecision
}

func CreateReactLoopToolsBranch(enableStream bool, policy ReactLoopBranchPolicy) *compose.GraphBranch {
	return CreateReactLoopToolsBranchToModel(enableStream, policy, constant.NodeKeyModel)
}

func CreateReactLoopToolsBranchToModel(enableStream bool, policy ReactLoopBranchPolicy, modelNodeName string) *compose.GraphBranch {
	if modelNodeName == "" {
		modelNodeName = constant.NodeKeyModel
	}
	endNodes := map[string]bool{
		modelNodeName:                      true,
		constant.NodeKeyToolResultTerminal: true,
	}
	if !enableStream {
		return compose.NewGraphBranch(func(ctx context.Context, msgs []*schema.Message) (string, error) {
			return toolsDecisionNode(ctx, policy, cloneMessages(msgs), modelNodeName)
		}, endNodes)
	}
	return compose.NewStreamGraphBranch(func(ctx context.Context, in *schema.StreamReader[[]*schema.Message]) (string, error) {
		msgs, err := collectMessageArrays(in)
		if err != nil {
			return "", err
		}
		return toolsDecisionNode(ctx, policy, msgs, modelNodeName)
	}, endNodes)
}

func applyDecision(defaultDecision ReactLoopBranchDecision, override ReactLoopBranchDecision) ReactLoopBranchDecision {
	if override == ReactLoopBranchDefault {
		return defaultDecision
	}
	return override
}

func toolsDecisionNode(ctx context.Context, policy ReactLoopBranchPolicy, msgs []*schema.Message, modelNodeName string) (string, error) {
	decision := ReactLoopBranchToExecutor
	if policy != nil {
		override, err := policy.AfterTools(ctx, ReactLoopAfterToolsInput{
			Messages: cloneMessages(msgs),
			Default:  decision,
		})
		if err != nil {
			return "", err
		}
		decision = applyDecision(decision, override)
	}
	switch decision {
	case ReactLoopBranchToExecutor:
		if modelNodeName == "" {
			modelNodeName = constant.NodeKeyModel
		}
		return modelNodeName, nil
	case ReactLoopBranchToEnd:
		return constant.NodeKeyToolResultTerminal, nil
	case ReactLoopBranchToTools:
		return "", errors.New("react loop branch decision ToTools is invalid after tools")
	default:
		return "", errors.New("unknown react loop branch decision")
	}
}
