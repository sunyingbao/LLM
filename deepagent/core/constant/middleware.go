package constant

// ==================== 中间件名称 ====================

const (
	// MiddlewareFilesystem 文件系统中间件
	MiddlewareFilesystem = "filesystem"

	// MiddlewarePlanning 规划中间件
	MiddlewarePlanning = "planning"

	// MiddlewarePlan 轻量进度计划中间件
	MiddlewarePlan = "plan"

	// MiddlewareMemory 记忆中间件
	MiddlewareMemory = "memory"

	// MiddlewareSkills 技能中间件
	MiddlewareSkills = "skills"

	// MiddlewareSubAgent 子代理中间件
	MiddlewareSubAgent = "subagent"

	// MiddlewareHITL 人工审批中间件
	MiddlewareHITL = "human_in_the_loop"

	// MiddlewarePatchToolCalls 工具调用修补中间件
	MiddlewarePatchToolCalls = "patch_tool_calls"

	// MiddlewareWeb Web 工具中间件
	MiddlewareWeb = "web"
)

// ==================== Planning 配置 ====================

const (
	// DefaultTodoPriority 默认任务优先级
	DefaultTodoPriority = 3
)

// ==================== Memory 配置 ====================

const (
	// DefaultMemoryFilename 默认记忆文件名
	DefaultMemoryFilename = "AGENTS.md"
)

// ==================== Skills 配置 ====================

const (
	// SkillMetadataFile 技能元数据文件名
	SkillMetadataFile = "SKILL.md"

	// MaxSkillNameLength 技能名称最大长度 (Agent Skills 规范)
	MaxSkillNameLength = 64

	// MaxSkillDescriptionLength 技能描述最大长度 (Agent Skills 规范)
	MaxSkillDescriptionLength = 1024

	// MaxSkillFileSize 技能文件最大大小 (10MB, 防止 DoS)
	MaxSkillFileSize = 10 * 1024 * 1024
)

// ==================== SubAgent 配置 ====================

const (
	// ExplorerName 探索型子代理名称
	ExplorerName = "explorer"

	// ExecutorName 执行型子代理名称
	ExecutorName = "executor"

	// DefaultExplorerDescription Explorer 子代理描述
	DefaultExplorerDescription = "Read-only research agent for exploring codebases, searching files, reading content, and analyzing information. Use this agent when you need to investigate, search, or understand something without modifying any files."

	// DefaultExecutorDescription Executor 子代理描述
	DefaultExecutorDescription = "Full-capability execution agent that can read, write, edit files and execute commands. Use this agent for tasks that require making changes, creating files, or running operations. This agent has the same capabilities as the main agent."

	// DefaultExplorerPrompt Explorer 子代理系统提示词
	DefaultExplorerPrompt = "You are a research and analysis agent. You can ONLY read and search files — you cannot create, edit, or delete any files. Focus on providing thorough analysis and clear findings."

	// DefaultExecutorPrompt Executor 子代理系统提示词
	DefaultExecutorPrompt = "You are an execution agent with full file system capabilities. Complete the given task efficiently and report back with the results."

	// DefaultExplorerMaxSteps Explorer 默认最大步数（调研型，步数较少）
	DefaultExplorerMaxSteps = 30

	// DefaultExecutorMaxSteps Executor 默认最大步数（执行型，步数较多）
	DefaultExecutorMaxSteps = 60

	// DefaultDispatchConcurrency dispatch_tasks 默认最大并行子任务数
	// 避免同时启动过多 SubAgent 导致 TCP 端口耗尽
	DefaultDispatchConcurrency = 3
)
