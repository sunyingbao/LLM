# Unified DeepAgent Runtime

本仓库将原 SGADK 本地 Agent 与服务端 Agent SDK 融合为同一个 Agent
Runtime。所有 Agent 执行逻辑位于 `deepagent/`；SGADK 只保留配置、TUI
和会话展示等产品壳能力。

## 架构入口

```text
deepagent/
  definition/       AgentDefinition 与能力解析
  runtime/          RuntimeClient、local/remote client 和路由
  worker/           in-process 与 Agent Coordinator worker
  cloud/            Cloud API、协议与服务端适配
  core/             模型循环、AgentThread、middleware、workspace backend
  host/             SGADK provider、配置和运行时绑定
  tools/            共享工具及 SGADK 工具绑定
  memory/           structured memory、dream memory 与 consolidation

deepagent/cmd/deepagent/  统一 DeepAgent/SGADK 本地 CLI 入口
backend/cli/tui/     Bubble Tea 展示层
backend/config/      SGADK 本地配置
```

仓库只有根目录一个 Go module。历史入口 `cmd/sgadk` 以及目录 `arch/`、`agent/` 和
`backend/agent/` 已不存在，也没有第二套 Agent loop。

## 运行 SGADK

准备 `yaml/config.yaml` 后：

```bash
export OPENAI_API_KEY=<your-openai-key>
go run ./deepagent/cmd/deepagent --root /Users/bytedance/go/src/content/LLM
```

本地 runtime 是默认值。也可以显式设置：

```bash
SGADK_RUNTIME=local go run ./deepagent/cmd/deepagent
```

远端 runtime 需要 Agent Coordinator 配置：

```bash
SGADK_RUNTIME=remote \
SGADK_REMOTE_AC_PSM=<coordinator-psm> \
SGADK_REMOTE_NAMESPACE=<worker-namespace> \
SGADK_REMOTE_USER_ID=<user-id> \
go run ./deepagent/cmd/deepagent
```

可选变量包括 `SGADK_REMOTE_AC_CLUSTER`、
`SGADK_REMOTE_AC_HOSTPORTS` 和 `SGADK_REMOTE_ENV`。local 与 remote thread
使用不可变 `GlobalThreadRef` 路由，不会自动互相回退。

## 安装与验证

安装本地命令：

```bash
bash scripts/install-sgadk.sh
sgadk
```

常用验证：

```bash
go test ./...
go build ./...
git diff --check
```

真实 API key 只能通过本地配置或环境变量提供，不应提交到仓库。
