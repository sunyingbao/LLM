# Cloud Agent Local Runbook for Codex

这份 runbook 的目标是让一台有 Go 编译器、能访问 `aic_agent_sdk` 和 `aic_agent_coordinator` 仓库的机器，把本地 Cloud Agent 全链路跑起来。

它面向 Codex 执行，不是架构说明。执行时优先跑命令、看成功判据、按排错路径收敛问题。

## 0. 目标链路

本地需要同时运行四类组件：

1. MySQL：存 `aic_agent_coordinator` 控制面表、`aic_agent_sdk_session` 表、worker history 表、memory 表。
2. Redis：支撑 `aic_agent_coordinator` mailbox 和 session stream。
3. `aic_agent_coordinator`：监听 `127.0.0.1:8888`。
4. `aic_agent_sdk/cmd/cloud_agent` 三服务：
   - `aic_agent_sdk_session`，默认 `127.0.0.1:8890`
   - `aic_agent_sdk_worker`
   - `aic_agent_sdk_api`，默认 `127.0.0.1:6789`

最终成功判据：

- `aic_agent_coordinator` 监听 `:8888`
- `python3 cmd/cloud_agent/dev.py doctor` 全部通过
- `python3 cmd/cloud_agent/dev.py start` 成功
- `python3 cmd/cloud_agent/dev.py smoke` 成功
- 浏览器能打开 `http://127.0.0.1:6789/`

## 1. 给 Codex 的执行 Prompt

可以把下面这段直接交给对方机器上的 Codex：

```text
请在这台机器上把 Cloud Agent 本地全链路跑起来。你有 Go 编译器，并且有 code.byted.org 上 `ad/aic_agent_sdk` 和 `ad/aic_agent_coordinator` 两个仓库权限。

目标：
1. 准备或确认本地 MySQL、Redis。
2. 如果机器上已有旧版部署，先按 runbook 的“已有部署升级流程”停旧服务、更新代码、补齐 schema、重生成配置，再启动。
3. 启动 `aic_agent_coordinator`，监听 `127.0.0.1:8888`。
4. 用 `aic_agent_sdk/cmd/cloud_agent/dev.py` 配置并启动 `aic_agent_sdk_session`、`aic_agent_sdk_worker`、`aic_agent_sdk_api`。
5. 跑通 `doctor`、`smoke`，最后给出 Web URL、进程状态、日志路径。

约束：
- 不要把模型 key、token、密码明文贴到对话里。
- 如果需要模型 key，让我提供一个本地 env 文件路径，或让我确认当前 shell 已经有模型环境变量。
- 主动提醒用户建议开启 Fornax trace，便于后续排查 worker/model/tool 链路；按 runbook 的“Fornax Trace”小节说明需要用户提供什么，以及如何写入 `dev_config.json`。
- 不要修改业务代码，除非启动脚本或文档明显有 bug。
- 遇到失败先看日志，再做最小修复；不要删除数据库或清空 Redis，除非我明确同意。
- 如果已有 `~/.config/aic_agent_sdk/dev_config.json`，不要用 `configure --defaults --force` 覆盖它；只在配置缺失时创建默认配置。

执行步骤请参考仓库内 `docs/runbooks/cloud-agent-codex-local-runbook.md`，按里面的命令和成功判据执行。
```

## 2. 仓库准备

建议把两个仓库放在同一个父目录下：

```bash
export WORKROOT="$HOME/go/src/code.byted.org/cc-family-infra"
mkdir -p "$WORKROOT"

cd "$WORKROOT"
test -d aic_agent_coordinator || git clone ssh://git@code.byted.org/ad/aic_agent_coordinator.git
test -d aic_agent_sdk || git clone ssh://git@code.byted.org/ad/aic_agent_sdk.git

export AC_REPO="$WORKROOT/aic_agent_coordinator"
export SDK_REPO="$WORKROOT/aic_agent_sdk"
```

如果仓库已经存在，先只做非破坏性检查：

```bash
cd "$AC_REPO" && git status --short
cd "$SDK_REPO" && git status --short
```

有本地改动时不要重置，先继续做环境启动。

### 2.1 推荐的一键启动

