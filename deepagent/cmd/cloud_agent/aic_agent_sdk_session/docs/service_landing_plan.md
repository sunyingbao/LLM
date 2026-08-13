# aic_agent_sdk_session 服务落地方案

## 背景和目标

`cmd/cloud_agent` 正在拆成三个可独立部署的服务：

- `aic_agent_sdk_api`：浏览器/业务 HTTP 入口，负责登录态解析、HTTP 协议适配、timeline 查询和 SSE/stream 转换。
- `aic_agent_sdk_session`：session/index 管理服务，目标 PSM 是 `ad.creative.aic_agent_sdk_session`。
- `aic_agent_sdk_worker`：真实 agent worker，负责从 `aic_agent_coordinator` claim runnable thread，构造 DeepAgent/AgentThread runtime，执行模型和工具，并把运行事件写回 `aic_agent_coordinator` EventLog。

`aic_agent_sdk_session` 的落地目标不是薄封装 `aic_agent_coordinator`，而是提供一个稳定的业务会话控制面：管理用户视角的 agent session 目录、会话元信息、主 thread 绑定关系和关闭语义。创建 thread、发送消息、停止运行、订阅事件这些 AC 能力由 `aic_agent_sdk_api` 在完成 session 归属校验后直接调用。

## 文档结构

这份文档按实现落地顺序组织：

1. 先定义服务职责边界，明确哪些能力属于 session 服务，哪些属于 API、worker 或 AC。
2. 再定义模块关系，说明 `aic_agent_sdk_session` 在三服务拆分后的调用位置。
3. 然后定义 `t_agent_session` 数据模型和索引，这是服务实现的事实基础。
4. 接着定义 RPC 接口语义，接口必须能直接映射到数据模型或明确的 close 编排。
5. 再说明 timeline、登录态、sandbox/workdir 的边界，避免后续实现把运行态塞回 session 服务。
6. 最后给出第一阶段实现顺序和仍需确认的问题。

## 服务职责边界

`aic_agent_sdk_session` 负责：

- 管理 `agent_session` 索引和元信息，包括 `session_id`、`uid`、标题、状态、主 thread、最后活跃时间、最后消息摘要。
- 校验 `uid + session_id` 的归属关系，保证用户只能访问自己的 session。
- 提供会话目录接口：创建、列表、详情、更新标题/状态、关闭。
- 维护 session 和 `aic_agent_coordinator` thread 的业务映射，例如把 API 创建出的 main thread 绑定到 `agent_session.main_thread_id`。
- 关闭 session 时关闭该 session 下的 main/child threads，但不把 AC `CloseThread` 设计成外部 RPC 原语。
- 将 session 状态变化写入自有存储，必要时根据 `aic_agent_coordinator` 结果返回 thread 摘要。

`aic_agent_sdk_session` 不负责：

- 不直接运行模型、React loop、DeepAgent prompt、工具调用、HITL 策略或子 agent 调度；这些属于 `aic_agent_sdk_worker` 和 `deepagents/agentthread`。
- 不保存完整对话 timeline，不把 EventLog 做二次存储主表；timeline 的历史和订阅由 `aic_agent_sdk_api` 直接读 `aic_agent_coordinator`。
- 不定义前端 UI 状态，例如 active panel、status bar、具体 toast 文案。
- 不把 sandbox、workdir、checkpoint、history 表、工具策略变成 session 服务配置；这些是 worker/runtime 关注点。
- 不把 `aic_agent_coordinator` 的 `Thread/Message/EventLog` 原语原样透出给前端或业务方。

## 模块关系

```text
Browser / business client
        |
        | HTTP, no uid in request body
        v
aic_agent_sdk_api
        | parse login state -> uid
        | call session RPC for session metadata and mutations
        v
aic_agent_sdk_session
        | manage agent_session table
        | close session-owned threads
        v
aic_agent_coordinator
        ^                         |
        | scan / claim / pull     | ListEvents / SubscribeSession
        | ack / append / release  |
aic_agent_sdk_worker                   aic_agent_sdk_api
        |
        | build DeepAgent/AgentThread, run model/tools
        v
deepagents / agentthread runtime
```

关系说明：

