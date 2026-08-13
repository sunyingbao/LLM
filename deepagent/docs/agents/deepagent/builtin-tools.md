# 内置工具说明

<callout emoji="🧰" background-color="light-blue">

内置工具是 DeepAgent 可组合能力的一部分。它们降低业务建设 Agent 能力的成本，但不定义业务产品协议。
</callout>

## 工具从哪里来

DeepAgent 的工具主要来自四类来源：

- 业务通过 `WithTools` 传入的自定义工具。
- middleware 自动注入的工具。
- backend / sandbox 提供的工具。
- `agentworker.TaskTool` 提供的跨 thread 工具。

这些工具最终都会变成模型可见的 tool schema。

业务需要控制哪些工具可见、哪些可执行、哪些需要审批。

## 文件系统工具

启用方式：

```go
deepagents.WithFilesystem()
```

或：

```go
deepagents.WithFilesystemConfig(&deepagents.FilesystemConfig{
    WorkDir: workDir,
})
```

文件系统工具用于读取、写入、搜索和编辑工作目录。

常见能力：

- 列目录。
- 读文件。
- 写文件。
- 编辑文件。
- glob。
- grep。

如果启用 sandbox backend，也可以暴露命令执行能力。

## Sandbox

Sandbox 用于承载更危险的执行能力，例如 shell 命令。

接入时业务需要明确：

- 工作目录。
- 命令超时。
- 网络和文件权限。
- 是否允许上传下载。
- 哪些命令需要 HITL。

不要把本地 demo 的执行权限直接带到生产环境。

## Planning

Planning 工具用于让模型维护任务计划。

适合：

- 多步骤任务。
- 长时间运行任务。
- 前端需要展示任务进度。

Planning 不是调度系统。它只是 Agent runtime 内部的计划状态。

## Memory

Memory 用于把固定文件内容注入上下文。

适合：

- 项目规则。
- 业务约束。
- 团队规范。
- 长期偏好。

Memory 不是数据库，也不是完整的长期记忆系统。

## Skill

Skill 用于加载一组可复用的说明、资源和工具约束。

推荐使用：

```go
deepagents.WithSkillLoader(loader)
```

而不是只依赖目录路径。

业务需要决定：

- skill 从哪里加载。
- 哪些 skill 对当前 Agent 可见。
- skill 是否需要缓存。
- skill 内部工具是否需要审批。

## 子 Agent

DeepAgent 支持通过 middleware 注入子 Agent 能力。

它适合把任务委托给另一个 Agent 执行体。

但子 Agent 的产品语义不是 SDK 自动定义的：

- 子 Agent 是否属于同一个 session。
- 子 Agent 如何命名。
- 子 Agent 输出展示在哪里。
- 父 Agent 是否等待子 Agent。
- 父子 Agent 如何通信。

这些属于业务设计。

如果需要分布式多 Agent 通信，应结合 `agentworker.TaskTool` 和 Agent Coordinator。

## Task Tool

`agentworker.TaskTool` 提供服务端跨 thread 工具：

- `spawn_task`
- `send_message`
- `wait_message`

它只封装通用通信能力。

业务如果需要 alias、角色名、父子关系、展示文案，应通过 callback 和 metadata 实现。

模型只需要使用工具暴露的 target。target 如何解析成真实 thread，由业务自己决定。

## 工具安全

接入工具时至少要回答：

- 这个工具是否应该暴露给模型。
- 这个工具是否需要 HITL。
- 工具参数是否需要校验。
- 工具结果是否需要截断。
- 工具失败是否需要重试。
- 工具调用是否需要写事件。

工具越强，越应该有明确的边界和审批策略。