本机已经安装 `cloud-agent-start` 时，直接执行：

```bash
cloud-agent-start
```

没有安装快捷命令时，从 SDK 仓库执行同一份脚本：

```bash
cd "$SDK_REPO"
./cmd/cloud_agent/start_local_demo.sh
```

脚本会检查 MySQL、Redis 和模型环境，补齐 schema，生成权限为 `0600` 的
`/tmp/cloud_agent_demo/aic_agent_coordinator.local.yml`，通过
`AIC_AGENT_COORDINATOR_CONFIG` 启动 Coordinator，注册本地 namespace，再启动 SDK
Session、Worker、API 并执行 smoke。停止整套服务使用 `cloud-agent-stop` 或
`./cmd/cloud_agent/stop_local_demo.sh`。

### 2.2 已有部署升级流程

如果目标机器之前已经按本 runbook 启动过服务，不要直接 `git pull` 后复用旧进程。按下面顺序升级，目标是让 Codex 自己处理代码更新、schema 补齐、配置重生成和旧进程残留。

先停旧的 Cloud Agent 三服务：

```bash
cd "$SDK_REPO"
python3 cmd/cloud_agent/dev.py stop || true
```

确认没有旧 `aic_agent_sdk_worker`、`aic_agent_sdk_api`、`aic_agent_sdk_session` 残留。尤其要避免多个 `aic_agent_sdk_worker` 同时抢同一个 namespace 的线程：

```bash
ps -ef | grep -E 'aic_agent_sdk_worker|aic_agent_sdk_api|aic_agent_sdk_session|cmd/cloud_agent' | grep -v grep || true
ss -ltnp | grep -E ':8890|:6789' || true
```

如果还看到旧进程：

- 若 pid 来自 `cmd/cloud_agent/runtime/dev/pids/` 或明显是上一次 `dev.py` 启动的 Cloud Agent 进程，可以停止它。
- 若 pid 来源不明确，先让用户确认，不要误杀其他服务。

更新两个仓库代码。已有本地改动时不要重置，先报告并让用户确认：

```bash
cd "$AC_REPO"
git status --short
git fetch --all --prune
git pull --ff-only

cd "$SDK_REPO"
git status --short
git fetch --all --prune
git pull --ff-only
```

如果 `git pull --ff-only` 因本地改动或分叉失败，停止升级，报告 `git status --short` 和失败原因；不要 `reset --hard`。

如果已有 `~/.config/aic_agent_sdk/dev_config.json`，保留它。`dev.py start` 通过其中的 `worker_config` 选择一个 Worker YAML；该 YAML 是 Worker 唯一的行为配置源。`dev_config.json` 只负责三服务编排和给 YAML 中显式 `${NAME}` 占位符提供本机值，不再覆盖 namespace、AC、模型、Fornax、runtime 等字段。只有配置文件不存在时才创建默认配置：

```bash
test -f "$HOME/.config/aic_agent_sdk/dev_config.json" || \
  python3 cmd/cloud_agent/dev.py configure --defaults --force
```

补齐 Cloud Agent 业务表。当前 `init-db` 会创建缺失的 worker history 表和 memory 表，也会补 `t_agent_session` 缺失的 `project_name`、`project_path`、`idx_uid_project_active`：

```bash
cd "$SDK_REPO"
python3 cmd/cloud_agent/dev.py init-db
```

升级后快速看一下关键 schema。这里不要求输出完全一致，但至少应该能看到 `t_memory_*` 四张表，以及 `t_agent_session` 的项目字段：

```bash
MYSQL_PWD=ac_test_pwd_20260416 mysql -h127.0.0.1 -P3306 -uac_test -D agent_coordinator_test -e "
SHOW TABLES LIKE 't_memory%';
SHOW COLUMNS FROM t_agent_session LIKE 'project_name';
SHOW COLUMNS FROM t_agent_session LIKE 'project_path';
SHOW INDEX FROM t_agent_session WHERE Key_name='idx_uid_project_active';
"
```

