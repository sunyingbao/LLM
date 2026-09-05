# Agent SDK 使用指南

<callout emoji="🎁" background-color="light-blue">

这篇文档面向正在构建业务 Agent 的工程师。它先解释业务 Agent 构建会遇到哪些核心问题，再说明 Agent SDK 的分层能力如何承接这些问题。
</callout>

## 仓库

https://code.byted.org/ad/deep_agent_sdk

## 这套文档在讲什么

这套文档讲的不是某个单机 demo，也不是纯 API reference，而是一套 Agent 构建体系。

业务要让 Agent 真正落地，通常会同时遇到几类问题：

- 一次 Agent 执行怎么跑完。
- 多轮上下文、运行事件和恢复怎么管理。
- 工具、文件、Sandbox、Skill、Memory 这些能力怎么组合。
- 如果业务要把 Agent 做成服务端产品，能不能直接复用一套接近成品的 Agent 运行框架。
- 如果确实要自定义底层 worker 机制，任务认领、输入交付、过程记录和恢复怎么处理。
- 多 Agent 协作时，任务创建、消息发送和等待结果怎么接入。

Agent SDK 把这些问题分层沉淀下来。底层 runtime 可以在端侧、服务端或业务进程中复用；当业务要做服务端 Agent 时，默认推荐从 CloudAgent 开始。Agent Worker 是 CloudAgent 背后的底层 worker 机制，不是大多数业务的首选接入面。

## 业务构建 Agent 的问题地图

业务工程师进入文档时，通常不是想先背模块名，而是想知道自己正在遇到的问题由哪一层能力承接。

| 构建问题 | SDK 能力 | 继续阅读 |
| --- | --- | --- |
| 怎么跑完一次 Agent 执行 | `DeepAgent` | [DeepAgent 接入指南](./deepagent/index.md) |
| 怎么维护多轮上下文、事件和恢复 | `Agent Thread` | [Agent Thread 接入指南](./agentthread/index.md) |
| 怎么接工具、文件、Sandbox、Skill、Memory | `Middleware / Backend` | [DeepAgent 接入指南](./deepagent/index.md)、[内置工具说明](./deepagent/builtin-tools.md) |
| 如何快速接入一个服务端 Agent 运行框架 | `CloudAgent` | [CloudAgent 接入指南](./cloudagent/index.md) |
| 怎么做多 Agent 协作 | `TaskTool / Agent Coordinator` | [CloudAgent 接入指南](./cloudagent/index.md)、[内置工具说明](./deepagent/builtin-tools.md) |
| 必须自定义底层 worker 机制时怎么办 | `Agent Worker` | [Agent Worker 底层机制说明](./agentworker/index.md) |

这几层是不同抽象层级，不是强绑定关系。业务可以只使用 `DeepAgent`，也可以在 `Agent Thread` 上构建自己的多轮 runtime。多数服务端 Agent 接入应优先看 `CloudAgent`；只有当业务明确要替换默认运行框架、自己实现 worker host contract 时，才需要下钻到 `Agent Worker`。

## 接入时需要分清的责任

使用 SDK 不等于业务系统已经完整。接入前需要先分清：哪些问题 SDK 已经抽出来，哪些信息仍然必须由业务提供。

SDK 负责：

- Agent runtime：模型调用、工具调用、ReAct loop、middleware、backend。
- Thread runtime：多轮上下文、pending input、事件、HITL 中断和恢复。
- CloudAgent：接近成品的服务端 Agent 运行框架，组装 DeepAgent、Agent Thread、工具、history、checkpoint、approval 和协作能力。
- Agent Worker：CloudAgent 背后的底层 worker host contract，处理服务端任务推进、输入交付、事件输出、lease 和生命周期。
- 能力组合：工具、Sandbox、Skill、Memory、Checkpoint、TaskTool。

业务负责：

- 产品协议、权限、登录态和用户身份。
- UI、渲染、session 目录、资产模型和项目组织。
- 业务工具、工具安全策略和审批策略。
- 模型选择、prompt 策略和业务角色定义。
- 部署形态、监控告警和线上运维。