- `aic_agent_sdk_api -> aic_agent_sdk_session`：session 目录类操作走 RPC，包括 create/list/get/update/close/bind_main_thread/touch。`aic_agent_sdk_api` 只传后端解析出的 `uid`，不信任前端传用户身份。
- `aic_agent_sdk_api -> cloudagent/api -> aic_agent_coordinator`：thread/message/control/timeline 运行时能力由 `cloudagent/api.AgentAPI` 编排，底层再调用 AC `CreateThread/SendMessage/ResumeFromBlock/CancelInput/ListEvents/ListTurnEvents/SubscribeSession`。
- `aic_agent_sdk_session -> aic_agent_coordinator`：只在 `GetSession(include_threads=true)` 或 `CloseSession` 时调用，例如 `ListSessionThreads` / `CloseThread`。创建 thread、发送 message、恢复 block、停止当前 turn 由 `aic_agent_sdk_api` 校验 session 归属后调用 `cloudagent/api`。
- `aic_agent_sdk_worker -> aic_agent_coordinator`：worker 的主链路仍然是 scan/claim/pull message/ack/append event/release/block/resume。`aic_agent_sdk_session` 不参与 worker 执行循环。

## 代码目录结构

`aic_agent_sdk_session` 第一阶段按下面的目录结构落地：

```text
cmd/cloud_agent/aic_agent_sdk_session/
├── conf/                         # Kitex、本地 MySQL、AC cluster/直连配置
├── docs/                         # 服务设计和落地说明
├── dal/
│   └── session/                  # t_agent_session 的 model/store/query
├── service/
│   └── session/                  # Create/List/Get/Update/Close/Bind/Touch 业务逻辑
├── infra/
│   ├── ac/                       # Agent Coordinator client 初始化，固定 PSM + cluster/direct_hostports
│   ├── idgen/                    # 公司 id gen 封装，生成 session_id
│   └── mysql/                    # MySQL 初始化，本地 DSN 和线上配置适配
├── handler.go                    # Kitex handler，只做 req/rsp 适配和 service 调用
└── main.go                       # 服务启动
```

依赖方向固定为：`handler -> service/session -> dal/session + infra/*`。`dal` 不反向依赖 AC，`infra/ac` 不理解 session 表结构。

## 核心数据模型：agent_session

建议新增独立表 `t_agent_session`。旧 demo 里的 `agentworker_session_index` 可以作为字段参考，但线上服务不要把 CLI 的 `scope_key/cwd` 设计带入核心模型。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `session_id` | BIGINT UNSIGNED | 主键，由公司 id gen 生成；不使用 MySQL 自增 |
| `uid` | BIGINT | 用户 id，来自 `aic_agent_sdk_api` 登录态解析 |
| `title` | VARCHAR(256) | 会话标题，可由用户改名或首次输入生成 |
| `status` | TINYINT | `ACTIVE / ARCHIVED / CLOSED` |
| `main_thread_id` | BIGINT UNSIGNED | session 主 thread，首次输入时可按需创建 |
| `last_message_preview` | VARCHAR(512) | 左侧列表展示摘要，提交输入或收到终态事件后更新 |
| `last_active_at_ms` | BIGINT | 列表排序时间 |
| `created_at_ms` | BIGINT | 创建时间 |
| `updated_at_ms` | BIGINT | 更新时间 |
| `closed_at_ms` | BIGINT | 关闭时间，未关闭为空或 0 |
| `metadata_json` | TEXT | 少量业务扩展信息，不能放 runtime 大对象 |

索引建议：

```sql
CREATE TABLE t_agent_session (
  session_id BIGINT UNSIGNED NOT NULL COMMENT 'idgen generated session id',
  uid BIGINT NOT NULL COMMENT 'owner user id',
  title VARCHAR(256) NOT NULL DEFAULT '' COMMENT 'session title',
  status TINYINT NOT NULL DEFAULT 1 COMMENT '1 active, 2 archived, 3 closed',
  main_thread_id BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'main AC thread id',
  last_message_preview VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'list preview',
  last_active_at_ms BIGINT NOT NULL DEFAULT 0 COMMENT 'last active time in ms',
  created_at_ms BIGINT NOT NULL DEFAULT 0 COMMENT 'create time in ms',
  updated_at_ms BIGINT NOT NULL DEFAULT 0 COMMENT 'update time in ms',
  closed_at_ms BIGINT NOT NULL DEFAULT 0 COMMENT 'close time in ms',
  metadata_json TEXT COMMENT 'small business metadata json',
  PRIMARY KEY (session_id),
  KEY idx_user_session (uid, session_id),
  KEY idx_user_status_active (uid, status, last_active_at_ms),
  KEY idx_main_thread (main_thread_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci ROW_FORMAT=DYNAMIC COMMENT='AIC Agent SDK session index';
```

