# Agent SDK 介绍（服务端能力版）

<callout emoji="🎁" background-color="light-blue">

Agent SDK 是面向视频生产场景的 Agent 能力底座。它把 Agent 运行时、服务端运行框架和业务接入方式沉淀为通用能力，帮助业务更低成本地构建可落地的服务端 Agent。
</callout>

## 背景

视频生产场景中的 Agent 需求，天然同时包含通用能力和场景能力两部分。

一方面，模型调用、工具调用、上下文管理、文件操作、任务规划、中断恢复等能力，在不同业务和不同 Agent 形态中反复出现。如果每个场景分别建设，不仅成本高，也难以形成统一的运行和演进基础。

另一方面，仅有单机 Agent 能力并不足以支撑真实业务。服务端 Agent 还需要长期运行、输入通信、事件推流、断线恢复、多 Agent 协作等能力。否则 Agent 仍然容易被绑定在一次请求、一次连接或某个具体进程里。

基于这一判断，Agent SDK 的建设目标不是单纯提供一套 Agent 工具库，而是面向真实业务链路，提供可复用、可扩展、可持续演进的服务端 Agent 能力底座。

## Agent SDK 是什么

<image src="../assets/agent-sdk-server-architecture.png" align="center"/>

从系统形态上看，Agent SDK 连接业务 Agent 实现和服务端控制面。

业务侧保留自己的 Agent 逻辑、工具策略、状态存储和产品协议。

SDK 提供两类核心能力：

- Agent Runtime：负责模型、工具、上下文、Sandbox、Skill 等单机运行能力。
- Worker Runtime：负责输入接入、事件输出和生命周期控制。

Agent Coordinator 提供服务端控制面，负责 Agent 注册与管理、Agent 输入与通信、事件与推流系统。

## SDK 能力现状

从功能模块上看，当前 Agent SDK 已经覆盖 Agent Runtime、Worker Runtime、Agent Coordinator 接入和参考实现四个核心模块。

| 模块 | 能力项 | 当前状态 | 说明 |
| --- | --- | --- | --- |
| Agent Runtime | ReAct Loop | 已落地 | 底层为 Eino，兼容基于 Eino 的模型和工具 |
| Agent Runtime | ContextManager | 已落地 | 支持多轮上下文管理、恢复和压缩 |
| Agent Runtime | 文件与代码工具 | 已落地 | 支持文件读写、编辑、检索和命令执行 |
| Agent Runtime | Sandbox | 已落地 | 支持本地环境和业务 Sandbox 接入 |
| Agent Runtime | Skill | 已落地 | 支持 Skill 读取与使用 |
| Agent Runtime | Sub-Agent | 演进中 | 支持任务委托，协作范式仍在继续打磨 |
| Worker Runtime | 输入接入 | 已落地 | 将用户、系统、其他 Agent 的输入接入业务 Agent Worker |
| Worker Runtime | 事件输出 | 已落地 | 将业务运行过程转换为可推流、可回放的事件 |
| Worker Runtime | 生命周期控制 | 已落地 | 封装服务端 worker 的通用运行逻辑 |
| Agent Coordinator 接入 | Agent 注册与管理 | 已落地 | 提供服务端 Agent 运行单元和处理权管理 |
| Agent Coordinator 接入 | Agent 输入与通信 | 已落地 | 提供统一输入通道，支持用户输入和 Agent 间通信 |
| Agent Coordinator 接入 | 事件与推流系统 | 已落地 | 支持历史事件回放和实时推流 |
| 参考实现 | DeepAgent Worker | 建设中 | 用真实 Agent 验证服务端接入链路 |
| 参考实现 | WorkerCtl / Chat | 建设中 | 用于本地联调、体验和发现 SDK 边界问题 |

## 业务如何接入

业务接入 Agent SDK 时，仍然需要建设自己的业务 Agent Worker。

SDK 负责提供 Agent Runtime 和 Worker Runtime，业务负责实现自己的 Agent 逻辑、工具策略、状态存储和事件协议。

具体接入方式见接入文档。

## 未来规划（待对齐）

Agent SDK 的目标，是围绕视频生产领域的通用问题，构建一套可复用、可落地、可稳定使用的服务端 Agent 能力体系。

目前，SDK 已经具备较完整的通用运行底座。下一阶段工作的重点，将从单点能力补齐，转向真实业务接入链路的持续打磨。

从这一目标出发，当前仍存在三类核心能力缺口：

| 能力维度 | 当前不足 | 对应建设 |
| --- | --- | --- |
| 场景能力 | 当前能力以通用能力为主，对图片、视频、剪辑等视频生产环节覆盖仍不足 | 垂类工具 |
| 项目连续性 | 长时任务中的项目背景、创作偏好和上下文约束仍需要稳定沉淀 | 长期记忆 |
| 协同能力 | 多 Agent 之间的任务拆分、通信、等待和恢复机制仍需要继续打磨 | 多 Agent 通信与管理 |

基于这三个缺口，下一阶段的重点也更加明确：一方面继续补齐面向视频生产场景的能力覆盖，另一方面补齐长时任务所需的项目连续性和协同能力。
