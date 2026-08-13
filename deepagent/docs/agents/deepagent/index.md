# DeepAgent 接入指南

<callout emoji="⚙️" background-color="light-blue">

DeepAgent 是 SDK 内置的一次 Agent 执行体。它负责模型请求、工具调用、工具结果回写和 ReAct loop；middleware 是它扩展能力的核心机制。
</callout>

## 它解决什么

当业务想做一个“能调用模型和工具的 Agent 执行单元”时，可以从 DeepAgent 开始。

DeepAgent 负责一次执行里的事情：

- 把输入 messages 发给模型。
- 让模型选择工具。
- 执行服务端工具。
- 把工具结果写回模型上下文。
- 继续推理，直到得到最终输出。
- 在需要时通过 checkpoint / resume 恢复中断执行。

DeepAgent 不负责：

- 多轮对话生命周期。
- 长期上下文管理。
- 服务端任务认领和调度。
- session、权限、渲染、事件存储。

如果你开始手写多轮上下文、pending input、事件流，应该上移到 [Agent Thread 接入指南](../agentthread/index.md)。

## 它负责什么，不负责什么

DeepAgent 负责一次执行里的模型和工具循环：

- 把输入 messages 交给模型。
- 让模型选择并执行工具。
- 执行工具，并把工具结果写回模型上下文。
- 通过 middleware 注入 prompt、工具、memory、skill、HITL 和运行状态。
- 在需要时通过 checkpoint / resume 恢复本次执行。

DeepAgent 不负责：

- 多轮上下文生命周期。
- pending input、事件持久化和断线回放。
- 服务端 scan、claim、ack、release。
- 产品 session、权限、UI、资产模型和业务协议。

## 运行模型

```plaintext
messages
  -> DeepAgent
      -> middleware
      -> model
      -> tool calls
      -> tools / backend
      -> model
  -> final message / stream
```

几个概念只需要这样理解：

- `messages`：本次执行的输入，由业务传入。
- `model`：支持 tool calling 的聊天模型。
- `tools`：模型可以调用的动作。
- `backend / sandbox`：Agent 可以操纵的一台“电脑”，文件、命令、工作目录都在这里发生。
- `middleware`：在执行链路中注入能力，例如文件系统、规划、记忆、skill、HITL。

## 最小接入路径

### 1. 创建 DeepAgent

```go
sandboxBackend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
    RootDir:     workDir,
    VirtualMode: true,
})
// 这里用本地文件系统模拟一台 Agent 可以操纵的电脑。
// 生产环境也可以替换成 Docker、AI Infra、ByteSandbox 或业务自己的 SandboxBackend。

agent, err := deepagents.New(ctx,
    deepagents.WithModel(chatModel),
    deepagents.WithSandboxBackend(sandboxBackend),
    deepagents.WithFilesystem(),
    deepagents.WithPlanMiddleware(nil),
)
if err != nil {
    return err
}
defer agent.Close(ctx)
```

这里有两个点容易误解：

- `WithFilesystem` 只是启用文件系统工具。
- 真正的文件读写和命令执行能力来自 `Backend` / `SandboxBackend`，也就是 Agent 可以操作的那台“电脑”。

生产环境不要直接照搬本地 sandbox 权限，应该替换成业务自己的隔离执行环境。

### 2. Run

```go
msg, err := agent.Run(ctx, []*schema.Message{
    schema.UserMessage("读取 input.txt 并总结内容"),
})
```

`Run` 返回最终 assistant message。

它适合一次性任务，或者业务自己已经有外层历史和事件处理。

### 3. Stream

```go
stream, err := agent.Stream(ctx, messages)
if err != nil {
    return err
}
defer stream.Close()
```

`Stream` 适合前端展示模型输出过程。

DeepAgent 的 stream 是本次执行的输出流，不等于服务端事件日志。如果你需要断线回放和事件持久化，需要上层系统自己处理，或者接入 Agent Thread / Agent Worker。

## Middleware 是什么

Middleware 是 DeepAgent 的核心扩展机制。

如果只会传 tools，你只能做一个能调用工具的模型。

理解 middleware，才算真正会用 DeepAgent。

Middleware 不是简单的“工具列表”，而是可以参与 Agent 执行链路：

- 修改 system prompt。
- 注入工具。
- 修改模型请求。
- 处理模型响应。
- 构建初始上下文。
- 触发 HITL。
- 包裹工具调用。
- 更新计划状态。
- 保存和恢复运行状态。

很多看起来是 DeepAgent 的能力，本质都是通过 middleware 或相关配置组合出来的。

例如：

- 文件系统工具。
- planning。
- memory。
- skill。
- subagent。
- HITL。

业务接入 DeepAgent 时，真正要设计的是：这次执行需要哪些 middleware、哪些工具、哪些 backend，以及这些能力的安全边界。

### Middleware 接口

自定义 middleware 通常 embed `BaseMiddleware`，只 override 自己需要的点。