如果之前跑过 memory 试验版本，本地库里可能已有旧结构 memory 表。`init-db` 不会自动迁移已有旧版 memory 表；如果后续 worker 日志出现 `Unknown column 'id'`、`Unknown column 'last_stage1_output_key'`、`Unknown column 'input_watermark'` 等错误，说明 memory 表结构过旧。测试数据可丢弃时，先停 Cloud Agent，drop 四张 `t_memory_*` 表，再重新执行 `init-db`。如果数据需要保留，先让用户确认迁移策略，不要直接删表。

重启 `aic_agent_coordinator`。如果旧进程是本 runbook 用 `/tmp/cloud_agent_demo/aic_agent_coordinator.pid` 启动的，可以先停再按第 5 节启动；如果 `:8888` 是未知进程占用，先让用户确认：

```bash
if [ -f /tmp/cloud_agent_demo/aic_agent_coordinator.pid ]; then
  kill "$(cat /tmp/cloud_agent_demo/aic_agent_coordinator.pid)" || true
fi
ss -ltnp | grep ':8888' || true
```

然后按第 5 节启动 `aic_agent_coordinator`，再启动 Cloud Agent：

```bash
cd "$SDK_REPO"
python3 cmd/cloud_agent/dev.py doctor
python3 cmd/cloud_agent/dev.py start
python3 cmd/cloud_agent/dev.py status
python3 cmd/cloud_agent/dev.py smoke
```

启动后重点检查 worker 内部日志，确认没有 schema 错误、重复 worker 抢线程、memory consolidation 配置不一致等问题：

```bash
tail -n 160 cmd/cloud_agent/runtime/dev/logs/worker_internal/cloud_agent_worker.log
grep -E 'Unknown column|requires memory enabled|process thread failed|panic|fatal' \
  cmd/cloud_agent/runtime/dev/logs/worker_internal/cloud_agent_worker.log || true
```

如果要启用 memory 或 Fornax，直接修改 `worker_config` 指向的 YAML，然后重新 `dev.py start`。默认本地 YAML不会自动打开 memory 或 Fornax。

## 3. 基础依赖检查

```bash
go version
python3 --version
mysql --version
redis-cli --version
```

检查端口：

```bash
ss -ltnp | grep -E ':3306|:6379|:8888|:8890|:6789' || true
```

如果 `mysql` 或 `redis-cli` 不存在，需要先让机器安装 MySQL/MariaDB client 和 Redis client/server。Codex 不应猜测系统包管理方式，先向用户确认。

## 4. MySQL 与 Redis

默认本地配置使用：

```text
MySQL: 127.0.0.1:3306
Redis: 127.0.0.1:6379
DB:    agent_coordinator_test
User:  ac_test
Pass:  ac_test_pwd_20260416
```

使用 `cmd/cloud_agent/start_local_demo.sh` 时，脚本会自动创建该数据库并授权给
`ac_test`。默认使用本地 `root` 管理员账号和空密码；环境不同时通过
`MYSQL_ADMIN_USER`、`MYSQL_ADMIN_PASSWORD` 覆盖。直接使用 `dev.py` 时仍需先按
下面的命令准备数据库和账号。该脚本还会在 Coordinator 启动后通过
`RegisterAgentNamespace` 幂等注册 `cloud_agent`，因此空库不需要手工插入 namespace。

先检查服务是否可用：

```bash
redis-cli -h 127.0.0.1 -p 6379 ping
MYSQL_PWD=ac_test_pwd_20260416 mysql -h127.0.0.1 -P3306 -uac_test -e "SELECT 1;"
```

如果 MySQL 用户或库不存在，使用有权限的本地账号创建：

```bash
sudo mysql <<'SQL'
CREATE DATABASE IF NOT EXISTS agent_coordinator_test DEFAULT CHARSET utf8mb4;
CREATE USER IF NOT EXISTS 'ac_test'@'%' IDENTIFIED BY 'ac_test_pwd_20260416';
CREATE USER IF NOT EXISTS 'ac_test'@'localhost' IDENTIFIED BY 'ac_test_pwd_20260416';
GRANT ALL PRIVILEGES ON agent_coordinator_test.* TO 'ac_test'@'%';
GRANT ALL PRIVILEGES ON agent_coordinator_test.* TO 'ac_test'@'localhost';
FLUSH PRIVILEGES;
SQL
```