特别注意：

- `deepagent/cmd/cloud_agent` 是 reference implementation，用来验证 SDK 边界和提供 dogfood 服务。
- `deepagent/cmd/cloud_agent/deep_agent_sdk`、`deep_agent_sdk_session` 和 WebUI 可以参考、部署或 fork，但不是稳定 SDK public API。
- 如果业务已有自己的产品服务，应优先复用 `deepagent/cloud/api` 和 `deepagent/cloud/worker`。只有明确要自定义底层 worker 机制时，再考虑直接使用 `agentworker`。

## 主接入路径

理解问题地图后，再按主路径进入对应文档。

如果要阅读实现，先看[后端代码地图](./backend-code-map.md)：它按实际目录说明状态归属和调用链。

| 你要做什么 | 先看 |
| --- | --- |
| 使用接近成品的服务端 Agent 运行框架 | [CloudAgent 接入指南](./cloudagent/index.md) |
| 在业务服务或端侧运行一个多轮 Agent runtime，自己管理外层调度和事件存储 | [Agent Thread 接入指南](./agentthread/index.md) |
| 只需要一次模型 + 工具的 Agent 执行，或者已经有自己的会话层 | [DeepAgent 接入指南](./deepagent/index.md) |
| 确认要自定义底层 worker host contract | [Agent Worker 底层机制说明](./agentworker/index.md) |

## 最小心智模型

```plaintext
业务产品层
  - 产品协议 / 权限 / UI / 资产模型 / 业务工具策略

CloudAgent
  - 接近成品的服务端 Agent 运行框架
  - 组装 DeepAgent、Agent Thread、tools、skills、history、checkpoint、approval
  - 提供默认 worker 接入和运行时 API

Agent Worker
  - CloudAgent 背后的底层 worker host contract
  - 管理 scan、claim、pull、ack、append event、release
  - 不推荐业务默认直接接入

Agent Thread
  - 多轮 Agent runtime
  - 管理上下文、pending input、事件、HITL 中断和恢复

DeepAgent
  - 一次 Agent 执行体
  - 组合模型、工具、middleware、backend 和 checkpoint

Middleware / Backend
  - 文件系统、Sandbox、Planning、Skill、Memory 等可组合能力
```

## 能力专题索引

阶段一先把能力放到正确位置，专题正文后续再按需要展开。

| 能力 | 主要挂载位置 | 说明 |
| --- | --- | --- |
| 工具 / Backend / Sandbox | [DeepAgent 接入指南](./deepagent/index.md) | 决定模型能调用什么、在哪里执行、权限如何控制 |
| History / Context / Compaction | [Agent Thread 接入指南](./agentthread/index.md) | 决定多轮上下文如何恢复、压缩和进入模型请求 |
| HITL / Checkpoint / Resume | [Agent Thread 接入指南](./agentthread/index.md)、[DeepAgent 接入指南](./deepagent/index.md) | DeepAgent 负责执行中断恢复，Agent Thread 负责把它变成多轮 runtime 事件 |
| TaskTool / 多 Agent 协作 | [CloudAgent 接入指南](./cloudagent/index.md)、[内置工具说明](./deepagent/builtin-tools.md) | 服务端跨 thread 创建任务、发送消息和等待结果 |
| Memory / Skill | [DeepAgent 接入指南](./deepagent/index.md)、[内置工具说明](./deepagent/builtin-tools.md) | 作为可组合能力注入执行上下文或工具约束 |

## 子文档

- [CloudAgent 接入指南](./cloudagent/index.md)
- [Agent Worker 底层机制说明](./agentworker/index.md)
- [Agent Thread 接入指南](./agentthread/index.md)
- [DeepAgent 接入指南](./deepagent/index.md)
- [内置工具说明](./deepagent/builtin-tools.md)
- [恢复、HITL 与 Block/Resume](./agentthread/recovery-hitl-block-resume.md)
