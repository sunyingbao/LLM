# deep_agent_sdk 文档索引

本目录包含当前 SDK 使用指南、运行手册和整体介绍。阅读时先看本文档中的入口和状态，并以代码为最终事实。

## 目录规范

| 目录 | 语义 |
| --- | --- |
| [`agents/`](./agents/index.md) | 当前稳定的 SDK 分层、模块边界和接入指南 |
| [`runbooks/`](./runbooks/) | 本地启动、联调、运维操作手册 |
| [`overview/`](./overview/) | 对外介绍、整体架构和配套材料 |
| [`assets/`](./assets/) | 图片、图表等文档资产 |

`cmd/*/docs/` 只说明对应 reference service 或命令，不作为 SDK 总入口。

## 状态说明

| 状态 | 含义 |
| --- | --- |
| current | 当前推荐入口或仍然有效的设计依据 |
| draft | 草稿或待对齐内容，可以参考但不应当直接作为实现契约 |
| archived | 历史方案、gap analysis 或已被后续实现/设计覆盖的内容 |

## 推荐阅读路径

1. 项目定位与服务端 SDK 边界：[`overview/agent-sdk-intro.md`](./overview/agent-sdk-intro.md)
2. 分层 SDK 使用指南：[`agents/index.md`](./agents/index.md)
3. CloudAgent 服务端 Agent 接入层：[`agents/cloudagent/index.md`](./agents/cloudagent/index.md)
4. 单机 DeepAgent runtime 使用：[`agents/deepagent/index.md`](./agents/deepagent/index.md)
5. AgentThread 嵌入式接入：[`agents/agentthread/index.md`](./agents/agentthread/index.md)
6. Agent Worker 底层机制说明：[`agents/agentworker/index.md`](./agents/agentworker/index.md)
7. Worker 本地全链路联调：[`runbooks/deepagent-worker-e2e.md`](./runbooks/deepagent-worker-e2e.md)
8. 让 Codex 在新机器上拉起 Cloud Agent：[`runbooks/cloud-agent-codex-local-runbook.md`](./runbooks/cloud-agent-codex-local-runbook.md)
9. Cloud Agent BOE 准备计划：[`runbooks/cloud-agent-boe-readiness-plan.md`](./runbooks/cloud-agent-boe-readiness-plan.md)
10. CLI 与 WebUI 共用后端：[`runbooks/http-cli.md`](./runbooks/http-cli.md)

## 当前入口

| 文档 | 状态 | 范围 | 备注 |
| --- | --- | --- | --- |
| [`overview/agent-sdk-intro.md`](./overview/agent-sdk-intro.md) | draft | overview | 服务端 SDK 介绍，落地前需要继续确认最终表述和配图资产 |
| [`agents/index.md`](./agents/index.md) | current | user-guide | 当前推荐的分层 SDK 使用指南入口 |
| [`agents/backend-code-map.md`](./agents/backend-code-map.md) | current | implementation | 后端模块职责、状态所有权和代码阅读路径 |
| [`agents/cloudagent/index.md`](./agents/cloudagent/index.md) | current | cloudagent | 推荐的服务端 Agent 接入层，说明 `deepagent/cloud/worker`、`deepagent/cloud/api` 与 `deepagent/cmd/cloud_agent` reference service 的关系 |
| [`agents/deepagent/index.md`](./agents/deepagent/index.md) | current | deepagents | 单机 DeepAgent 使用指南 |
| [`agents/agentthread/index.md`](./agents/agentthread/index.md) | current | agentthread | AgentThread 嵌入式接入指南 |
| [`agents/agentworker/index.md`](./agents/agentworker/index.md) | current | agentworker | Agent Worker 底层机制说明，只有自定义 worker host contract 时才建议阅读 |
| [`runbooks/deepagent-worker-e2e.md`](./runbooks/deepagent-worker-e2e.md) | current | worker/cmd | 本地真实 worker 链路测试手册 |
| [`runbooks/http-cli.md`](./runbooks/http-cli.md) | current | cli/http | CLI 的 HTTP 连接、后端会话恢复与本地模式边界 |
| [`runbooks/cloud-agent-codex-local-runbook.md`](./runbooks/cloud-agent-codex-local-runbook.md) | current | cmd | 面向另一台机器上的 Codex 的 Cloud Agent 本地环境构建 runbook |
| [`runbooks/cloud-agent-boe-readiness-plan.md`](./runbooks/cloud-agent-boe-readiness-plan.md) | draft | cmd/boe | DeepAgent 单进程 BOE 部署的配置、存储、ID 和验收准备清单 |

## 归档文档

| 文档 | 状态 | 范围 | 备注 |
| --- | --- | --- | --- |

## 根目录文档

| 文档 | 状态 | 范围 | 备注 |
| --- | --- | --- | --- |
| [`../README.md`](../README.md) | current | overview | 仓库入口，应该只保留定位、快速开始和文档导航 |
| [`../core/README.md`](../core/README.md) | current | deepagents | `deepagents` 包级 API 文档 |

## 后续治理规则

- `docs/agents/` 放当前稳定的 SDK 分层、逻辑边界和接入指南。
- `docs/runbooks/` 放本地启动、联调、运维操作手册。
- `cmd/*/docs/` 只说明对应 reference service 或命令，不作为 SDK 总入口。
- package-local `README.md` 面向读代码的人，必须链接到 `docs/agents/` 的主入口，避免形成两套接入口径。
- 每个主题最多保留一个 `current` 使用指南。
- 历史方案先标 `archived`，确认无人引用后再移动或删除。
- 文档引用本地代码路径时，修改对应代码后必须同步核对路径是否仍然存在。
