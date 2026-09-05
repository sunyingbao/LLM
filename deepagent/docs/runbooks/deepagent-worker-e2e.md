# DeepAgent Worker 真实链路验证

先按[本地运行说明](cloud-agent-codex-local-runbook.md)准备 MySQL、Redis、模型配置和
namespace，并启动单个 `cloud_agent` 进程。Session、Worker、API 和 Coordinator
由 [main.go](../../cmd/cloud_agent/main.go) 在进程内组装。

所有命令从仓库根目录执行。配置默认为 `~/.config/deepagent/dev_config.json`，
Worker 模板是 [worker.local.yml](../../cmd/cloud_agent/conf/worker.local.yml)。
本地 API 使用 `DEEP_AGENT_SDK_API_*` 环境变量，路由前缀为 `/ad/deep_agent_sdk`。

## 准备测试目标

```bash
python3 deepagent/cmd/cloud_agent/dev.py status
python3 deepagent/cmd/cloud_agent/dev.py smoke
```

[local.example.json](../../cmd/cloud_agent/tests/profiles/local.example.json) 默认访问
`http://127.0.0.1:6789`，测试用户 header 为 `X-Deep-Agent-SDK-Test-UID: 1234`。
端口、用户或能力开关不同，应复制一份私有 profile 并修改 `base_url`、
`headers` 和 `features`，不要覆盖共享示例。

P0 会创建测试项目、Session、模型 turn，并在工作区写入文件；请选择专门的测试
环境。模型配置来自 Worker YAML 与私有模型环境文件；E2E profile 不负责选择模型
或启动服务。

## 运行 P0

```bash
python3 deepagent/cmd/cloud_agent/tests/e2e_runner.py \
  --profile deepagent/cmd/cloud_agent/tests/profiles/local.example.json \
  --suite p0 \
  --artifact-dir /tmp/deepagent_e2e
```

当前 [e2e_runner.py](../../cmd/cloud_agent/tests/e2e_runner.py) 的 P0 包含：

| Case | 核心断言 |
| --- | --- |
| `basic_turn` | 提交输入后出现成功终态和助手结果 |
| `workspace_minimal` | 模型使用工具写文件，文件 API 读回符合要求的内容 |
| `streaming_recovery` | 收到 SSE queue/event frame，检查成功终态、timeline envelope 及 SSE 与历史事件 ID 的交集 |

当前 `streaming_recovery` 实现只建立一次订阅，没有实际模拟断线重连；通过这个
case 不能证明 `recover_queue_id` 的恢复行为。

只重跑一个 case：

```bash
python3 deepagent/cmd/cloud_agent/tests/e2e_runner.py \
  --profile deepagent/cmd/cloud_agent/tests/profiles/local.example.json \
  --case streaming_recovery \
  --artifact-dir /tmp/deepagent_e2e
```

每轮输出独立的 `run_id`，报告默认为
`/tmp/deepagent_e2e/<run_id>/report.json`；可用 `--out` 指定其他路径。
同目录保存各 case 的请求、timeline、SSE、文件结果和失败详情。

通过标准是报告 `status=passed` 且三个 case 全部 `passed`。当前 runner 在存在
`skipped`、没有 `failed` 时也会返回退出码 0，不能仅用退出码作为全通过判据。
`smoke` 成功也不等于 P0 成功。

失败时用报告中的 `session_id/thread_id/turn_id/message_id/logids` 关联
`deepagent/cmd/cloud_agent/runtime/dev/logs/cloud_agent.log` 和 YAML 指定的 Worker
内部日志。依据终态、事件归属和文件副作用排查，保留报告与工作区证据。

测试后只停止自己确认归属的 dev.py 实例；已有后端、GoLand 进程或未知端口不能
盲目终止。E2E 产物清理和业务数据清理应单独确认范围。