字段原则：

- `session_id` 是业务会话 id，同时也是表主键；由公司 id gen 生成 `BIGINT UNSIGNED`，不使用 MySQL `AUTO_INCREMENT`。
- `session_id` 不等同于 AC `thread_id`，只是 session 目录项的稳定 id。
- `main_thread_id` 可以为空。创建空白会话时只写 `agent_session`；首次用户输入由 `aic_agent_sdk_api` 创建 AC main thread 后调用 `BindMainThread` 回填。
- `status` 是用户视角的目录状态，不等同于 AC thread runtime status。
- thread 列表不建议落在 `agent_session` 表里。`GetSession` 需要 thread 摘要时可以从 AC `ListSessionThreads` 读取，并把主 thread 绑定回填。
- 子 thread friendly ref 可以继续由 worker 维护 `t_agent_thread_ref`，不作为 session 服务核心表。

## RPC 接口语义

`aic_agent_sdk_session` 的 RPC 应围绕 session index 和会话元信息内聚。Thread/message/control/timeline 是 AC 的事实边界，除非某个动作必须同时修改 `agent_session` 表，否则不应在 session 服务里镜像一套 AC 接口。

第一版建议把接口收敛成下面几类。

### CreateSession

语义：创建一个用户视角的会话目录项。

处理步骤：

1. 校验 `uid`。
2. 使用公司 id gen 生成 `session_id`。
3. 插入 `t_agent_session`，默认 `status=ACTIVE`，`main_thread_id=0`。
4. 返回 `AgentSessionView`，通常只有 session，不需要创建 thread。

边界：这不是 AC `CreateThread` 的薄封装。空白会话允许没有 main thread。

### ListSessions

语义：列出某用户的会话目录，用于左侧栏。

处理步骤：

1. 按 `uid + status` 查询 `t_agent_session`。
2. 按 `last_active_at_ms DESC, updated_at_ms DESC` 排序。
3. 使用 cursor 分页，返回轻量 `AgentSession` 列表。

边界：不读取 timeline，不唤醒 thread，不订阅 AC。

### GetSession

语义：打开会话详情页时获取首屏聚合视图。

处理步骤：

1. 校验 session 归属。
2. 读取 `agent_session`。
3. 如果 `include_threads=true`，调用 AC `ListSessionThreads` 获取 thread 摘要。
4. 将 AC `ThreadStatus` 映射成 `aic_agent_sdk_common.AgentThreadStatus`，返回 `AgentSessionView`。

边界：`include_threads` 默认为 false，pending human input、context usage、完整消息历史都不在这里返回；这些由 timeline 接口从 EventLog 取。

### UpdateSession

语义：更新会话目录属性。

可更新字段：

- `title`
- `status`，仅限 `ACTIVE <-> ARCHIVED` 这类目录态变更

边界：不能通过该接口改 AC thread runtime 状态；不能修改 `uid`、`session_id`。

### CloseSession

语义：用户视角关闭会话。

处理步骤：

1. 校验 session 归属。
2. 调用 AC `ListSessionThreads` 找到该 session 下的 main/child threads。
3. 对未关闭的 thread 调用 AC `CloseThread`。关闭失败不能静默吞掉，需要返回错误或记录明确的部分失败。
4. 将 `agent_session.status` 更新为 `CLOSED`，记录 `closed_at_ms`。
5. 返回最新 `AgentSessionView`。

边界：`CloseSession` 是 session 服务内部的关闭编排，直接关闭该 session 的内部 thread；接口仍然不暴露 AC `CloseThread` 的控制消息细节。

### BindMainThread

语义：把 session 的主 thread 绑定到 `agent_session.main_thread_id`。

处理步骤：