```go
type Middleware interface {
    Name() string
    Tools(ctx context.Context) ([]tool.BaseTool, error)
    BeforeAgent(ctx context.Context) error
    BuildInitialContext(ctx context.Context) ([]*schema.Message, error)
    ModifyModelRequest(ctx context.Context, initialContext []*schema.Message, messages []*schema.Message, state *types.GraphState) ([]*schema.Message, error)
    ModifyModelResponse(ctx context.Context, modelResp *schema.Message, state *types.GraphState) (*schema.Message, error)
    ModifyModelStreamResponse(ctx context.Context, modelResp *schema.StreamReader[*schema.Message], state *types.GraphState) (*schema.StreamReader[*schema.Message], error)
    WrapToolCall() compose.ToolMiddleware
    BuildStateHandler() types.RunTimeStateful
}
```

不要被接口吓到。大多数 middleware 只需要实现其中一两个方法。

### 每个方法解决什么

| 方法 | 什么时候用 |
| --- | --- |
| `Tools` | 给模型增加可调用工具 |
| `BeforeAgent` | 每次 `Run` / `Stream` 前做准备 |
| `BuildInitialContext` | 注入 system prompt、memory、固定上下文 |
| `ModifyModelRequest` | 模型请求前改 messages，常用于上下文管理 |
| `ModifyModelResponse` | 模型响应后处理完整 assistant message |
| `ModifyModelStreamResponse` | 模型流式响应后合并、观察或改写输出 |
| `WrapToolCall` | 包裹工具执行，做审批、日志、参数修正、权限检查 |
| `BuildStateHandler` | 让 middleware 自己的状态参与 checkpoint / resume |

### 怎么写一个 middleware

一个最小 middleware 可以只提供工具：

```go
type MyTools struct {
    middleware.BaseMiddleware
}

func (m *MyTools) Name() string { return "my_tools" }

func (m *MyTools) Tools(ctx context.Context) ([]tool.BaseTool, error) {
    return []tool.BaseTool{myTool}, nil
}
```

需要注入 system prompt 时，实现 `BuildInitialContext`：

```go
func (m *MyPrompt) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
    return []*schema.Message{
        schema.SystemMessage("你是一个严格遵循业务规则的 Agent。"),
    }, nil
}
```

需要拦截工具调用时，实现 `WrapToolCall`。

这类 middleware 适合做：

- HITL。
- 工具参数修正。
- 工具调用日志。
- 权限检查。
- 工具结果截断。

### Middleware 的设计边界

Middleware 运行在一次 DeepAgent 执行里。

它适合影响这次执行的 prompt、tools、model request、tool call 和状态恢复。

它不适合承载：

- 服务端任务调度。
- 用户连接状态。
- 分布式 message id。
- Agent Worker lease / ack / release。
- 产品 UI 状态。

这些应该放到 Agent Thread、Agent Worker 或业务服务里。

## 工具和 Backend

工具是模型可调用的动作。

Backend / Sandbox 是 Agent 可以操纵的一台“电脑”。

工具通过它访问文件、执行命令、读写工作目录。

例如文件系统能力：

```plaintext
模型调用 read_file
  -> 文件系统工具
      -> backend.Read(...)
```

如果 backend 实现了 `SandboxBackend`，DeepAgent 还可以暴露命令执行能力。

业务需要自己控制：

- 哪些工具对模型可见。
- 哪些工具允许执行。
- 哪些工具需要 HITL。
- 工具参数如何校验。
- 工具结果是否要截断或脱敏。

SDK 提供组合机制，不替业务决定工具安全策略。

## 常用能力

| 目标 | 入口 | 说明 |
| --- | --- | --- |
| 文件系统 | `WithFilesystem` / `WithSandboxBackend` | 文件读写、搜索；sandbox backend 可支持命令执行 |
| 自定义工具 | `WithTools` | 注入业务工具 |
| 工具过滤 | `WithToolMask` | 控制最终对模型可见且可执行的工具 |
| Planning | `WithPlanMiddleware` | 任务计划和进度 |
| Memory | `WithMemory` | 注入固定记忆文件 |
| Skill | `WithSkillLoader` | 加载技能 |
| SubAgent | `WithSubAgents` / `WithSubAgentsDir` | 单次执行内的子 Agent 能力 |
| Web | `WithWeb` | Web 搜索、HTTP、URL 抓取 |
| HITL | `WithHITLConfig` | 工具审批、追问、review/edit |
| Checkpoint | `WithCheckpointStore` | 中断恢复 |

这些能力可以独立组合。

不要为了使用某个工具能力，引入更高层 runtime。

## Run、Stream 与 Checkpoint

`Run` 和 `Stream` 都是对 Eino graph 执行的简单封装，语义上都是一次执行。

区别是：

- `Run` 等执行结束，返回最终 message。
- `Stream` 返回流，业务可以边读边展示。

Checkpoint 不是多轮历史。

Checkpoint 解决的是：本次执行因为 HITL 或 interrupt 暂停后，如何从中断点继续。

底层执行仍然支持 Eino 的多种 option 能力，例如 checkpoint、resume、callback 等。DeepAgent 只封装常用入口；更细的 graph / callback 行为应参考 Eino 文档。

如果业务要支持恢复：

