# DeepAgent Runtime

`deepagent` 是本仓库唯一的 Agent 实现根目录。它把 SGADK 本地交互、Eino
Agent loop、长生命周期 thread、分布式 worker 和 CloudAgent 接入统一在同一套
定义、运行时协议和 timeline 语义下。

`cmd/deepagent` 是统一的本地产品入口：默认复用 SGADK 的 YAML 配置、session、sandbox、
local/remote runtime 和交互 TUI；一次性 prompt、stdin、thread resume 等 DeepAgent
能力仍然可用。`cmd/cloud_agent` 是服务端参考实现。它们都复用这里的执行核心。

## 主要模块

| 模块 | 作用 |
| --- | --- |
| `core` | 单机 Agent runtime：模型调用、工具调用、中间件、文件系统/sandbox backend、HITL、计划、技能、子 Agent、checkpoint 和上下文管理 |
| `core/agentthread` | 长生命周期线程 runtime：输入队列、事件、历史、上下文压缩、checkpoint/resume |
| `definition` | AgentDefinition 与 capability resolver，保存可移植声明而不是 host secret |
| `runtime` | local/remote 共用的 RuntimeClient、timeline 与路由契约 |
| `worker` | in-process 与 Agent Coordinator worker host |
| `cloud/api` | 不绑定 Hertz 的 CloudAgent API facade：submit、timeline list/subscribe、stop/control |
| `cloud/worker` | 可复用的默认 CloudAgent worker 组装层 |
| `host` | SGADK provider、配置与运行时绑定 |
| `tools` | 共享工具以及 SGADK 工具绑定 |
| `memory` | structured memory、dream memory 与 consolidation |
| `cmd/deepagent` | 本地 DeepAgent CLI |
| `cmd/cloud_agent` | CloudAgent 三服务参考实现，用于 dogfood 和端到端验证 |

## 文档入口

详细文档以 [`docs/README.md`](./docs/README.md) 为入口和目录规范。根
`README.md` 只保留仓库概览，不复制各主题的完整内容。

推荐阅读路径：

- [`docs/README.md`](./docs/README.md)：文档目录、状态说明和治理规则。
- [`docs/agents/index.md`](./docs/agents/index.md)：当前稳定的 SDK 分层和接入指南。
- [`docs/agents/cloudagent/index.md`](./docs/agents/cloudagent/index.md)：推荐的服务端 Agent 接入层。
- [`docs/agents/deepagent/index.md`](./docs/agents/deepagent/index.md)：单机 DeepAgent 使用说明。
- [`docs/agents/agentthread/index.md`](./docs/agents/agentthread/index.md)：AgentThread 嵌入式接入。
- [`docs/agents/agentworker/index.md`](./docs/agents/agentworker/index.md)：Agent Worker 底层机制说明，非默认业务接入路径。
- [`docs/testing/cloud-agent-test-suite.md`](./docs/testing/cloud-agent-test-suite.md)：Cloud Agent 测试集合。
- [`docs/runbooks/deepagent-worker-e2e.md`](./docs/runbooks/deepagent-worker-e2e.md)：真实 worker 本地全链路联调。
- [`docs/runbooks/cloud-agent-codex-local-runbook.md`](./docs/runbooks/cloud-agent-codex-local-runbook.md)：让 Codex 在新机器上拉起本地 Cloud Agent 环境。

## 快速开始

本地 CLI：

```bash
export OPENAI_API_KEY=<your-key>

# 默认启动统一 SGADK runtime/TUI（可用 --root 或 SGADK_ROOT 指定仓库根目录）
go run ./cmd/deepagent

# 保留 DeepAgent 的一次性执行能力
go run ./cmd/deepagent -workdir=. -prompt "读取 README.md，总结这个仓库的主要模块"

# 需要旧版 Bubble Tea 本地 CLI 时显式开启兼容模式
go run ./cmd/deepagent --legacy -prompt "执行一次本地任务"
```

CloudAgent 本地三服务链路：

```bash
python3 cmd/cloud_agent/dev.py
```

更多模型配置、服务配置、建表和排错步骤见
[`docs/runbooks/deepagent-worker-e2e.md`](./docs/runbooks/deepagent-worker-e2e.md)。

## 常用验证

```bash
go test ./core/... ./worker/... ./cloud/...
go test ./definition/... ./runtime/... ./host/... ./tools/... ./memory/...
go test ./cmd/deepagent/... ./cmd/cloud_agent/...
git diff --check
```

### 重新生成 reference service 模型

`cmd/cloud_agent` 的 HTTP API 与 Session IDL 模型直接生成在本仓库中，不依赖
API/Session 专用的远程 Overpass 仓库。Coordinator 是多个接入方共享的运行时
协议，仍使用统一发布的 Coordinator Overpass 依赖。

修改 `cmd/cloud_agent/idl/*.thrift` 后运行：

```bash
./cmd/cloud_agent/generate_local_code.sh
```

生成脚本固定使用 HertzTool `v3.4.7` 和 Kitex Tool `v1.22.1`，输出分别位于
`aic_agent_sdk_api/hertz_gen` 与 `aic_agent_sdk_session/kitex_gen`。

更完整的测试分层和成功判据见
[`docs/testing/cloud-agent-test-suite.md`](./docs/testing/cloud-agent-test-suite.md)。