### 4.1 导入 aic_agent_coordinator 控制面表

`aic_agent_coordinator` 需要 4 张控制面表，DDL 都在 `aic_agent_coordinator/sql/` 下：

| 表 | DDL |
| --- | --- |
| `t_agent_namespace` | `sql/t_agent_namespace.sql` |
| `t_thread` | `sql/t_thread.sql` |
| `t_mailbox_message` | `sql/t_mailbox_message.sql` |
| `t_event_log` | `sql/t_event_log.sql` |

导入命令：

```bash
cd "$AC_REPO"
for f in sql/t_agent_namespace.sql sql/t_thread.sql sql/t_mailbox_message.sql sql/t_event_log.sql; do
  MYSQL_PWD=ac_test_pwd_20260416 mysql -h127.0.0.1 -P3306 -uac_test agent_coordinator_test < "$f"
done

MYSQL_PWD=ac_test_pwd_20260416 mysql -h127.0.0.1 -P3306 -uac_test -D agent_coordinator_test -e "SHOW TABLES;"
```

至少应看到：

```text
t_agent_namespace
t_thread
t_mailbox_message
t_event_log
```

### 4.2 导入 Cloud Agent 业务表

Cloud Agent 自己还需要 session、worker history 和 memory 相关表。session 服务表仍在 `cmd/cloud_agent` 下；worker 默认表是 SDK 约定，DDL 放在 `cloudagent/worker/sql/` 下；memory 表 DDL 放在 `deepagents/memory/gorm_store/sql/` 下：

| 表 | DDL | 用途 |
| --- | --- | --- |
| `t_agent_session` | `aic_agent_sdk_session/sql/t_agent_session.sql` | session 列表、主 thread 绑定、项目目录 |
| `t_agent_thread_ref` | `../../cloudagent/worker/sql/t_agent_thread_ref.sql` | session 内 friendly thread name 到 AC thread_id 的映射 |
| `t_agentthread_history` | `../../cloudagent/worker/sql/t_agentthread_history.sql` | DeepAgentThread 历史、上下文压缩记录 |
| `t_memory_source` | `../../deepagents/memory/gorm_store/sql/t_memory_source.sql` | memory stage1 输入源状态 |
| `t_memory_stage1_output` | `../../deepagents/memory/gorm_store/sql/t_memory_stage1_output.sql` | stage1 产出的原始记忆 |
| `t_memory_stage2_job` | `../../deepagents/memory/gorm_store/sql/t_memory_stage2_job.sql` | 用户维度 stage2 consolidation job 状态 |
| `t_memory_baseline` | `../../deepagents/memory/gorm_store/sql/t_memory_baseline.sql` | stage2 文件基线与幂等校验 |

推荐直接用 dev 脚本建表，它会导入上述表，并补齐老表缺失的 `project_name`、`project_path`、`idx_uid_project_active`：

```bash
cd "$SDK_REPO"
test -f "$HOME/.config/aic_agent_sdk/dev_config.json" || \
  python3 cmd/cloud_agent/dev.py configure --defaults --force
python3 cmd/cloud_agent/dev.py init-db
```

如果要手动导入，执行：

```bash
cd "$SDK_REPO/cmd/cloud_agent"
for f in \
  aic_agent_sdk_session/sql/t_agent_session.sql \
  ../../cloudagent/worker/sql/t_agent_thread_ref.sql \
  ../../cloudagent/worker/sql/t_agentthread_history.sql \
  ../../deepagents/memory/gorm_store/sql/t_memory_source.sql \
  ../../deepagents/memory/gorm_store/sql/t_memory_stage1_output.sql \
  ../../deepagents/memory/gorm_store/sql/t_memory_stage2_job.sql \
  ../../deepagents/memory/gorm_store/sql/t_memory_baseline.sql; do
  MYSQL_PWD=ac_test_pwd_20260416 mysql -h127.0.0.1 -P3306 -uac_test agent_coordinator_test < "$f"
done

MYSQL_PWD=ac_test_pwd_20260416 mysql -h127.0.0.1 -P3306 -uac_test -D agent_coordinator_test -e "SHOW TABLES LIKE 't_agent%'; SHOW TABLES LIKE 't_memory%';"
```

