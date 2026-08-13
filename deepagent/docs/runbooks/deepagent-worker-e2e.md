# CloudAgent Worker 真实链路验证

本文只保留 Worker 真实链路的启动入口。完整 case、断言和产物结构以
[`docs/testing/cloud-agent-test-suite.md`](../testing/cloud-agent-test-suite.md) 为准。

旧版 `cmd/cloud_agent worker`、通用 `AGENT_WORKER_*` 行为覆盖和 Worker CLI 配置
flag 已删除。当前参考 Worker 通过 `cloudagent/worker/bootstrap` 启动，除配置文件
选择外，所有行为配置只来自一份 YAML。

## 1. 配置

本地默认文件是：

```text
cmd/cloud_agent/aic_agent_sdk_worker/conf/worker.local.yml
```

远端默认模板是：

```text
cmd/cloud_agent/aic_agent_sdk_worker/conf/worker.remote.yml
```

`-conf` 或 `AGENT_WORKER_CONF` 只负责选择文件。模型、Fornax、AC、MySQL、
Redis/Abase、checkpoint、runtime、backend 和日志必须写在所选 YAML 中。环境变量
只会在 YAML 显式写出 `${NAME}` 时作为占位值读取；未声明的环境变量不能覆盖配置。

本机模型固定使用 `https://super-relay.byted.org/v1` 的
`model_api/experimental_0630`。私有文件 `~/.config/aic_agent_sdk/model.env` 提供：

```bash
DEEPSEEK_API_KEY=<super-relay-token>
OPENAI_BASE_URL=https://super-relay.byted.org/v1
OPENAI_MODEL=model_api/experimental_0630
```

## 2. 启动本地四服务

先启动 `aic_agent_coordinator`，确保 `127.0.0.1:8888` 可用。随后：

```bash
cd /Users/bytedance/workspace/go/src/code.byted.org/ad/aic_agent_sdk
python3 cmd/cloud_agent/dev.py doctor
python3 cmd/cloud_agent/dev.py start
python3 cmd/cloud_agent/dev.py status
```

`dev.py` 的 `worker_config` 字段可以选择另一份 YAML，但不会把
`dev_config.json` 中的值当作 Worker 行为覆盖。

## 3. 运行 P0 主链路

```bash
python3 cmd/cloud_agent/tests/e2e_runner.py \
  --profile cmd/cloud_agent/tests/profiles/local.example.json \
  --suite p0 \
  --out /tmp/aic_agent_sdk_e2e_report.json
```

P0 必须同时通过：

- `basic_turn`：发送消息并收到模型结果。
- `workspace_minimal`：模型调用工具写文件，API 能读回。
- `streaming_recovery`：等待工具/模型事件，断流后仍能恢复最终结果。

失败时以报告中的 `run_id/session_id/thread_id/turn_id/message_id` 和产物目录为准，
不要用固定 sleep 或自然语言回复相似度代替状态与副作用断言。

## 4. 停止

```bash
python3 cmd/cloud_agent/dev.py stop
```
