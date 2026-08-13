# aic_agent_sdk_api 逻辑方案

## 目标

`aic_agent_sdk_api` 是 Web/前端访问 AIC Agent SDK 的 HTTP/Hertz 接入层。它的目标是薄逻辑：

- 解析 HTTP 请求、登录态和 uid。
- 做前端入参校验、默认 ac_namespace 适配、BaseResp/HTTP/SSE 错误映射。ac_env 是调用 AC/worker 的运行配置，不进入 session 目录模型。
- 调用 `aic_agent_sdk_session` 管理用户视角的 session 目录元信息。
- 对 thread/message/event/timeline 这类已经属于 CloudAgent runtime 的能力，通过 `cloudagent/api.AgentAPI` 调用 AC，不在 API 层或 session RPC 层重新发明一套线程协议。

当前代码依据：

- HTTP IDL：`cmd/cloud_agent/idl/aic_agent_sdk_api.thrift`
- session RPC IDL：`cmd/cloud_agent/idl/aic_agent_sdk_session.thrift`
- 事件模型：`cmd/cloud_agent/idl/aic_agent_sdk_common.thrift`
- Hertz handler 桩：`cmd/cloud_agent/aic_agent_sdk_api/biz/handler/*.go`

## 文档结构

这份文档按 HTTP 接入层的实现路径组织：

1. 先说明 `aic_agent_sdk_api` 的职责边界，避免 API 层变成 session/AC/worker 的混合服务。
2. 再说明调用关系和接口分组，明确每类 HTTP 接口应该落到哪个下游。
3. 接着按具体请求流描述 session 目录、提交输入、停止运行、关闭会话、timeline 查询和订阅。
4. 然后说明 uid/login、错误处理、配置部署这些横切逻辑。
5. 最后只保留真正还没拍板的问题；已经确定的 session_id、request_id、CloseSession、title、ac_namespace 规则不再放到未决问题里。

## 职责边界

### aic_agent_sdk_api 负责

| 领域 | 具体职责 |
| --- | --- |
| HTTP 入口 | request bind/validate、response 编码、SSE 输出、跨端字段兼容 |
| 身份 | 从线上登录态解析 uid；本地开发允许配置或 mock uid；HTTP IDL 不接受前端传 uid |
| session 元信息 | 调 `aic_agent_sdk_session` 创建、读取、更新、关闭用户视角 session 目录 |
| thread/message 操作 | 调 `cloudagent/api.AgentAPI` 执行 `CreateThread`、`Submit`、`StopRunning`；底层由 AgentAPI 调 AC |
| timeline | 调 `cloudagent/api.AgentAPI` 的 `ListTimeline`、`SubscribeTimeline`，输出 AC event envelope，并把 AC payload 作为 opaque worker timeline payload 透传 |
| 错误和恢复 | 统一 BaseResp/HTTP/SSE 错误；支持 `recover_queue_id`、`after_event_id` 恢复 |

### aic_agent_sdk_api 不负责

- 不拼 DeepAgent prompt，不理解 React loop、模型请求、工具执行、checkpoint 细节。
- 不实现 worker scan/claim/lease/ack/release，这些是 `aic_agent_sdk_worker` 和 `agentworker/cloud` 的职责。
- 不维护另一套 message/event 存储，不复制 AC eventlog/mailbox。
- 不把 friendly name、active panel、status bar、前端文案变成 SDK 核心协议。
- 不在 `aic_agent_sdk_session` 上扩展一套 `ListEvents`、`SubscribeTimeline`、`CancelInput` 的镜像 RPC。

保持薄 API 的原因很直接：thread/message/event 的一致性边界在 AC。如果 API 或 session RPC 再存一份“运行态”，就会出现消息已入 AC 但 session 状态未更新、事件已 append 但 API 自己的 cursor 不一致、worker cancel/close 控制消息语义漂移等问题。API 只做组合和适配，状态事实以 `aic_agent_sdk_session` 的 session 目录和 AC 的 thread/mailbox/eventlog 为准。

## 调用关系

```text
Browser/Web
  |
  v
aic_agent_sdk_api (Hertz)
  |-- auth middleware -> uid
  |-- aic_agent_sdk_session RPC
  |     |-- session directory: create/list/get/update/status/last preview/main thread ref
  |
  |-- cloudagent/api.AgentAPI
        |-- CreateThread / Submit / StopRunning
        |-- ListTimeline / SubscribeTimeline
        |-- adapter: Agent Coordinator RPC/stream

aic_agent_sdk_worker
  |-- scans/claims AC runnable threads
  |-- runs deepagents/agentthread
  |-- appends worker timeline event JSON as AC event payload
```

