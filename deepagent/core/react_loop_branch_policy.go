package deepagents

import graph_lib "eino-cli/deepagent/core/graph"

// ReactLoopBranchDecision 表示 DeepAgent react loop 在 model/tools 节点后的下一跳。
//
// 使用者通常不需要直接构造 graph；实现 ReactLoopBranchPolicy 后通过
// WithReactLoopBranchPolicy 注入即可。
type ReactLoopBranchDecision = graph_lib.ReactLoopBranchDecision

const (
	// ReactLoopBranchDefault 表示不覆盖 DeepAgent 默认路由。
	ReactLoopBranchDefault = graph_lib.ReactLoopBranchDefault

	// ReactLoopBranchToTools 表示 model 后进入 Tools；只能从 AfterModel 返回。
	ReactLoopBranchToTools = graph_lib.ReactLoopBranchToTools

	// ReactLoopBranchToExecutor 表示回到 Executor(model)。
	//
	// 从 AfterModel 返回时会经由 Continue 节点，并给下一次 Executor empty input。
	// 从 AfterTools 返回时等价于默认 Tools -> Executor。
	ReactLoopBranchToExecutor = graph_lib.ReactLoopBranchToExecutor

	// ReactLoopBranchToEnd 表示结束当前 react loop。
	ReactLoopBranchToEnd = graph_lib.ReactLoopBranchToEnd
)

// ReactLoopBranchPolicy 控制 DeepAgent react loop 的分支选择。
//
// 它适合表达“模型本来要结束，但同一个 turn 还有输入要继续处理”、
// “工具结果已经是最终结果，不要再回模型”这类 graph 路由问题。
//
// 它不应该承载 mailbox、message id、持久化或 agentworker 的概念。
type ReactLoopBranchPolicy = graph_lib.ReactLoopBranchPolicy

// ReactLoopAfterModelInput 是 model 节点完成后的分支决策输入。
type ReactLoopAfterModelInput = graph_lib.ReactLoopAfterModelInput

// ReactLoopAfterToolsInput 是 tools 节点完成后的分支决策输入。
type ReactLoopAfterToolsInput = graph_lib.ReactLoopAfterToolsInput