至少应看到：

```text
t_agent_namespace
t_agent_session
t_agent_thread_ref
t_agentthread_history
t_memory_source
t_memory_stage1_output
t_memory_stage2_job
t_memory_baseline
```

如果这台机器之前跑过旧版本，本地库里可能已有旧结构 memory 表。`dev.py init-db` 只负责创建缺失表，不负责自动迁移已有旧表；如果启动后 worker 日志出现 `Unknown column 'id'`、`Unknown column 'last_stage1_output_key'` 等 schema 错误，说明本地 memory 表结构过旧。测试数据可丢弃时，先停服务，drop 这 4 张 `t_memory_*` 表后重新执行 `python3 cmd/cloud_agent/dev.py init-db`；如果数据需要保留，先让用户确认迁移策略，不要直接删表。

## 5. 启动 aic_agent_coordinator

推荐直接使用 `start_local_demo.sh`，不要修改仓库内的 `conf/conf.yml`。如果需要手动
启动，先创建一份不进入 Git 的本地直连配置：

```yaml
mysql:
  dsn: ac_test:ac_test_pwd_20260416@tcp(127.0.0.1:3306)/agent_coordinator_test?charset=utf8mb4&parseTime=True&loc=Local
  db_name: agent_coordinator_test
abase:
  addr: 127.0.0.1:6379
  db: 0
```

例如把它保存为 `/tmp/cloud_agent_demo/aic_agent_coordinator.local.yml`，权限设为
`0600`。启动时必须显式传入该文件：

```bash
cd "$AC_REPO"
mkdir -p /tmp/cloud_agent_demo /tmp/aic_agent_coordinator_kitex_log
chmod 600 /tmp/cloud_agent_demo/aic_agent_coordinator.local.yml
nohup env \
  AIC_AGENT_COORDINATOR_CONFIG=/tmp/cloud_agent_demo/aic_agent_coordinator.local.yml \
  KITEX_CONFIG_SOURCE=file \
  KITEX_LOG_DIR=/tmp/aic_agent_coordinator_kitex_log \
  go run . \
  > /tmp/cloud_agent_demo/aic_agent_coordinator.log 2>&1 &
echo $! > /tmp/cloud_agent_demo/aic_agent_coordinator.pid
```

成功判据：

```bash
sleep 3
ss -ltnp | grep ':8888'
tail -n 80 /tmp/cloud_agent_demo/aic_agent_coordinator.log
```

如果没有监听 `:8888`，先看 `/tmp/cloud_agent_demo/aic_agent_coordinator.log`。常见原因是 MySQL/Redis 不通、没有设置 `AIC_AGENT_COORDINATOR_CONFIG`、外置配置缺少 `mysql.dsn`、Go 私有依赖拉取失败。

## 6. 模型环境

Cloud Agent worker 必须能调用模型。当前本机联调统一使用
`https://super-relay.byted.org/v1` 的 `model_api/experimental_0630`。凭证只写入用户目录，
不提交到仓库：

```bash
install -d -m 700 "$HOME/.config/aic_agent_sdk"
umask 077
cat > "$HOME/.config/aic_agent_sdk/model.env" <<'EOF'
DEEPSEEK_API_KEY=<super-relay-token>
OPENAI_BASE_URL=https://super-relay.byted.org/v1
OPENAI_MODEL=model_api/experimental_0630
EOF
chmod 600 "$HOME/.config/aic_agent_sdk/model.env"
```

让用户填入真实 token。不要打印文件内容，也不要把 token 写进仓库文件。`worker.local.yml` 统一使用 OpenAI-compatible provider，并通过 `${OPENAI_BASE_URL}`、`${OPENAI_MODEL}` 引用本地私有配置；模型切换只修改 `model.env`，无需再改 YAML。

`dev.py` 在 `model_env.mode=file` 时从该文件读取 YAML 明确引用的密钥，并清除父 shell继承的模型供应商变量。

### 6.1 推荐：开启 Fornax Trace

Fornax 不是默认开启项，但对于演示、联调、交付给其他人试用的环境，Codex 应主动提醒用户开启。开启后可以在 Fornax 里按 logid/trace 查看 worker、model、tool 调用链路，后续排查问题会快很多。

