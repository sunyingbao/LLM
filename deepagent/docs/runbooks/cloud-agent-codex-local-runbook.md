# DeepAgent 本地运行

当前入口是 [main.go](../../cmd/cloud_agent/main.go)：一个 `cloud_agent` 进程同时运行
HTTP API、Session、Worker 和进程内 Coordinator。外部依赖是 MySQL、Redis 和模型
服务。以下命令均从仓库根目录执行。

## 配置本机环境

准备仓库 `go.mod` 要求的 Go、Python 3、MySQL 客户端，以及可连接的 MySQL 和
Redis 服务。使用专门的本地测试数据库；数据库账号需要执行建库、建表和必要的
`ALTER TABLE`。已有服务先确认用途，不要为了腾出端口盲目停止或重启服务。

首次配置：

```bash
python3 deepagent/cmd/cloud_agent/dev.py configure
```

配置默认保存到 `~/.config/deepagent/dev_config.json`；设置 `XDG_CONFIG_HOME` 时，
使用该目录下的 `deepagent/dev_config.json`。已有配置会保留，不要用
`configure --defaults --force` 覆盖。可在子命令前加
`--config /绝对路径/dev_config.json` 选择另一份配置。

| 字段 | 用途 |
| --- | --- |
| `mysql_dsn` | 格式为 `用户名:密码@tcp(主机:端口)/数据库?parseTime=true` |
| `redis.addr/password/db` | 本机依赖记录；doctor 使用地址检查 TCP 连通性 |
| `worker_config` | 实际使用的 Worker YAML 绝对路径 |
| `model_env` | `mode` 选 `shell` 或 `file`；后者的 `file` 指向私有环境文件 |
| `workspace_root`、`local_uid` | 本地项目工作区和测试用户 |
| `ports.cloud_agent` | HTTP 端口，默认 `6789` |

默认 Worker 模板是 [worker.local.yml](../../cmd/cloud_agent/conf/worker.local.yml)。
有本机差异时，可复制为 `~/.config/deepagent/worker.local.yml`，再修改
`worker_config`。Worker 行为由 YAML 决定；环境变量只有被 YAML 的 `${NAME}`
显式引用才会展开。实际 Redis 连接来自 YAML 的 `abase`，应与 JSON 中的 Redis
配置一致，JSON 不会自动覆盖 YAML。

模型环境文件建议使用 `~/.config/deepagent/model.env`，在本机编辑器中填写：

```dotenv
OPENAI_API_KEY=<模型服务凭证>
OPENAI_BASE_URL=<OpenAI-compatible服务地址>
OPENAI_MODEL=<模型标识>
```

将 `model_env.mode` 设为 `file`，`model_env.file` 设为该文件的绝对路径。
当前模板引用上面三个变量，不固定具体供应商。`file` 模式会先清除父 shell 继承的
模型供应商变量，再读取文件。配置可能含凭证，限制为本人可读，不提交到 Git，
不在对话或日志中打印内容。

[dev.py](../../cmd/cloud_agent/dev.py) 设置的主要环境变量如下，通常无需手动导出：

| 环境变量 | 来源 |
| --- | --- |
| `AGENT_WORKER_CONF` | `worker_config`，选择 YAML |
| `AGENT_WORKER_MYSQL_DSN` | `mysql_dsn`，供本地 YAML 引用 |
| `DEEP_AGENT_SDK_WORKSPACE_ROOT` | `workspace_root`，供本地 YAML 引用 |
| `DEEP_AGENT_SDK_API_ADDRESS` | `127.0.0.1:<ports.cloud_agent>` |
| `DEEP_AGENT_SDK_API_AUTH_MODE` | 本地开发使用 `local` |
| `DEEP_AGENT_SDK_API_LOCAL_DEFAULT_UID` | `local_uid` |
| `DEEP_AGENT_SDK_API_WORKSPACE_ROOT`、`DEEP_AGENT_SDK_API_BACKEND_LOCAL_ROOT` | `workspace_root` |
| `DEEP_AGENT_SDK_API_BACKEND_TYPE` | 本地开发使用 `local` |

