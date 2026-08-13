# Cloud Agent BOE Readiness Plan

本文档收敛 `cmd/cloud_agent` 三服务上 BOE 前的准备工作。当前阶段先处理两类可落地事项：

1. 输出 BOE conf，并把线上非 secret 配置从环境变量收敛到 conf。
2. 修正必开表的 idgen 生成链路，并关闭首发不需要的 `threadRef` 能力。

服务创建、mesh 端口、MySQL/Abase 可用性验证和完整 sandbox 验证不纳入本轮。MySQL 直接复用 `aic_agent_coordinator` 依赖；Abase 由 `aic_agent_sdk_worker` 用于按 thread 递增的 history seq 生成器，并在 checkpoint 使用 Redis 存储时复用为 checkpoint backend，配置同样复用 `aic_agent_coordinator` 的 Abase。Fornax AK/SK 作为 secret 仍允许走环境变量。

## 问题与范围

上线准备目标是让三个 cmd 服务有可 review、可上线的 BOE 配置输出，并保证启用的业务表符合线上规范。改造前有三类问题，本轮按这个边界闭环：

1. `aic_agent_sdk_worker` 已经支持 YAML conf，但只有本地 `conf.yml`，BOE 需要独立 conf 输出。
2. `aic_agent_sdk_session` 和 `aic_agent_sdk_api` 目前主要从环境变量和常量读取配置，BOE 上线前需要补 conf 读取。
3. 必开表的 schema 基本符合，但运行时需要接入线上 idgen；`t_agent_thread_ref` 是可选 friendly thread name 能力，BOE 首发可以关闭。

本计划覆盖这些配置输出：

| 服务 | 目标 BOE conf | 当前能力 | 本轮结论 |
| --- | --- | --- | --- |
| `cmd/cloud_agent/aic_agent_sdk_api` | `cmd/cloud_agent/aic_agent_sdk_api/conf/conf_china-boe.yml` | 仅 env / 常量 | 需要新增 conf loader |
| `cmd/cloud_agent/aic_agent_sdk_session` | `cmd/cloud_agent/aic_agent_sdk_session/conf/conf_china-boe.yml` | 仅 env / 本地默认 DSN | 需要新增 conf loader |
| `cmd/cloud_agent/aic_agent_sdk_worker` | `cmd/cloud_agent/aic_agent_sdk_worker/conf/worker.remote.yml` | 已有 YAML loader | 需要输出 BOE conf，并补缺失字段 |

Abase 直接使用范围：

| 服务 | 是否直接使用 Abase | 说明 |
| --- | --- | --- |
| `aic_agent_sdk_api` | 否 | 只通过 AC / aic_agent_sdk_session RPC 工作，不直接访问 Redis/Abase |
| `aic_agent_sdk_session` | 否 | 只访问 MySQL 和 AC RPC，不直接访问 Redis/Abase |
| `aic_agent_sdk_worker` | 是 | history seq generator 必须使用 Redis/Abase；当 `checkpoint.store` 使用 `redis://...` 时，同一个 Redis/Abase client 也会作为 checkpoint backend |

本计划覆盖这些表：

| 表 | 所属模块 | BOE 首发是否需要 |
| --- | --- | --- |
| `t_agent_session` | `cmd/cloud_agent/aic_agent_sdk_session` | 需要 |
| `t_agentthread_history` | `cmd/cloud_agent/aic_agent_sdk_worker` | 需要 |
| `t_memory_baseline` | `deepagents/memory/gorm_store` | 仅 memory 开启时需要 |
| `t_memory_source` | `deepagents/memory/gorm_store` | 仅 memory 开启时需要 |
| `t_memory_stage1_output` | `deepagents/memory/gorm_store` | 仅 memory 开启时需要 |
| `t_memory_stage2_job` | `deepagents/memory/gorm_store` | 仅 memory 开启时需要 |
| `t_agent_thread_ref` | `cmd/cloud_agent/aic_agent_sdk_worker` | BOE 首发关闭，不建表 |

## BOE Conf 输出目标

### 共同原则

- 非 secret 配置优先进入 conf，不依赖线上环境变量。
- Fornax AK/SK 是 secret，在 Worker YAML 中通过 `${FORNAX_AK}` / `${FORNAX_SK}` 显式引用部署注入的环境变量；环境变量不能覆盖其他行为字段。
- 端口配置不进入本计划，线上由 mesh 管理。
- direct hostports 只用于本地，不进入 BOE conf。
- MySQL 复用 AC 配置：
  - MySQL PSM：`toutiao.mysql.cc_family_agent_coordinator`
  - MySQL DB：`cc_family_agent_coordinator`