`aic_agent_sdk_session` 是 session 目录服务，不是 AC facade。它保存 `session_id`、`uid`、`title`、`status`、`main_thread_id`、`last_message_preview`、`last_active_at` 等产品会话字段。thread runtime 状态来自 AC，timeline 来自 AC eventlog。ac_namespace/ac_env 来自服务端配置，不进入 HTTP 请求和 `t_agent_session`。

## 代码目录结构

`aic_agent_sdk_api` 第一阶段按下面的目录结构落地：

```text
cmd/cloud_agent/aic_agent_sdk_api/
├── conf/                         # Hertz、本地 AC 直连、cluster、ac_namespace/ac_env 配置
├── docs/                         # HTTP 逻辑设计和接入说明
├── biz/
│   ├── handler/                  # Hertz handler，只做 bind、passport、调用 service
│   └── router/                   # Hertz 路由注册
├── service/
│   ├── session/                  # session HTTP 到 aic_agent_sdk_session RPC 的适配
│   ├── input/                    # SubmitInput：AC message + BindMainThread/TouchSession
│   ├── control/                  # StopRunning / CloseSession 编排
│   └── timeline/                 # ListTimeline / SubscribeTimeline
├── infra/
│   ├── passport/                 # GetUserID(hertzCtx)，线上登录态适配；第一阶段只保留接口
│   ├── sessionrpc/               # aic_agent_sdk_session client，固定 PSM + cluster
│   └── ac/                       # Agent Coordinator client，固定 PSM + cluster/direct_hostports
├── router.go
└── main.go
```

依赖方向固定为：`handler -> service/* -> infra/*`。`infra/passport` 是唯一的 uid 来源入口；handler 不直接解析 cookie/session。

## 接口分组

| 分组 | HTTP 接口 | 下游 |
| --- | --- | --- |
| Session 目录 | `CreateSession`、`ListSessions`、`GetSession`、`UpdateSession`、`CloseSession` | `aic_agent_sdk_session` |
| 用户输入 | `SubmitInput` | `cloudagent/api.AgentAPI`；`aic_agent_sdk_session.BindMainThread/TouchSession` |
| 运行控制 | `StopRunning` | `cloudagent/api.AgentAPI` |
| Timeline | `ListTimeline`、`SubscribeTimeline` | `cloudagent/api.AgentAPI` |

## 通用请求流水

1. `binding.BindAndValidate` 解析 thrift HTTP 请求。
2. 登录态 middleware 解析 uid，写入 request context。线上 uid 只能来自可信登录态；本地开发可以使用配置 uid 或 mock middleware。
3. 读取服务端配置中的 ac_namespace/ac_env。HTTP 请求不接收 ac_namespace/ac_env。
4. 校验 session 归属：所有带 `session_id` 的请求先通过 `aic_agent_sdk_session.GetSession(uid, session_id)` 确认归属。
5. 调下游：目录类请求走 `aic_agent_sdk_session`；message/event/control 类请求走 `cloudagent/api`，由 AgentAPI 适配到底层 AC。
6. 统一返回：普通 HTTP 返回 thrift response + `BaseResp`；streaming 接口用 SSE 输出每个 event，结束前写错误 event。

## Create/List/Get/Update Session

这些接口只处理 session 目录数据。

- `CreateSession`：调用 `aic_agent_sdk_session.CreateSession(uid, title?)`。`session_id` 由 session 服务使用 id gen 生成；不强制创建 AC thread，允许空白会话先出现在左侧栏。
- `ListSessions`：调用 `aic_agent_sdk_session.ListSessions(uid, status?, page)`。不读 timeline，不唤醒 thread。
- `GetSession`：调用 `aic_agent_sdk_session.GetSession(uid, session_id, include_threads?)` 返回 session view。`include_threads=true` 时由 session 服务打包 thread 摘要；默认只返回 session 元信息。
- `UpdateSession`：调用 `aic_agent_sdk_session.UpdateSession` 更新 title/status 等目录属性。不要把 thread runtime status 写成 session status。

## SubmitInput

`SubmitInput` 是用户交互入口，但不应该把消息语义藏进 session RPC。建议 API 层按下面流程组合：

