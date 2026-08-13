# deepagents

DeepAgent 核心库，提供基于中间件架构的智能代理实现。

## 导入

```go
import "eino-cli/deepagent/core"
```

## 快速开始

体验型默认装配入口：

```go
agent, err := deepagents.New(ctx,
    deepagents.WithModel(model),
    deepagents.WithFilesystem(),
    deepagents.WithPlanning(),
)
defer agent.Close(ctx)

resp, err := agent.Run(ctx, messages)
```

核心构造入口：

```go
agent, err := deepagents.NewFromSpec(ctx, &deepagents.DeepAgentSpec{
    Model: model,
    Middlewares: []middleware.Middleware{
        contextmanager.New(),
    },
    MaxSteps:       100,
    CheckpointStore: store,
})
defer agent.Close(ctx)
```

## API

### New

创建一个默认装配的 DeepAgent 实例。

```go
func New(ctx context.Context, opts ...Option) (*DeepAgent, error)
```

### NewFromSpec

创建一个核心 DeepAgent 实例。

```go
func NewFromSpec(ctx context.Context, spec *DeepAgentSpec) (*DeepAgent, error)
```

### New 配置选项

| Option | 描述 |
|--------|------|
| `WithModel(m)` | 设置聊天模型（必需） |
| `WithMaxSteps(n)` | 设置最大执行步数（默认 100） |
| `WithWorkDir(dir)` | 设置工作目录 |
| `WithTools(tools...)` | 添加自定义工具 |
| `WithToolMask(mask)` | 过滤最终工具集合 |
| `WithBackend(b)` | 设置文件系统后端 |
| `WithSandboxBackend(b)` | 设置沙箱后端（文件能力 + 命令执行） |
| `WithFilesystem()` | 启用文件系统访问 |
| `WithFilesystemConfig(cfg)` | 使用自定义配置启用文件系统访问 |
| `WithPlanning()` | 启用任务规划 |
| `WithPlanMiddleware(cfg)` | 启用轻量进度计划中间件 |
| `WithMemory(paths...)` | 设置记忆文件路径 |
| `WithSkillsDir(dir)` | 设置技能目录 |
| `WithSkillsDirs(dirs...)` | 设置多个技能目录 |
| `WithSkillLoader(loader)` | 使用自定义技能加载器 |
| `WithSkillMask(mask)` | 设置内置 FileSystemSkillLoader 的技能过滤函数 |
| `WithSubAgents(agents...)` | 设置子代理 |
| `WithSubAgentsDir(dir)` | 从目录加载子代理配置 |
| `WithSubAgentsDirs(dirs...)` | 从多个目录加载子代理配置 |
| `WithSubAgentTaskStreaming()` | 使 task 工具流式输出子 agent 最终回复 |
| `WithReactLoopBranchPolicy(policy)` | 自定义 ReAct loop 分支策略 |
| `WithMiddleware(m)` | 添加自定义中间件 |
| `WithPatchToolCalls()` | 启用工具调用修补 |
| `WithStreamToolCall()` | 启用流式工具调用执行 |
| `WithWeb()` | 启用 Web 工具 |
| `WithWebConfig(cfg)` | 使用自定义配置启用 Web 工具 |
| `WithDefaultCallbacks(handlers...)` | 注入默认 Eino callbacks |
| `WithCheckpointStore(store)` | 设置 Eino checkpoint 存储 |
| `WithInterruptBeforeNodes(nodes...)` | 在指定节点执行前中断 |
| `WithInterruptAfterNodes(nodes...)` | 在指定节点执行后中断 |
| `WithHITLConfig(cfg)` | 设置 HITL 工具配置 |
| `WithContextManager(manager)` | 设置上下文管理中间件 |
| `WithToolInfoRewriter(rewriter)` | 重写工具信息 |
| `WithToolNodePreHandler(handler)` | 设置非流式 tools 节点 pre handler |
| `WithToolNodePostHandler(handler)` | 设置非流式 tools 节点 post handler |
| `WithAllFeatures()` | 启用所有功能 |

### DeepAgentSpec

`DeepAgentSpec` 是核心构造模型，适合框架层或高级调用方显式传入已装配的依赖：

- `Model`
- `Middlewares`
- `Tools`
- `Backend`
- `MaxSteps`
- `CheckpointStore`
- `InterruptBeforeNodes`
- `InterruptAfterNodes`
- `CustomGraphState`
- `EnableStreamToolCall`
- `EnableSubAgentTaskStreaming`
- `Callbacks`
- `Depth`
- `HITLConfig`
- `ToolInfoRewriter`
- `ToolNodePreHandler`
- `ToolNodePostHandler`
- `ReactLoopBranchPolicy`

其中：

- `ToolNodePreHandler func(ctx context.Context, input *schema.Message) (*schema.Message, error)`
- `ToolNodePostHandler func(ctx context.Context, output []*schema.Message) ([]*schema.Message, error)`

### DeepAgent 方法

#### Run

同步执行 Agent。

```go
func (a *DeepAgent) Run(ctx context.Context, messages []*schema.Message, opts ...RunOptionFunc) (*schema.Message, error)
```

#### Stream

流式执行 Agent。

```go
func (a *DeepAgent) Stream(ctx context.Context, messages []*schema.Message, opts ...RunOptionFunc) (*schema.StreamReader[*schema.Message], error)
```

#### GraphState

获取当前图运行状态。

```go
func (a *DeepAgent) GraphState() *types.GraphState
```

#### Backend

获取后端实例。

```go
func (a *DeepAgent) Backend() backends.Backend
```

#### Close

关闭 Agent，执行清理。

```go
func (a *DeepAgent) Close(ctx context.Context) error
```

## 子包

### backends

文件存储后端接口和实现。

```go
import "eino-cli/deepagent/core/backends"
```

- `Backend` - 文件系统后端接口
- `SandboxBackend` - 文件能力 + 结构化命令执行能力
- `FilesystemBackend` - 文件系统后端
- `SandboxFilesystemBackend` - 支持命令执行的文件系统后端

### middleware

中间件接口和实现。

```go
import "eino-cli/deepagent/core/middleware"
```

- `Middleware` - 中间件接口
- `BaseMiddleware` - 基础中间件实现
- `FilesystemMiddleware` - 文件操作中间件
- `PlanningMiddleware` - 任务规划中间件
- `MemoryMiddleware` - 记忆中间件
- `SkillsMiddleware` - 技能系统中间件
- `SubAgentMiddleware` - 子代理中间件
- `PatchToolCallsMiddleware` - 工具调用修补中间件

### tools

工具包装器。

```go
import "eino-cli/deepagent/core/tools"
```

- `WrapTool` - 工具包装器，支持预处理和后处理

## 示例

可参考仓库根目录的 [文档索引](../docs/README.md) 和 `cmd/deepagent`、`cmd/cloud_agent`、`cmd/cloud_agent` 下的参考实现。