- `aic_agent_sdk_worker` 的 history seq generator 必须使用 Redis/Abase，BOE 复用 AC 配置；`checkpoint.store: redis://...` 时复用同一个 Redis/Abase client：
  - Abase PSM：`bytedance.abase2.cc_family_agent_coordinator`
  - Abase primary PSM：`bytedance.abase2.cc_family_agent_coordinator_primary`
  - Abase table：`aic_agent_sdk`
- Sandbox 参考 `cmd/ai_infra_sandbox`，BOE worker 使用 `pippit.sandbox.gateway`，`biz_type` 固定为 `test`。

### aic_agent_sdk_worker BOE conf 草案

`aic_agent_sdk_worker` 已有 YAML 配置能力，本轮应输出 `cmd/cloud_agent/aic_agent_sdk_worker/conf/worker.remote.yml`，关键字段如下：

```yaml
base_prompt: conf/prompt/base.md

worker:
  namespace: cloud_agent
  concurrency: 8
  scan_limit: 20
  message_limit: 50
  lease_ms: 60000
  scan_interval_ms: 1000
  message_poll_interval_ms: 500
  idle_timeout_ms: 10000
  shutdown_drain_timeout_ms: 120000
  shutdown_interrupt_drain_timeout_ms: 120000
  interrupt_drain_timeout_ms: 30000

coordinator:
  psm: ad.creative.aic_agent_coordinator
  cluster: ""
  direct_hostports: ""

mysql:
  psm: toutiao.mysql.cc_family_agent_coordinator
  db_name: cc_family_agent_coordinator
  dsn: ""
  read_dsn: ""
  read_timeout_ms: 5000

abase:
  psm: bytedance.abase2.cc_family_agent_coordinator
  primary_psm: bytedance.abase2.cc_family_agent_coordinator_primary
  table: aic_agent_sdk
  addr: ""
  password: ""
  db: 0
  read_timeout_ms: 500
  write_timeout_ms: 500

idgen:
  namespace: videocut_aigc_agent_coordinator

features:
  thread_refs:
    enabled: false

tables:
  history: t_agentthread_history
  thread_ref: ""
  memory_source: t_memory_source
  memory_stage1_output: t_memory_stage1_output
  memory_stage2_job: t_memory_stage2_job
  memory_baseline: t_memory_baseline

checkpoint:
  store: redis://cloud_agent

memory:
  enabled: false

models:
  default:
    sdk_type: ark
    model_name: doubao
    model_endpoint_id: "" # TODO: fill from BOE model config
    max_tokens: 32768

roles:
  main:
    models: [default]
    default_model: default
    approval_policy: normal
  explorer:
    models: [default]
    default_model: default
    approval_policy: readonly
  worker:
    models: [default]
    default_model: default
    approval_policy: permissive

runtime:
  workdir: /home/gem/aic_agent_sdk
  skills_dir: ""
  spawn_metadata_description: ""
  auto_compact_limit_tokens: 0
  compact_kept_user_tokens: 4000
  compact_prompt_append: ""

backend:
  type: ai_infra
  ai_infra:
    psm: pippit.sandbox.gateway
    biz_type: test
    biz_id_template: "user_{uid}"
    workdir_template: "/home/gem/aic_agent_sdk/{project_name}"
    action: SandboxToolCall

mcp:
  enabled: false
  request_timeout_ms: 300000
  region: ""
  servers: []

fornax:
  enabled: true
  ak: ${FORNAX_AK}
  sk: ${FORNAX_SK}
  region: ""
  http_timeout_ms: 3000

log:
  dir: /opt/tiger/log/cloud_agent/aic_agent_sdk_worker
  retention_days: 7
  enable_console: false
```

需要注意：`idgen` 和 `features.thread_refs` 是 `aic_agent_sdk_worker` 的 cmd 服务配置，不是 SDK 层配置。`abase` 配置服务于 history seq generator；如果 BOE 使用 `checkpoint.store: redis://cloud_agent`，同一份配置也会服务 checkpoint backend。

`worker.env` 不进入 conf。worker 运行时需要传给 AC 的 lane 由 `code.byted.org/gdp/env.Env()` 读取，避免配置文件和请求上下文中的泳道语义互相覆盖。

### aic_agent_sdk_session BOE conf 草案

`aic_agent_sdk_session` 当前只支持 env，本轮需要新增 conf loader，并输出 `cmd/cloud_agent/aic_agent_sdk_session/conf/conf_china-boe.yml`：

