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

	// NodeKeyContinue returns an empty input to the model for another pass.
	NodeKeyContinue = "continue"
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