1. 鉴权并读取 session：`aic_agent_sdk_session.GetSession(uid, session_id, include_threads=true)`。
2. 判定输入类型：
   - 普通输入：`resume_ref` 为空，`content` 必填。
   - 恢复 block：`resume_ref` 非空，`thread_id` 必填；approval 恢复使用 `approval`，plan input 恢复使用 `content` 或后续结构化 answers。
   - `mode=IMPL_PLAN`：在 AC message metadata 里写入 plan mode 标记，不新增 API 侧状态机。
3. 解析目标 thread：
   - 如果请求带 `thread_id`，校验该 thread 属于当前 session。
   - 如果不带，使用 session 的 `main_thread_id`。
   - 如果没有 main thread，API 使用配置中的 ac_namespace/ac_env 调用 AC `CreateThread` 创建 main thread，然后调用 `aic_agent_sdk_session.BindMainThread` 回写 `main_thread_id`。
4. 写入 runtime：
   - 普通输入调用 `cloudagent/api.AgentAPI.Submit`，AgentAPI 编码 `cloudagent/protocol/input.UserMessage`，底层调用 AC `SendMessage`，`WakeThread=true`，`SenderType=USER`，`SenderId` 使用 uid 或服务端 sender id，`MessageType=deepagent.input`。
   - `mode=IMPL_PLAN` 时 metadata 增加 `turn_mode=plan` 这类 worker 已识别字段。
   - 恢复 block 调用 `cloudagent/api.AgentAPI.Submit` 的 resume 分支，AgentAPI 编码 `ResumeTurnPayload`，底层调用 AC `ResumeFromBlock`。
5. 回写 session 目录：调用 `aic_agent_sdk_session.TouchSession` 更新 `last_message_preview/last_active_at`。如果创建 session 时没有传 title，`TouchSession.title_if_empty` 使用首条用户消息生成标题。回写失败不应撤销已发送 AC message，但需要打日志并在 response 中保留 message ref。
6. 返回 `MessageRef{thread_id,message_id}` 和最新 `AgentSessionView`。

关键约束：

- 不在 API 层生成 turn 状态；turn 由 worker/agentthread 运行后通过 AC eventlog 输出。
- 不为了前端 pending 展示单独写“submitted event”；提交成功的事实由 HTTP response 的 `MessageRef` 表达。
- 对同一 session 首次输入的 main thread 创建要有幂等保护，避免双击同时创建两个 main thread。第一阶段不在 IDL 补 `request_id`，由 `BindMainThread` 条件更新兜底。

## StopRunning

`StopRunning` 表示用户停止当前运行中的 turn，不关闭 session。

1. 鉴权并读取 session。
2. 调 `cloudagent/api.AgentAPI.StopRunning`。
   - 请求带 `thread_id` 时只停该 thread。
   - 不带时 AgentAPI 调 AC `ListSessionThreads(session_id)`，选择 `RUNNING` 优先，其次选择 `BLOCKED`。
3. AgentAPI 对每个目标 thread 先调 AC `CancelInput(thread_id, reason)`。当前 HTTP IDL 不暴露 `cutoff_message_id`，因此由 AC 按当前线程 pending/running 输入计算取消范围。
4. 如果目标 thread 已经 approval-blocked，AC 会拒绝 `CancelInput`。AgentAPI 会读取最新 `APPROVAL_REQUIRED` event，发送带 `cancel_turn=true` 的拒绝型 resume；worker 收到后只输出 `TURN_INTERRUPTED`/`TURN_FINISHED`，不再把控制请求交还给模型继续执行。
5. 返回 AC control message refs。session status 不改为 `CLOSED`，最多刷新 session view 中 thread 摘要。

错误处理：

- 没有 running thread 时返回成功，`control_messages=[]`，不要把它当 500。
- AC 返回 lease/race 类冲突时返回可重试错误，前端随后通过 `ListTimeline`/`SubscribeTimeline` 校准真实状态。

## CloseSession

`CloseSession` 是用户视角会话结束或归档，比 `StopRunning` 更强。

处理流程：

1. API 鉴权后调用 `aic_agent_sdk_session.CloseSession(uid, session_id, reason)`。
2. `aic_agent_sdk_session` 内部查询该 session 下的 main/child threads，并直接调用 AC `CloseThread` 关闭。
3. session 服务更新 `agent_session.status=CLOSED` 后返回 session view。

边界：