1. 校验 session 归属且未 `CLOSED`。
2. 只允许从 `main_thread_id=0` 绑定到非 0 thread，或幂等绑定到相同 thread。
3. 条件更新 `agent_session.main_thread_id`，避免首次输入并发创建两个 main thread 时互相覆盖。
4. 返回最新 `AgentSessionView`。

边界：该接口不创建 AC thread，也不发送消息。AC `CreateThread` 由 `aic_agent_sdk_api` 直接调用，绑定只是 session 元信息更新。

### TouchSession

语义：更新会话列表展示所需的轻量活跃信息。

可更新字段：

- `last_message_preview`
- `last_active_at_ms`
- `title_if_empty`

边界：这个接口只服务左侧 session 列表体验。消息是否真的进入运行队列，以 AC `SendMessage` 结果为准；`TouchSession` 失败不应撤销已经写入 AC 的消息。`title_if_empty` 只在当前 title 为空时生效，用来表达“创建时传了 title 就用 title，没传则用首条用户消息生成标题”。

### SubmitInput / StopRunning 的位置

`SubmitInput`、`ResumeBlockedTurn`、`StopRunning` 这些动作的核心事实在 AC thread/mailbox/eventlog。推荐由 `aic_agent_sdk_api` 在校验 session 归属后调用 `cloudagent/api.AgentAPI`，再用 `BindMainThread/TouchSession` 更新 session 元信息。

因此 `aic_agent_sdk_session.thrift` 不定义 `SubmitInput/StopRunning`。HTTP 层仍然有这两个接口，但实现上是 API 组合 `cloudagent/api` 调用和 `aic_agent_sdk_session` 元信息更新。

## 事件和 timeline 边界

`aic_agent_sdk_session` 不提供 `ListTimeline` 和 `SubscribeTimeline` RPC。

原因：

- `aic_agent_sdk_worker` 把 worker timeline payload 写入 AC EventLog；payload 是 `cloudagent/protocol/event` 的 payload-only JSON，不重复携带 AC event header。
- `aic_agent_sdk_api` 的 HTTP IDL 已经明确：`ListTimeline` 和 `SubscribeTimeline` 不走 `aic_agent_sdk_session`，而是通过 `cloudagent/api.AgentAPI` 查询或订阅 AC event。
- timeline 是高频读和长连接流，绕过 `aic_agent_sdk_session` 可以避免 session 服务成为 stream fanout 瓶颈。

`aic_agent_sdk_session` 只需要保证 timeline 查询前的授权边界：

- `aic_agent_sdk_api` 在读 timeline 前，应先调用 `aic_agent_sdk_session.GetSession` 确认 `uid + session_id` 归属。
- 确认归属后，`aic_agent_sdk_api` 使用 session_id/thread_id/turn_id 调 `cloudagent/api` 读 AC event。
- AC event envelope 中的 `event_id/session_id/thread_id/turn_id/created_at_ms/event_type` 是排序、恢复和过滤依据；`payload` 是 worker 原始 JSON，`aic_agent_sdk_api` 不反序列化 worker payload schema，只做 HTTP/SSE 协议转换。

## 线上登录态和 uid 传递

前端 HTTP 请求体不应该出现可信 `uid` 字段。

线上链路：

1. `aic_agent_sdk_api` 在 Hertz middleware 或 handler 前置逻辑中解析公司统一登录态。
2. 解析出的 `uid` 写入 request context。
3. `aic_agent_sdk_api` 调用 `aic_agent_sdk_session` RPC 时，把 `uid` 填入 `CreateSessionRequest/ListSessionsRequest/...`。
4. `aic_agent_sdk_session` 只信任来自 `aic_agent_sdk_api` 的 RPC 调用，不接受前端直接传入 uid。
5. 所有写操作必须用 `uid + session_id` 做 owner 校验。

本地开发链路：

- 可以提供默认测试 uid 或显式本地配置 uid，但本地入口必须和线上入口区分清楚。
- 不要把本地 username、项目 cwd、session resume scope 当作线上身份模型。

## sandbox/workdir 不属于 session 服务

`workdir` 和 sandbox 是 agent runtime 的执行环境，不是 session index 的产品元信息。

当前 worker 代码已经体现这个边界：

