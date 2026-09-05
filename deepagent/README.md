# DeepAgent Runtime

`deepagent` 是本仓库唯一的 Agent 实现根目录。它把 DeepAgent 本地交互、Eino
Agent loop、长生命周期 thread、分布式 worker 和 CloudAgent 接入统一在同一套
运行时协议和 timeline 语义下。

`cmd/deepagent` 是 CLI 入口：本地模式使用 YAML、session 和 sandbox；远端模式只连接
Cloud Agent 的 HTTP/SSE，与 WebUI 共用后端会话和调度。一次性 prompt、stdin 和会话恢复
仍然可用。`cmd/cloud_agent` 是 HTTP、Coordinator、Worker 单进程入口。

## 主要模块

| 模块 | 作用 |
| --- | --- |
| `backend` | 本地配置、sandbox、session 数据和交互 TUI |
| `core` | 单机 Agent runtime：模型调用、工具调用、中间件、文件系统/sandbox backend、HITL、计划、技能、子 Agent、checkpoint 和上下文管理 |
| `core/agentthread` | 长生命周期线程 runtime：输入队列、事件、历史、上下文压缩、checkpoint/resume |
| `runtime` | local/remote 共用的 RuntimeClient、timeline 与路由契约 |
| `coordinator` | 线程状态、执行权、输入邮箱和持久事件；不运行模型 |
| `worker/cloud`、`worker/inprocess` | 服务端认领执行、本地直接执行；负责投递输入和收集输出 |
| `worker/thread` | 两种 Worker 共用的 AgentThread 适配：消息、事件和中断协议转换 |
| `cloud/api` | 不绑定 Hertz 的 CloudAgent API facade：submit、timeline list/subscribe、stop/control |
| `cloud/worker` | 可复用的默认 CloudAgent worker 组装层 |
| `host` | DeepAgent provider、配置与运行时绑定 |
| `host/runtime` | CLI 的本地执行适配与远端 HTTP/SSE 客户端 |
| `tools` | 共享工具以及 DeepAgent 工具绑定 |
| `memory` | 结构化事实提取、合并与提示词注入 |
| `cmd/deepagent` | 本地 DeepAgent CLI |
| `cmd/cloud_agent` | CloudAgent 单进程参考实现，用于 dogfood 和端到端验证 |

## 文档入口

详细文档以 [`docs/README.md`](./docs/README.md) 为入口和目录规范。根
`README.md` 只保留仓库概览，不复制各主题的完整内容。

推荐阅读路径：

- [`docs/README.md`](./docs/README.md)：文档目录、状态说明和治理规则。
- [`docs/agents/index.md`](./docs/agents/index.md)：当前稳定的 SDK 分层和接入指南。
- [`docs/agents/backend-code-map.md`](./docs/agents/backend-code-map.md)：后端目录职责、状态归属和读代码的主调用链。
- [`docs/agents/cloudagent/index.md`](./docs/agents/cloudagent/index.md)：推荐的服务端 Agent 接入层。
- [`docs/agents/deepagent/index.md`](./docs/agents/deepagent/index.md)：单机 DeepAgent 使用说明。
- [`docs/agents/agentthread/index.md`](./docs/agents/agentthread/index.md)：AgentThread 嵌入式接入。
- [`docs/agents/agentworker/index.md`](./docs/agents/agentworker/index.md)：Agent Worker 底层机制说明，非默认业务接入路径。
- [`docs/runbooks/deepagent-worker-e2e.md`](./docs/runbooks/deepagent-worker-e2e.md)：真实 worker 本地全链路联调。
- [`docs/runbooks/cloud-agent-codex-local-runbook.md`](./docs/runbooks/cloud-agent-codex-local-runbook.md)：让 Codex 在新机器上拉起本地 Cloud Agent 环境。

## 快速开始

本地 CLI：

```bash
export OPENAI_API_KEY=<your-key>

# 默认启动统一 DeepAgent runtime/TUI（可用 --root 或 DEEPAGENT_ROOT 指定仓库根目录）
go run ./deepagent/cmd/deepagent

# 保留 DeepAgent 的一次性执行能力
go run ./deepagent/cmd/deepagent -workdir=. -prompt "读取 README.md，总结这个仓库的主要模块"

# 恢复指定 thread 后继续执行
go run ./deepagent/cmd/deepagent -resume_thread_id <thread-id> -prompt "继续执行"
```

CloudAgent 本地单进程链路：

```bash
python3 deepagent/cmd/cloud_agent/dev.py
```

更多模型配置、服务配置、建表和排错步骤见
[`docs/runbooks/deepagent-worker-e2e.md`](./docs/runbooks/deepagent-worker-e2e.md)。
CLI 连接同一后端的用法见 [HTTP CLI 运行说明](./docs/runbooks/http-cli.md)。

## 常用验证

```bash
go test ./deepagent/core/... ./deepagent/worker/... ./deepagent/cloud/...
go test ./deepagent/runtime/... ./deepagent/host/... ./deepagent/tools/... ./deepagent/memory/...
go test ./deepagent/backend/... ./deepagent/cmd/...
git diff --check
```

### 重新生成 reference service 模型

`cmd/cloud_agent` 只生成对外 HTTP API 模型。SessionService 和 Coordinator
都是进程内 Go 调用，不再使用内部 Session Kitex 协议或 Coordinator Overpass。
外部 AIInfra 沙箱 RPC 依赖保留。

修改 `deepagent/cmd/cloud_agent/idl/*.thrift` 后运行：

```bash
./deepagent/cmd/cloud_agent/generate_local_code.sh
```

生成脚本固定使用 HertzTool `v3.4.7`，输出位于 `deep_agent_sdk/hertz_gen`。