- `CloseSession` 不删除 AC eventlog，也不清理 worker checkpoint。
- close 是 session 产品动作；具体 close control message、`CompleteCloseThread`、ack/release 属于 AC/worker 协议，不暴露给 HTTP。
- 如果业务只想“停止这次回答”，使用 `StopRunning`，不要复用 `CloseSession`。

## ListTimeline

`ListTimeline` 用于刷新、重连后补历史、打开会话首屏。

推荐映射：

- 有 `thread_id + turn_id`：`cloudagent/api.AgentAPI.ListTimeline` 调用 AC `ListTurnEvents(thread_id, turn_id, cursor, limit)`。
- 有 `thread_id`：AgentAPI 调用 AC `ListEvents(thread_id, cursor, limit, direction)`。
- 只有 `session_id`：AgentAPI 调用 AC `ListSessionEvents(session_id, cursor, limit, direction)`。session 级历史排序、分页和 opaque cursor 都由 AC 负责，API 不再按 thread 拉取后本地合并。

事件输出：

- `aic_agent_sdk_worker` 把 worker timeline event JSON 写入 AC event payload。
- API 不反序列化 worker payload，也不依赖 `cloudagent/protocol/event` 的 schema 发版。
- API 对前端输出 `TimelineEvent = AC event envelope + opaque payload`：`event_id/session_id/thread_id/turn_id/created_at_ms/event_type` 来自 AC event，`payload` 是 worker 原始 JSON。
- worker payload 使用 `cloudagent/protocol/event` 中的 `xxxEventPayload` 类型，只描述 payload 本体，不重复携带 AC event header。
- 前端使用 AC envelope 做恢复、去重、排序，使用 `payload` 做渲染。这样 worker timeline payload schema 演进时，通常只需要发布 worker 和前端，不需要发布 `aic_agent_sdk_api`。

`after_event_id` 的处理：

- 如果 AC 原生支持从 event id 恢复，直接下传。
- 如果当前 AC 只支持 cursor，API 可以在短期内把 `after_event_id` 转换为 cursor；不能转换时返回明确错误，让前端退化为按 `cursor` 或首屏重拉。

## SubscribeTimeline

`SubscribeTimeline` 是 server streaming HTTP 接口，Hertz 实现建议输出 SSE：

```text
event: queue
data: {"queue_id":"..."}

event: event
data: {"event":{"event_id":"...","event_type":"ASSISTANT_DELTA","thread_id":"...","turn_id":"...","created_at_ms":123,"payload":{"delta":"..."}}}

event: error
data: {"code":"AC_STREAM_RECV_FAILED","message":"...","retryable":true}
```

流程：

1. 鉴权并确认 session 归属。
2. 用 `cloudagent/api.AgentAPI.SubscribeTimeline` 订阅 AC `SubscribeSession(session_id, recover_queue_id?)`。
3. 第一帧如果拿到 `queue_id`，立即发给前端；前端重连时带回 `recover_queue_id`。
4. AgentAPI 对每个 AC event 按 AC envelope 中的 `thread_id/turn_id` 过滤，然后输出 AC envelope + opaque payload。
5. 如果 `recover_queue_id` 过期，AgentAPI 会去掉 recover queue 后重新订阅；前端仍应保存最后处理的 `event_id`，必要时通过 `ListTimeline` 补历史。
6. 客户端断开时取消 context，关闭 AC stream，不需要额外写 session 状态。

恢复策略：

- 前端保存 `queue_id` 和最后成功处理的 `event_id`。
- 短断线：用 `recover_queue_id` 续 AC stream。
- recover 过期：用 `ListTimeline(after_event_id)` 补历史，再新建订阅。
- 如果历史补偿也失败，前端重拉 `GetSession + ListTimeline` 首屏，避免显示半截运行态。

## uid/login

线上方式：

- `aic_agent_sdk_api` 统一通过 `infra/passport.GetUserID(hertzCtx)` 获取 uid。
- `infra/passport` 是唯一的登录态适配入口。第一阶段只保留包和函数签名，暂不接真实 passport/session 库。
- uid 只写入 server context 和下游 RPC，不出现在 `aic_agent_sdk_api.thrift` 请求结构中。
- 调 `aic_agent_sdk_session` 时传 `uid`；调 AC 创建 thread 时传 `UserId=uid` 和配置中的 ac_namespace/ac_env；发送用户消息时 `SenderId` 使用稳定 uid 字符串或服务约定 sender id。

本地兼容：