- `aic_agent_sdk_worker` 的 `runtime.workdir` 配置决定默认工作目录根。
- AC `ThreadProfile.Cwd` 可以承载某个 thread 的运行 cwd。
- `aic_agent_sdk_worker/worker/threadbuilder` 会根据 AC thread profile 或 worker runtime 配置构造 `SandboxFilesystemBackend`。
- checkpoint、history store、execute policy、tool mask 都在 worker/thread runtime 初始化时生效。

因此：

- `agent_session` 不存 sandbox 根目录。
- `CreateSession` 不接收 workdir。
- 首次输入创建 main thread 时，如确实需要指定 cwd，也应由服务端配置或业务策略写入 AC `ThreadProfile`，不让前端直接控制任意路径。
- cwd/scope 可以作为某些客户端的 resume 过滤条件，但不能成为线上 session 服务的核心主键。

## 配置和部署关注点

`aic_agent_sdk_session` 作为 PSM 服务落地时建议配置以下项：

| 配置 | 说明 |
| --- | --- |
| `mysql.dsn` | 本地联调 MySQL DSN |
| `mysql.db_name` / `mysql.cluster` | 线上 MySQL 配置，具体字段按公司配置规范落地 |
| `tables.agent_session` | 默认 `t_agent_session` |
| `ac.cluster` | AC 服务 cluster；PSM 固定，不做配置项 |
| `ac.direct_hostports` | 本地直连 AC 联调用 |
| `ac.namespace` | AC namespace，必须与 `aic_agent_sdk_worker` 消费侧一致 |
| `idgen` | session id 生成策略，需保证全局唯一且可读性足够 |
| `log.enable_access` | 记录 session_id、uid、method、latency、error_code |

部署注意事项：

- PSM：`ad.creative.aic_agent_sdk_session`。
- IDL：`cmd/cloud_agent/idl/aic_agent_sdk_session.thrift`。
- Kitex 配置当前位于 `cmd/cloud_agent/aic_agent_sdk_session/conf/kitex.yml`，本地端口是 `:8888`，线上需要避免和 AC 本地联调端口混用。
- MySQL 表需要先走正式 DDL，而不是依赖 GORM AutoMigrate。
- `ListSessions` 是高频接口，必须有 `uid + status + last_active_at_ms` 索引。
- 首次输入需要幂等策略：客户端重试时不能重复创建 main thread 或重复发送同一用户输入。`aic_agent_sdk_api` 负责 AC 消息幂等，`aic_agent_sdk_session` 负责 `BindMainThread` 条件更新。
- `CloseSession/StopRunning` 涉及 AC 控制消息时，必须记录 logid，方便从 API/session -> AC -> worker 串联排查；其中 `CloseSession` 由 session 服务关闭内部 threads，`StopRunning` 由 API 通过 `cloudagent/api` 编排。
- session 服务不持有长连接；timeline SSE 压力在 `aic_agent_sdk_api` 和 AC stream 链路。

## 第一阶段实现建议

第一阶段目标是让 `aic_agent_sdk_api` 可以通过 RPC 使用真实 session 服务，而不是继续在 HTTP handler 里空返回。

1. 建表 `t_agent_session`，实现 store：create/get/list/update/close/touch/set_main_thread。
2. 实现 `CreateSession/ListSessions/GetSession/UpdateSession`，这四个接口只依赖 MySQL 和可选 AC `ListSessionThreads`。
3. 实现 `BindMainThread/TouchSession`，支撑 API 通过 `cloudagent/api` 创建 thread、发送 message 后更新 session 元信息；timeline 鉴权先复用 `GetSession`。
4. `CloseSession` 直接关闭该 session 下的 main/child threads，并更新 session 目录态。
5. `aic_agent_sdk_api` handler 接入登录态 uid，并把 session 目录类 HTTP 接口转发到 `aic_agent_sdk_session`。
6. `SubmitInput/StopRunning/ListTimeline/SubscribeTimeline` 由 `aic_agent_sdk_api` 校验 session 归属后调用 `cloudagent/api`。

## 未决问题

1. title 默认值的截断规则：创建时传 title 就用；没传时用首条用户消息，但需要确认最大长度和空白消息处理。
2. 如果 timeline 鉴权压测证明 `GetSession` 过重，再考虑新增更轻量的内部 RPC；第一阶段不预留接口。