开启 Fornax 时，把密钥放进同一个本地 `model.env`，并让 Worker YAML 显式引用。不要把真实值贴到对话或仓库：

```bash
cat >> "$HOME/.config/aic_agent_sdk/model.env" <<'EOF'
FORNAX_AK=...
FORNAX_SK=...
EOF
grep -Eq '^FORNAX_AK=' "$HOME/.config/aic_agent_sdk/model.env" && echo 'fornax ak present'
grep -Eq '^FORNAX_SK=' "$HOME/.config/aic_agent_sdk/model.env" && echo 'fornax sk present'
```

在 `worker.local.yml` 中配置；如果本机使用复制出的私有 YAML，则修改 `dev_config.json.worker_config` 指向该文件：

```yaml
fornax:
  enabled: true
  ak: ${FORNAX_AK}
  sk: ${FORNAX_SK}
  region: CN
  http_timeout_ms: 10000
```

重启 Cloud Agent 后只检查 YAML 开关和 Worker 日志，不打印密钥：

```bash
cd "$SDK_REPO"
python3 cmd/cloud_agent/dev.py stop
python3 cmd/cloud_agent/dev.py start

grep -A5 '^fornax:' cmd/cloud_agent/aic_agent_sdk_worker/conf/worker.local.yml
grep -E 'fornax trace ready|init fornax trace|init fornax client|fornax\\.ak is required|fornax\\.sk is required' \
  cmd/cloud_agent/runtime/dev/logs/worker_internal/cloud_agent_worker.log || true
```

成功时 Worker 内部日志应出现 `fornax trace ready`。如果启动失败并提示缺少环境变量或 `fornax.ak/sk is required`，检查 YAML 占位符与 `model.env` 中的变量名是否一致。

如果用户暂时没有 Fornax AK/SK，可以先不开启 Fornax，把 Cloud Agent 基础链路跑通；但最终交付或演示前，建议补上 Fornax trace。

## 7. 配置并启动 Cloud Agent 三服务

进入 SDK 仓库：

```bash
cd "$SDK_REPO"
```

写默认配置：

```bash
test -f "$HOME/.config/aic_agent_sdk/dev_config.json" || \
  python3 cmd/cloud_agent/dev.py configure --defaults --force
```

把配置里的 model env 文件指向用户目录下的私有文件：

```bash
python3 - <<'PY'
import json
from pathlib import Path

path = Path.home() / ".config" / "aic_agent_sdk" / "dev_config.json"
cfg = json.loads(path.read_text())
cfg["model_env"] = {"mode": "file", "file": str(Path.home() / ".config" / "aic_agent_sdk" / "model.env")}
cfg["agent_coordinator"]["mode"] = "direct"
cfg["agent_coordinator"]["hostports"] = ["127.0.0.1:8888"]
cfg["agent_coordinator"]["namespace"] = "cloud_agent"
cfg["mysql_dsn"] = "ac_test:ac_test_pwd_20260416@tcp(127.0.0.1:3306)/agent_coordinator_test?charset=utf8mb4&parseTime=True&loc=Local"
cfg["redis"] = {"addr": "127.0.0.1:6379", "password": "", "db": 0}
path.write_text(json.dumps(cfg, indent=2, ensure_ascii=False) + "\n")
print(path)
PY
```

跑依赖检查：

```bash
python3 cmd/cloud_agent/dev.py doctor
```

显式建表：

```bash
python3 cmd/cloud_agent/dev.py init-db
```

成功后启动三服务：

```bash
python3 cmd/cloud_agent/dev.py start
```

成功判据：

```bash
python3 cmd/cloud_agent/dev.py status
python3 cmd/cloud_agent/dev.py smoke
python3 cmd/cloud_agent/dev.py logs
```

Web 地址：

```text
http://127.0.0.1:6789/
```

如果是在远端机器运行，需要用 SSH 端口转发或浏览器能访问远端 `6789` 端口。

## 8. 日志与进程位置

Cloud Agent dev 脚本的运行产物默认在：

```text
cmd/cloud_agent/runtime/dev/
```

常用文件：

