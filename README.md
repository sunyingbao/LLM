# Unified DeepAgent Runtime

DeepAgent 提供本地 CLI 和云端 Web 工作台，共用一个 Agent Runtime。
模型执行、工具、中间件和线程逻辑都位于 `deepagent/`。

## 架构入口

```text
deepagent/
  backend/          本地配置、沙箱、会话数据和 TUI
  runtime/          RuntimeClient、local/remote client 和路由
  worker/           in-process 与 Agent Coordinator worker
  cloud/            Cloud API、协议与服务端适配
  core/             模型循环、AgentThread、middleware、workspace backend
  host/             本地模型、配置和运行时装配
  tools/            本地扩展工具，通用工具复用 core
  memory/           结构化事实提取、合并与提示词注入

deepagent/cmd/deepagent/  本地 CLI 入口
deepagent/cmd/cloud_agent/  HTTP、Coordinator、Worker 单进程入口
```

仓库只有根目录一个 Go module，没有第二套 Agent loop。

## 运行 DeepAgent

准备 `yaml/config.yaml` 后：

```bash
export OPENAI_API_KEY=<your-openai-key>
go run ./deepagent/cmd/deepagent --root /Users/bytedance/go/src/content/LLM
```

本地 runtime 是默认值。也可以显式设置：

```bash
DEEPAGENT_RUNTIME=local go run ./deepagent/cmd/deepagent
```

远端 CLI 和 WebUI 共用 Cloud Agent 的 HTTP/SSE 接口。先启动后端，再连接已有项目：

```bash
DEEPAGENT_RUNTIME=remote \
DEEPAGENT_SERVER_URL=http://127.0.0.1:6789 \
DEEPAGENT_PROJECT=<project-name> \
go run ./deepagent/cmd/deepagent
```

需要认证的服务使用 `DEEPAGENT_USER_TOKEN`（发送为 `X-Bytedance-User`）。远端 CLI
不加载模型 YAML，不连接 MySQL/Redis，也不启动本地沙箱；执行目录由后端项目决定。
`--resume_session_id <session-id>` 恢复后端会话，`--auto_resume` 选择当前项目最近会话。
本地模式仍使用 `--resume_thread_id`，两种模式不会自动互相回退。
详细用法见 [HTTP CLI 运行说明](deepagent/docs/runbooks/http-cli.md)。

## 安装与验证

安装本地命令：

```bash
bash scripts/install-deepagent.sh
deepagent
```

常用验证：

```bash
go test ./...
go build ./...
git diff --check
```

真实 API key 只能通过本地配置或环境变量提供，不应提交到仓库。