- 支持配置默认 uid，例如环境变量或本地配置项，便于无登录态压测。
- 可以保留旧 Web demo 的本地用户表/用户名登录作为开发模式，但必须用开关隔离，不能在线上接受前端传入 uid。
- 本地 mock uid 也要走同一套鉴权结果注入路径，避免 handler 里到处判断“有没有登录”。

## 错误处理

建议错误分层：

| 场景 | HTTP/SSE 表达 | BaseResp/错误码 |
| --- | --- | --- |
| JSON/bind/必填字段错误 | HTTP 400 | `INVALID_ARGUMENT` |
| 未登录 | HTTP 401 | `UNAUTHENTICATED` |
| session 不属于 uid | HTTP 403 | `PERMISSION_DENIED` |
| session/thread 不存在 | HTTP 404 | `NOT_FOUND` |
| AC/session 返回 BaseResp 非 0 | HTTP 200 或 502，按网关约定；body 保留 BaseResp | 映射下游 code/message |
| 下游超时 | HTTP 504；SSE 发 error 后断开 | `DOWNSTREAM_TIMEOUT` |
| SSE 中途失败 | `event:error` 后关闭 | `STREAM_INTERRUPTED` |

实现注意：

- 普通 JSON API 尽量返回结构化 thrift response，不直接 `c.String(500, err.Error())`。
- 下游 transport error 和 BaseResp business error 都要带 `logid`、下游 PSM、method。
- 对 SubmitInput 的“AC SendMessage 成功但 session touch 失败”，返回 message ref + warning 级日志；不要误报消息提交失败。
- 对 CloseSession 的部分 thread close 失败，要返回部分失败详情或可重试错误，不能只返回 session 已关闭。

## 配置和部署关注点

必需配置：

- `aic_agent_sdk_session.cluster`：aic_agent_sdk_session 的 cluster；PSM 固定为 `ad.creative.aic_agent_sdk_session`，不做配置项。
- `ac.cluster`：AC 服务 cluster；PSM 固定为 `ad.creative.aic_agent_coordinator`，不做配置项。
- `ac.direct_hostports`：本地直连 AC 调试使用，线上为空走服务发现。
- `ac.namespace` / `ac.env`：AC/worker 运行配置，必须与 `aic_agent_sdk_worker` 消费侧对齐；其中 env 不写入 `agent_session`。
- `local.default_uid`：本地无 passport 时使用的测试 uid，线上不能启用。
- HTTP 端口和 Hertz 配置：当前 `conf/hertz.config.yaml` 默认 service port `6789`。
- auth mode：`online` / `local`，控制 uid 来源。
- stream 超时：SSE idle timeout、单连接最大时长、心跳间隔。
- list limit：timeline 默认/最大 limit，避免 session 级聚合拖垮 API。

部署校验：

- `aic_agent_sdk_api` 和 `aic_agent_sdk_worker` 使用同一 ac_namespace/ac_env；`aic_agent_sdk_session` 不存 ac_namespace/ac_env。
- `aic_agent_sdk_worker` 已注册并消费该 ac_namespace，否则 SubmitInput 只会入 AC mailbox，不会有后续 event。
- AC stream client 需要配置 server streaming 支持；HTTP 网关需要允许 SSE 长连接。
- 日志必须能按 HTTP logid 串到 `aic_agent_sdk_session` 和 AC RPC logid。
- 指标至少覆盖：请求量/错误率/延迟、下游 RPC 错误、SSE 连接数、SSE 断开原因、recover_queue 过期次数、payload 非法 JSON 次数。

## 未决问题

1. `after_event_id` 到 AC cursor 的映射是否有稳定支持。没有稳定映射时，前端恢复协议应以 `recover_queue_id + cursor` 为主。
2. `StopRunning` 不带 `thread_id` 时的选择策略需要产品确认：只停 main thread，还是停 session 下所有 running child thread。
3. resume plan input 当前 HTTP IDL 只有 `content`，而旧 planmode 是结构化 answers；是否需要在 `SubmitInputHTTPRequest` 增加结构化 plan answers。
4. 本地登录兼容保留到什么程度。旧 Web demo 有本地用户表和密码逻辑，线上 aic_agent_sdk_api 应该只接受可信登录态。
5. worker timeline payload schema 是否需要继续放在 `cloudagent/protocol/event` 中约束。无论是否约束，`aic_agent_sdk_api` 都不应该反序列化该 payload。