单进程入口让 API、Session 和 Worker 共用 Coordinator，并从 Worker YAML 统一
MySQL、namespace 和 backend 配置。本地认证模式仅用于本机开发。

## 初始化与启动

```bash
python3 deepagent/cmd/cloud_agent/dev.py doctor
python3 deepagent/cmd/cloud_agent/dev.py init-db
```

`doctor` 检查命令、MySQL/Redis TCP、模型凭证是否存在以及 YAML 文件是否存在；
它不验证模型调用、数据库权限或完整业务链路。

`init-db` 导入缺失的 Coordinator、Session、history、thread reference 和 memory
表，并补齐已实现的 Session 项目字段、history sequence 字段及索引。它不是通用
旧表迁移工具。DDL 来源为 [coordinator/sql](../../coordinator/sql)、
[Session SQL](../../cmd/cloud_agent/deep_agent_sdk_session/sql)、
[Worker SQL](../../cloud/worker/sql) 和 [memory SQL](../../core/memory/gorm_store/sql)。

首次使用空测试库还需要注册 YAML 的 `worker.namespace`；`init-db` 目前只建表，
不会注册 namespace。使用本机已有的 MySQL 客户端连接到上述同一个测试库，
默认 namespace 为 `cloud_agent` 时执行：

```sql
INSERT INTO t_agent_namespace
  (namespace_id, namespace, description, created_by, metadata_json, created_at, updated_at)
SELECT 1, 'cloud_agent', 'Local development', 'local', '{}', NOW(6), NOW(6)
WHERE NOT EXISTS (SELECT 1 FROM t_agent_namespace WHERE namespace = 'cloud_agent');

SELECT namespace FROM t_agent_namespace WHERE namespace = 'cloud_agent';
```

示例 ID `1` 只适用于空测试库；已有其他 namespace 时选择未占用的 ID，保留原记录。
修改了 YAML namespace 时同步修改 SQL；最后的查询应返回配置中的 namespace。

```bash
python3 deepagent/cmd/cloud_agent/dev.py start
python3 deepagent/cmd/cloud_agent/dev.py status
python3 deepagent/cmd/cloud_agent/dev.py smoke
```

`start` 会再次检查依赖、幂等建表、编译并启动 `cloud_agent`。已有记录中的进程
仍存活时会直接复用，不会自动加载新代码或配置。默认 Web 地址是
`http://127.0.0.1:6789/`，API 前缀为 `/ad/deep_agent_sdk`。
`smoke` 调用 `POST /ad/deep_agent_sdk/list_projects`。HTTP 可用后继续运行
[Worker E2E](deepagent-worker-e2e.md)，验证模型、工具和事件链路。

## 日志、重启与排错

```bash
python3 deepagent/cmd/cloud_agent/dev.py logs
```

运行产物位于 `deepagent/cmd/cloud_agent/runtime/dev/`，主要文件为
`pids/cloud_agent.pid`、`logs/cloud_agent.log` 和 `bin/cloud_agent`。
Worker 内部日志目录由 YAML 的 `log.dir` 指定。

更换代码或配置后，先核实 PID 对应当前工作区、本次 dev.py 管理的进程，再执行：

```bash
python3 deepagent/cmd/cloud_agent/dev.py stop
python3 deepagent/cmd/cloud_agent/dev.py start
```

`stop` 操作 PID 文件指向的进程组。已有进程、GoLand 启动的后端或未知端口占用，
不能仅凭端口号或陈旧 PID 文件盲目终止。先检查进程命令和所属工作区；无法确认
归属时保留进程并联系负责人，或为自己的实例配置另一个端口。不要直接清库、删表
或清空 Redis 来排查启动失败。

- `namespace not found`：核对 YAML namespace 和同一 MySQL 库中的注册记录。
- `Unknown column`：对照当前 DDL，制定保留数据的迁移；init-db 不会重建旧表。
- Redis 检查通过但 Worker 连接失败：核对 YAML `abase` 的地址、认证和 DB。
- 模型失败：核对私有环境文件、YAML 引用及模型服务响应，不输出凭证。
- 端口占用：核实所有者；start 不会接管未记录的监听进程。
- Go 依赖失败：检查工具链和私有模块访问权限，再重试构建。