```yaml
mysql:
  psm: toutiao.mysql.cc_family_agent_coordinator
  db_name: cc_family_agent_coordinator
  dsn: ""
  read_dsn: ""
  read_timeout_ms: 5000

tables:
  agent_session: t_agent_session

coordinator:
  psm: ad.creative.aic_agent_coordinator
  cluster: ""
  direct_hostports: ""
  namespace: cloud_agent
  disabled: false

idgen:
  namespace: videocut_aigc_agent_coordinator
```

需要注意：`aic_agent_sdk_session` 现在的 MySQL 打开逻辑只有 DSN，本轮要么复用 worker 的 MySQL PSM 打开方式，要么沉淀一份同构的 MySQL config，不能让 BOE 依赖 `AIC_AGENT_SDK_SESSION_MYSQL_DSN`。

### aic_agent_sdk_api BOE conf 草案

`aic_agent_sdk_api` 当前也只支持 env 和常量，本轮需要新增 conf loader，并输出 `cmd/cloud_agent/aic_agent_sdk_api/conf/conf_china-boe.yml`：

```yaml
auth:
  mode: online

coordinator:
  namespace: cloud_agent
  psm: ad.creative.aic_agent_coordinator
  cluster: ""
  direct_hostports: ""

aic_agent_sdk_session:
  psm: ad.creative.aic_agent_sdk_session
  cluster: ""
  direct_hostports: ""

workspace:
  root: /home/gem/aic_agent_sdk

backend:
  type: ai_infra
  ai_infra:
    psm: pippit.sandbox.gateway
    biz_type: test
    biz_id_template: "user_{uid}"
    workdir_template: "/home/gem/aic_agent_sdk/{project_name}"
    action: SandboxToolCall

timeline:
  default_limit: 50
  max_limit: 200
```

需要注意：`AgentCoordinatorPSM` 和 `AICAgentSDKSessionPSM` 现在是代码常量，本轮要允许 conf 覆盖；env 只保留本地或 emergency override。

`aic_agent_sdk_api` 不配置也不主动设置 AC `env`。API 服务调用下游时，泳道由请求 `ctx` 和 RPC 框架透传，不应在业务配置里重复表达。

## 现状审查

### 改造基线

| 问题 | 影响 | 证据 | 当前判断 |
| --- | --- | --- | --- |
| `aic_agent_sdk_api` 缺 BOE conf loader | 线上配置会继续散落在环境变量和常量里 | `cmd/cloud_agent/aic_agent_sdk_api/service/config/config.go` 当前 `Load()` 读取 env | P0 |
| `aic_agent_sdk_session` 缺 BOE conf loader | MySQL、AC、idgen 无法按 conf 管理 | `cmd/cloud_agent/aic_agent_sdk_session/config/config.go` 当前 `LoadFromEnv()` 读取 env | P0 |
| `aic_agent_sdk_worker` 缺 BOE conf 输出 | 已有 YAML loader，但只有本地 direct hostports / local backend 配置 | `cmd/cloud_agent/aic_agent_sdk_worker/conf/worker.local.yml` 是本地配置 | P0 |
| `aic_agent_sdk_session` 使用本地 id generator | `t_agent_session.session_id` 虽是 `BIGINT` 主键，但不是线上 idgen | `cmd/cloud_agent/aic_agent_sdk_session/main.go` 注入 `sessionidgen.NewLocalGenerator()` | P0 |
| `aic_agent_sdk_worker` 使用本地 id generator | history 和 memory 表虽然是 `BIGINT` 主键，但不是线上 idgen | `cmd/cloud_agent/aic_agent_sdk_worker/run.go` 注入 `idgen.NewLocalIDGenerator()` | P0 |
| `t_agent_thread_ref` 主键不符合规范 | 当前是 `(user_id, session_id, thread_name)` 复合主键，没有 idgen `BIGINT` 主键 | `cloudagent/worker/sql/t_agent_thread_ref.sql` | P0，但首发可通过关闭特性规避 |
| `cmd/cloud_agent/aic_agent_sdk_worker` 无条件注入 ThreadRefs | 即使 BOE 不想开启 friendly ref，也会依赖该表 | `cmd/cloud_agent/aic_agent_sdk_worker/run.go` 无条件 `threadrefs.New(...)` | P0 |

### 风险假设

