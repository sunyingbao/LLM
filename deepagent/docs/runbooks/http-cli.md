# CLI 连接 Cloud Agent

状态：当前实现。CLI 与 WebUI 是两个客户端，共用 Cloud Agent 的 HTTP/SSE API。

```text
WebUI ─┐
       ├─ HTTP/SSE → Cloud Agent → Coordinator → Worker → AgentThread
CLI ───┘
```

## 启动

先按 [本地运行说明](cloud-agent-codex-local-runbook.md) 启动后端，并创建项目。
CLI 不自动启动后端；连接失败也不会改成本地执行。

```bash
export DEEPAGENT_RUNTIME=remote
export DEEPAGENT_SERVER_URL=http://127.0.0.1:6789
export DEEPAGENT_PROJECT=<已有的项目名>
# 需要认证时，设置由服务端认可的用户 token：
# export DEEPAGENT_USER_TOKEN=<token>

go run ./deepagent/cmd/deepagent
go run ./deepagent/cmd/deepagent --prompt '概述当前项目'
go run ./deepagent/cmd/deepagent --resume_session_id <session-id>
go run ./deepagent/cmd/deepagent --auto_resume
```

`DEEPAGENT_USER_TOKEN` 通过 `X-Bytedance-User` 传递。身份验证由后端负责；
仅明确配置为本地开发认证的服务允许不带 token。不要把真实 token 写入仓库。

远端模式不需要模型 API key、模型 YAML、MySQL 或 Redis 环境变量，
不创建本地执行历史或文件快照，也不读取本地 skills。
执行目录来自后端项目；CLI 的 `--root`、`--workdir` 不改变远端执行目录。

## 会话与交互

- `/sessions`：列出当前项目的后端会话。
- `/resume <session-id>`：打开后端会话，加载历史并跟随尚未结束的执行。
- `/history`：读取当前后端会话历史，不执行本地文件回滚。
- `/clear`：清除当前选择，不删除后端会话。
- 审批与用户问答通过同一恢复输入接口提交；一次性 `--prompt` 模式遇到交互请求会报错退出，需进入交互模式继续。

`session-id` 是产品会话 ID，`thread-id` 是后端执行线程 ID，不能互换。
历史页面展示后端实际持久化的事件；当前后端未保存为事件的旧用户输入不会凭空补回。
远端只接受 `--resume_session_id`；本地保留 `--resume_thread_id`。
关闭 CLI 只断开观察，不等于停止服务端执行；显式停止后，仍等待服务端终止事件。
断线重连可读取持久化历史；发消息、审批和停止请求不自动重试，以免重复执行。
一次性输出会补齐恢复消息中缺失的尾部；若已经打印的内容不是完整消息的前缀，
终端无法撤回旧输出，会标注 `[recovered message]` 后打印完整恢复消息。

## 本地模式

```bash
DEEPAGENT_RUNTIME=local go run ./deepagent/cmd/deepagent
```

本地模式仍加载 YAML 并在本机执行，保留历史与文件回滚能力。
远端会话恢复只恢复对话和执行观察，不提供远端文件回滚。

升级后需重启 Cloud Agent 才能加载新增的结构化问答和审批取消字段；仅重启 CLI 不会更新后端。