```text
cmd/cloud_agent/runtime/dev/pids/
cmd/cloud_agent/runtime/dev/logs/aic_agent_sdk_session.log
cmd/cloud_agent/runtime/dev/logs/aic_agent_sdk_worker.log
cmd/cloud_agent/runtime/dev/logs/aic_agent_sdk_api.log
cmd/cloud_agent/runtime/dev/logs/worker_internal/cloud_agent_worker.log
cmd/cloud_agent/aic_agent_sdk_worker/conf/worker.local.yml
```

停止 Cloud Agent：

```bash
cd "$SDK_REPO"
python3 cmd/cloud_agent/dev.py stop
```

停止 `aic_agent_coordinator`：

```bash
kill "$(cat /tmp/cloud_agent_demo/aic_agent_coordinator.pid)"
```

## 9. 常见失败与处理

### `doctor` 报 `agent_coordinator missing`

检查：

```bash
ss -ltnp | grep ':8888'
tail -n 100 /tmp/cloud_agent_demo/aic_agent_coordinator.log
```

通常是 `aic_agent_coordinator` 没启动或启动后退出。

如果日志包含 `mysql.psm or mysql.dsn is required`，说明 Coordinator 回退读取了仓库
内的 `conf/conf.yml`。使用修复后的 `cloud-agent-start`，或在手动启动命令中显式设置
`AIC_AGENT_COORDINATOR_CONFIG`，不要把本地 DSN 写回受版本控制的配置。

### `doctor` 报 `model env missing`

检查 `~/.config/aic_agent_sdk/model.env` 是否真的包含固定模型配置。不要把 key
打印到对话里，只打印非敏感的模型名和 base URL：

```bash
grep -E '^(OPENAI_BASE_URL|OPENAI_MODEL)=' "$HOME/.config/aic_agent_sdk/model.env"
```

### `start` 失败且端口占用

查看占用：

```bash
ss -ltnp | grep -E ':8890|:6789'
```

如果是上一次 `dev.py` 启动的进程：

```bash
python3 cmd/cloud_agent/dev.py stop
python3 cmd/cloud_agent/dev.py start
```

如果是未知进程，先让用户确认是否能停。

### worker 启动后没有处理消息

优先确认静态 Local profile，以及启动日志里的 env 覆盖后有效配置：

```bash
sed -n '1,160p' cmd/cloud_agent/aic_agent_sdk_worker/conf/worker.local.yml
grep -E '\[cloud_agent worker\] config:' cmd/cloud_agent/runtime/dev/logs/worker_internal/cloud_agent_worker.log | tail -1
tail -n 120 cmd/cloud_agent/runtime/dev/logs/worker_internal/cloud_agent_worker.log
```

静态 profile 的 `coordinator.direct_hostports` 可以为空；`dev.py` 会从 `dev_config.json` 注入本机地址。有效日志中应显示 direct hostports 已启用，namespace 应该和 `aic_agent_sdk_api`/`aic_agent_sdk_session` 使用的 namespace 一致。

### Go 私有依赖拉取失败

先确认公司代码权限和 Git 认证：

```bash
go env GOPRIVATE
git ls-remote ssh://git@code.byted.org/ad/aic_agent_sdk.git HEAD
git ls-remote ssh://git@code.byted.org/ad/aic_agent_coordinator.git HEAD
```

如果权限失败，让用户先完成公司 Git/Codebase 登录，不要在文档或对话里粘贴 token。

## 10. 下午演示建议

演示前在目标机器保留三个终端或让 Codex 分后台进程管理：

1. `aic_agent_coordinator` 日志：`tail -f /tmp/cloud_agent_demo/aic_agent_coordinator.log`
2. Cloud Agent 状态：`python3 cmd/cloud_agent/dev.py status`
3. Web 页面：`http://127.0.0.1:6789/`

现场如果时间紧，只需要完成：

```bash
cd "$SDK_REPO"
python3 cmd/cloud_agent/dev.py doctor
python3 cmd/cloud_agent/dev.py start
python3 cmd/cloud_agent/dev.py smoke
```

`smoke` 成功后再打开 Web。不要一边排 DB/Redis，一边改业务代码。