| 风险 | 说明 | 处理方式 |
| --- | --- | --- |
| idgen namespace 复用策略 | 复用 AC 服务既有 namespace：`videocut_aigc_agent_coordinator` | 写成 conf 项，不硬编码 |
| memory BOE 首发是否开启未最终确认 | memory 表本身符合规范，但如果不开 memory，不应进入必建表清单 | 由 `memory.enabled` 决定建表和验证范围 |
| 关闭 ThreadRefs 后模型体验会退化 | task 工具仍可使用数字 thread id，但不能用 `alice` / `bob` 这类 friendly name | BOE 首发接受；后续再补 schema 后开启 |
| YAML secret 占位符未注入 | 配置文件显式引用的模型或 Fornax 密钥缺失会导致 Worker 启动失败 | 发布检查必须覆盖 YAML 中每个 `${NAME}`；Worker 不再接受通用 env override |

### 外部依赖

- MySQL 复用 `aic_agent_coordinator` 依赖；Abase 由 `aic_agent_sdk_worker` 的 history seq generator 直接使用，并可复用作 Redis checkpoint backend，本计划不验证 readiness。
- BOE 服务创建和端口由 mesh / 服务治理处理，本计划不覆盖。
- Worker 的模型、角色、runtime 和 Fornax 配置全部写入 `worker.remote.yml`。部署系统只需要注入 YAML 中明确引用的模型/Fornax密钥；缺失引用变量时 Worker 会在启动阶段直接报错，不存在远端覆盖失败后静默回退到另一套模型的分支。

## 闭环计划

| 优先级 | 阶段 | 动作 | 完成定义 | 验收方式 | 依赖 / blocker | 状态 |
| --- | --- | --- | --- | --- | --- | --- |
| P0 | 配置闭环 | 为 `aic_agent_sdk_api` 新增 YAML conf loader，并输出 `conf/conf_china-boe.yml` | AC、aic_agent_sdk_session、backend、timeline、auth mode 都能从 conf 读取；常量只作为默认值 | `go test ./cmd/cloud_agent/aic_agent_sdk_api/...`；新增 config 单测覆盖 BOE YAML | 无 | 已完成 |
| P0 | 配置闭环 | 为 `aic_agent_sdk_session` 新增 YAML conf loader，并输出 `conf/conf_china-boe.yml` | MySQL、AC、session table、idgen 都能从 conf 读取；BOE 不依赖 DSN env | `go test ./cmd/cloud_agent/aic_agent_sdk_session/...`；新增 config 单测覆盖 BOE YAML | 需要复用或抽取 MySQL PSM 打开方式 | 已完成 |
| P0 | 配置闭环 | 输出 `aic_agent_sdk_worker/conf/worker.remote.yml`，并补 `idgen`、`features.thread_refs` 配置结构 | BOE worker conf 不含 local direct hostports / worker env；backend 为 `ai_infra` 且 `biz_type=test`；thread refs 关闭；idgen namespace 复用 AC；models / roles / runtime / log 字段可通过校验 | `go test ./cmd/cloud_agent/aic_agent_sdk_worker/...`；config 单测覆盖 BOE YAML | 模型 endpoint 发布前确认 | 已完成 |
| P0 | 开发闭环 | 在 `aic_agent_sdk_session` 增加线上 idgen 实现，复用 AC 的 `code.byted.org/gopkg/idgenerator/v2` 模式，通过 conf 读取 namespace；namespace 为空时才允许 local generator | `CreateSession` 写入的 `session_id` 来自 NT idgen；local generator 只用于本地默认配置 | 单测覆盖 namespace 配置、idgen 注入和空 namespace fallback；本地可用 stub generator 验证 | idgen namespace 命名需要发布前确认 | 已完成 |
| P0 | 开发闭环 | 在 `aic_agent_sdk_worker` 增加线上 idgen 实现，替换 history、memory、event id 的本地 generator 注入 | `t_agentthread_history.message_id`、memory 表 `id` 来自 NT idgen；event id 如继续是字符串，可以保留现有格式但底层数字来自同一 generator | 单测覆盖 `MessageID` / `NextID`，worker 启动配置测试覆盖 BOE namespace | idgen namespace 命名需要发布前确认 | 已完成 |
| P0 | 开发闭环 | 给 `cmd/cloud_agent/aic_agent_sdk_worker` 增加 `thread_refs.enabled` 配置，BOE conf 默认 `false`；为 `false` 时 `Deps.ThreadRefs` 保持 nil | BOE worker 不访问 `t_agent_thread_ref`；task 工具仍可用数字 thread id | 单测覆盖 enabled=false 时 deps 不注入 ThreadRefs；collab 目标解析可用数字 thread id | 无 | 已完成 |
| P0 | 数据准备 | BOE 首发建表清单移除 `t_agent_thread_ref` | 首发建表只包含必开表：`t_agent_session`、`t_agentthread_history`，以及 memory 开启时的四张 memory 表 | runbook / 建表脚本清单中不出现 `t_agent_thread_ref` | 需要确认 memory 是否开启 | 可立即执行 |
| P1 | 回归验证 | 更新本地 DDL 初始化路径，避免默认要求创建 `t_agent_thread_ref` | 本地 `dev.py init-db` 可以按配置决定是否创建 thread ref 表；默认本地是否开启可单独决定 | `python3 cmd/cloud_agent/dev.py init-db` 和相关单测通过 | 如果保留本地默认开启，需要保持原行为 | 可立即执行 |
| P1 | 发布准备 | 输出最终 BOE 配置 review 清单，明确哪些字段来自 conf、哪些字段允许来自 env | 发布前能逐项检查三份 conf；Fornax AK/SK 是唯一常规 env secret 例外 | 文档 review + 配置 diff review | 无 | 可立即执行 |
| P2 | 后续能力 | 若后续要开启 ThreadRefs，重做 `t_agent_thread_ref` schema：增加 `id BIGINT UNSIGNED NOT NULL` 主键，保留 `UNIQUE KEY uniq_user_session_name(user_id, session_id, thread_name)` | ThreadRefs 表也符合线上主键规范，现有 upsert 语义不变 | 新 DDL + store 单测 + 迁移方案 | 只有要开启 friendly ref 时才需要 | 延后 |

