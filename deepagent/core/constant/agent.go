package constant

// ==================== Agent 核心常量 ====================

// Graph 相关
const (
	// GraphName 图名称
	GraphName = "deep_agent"

	// ComponentName 组件名称（用于日志）
	ComponentName = "DeepAgent"
)

// 节点键
const (
	// NodeKeyModel 执行器节点键
	NodeKeyModel = "model"

	// NodeKeyTools 工具节点键
	NodeKeyTools = "tools"

	// NodeKeyContinue 继续执行节点键，用于无工具调用但需要继续回到 Executor 的场景。
	NodeKeyContinue = "continue"

	// NodeKeyToolResultTerminal 工具结果终止节点键，用于 tools 直接结束当前 react loop 的场景。
	NodeKeyToolResultTerminal = "tool_result_terminal"
)

// 默认配置
const (
	// DefaultMaxSteps 默认最大执行步数
	DefaultMaxSteps = 100

	// DefaultSubAgentMaxSteps 子代理默认最大执行步数
	DefaultSubAgentMaxSteps = 10
)

// 环境变量
const (
	// EnvMaxSteps 最大步数环境变量名
	EnvMaxSteps = "DEEPAGENT_MAX_STEPS"
)
