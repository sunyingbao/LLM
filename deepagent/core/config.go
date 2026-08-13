package deepagents

import (
	"time"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/plan"
	"eino-cli/deepagent/core/middleware/skill"
	"eino-cli/deepagent/core/middleware/subagent"
	"eino-cli/deepagent/core/middleware/web"
	"eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/core/types"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

type HITLConfig struct {
	// NeedApproveTools is the legacy bool approval hook. true means interrupt
	// for approval; false means run the tool.
	NeedApproveTools map[string]tools.NeedApproval

	// ToolPolicyGates is the three-state tool gate for allow / approval / deny.
	// A tool must not be configured here and in NeedApproveTools at the same time.
	ToolPolicyGates map[string]tools.ToolPolicyGate

	NeedReviewAndEditTools map[string]tools.NeedReviewAndEdit
	NeedFollowUpTool       bool
}

type ContextManagerConfig struct {
	// 上下文管理器, 必传, 如果只是想体验，可以用 SimpleContextManager
	// 需要注意的是，不建议每轮对话都构建新的上下文管理器，
	// 因为这样会丢失之前的对话历史，导致上下文不连续
	Manager middleware.Middleware
}

type FilesystemConfig struct {
	WorkDir               string
	ReadOnly              bool
	DisableUploadDownload bool
	DisableExecute        bool
	DisableApplyPatch     bool
	CommandTimeout        time.Duration
}

// 它描述的是默认装配意图，而不是 DeepAgent 的核心构造模型。
type Config struct {
	// Model 聊天模型（必需）
	Model model.ToolCallingChatModel

	// MaxSteps 最大执行步数
	MaxSteps int

	// MaxModelCalls limits ChatModel invocations in one logical DeepAgent turn.
	// Leave it <= 0 to disable this budget. Unlike MaxSteps, this counts model
	// calls and is persisted in GraphState across HITL resume when checkpointing
	// is enabled.
	MaxModelCalls int

	// WorkDir 工作目录
	WorkDir string

	// Tools 用户自定义工具
	Tools []tool.BaseTool

	// ToolMask 控制最终对 model 可见且可由服务端执行的工具集合。
	// 返回 true 表示保留该工具，返回 false 表示将其从最终工具集合中过滤掉。
	ToolMask tools.Mask

	// SubAgents 子代理配置
	SubAgents []*subagent.SubAgent

	// SubAgentsDirs 子代理配置目录（从 SUBAGENT.yaml 加载）
	SubAgentsDirs []string

	// SubAgentContextInjector 用于给子 agent 注入上下文
	SubAgentContextInjector subagent.SubAgentContextInjector

	// EnableSubAgentTaskStreaming 使 SubAgentMiddleware 的 task 工具流式输出子 agent 最终回复。
	EnableSubAgentTaskStreaming bool

	// MemoryPaths 记忆文件路径
	MemoryPaths []string

	// deprecated, 使用这种方式启用 skill middleware 会导致你无法获得 skill list 的句柄,当你想实现 list_skills 这样的功能时就无法做到
	// SkillsDirs 技能目录路径列表
	SkillsDirs []string
	// SkillLoader 技能加载器, 推荐使用这个来启用 skill middleware
	// 需要注意的是,你可以选择 skill 目录下已经内置的实现，比如 NewFileSystemSkillLoader；该实现可按构造参数决定是否启用一次性缓存
	SkillLoader skill.Loader
	// SkillMask 仅作用于框架内部创建的 FileSystemSkillLoader
	// 当 SkillLoader 由调用方自行传入时，过滤逻辑由调用方自己负责
	// 返回 true 表示该 skill 对外可见，返回 false 表示对外隐藏
	SkillMask skill.Mask

	// EnablePlanning 是否启用规划能力
	EnablePlanning bool

	// EnableFilesystem 是否启用文件系统访问
	EnableFilesystem bool

	// FilesystemConfig 文件系统能力配置
	FilesystemConfig *FilesystemConfig

	// EnablePatchToolCalls 是否启用工具调用修补
	EnablePatchToolCalls bool

	// EnableStreamToolCall 是否启用流式工具调用执行
	EnableStreamToolCall bool

	// EnableWeb 是否启用 Web 工具
	EnableWeb bool

	// DisableSubAgent 是否禁用子代理中间件
	// 默认情况下，当 backend != nil 或有 SubAgents 配置时自动启用
	// 设置为 true 可强制禁用子代理能力
	DisableSubAgent bool

	// DisableUploadDownload 禁用文件系统的 upload_files 和 download_files 工具
	DisableUploadDownload bool

	// DisableExecute 禁用文件系统的 execute 工具
	// 即使 Backend 实现了 SandboxBackend 接口，也不暴露 execute 工具
	DisableExecute bool

	// WebConfig Web 工具配置
	WebConfig *web.WebConfig

	HITLConfig *HITLConfig

	// ContextManagerCfg 上下文管理器配置
	ContextManagerCfg *ContextManagerConfig

	// Backend 文件系统后端
	// 如果传入的是 SandboxBackend，则自动启用 execute 工具
	Backend backends.Backend

	// Middlewares 自定义中间件
	Middlewares []middleware.Middleware

	// Callbacks 注入到 Eino compose 运行时的 callback handlers。
	Callbacks []callbacks.Handler

	// CheckpointStore eino 原生的 checkpoint 存储
	// 用于实现对话恢复、状态回滚等功能
	CheckpointStore compose.CheckPointStore

	// InterruptBeforeNodes 在这些节点执行前中断
	// 用于人工审批等场景
	InterruptBeforeNodes []string

	// InterruptAfterNodes 在这些节点执行后中断
	InterruptAfterNodes []string

	// Stateful
	CustomGraphState map[string]types.RunTimeStateful

	// SubAgentSharedCustomStateNames 指定哪些 CustomGraphState 可被子 agent 共享。
	// 共享状态由父 agent checkpoint 持久化，子 agent 仅运行时读写，不保存到自身 checkpoint。
	SubAgentSharedCustomStateNames []string

	Depth int

	// ToolInfoRewriter 用于重写工具的 ToolInfo（name/desc）
	// 如果设置，所有工具的 Info 将经过此函数处理
	ToolInfoRewriter tools.ToolInfoRewriter

	// ToolNodePreHandler 在非流式 tools 节点执行前处理完整 assistant message。
	// 当前仅在 EnableStreamToolCall=false 时生效。
	ToolNodePreHandler ToolNodePreHandler

	// ToolNodePostHandler 在非流式 tools 节点执行后处理完整 tool message 数组。
	// 当前仅在 EnableStreamToolCall=false 时生效。
	ToolNodePostHandler ToolNodePostHandler

	// ReactLoopBranchPolicy 控制 DeepAgent react loop 在 model/tools 节点后的分支。
	// 为空时保持默认行为：model 有服务端工具调用则进入 tools，否则结束；tools 后回到 model。
	ReactLoopBranchPolicy ReactLoopBranchPolicy

	workDirExplicit               bool
	disableUploadDownloadExplicit bool
	disableExecuteExplicit        bool
}

// Option 是 New builder 的配置选项函数。
type Option func(*Config)

func WithCustomGraphState(fields map[string]types.RunTimeStateful) Option {
	return func(c *Config) {
		c.CustomGraphState = fields
	}
}

// WithSubAgentSharedCustomState 指定子 agent 可共享的 CustomGraphState key。
// 共享状态仍由父 agent checkpoint 持久化；子 agent 不会把这些 state 写入自身 checkpoint。
func WithSubAgentSharedCustomState(names ...string) Option {
	return func(c *Config) {
		c.SubAgentSharedCustomStateNames = append(c.SubAgentSharedCustomStateNames, names...)
	}
}

// WithModel 设置模型
func WithModel(m model.ToolCallingChatModel) Option {
	return func(c *Config) {
		c.Model = m
	}
}

// WithMaxSteps 设置最大执行步数
func WithMaxSteps(steps int) Option {
	return func(c *Config) {
		c.MaxSteps = steps
	}
}

// WithMaxModelCalls 设置单个逻辑 turn 内最多允许的模型调用次数。
//
// 该限制不同于 WithMaxSteps：MaxSteps 是 Eino graph run step 保护；
// MaxModelCalls 只在每次 ChatModel 调用前扣减，并会随 GraphState 保存，
// 因此在 HITL resume 后继续使用剩余额度。
func WithMaxModelCalls(calls int) Option {
	return func(c *Config) {
		c.MaxModelCalls = calls
	}
}

// WithWorkDir 设置工作目录
func WithWorkDir(dir string) Option {
	return func(c *Config) {
		c.WorkDir = dir
		c.workDirExplicit = true
	}
}

// WithTools 设置用户自定义工具
func WithTools(tools ...tool.BaseTool) Option {
	return func(c *Config) {
		c.Tools = append(c.Tools, tools...)
	}
}

// WithToolMask 设置最终工具集合过滤器。
// 返回 true 表示保留该工具，返回 false 表示将其从最终工具集合中过滤掉。
func WithToolMask(mask tools.Mask) Option {
	return func(c *Config) {
		c.ToolMask = mask
	}
}

// WithReactLoopBranchPolicy 设置 DeepAgent react loop 分支策略。
//
// nil 策略保持现有行为不变：model 有服务端工具调用就进入 Tools，
// model 无服务端工具调用就 END，Tools 执行后回到 model。
//
// 只有当调用方确实需要改变 react loop 本身的路由时才应该设置该选项，
// 例如同一个 turn 内插入新输入后继续跑 model，或工具结果可直接作为最终结果。
func WithReactLoopBranchPolicy(policy ReactLoopBranchPolicy) Option {
	return func(c *Config) {
		c.ReactLoopBranchPolicy = policy
	}
}

// WithSubAgents 设置子代理
func WithSubAgents(agents ...*subagent.SubAgent) Option {
	return func(c *Config) {
		c.SubAgents = append(c.SubAgents, agents...)
	}
}

// WithSubAgentsDir 从目录加载子代理配置
// 目录结构应为: dir/agent-name/SUBAGENT.yaml
func WithSubAgentsDir(dir string) Option {
	return WithSubAgentsDirs(dir)
}

// WithSubAgentsDirs 设置多个子代理配置目录
func WithSubAgentsDirs(dirs ...string) Option {
	return func(c *Config) {
		c.SubAgentsDirs = append(c.SubAgentsDirs, dirs...)
	}
}

func WithSubAgentContextInjector(i subagent.SubAgentContextInjector) Option {
	return func(c *Config) {
		c.SubAgentContextInjector = i
	}
}

// WithSubAgentTaskStreaming 使 task 工具实现 StreamableTool。
// 当前仅流式输出子 agent 最终 assistant response，不转发子 agent 内部事件。
func WithSubAgentTaskStreaming() Option {
	return func(c *Config) {
		c.EnableSubAgentTaskStreaming = true
	}
}

// WithMemory 设置记忆文件路径
func WithMemory(paths ...string) Option {
	return func(c *Config) {
		c.MemoryPaths = append(c.MemoryPaths, paths...)
	}
}

// deprecated , 使用这种方式启用 skill middleware 会导致你无法获得 skill list 的句柄,当你想实现 list_skills 这样的功能时就无法做到.同样 , 当你不想每次都加载的时候，你也无法做到
func WithSkillsDir(dir string) Option {
	return WithSkillsDirs(dir)
}

// deprecated , 使用这种方式启用 skill middleware 会导致你无法获得 skill list 的句柄,当你想实现 list_skills 这样的功能时就无法做到.同样 , 当你不想每次都加载的时候，你也无法做到
func WithSkillsDirs(dirs ...string) Option {
	return func(c *Config) {
		c.SkillsDirs = append(c.SkillsDirs, dirs...)
	}
}

// WithSkillLoader 设置技能加载器
func WithSkillLoader(loader skill.Loader) Option {
	return func(c *Config) {
		c.SkillLoader = loader
	}
}

// WithSkillMask 设置内置 FileSystemSkillLoader 的技能可见性过滤函数。
// 返回 true 表示保留该 skill，返回 false 表示将其从对外暴露的技能列表中过滤掉。
func WithSkillMask(mask skill.Mask) Option {
	return func(c *Config) {
		c.SkillMask = mask
	}
}

// WithPlanning 启用规划能力
func WithPlanning() Option {
	return func(c *Config) {
		c.EnablePlanning = true
	}
}

// WithPlanMiddleware enables the lightweight update_plan progress checklist.
func WithPlanMiddleware(cfg *plan.PlanMiddlewareConfig) Option {
	return func(c *Config) {
		c.Middlewares = append(c.Middlewares, plan.New(cfg))
	}
}

// WithFilesystem 启用文件系统访问
func WithFilesystem() Option {
	return func(c *Config) {
		c.EnableFilesystem = true
	}
}

// WithFilesystemConfig 使用自定义配置启用文件系统访问。
func WithFilesystemConfig(cfg *FilesystemConfig) Option {
	return func(c *Config) {
		c.EnableFilesystem = true
		if cfg == nil {
			c.FilesystemConfig = &FilesystemConfig{}
			return
		}
		cloned := *cfg
		c.FilesystemConfig = &cloned
	}
}

// WithDisableSubAgent 禁用子代理中间件
func WithDisableSubAgent() Option {
	return func(c *Config) {
		c.DisableSubAgent = true
	}
}

// WithDisableUploadDownload 禁用 upload_files 和 download_files 工具
func WithDisableUploadDownload() Option {
	return func(c *Config) {
		c.DisableUploadDownload = true
		c.disableUploadDownloadExplicit = true
	}
}

// WithDisableExecute 禁用 execute 工具
// 即使 Backend 实现了 SandboxBackend 接口，也不暴露 execute 工具
func WithDisableExecute() Option {
	return func(c *Config) {
		c.DisableExecute = true
		c.disableExecuteExplicit = true
	}
}

// WithBackend 设置文件系统后端
func WithBackend(b backends.Backend) Option {
	return func(c *Config) {
		c.Backend = b
	}
}

// WithSandboxBackend 设置沙箱后端（支持命令执行）
// SandboxBackend 实现了 Backend 接口，所以会同时启用文件操作和命令执行
func WithSandboxBackend(b backends.SandboxBackend) Option {
	return WithBackend(b)
}

// WithPatchToolCalls 启用工具调用修补
func WithPatchToolCalls() Option {
	return func(c *Config) {
		c.EnablePatchToolCalls = true
	}
}

// WithStreamToolCall 启用流式工具调用执行
func WithStreamToolCall() Option {
	return func(c *Config) {
		c.EnableStreamToolCall = true
	}
}

// WithWeb 启用 Web 工具（web_search, http_request, fetch_url）
func WithWeb() Option {
	return func(c *Config) {
		c.EnableWeb = true
	}
}

// WithWebConfig 使用自定义配置启用 Web 工具
func WithWebConfig(config *web.WebConfig) Option {
	return func(c *Config) {
		c.EnableWeb = true
		c.WebConfig = config
	}
}

// WithMiddleware 添加自定义中间件
func WithMiddleware(m middleware.Middleware) Option {
	return func(c *Config) {
		c.Middlewares = append(c.Middlewares, m)
	}
}

// WithDefaultCallbacks 注入每次 Run/Stream 默认携带的 Eino compose callback handlers。
func WithDefaultCallbacks(handlers ...callbacks.Handler) Option {
	return func(c *Config) {
		for _, handler := range handlers {
			if handler != nil {
				c.Callbacks = append(c.Callbacks, handler)
			}
		}
	}
}

// WithCheckpointStore 设置 checkpoint 存储（eino 原生接口）
// 用于实现对话恢复、状态回滚等功能
func WithCheckpointStore(store compose.CheckPointStore) Option {
	return func(c *Config) {
		c.CheckpointStore = store
	}
}

// WithInterruptBeforeNodes 设置在指定节点执行前中断
// 用于人工审批等场景
func WithInterruptBeforeNodes(nodes ...string) Option {
	return func(c *Config) {
		c.InterruptBeforeNodes = append(c.InterruptBeforeNodes, nodes...)
	}
}

// WithInterruptAfterNodes 设置在指定节点执行后中断
func WithInterruptAfterNodes(nodes ...string) Option {
	return func(c *Config) {
		c.InterruptAfterNodes = append(c.InterruptAfterNodes, nodes...)
	}
}

// WithAllFeatures 启用所有功能
func WithAllFeatures() Option {
	return func(c *Config) {
		c.EnablePlanning = true
		c.EnableFilesystem = true
		c.EnablePatchToolCalls = true
		c.EnableWeb = true
	}
}

func WithHITLConfig(cfg *HITLConfig) Option {
	return func(c *Config) {
		c.HITLConfig = cfg
	}
}

func WithContextManager(manager middleware.Middleware) Option {
	return func(c *Config) {
		c.ContextManagerCfg = &ContextManagerConfig{
			Manager: manager,
		}
	}
}

// WithToolInfoRewriter 设置工具信息重写器
// rewriter 接收原始 ToolInfo，返回修改后的 ToolInfo
func WithToolInfoRewriter(rewriter tools.ToolInfoRewriter) Option {
	return func(c *Config) {
		c.ToolInfoRewriter = rewriter
	}
}

// WithToolNodePreHandler 设置非流式 tools 节点的 pre handler。
func WithToolNodePreHandler(handler ToolNodePreHandler) Option {
	return func(c *Config) {
		c.ToolNodePreHandler = handler
	}
}

// WithToolNodePostHandler 设置非流式 tools 节点的 post handler。
func WithToolNodePostHandler(handler ToolNodePostHandler) Option {
	return func(c *Config) {
		c.ToolNodePostHandler = handler
	}
}