## 上线路径

### 上线前必须完成

1. 三个服务都有 BOE conf 输出：
   - `cmd/cloud_agent/aic_agent_sdk_api/conf/conf_china-boe.yml`
   - `cmd/cloud_agent/aic_agent_sdk_session/conf/conf_china-boe.yml`
   - `cmd/cloud_agent/aic_agent_sdk_worker/conf/worker.remote.yml`
2. `aic_agent_sdk_api` 和 `aic_agent_sdk_session` 支持从 conf 读取线上配置。
3. `aic_agent_sdk_session` 和 `aic_agent_sdk_worker` 接入线上 idgen，并通过 conf 配置 namespace。
4. BOE 配置显式关闭 `thread_refs.enabled`。
5. BOE 首发建表清单不包含 `t_agent_thread_ref`。
6. 对必开表做 schema review：
   - `t_agent_session.session_id BIGINT UNSIGNED PRIMARY KEY`
   - `t_agentthread_history.message_id BIGINT UNSIGNED PRIMARY KEY`
   - memory 开启时四张表均为 `id BIGINT PRIMARY KEY`

### 可并行推进

- 确认 BOE 首发是否开启 memory；不开则 memory 表不进入 P0 建表清单。
- 整理建表 SQL 执行清单，交给负责 MySQL 的同学处理。
- Review 三份 BOE conf 的 PSM、DB、namespace、backend、memory、fornax 字段。

### 上线窗口内执行

1. 只执行本轮确认的 BOE 必建表 SQL。
2. 启动前确认三份服务 conf 生效，且 worker 配置中 `thread_refs.enabled=false`。
3. 启动后观察 session 创建、thread 执行、history 写入是否有 idgen / duplicate key / table missing 错误。

### 上线后观察和回滚条件

观察项：

- 三个服务启动日志中的 conf 路径和关键配置摘要。
- `CreateSession` 成功率和 `session_id` 写入错误。
- worker history 写入错误，尤其是 idgen、duplicate key、table missing。
- memory 开启时的四张 memory 表写入错误。
- task 工具使用数字 thread id 的成功率。

回滚条件：

- idgen 获取失败导致 session 或 worker 主链路不可用。
- 关闭 ThreadRefs 后仍出现 `t_agent_thread_ref` table missing，说明配置开关未生效或仍有无条件访问路径。
- 必开表出现主键冲突或非预期自增 / 本地生成 id。

## 最终判断

BOE 准备工作按三步闭环：

1. 先把三份 BOE conf 输出补齐，线上非 secret 配置以 conf 为主。
2. 必开链路修 idgen：`aic_agent_sdk_session` 和 `aic_agent_sdk_worker` 都不能继续使用本地时间戳 generator 作为 BOE 运行时默认。
3. `threadRef` 不先修表，BOE 首发关闭该 `cmd/cloud_agent/aic_agent_sdk_worker` 可选能力；等需要 friendly thread name 时，再做符合线上规范的 `BIGINT` 主键版本。

按这个路径，BOE 准备既覆盖配置输出，也避免把一个非必选 threadRef 能力提前复杂化。