```go
agent, err := deepagents.New(ctx,
    deepagents.WithModel(chatModel),
    deepagents.WithCheckpointStore(checkpointStore),
    deepagents.WithHITLConfig(hitlConfig),
)

msg, err := agent.Run(ctx, messages,
    deepagents.WithCheckpointID(checkpointID),
)
```

审批完成后，再用 resume data 恢复：

```go
msg, err := agent.Run(ctx, nil,
    deepagents.WithCheckpointID(checkpointID),
    deepagents.WithResumeData(resumeData),
)
```

如果你想恢复的是“多轮对话历史”，那不是 Checkpoint 的职责，应该使用 Agent Thread 的 History Store。

## HITL 在 DeepAgent 里怎么工作

HITL 发生在工具调用前。

当模型准备调用敏感工具时，HITL wrapper 可以中断当前执行，并把审批信息暴露给外部。

业务审批后，DeepAgent 通过 checkpoint 和 resume data 回到原来的中断点继续。

恢复后的表现：

- 审批通过：工具继续执行。
- 审批拒绝：拒绝原因作为工具结果返回给模型。
- follow-up：用户回答作为工具结果返回给模型。
- review/edit：编辑后的参数用于继续执行工具。

DeepAgent 只负责本次执行的 interrupt / resume。

如果你要把 HITL 表现成服务端 blocked 状态、断线后继续审批、审批后唤醒任务，那是 Agent Thread / Agent Worker 或业务服务要做的事。

## 高级特性

### 流式工具调用

```go
deepagents.WithStreamToolCall()
```

默认工具调用通常等模型完整输出工具调用后再执行。

流式工具调用适合模型边流式输出工具参数、服务端边解析执行的场景，可以降低等待时间，也能更早暴露工具调用过程。

它会改变工具执行时机，接入时要特别注意：

- 工具参数是否可能在流式过程中不完整。
- HITL 是否仍然能正确拦截。
- 工具事件是否按业务期望输出。
- checkpoint / resume 是否覆盖这个路径。

### 自定义 ReAct 分支

```go
deepagents.WithReactLoopBranchPolicy(policy)
```

默认 ReAct 路由是：

```plaintext
model 有服务端工具调用 -> tools -> model
model 没有服务端工具调用 -> end
```

`ReactLoopBranchPolicy` 允许业务改变这条路由。

典型场景：

- 模型本来要结束，但同一次执行里还有输入要继续处理。
- 工具结果已经是最终结果，不需要再回模型。
- 业务需要在 model / tools 后做特殊分支控制。

它只应该表达 DeepAgent react loop 的路由，不应该承载 mailbox、message id、持久化或 worker 概念。

### Tool Info Rewriter

```go
deepagents.WithToolInfoRewriter(rewriter)
```

用于统一改写工具对模型暴露的名称、描述或参数说明。

适合：

- 针对业务场景收敛工具描述。
- 隐藏内部实现细节。
- 调整模型更容易理解的工具说明。

不要用它绕过工具权限和安全策略。

## 和 Agent Thread / Agent Worker 的关系

```plaintext
DeepAgent
  - 一次 Agent 执行体

Agent Thread
  - 多轮 runtime
  - 每个 Turn 内部可以使用 DeepAgent

Agent Worker
  - CloudAgent 背后的底层 worker host contract
  - 只有自定义服务端 runtime 时才需要直接接入
```

三者不是强绑定关系。

业务可以单独使用 DeepAgent，也可以把它包进 Agent Thread。要接成服务端 Agent 时，默认应先看 CloudAgent；只有自定义底层 worker 机制时才直接接 Agent Worker。

## 接入清单

接入 DeepAgent 前，业务至少要准备：

- chat model。
- 输入 messages。
- 工具列表和工具策略。
- backend / sandbox。
- middleware 选择。
- HITL 策略。
- checkpoint store，如果需要中断恢复。
- 自己的外层历史管理，如果不用 Agent Thread。

如果你发现自己开始手写多轮上下文、pending input、事件流，就应该考虑上移到 Agent Thread。

## 常见误解

- `DeepAgent.Run` / `DeepAgent.Stream` 都是一次执行，不是多轮对话 runtime。
- `DeepAgent.Stream` 是本次执行的输出流，不是可恢复事件日志；断线回放需要外层系统或 Agent Thread / Agent Worker。
- Checkpoint 恢复的是本次执行中断点，不是业务多轮历史。
- Middleware 适合影响本次执行的 prompt、tools、model request、tool call 和状态恢复，不适合承载 worker lease、message id 或产品 UI 状态。
- `WithFilesystem` 只是启用文件系统工具；真正的文件读写和命令执行能力来自 backend / sandbox。

## 下一步

- 需要多轮上下文、pending input、事件和 HITL runtime：继续看 [`Agent Thread 接入指南`](../agentthread/index.md)。
- 需要接入服务端 Agent 运行框架：优先看 [`CloudAgent 接入指南`](../cloudagent/index.md)。
- 确认要自定义底层 worker host contract：再看 [`Agent Worker 底层机制说明`](../agentworker/index.md)。
- 只想了解工具、Sandbox、Skill、Memory 等能力：继续看 [`内置工具说明`](./builtin-tools.md)。
