# 后端代码地图

本文面向读实现的人。SDK 接入方式见[使用指南](./index.md)，启动方式见[本地运行手册](../runbooks/deepagent-worker-e2e.md)。

## 目录与责任

| 目录 | 拥有的逻辑 | 不负责什么 |
| --- | --- | --- |
| `cmd/cloud_agent` | HTTP、用户权限、项目、产品 Session、WebUI；组装单进程服务 | 模型循环和 Worker 调度规则 |
| `cloud/api` | 提交输入、控制执行、查询/订阅 timeline；转换 API 与 Coordinator 契约 | Hertz、产品数据表、模型调用 |
| `coordinator` | 持久线程状态、租约、输入邮箱、事件日志和订阅 | 执行模型/工具 |
| `worker/cloud` | 认领线程、续租、投递/确认输入、收集输出、释放或关闭 | Prompt、模型选择、业务工具装配 |
| `worker/inprocess` | 本地线程实例、输入交付、输出订阅 | 云端租约和持久邮箱 |
| `worker/thread` | 把 Worker 消息/控制交给 AgentThread，把执行事件转换为 Worker 输出 | 产品 Session、服务端依赖装配 |
| `cloud/worker` | 服务端角色、模型、工具、安全策略、workspace、history、checkpoint 装配 | 再实现一套调度或模型循环 |
| `core/agentthread` | 多轮输入队列、当前执行、历史、压缩、中断恢复和事件收尾 | 云端认领与 HTTP |
| `core` | Eino 模型/工具循环、中间件、计划、技能、HITL、子 Agent | 产品会话和云端线程状态 |
| `runtime` | 统一 local/remote 客户端；选择后端和 timeline 契约 | 替代两种后端各自的交付保证 |
| `host`、`backend` | CLI 配置绑定、TUI、本地会话存储、模型传输和沙箱 | 第二套 Agent 执行循环 |
| `core/tools`、`tools` | 核心工具和宿主能力绑定 | 调度状态 |
| `core/memory`、`memory` | 执行侧记忆/持久化、结构化事实提取和合并 | 调度线程与产品会话 |

`cloud/protocol` 是共享消息协议，保留独立边界。`worker/thread/runtimectx` 只携带执行身份；本地装配不依赖 `cloud/worker` 的默认服务端实现。

## 先沿这两条链读

服务端输入链：

```text
HTTP → cloud/api.AgentAPI → Coordinator 的输入邮箱
                               ↓ Worker 认领并拉取
                     worker/cloud → worker/thread
                                         ↓
                                core/agentthread
                                         ↓
                                  core 的 Eino 图
```

输出沿 `core/agentthread → worker/thread → worker/cloud → Coordinator 事件日志/订阅 → cloud/api → HTTP/SSE` 返回。HTTP 提交成功表示输入已接收，不表示模型执行完成。

本地链：`cmd/deepagent → host/runtime → runtime/local → worker/inprocess → worker/thread → core/agentthread → core`。没有云端认领步骤，但执行核心和消息适配共用。

## 从哪些文件开始

- [core/constructor.go](../../core/constructor.go) 的 `New` 组装工具与中间件；[core/graph_builder.go](../../core/graph_builder.go) 的 `buildGraphWithConfig` 按加节点、连边、编译的顺序建图。图使用 `Config` 的值快照，不再维护第二份建图配置。
- [core/agentthread/thread.go](../../core/agentthread/thread.go) 读输入接收、排队、创建执行和收尾；[core/agentthread/run.go](../../core/agentthread/run.go) 读单次执行。`TurnStartRequest` 直接传到创建处，不拆开再拼回。
- [coordinator/coordinator.go](../../coordinator/coordinator.go) 直接处理输入、线程状态、租约、取消和关闭，没有第二个 `ThreadControl` 业务对象。[internal/storage](../../coordinator/internal/storage/) 负责线程/输入的 SQL 条件更新、查询与 Redis 队列读写，不决定下一步业务动作。
- [eventlog/publish.go](../../coordinator/internal/service/eventlog/publish.go) 处理事件保存与实时交付；[subscription.go](../../coordinator/subscription.go) 管理订阅的读取、确认、续期和清理。Worker 才负责调用 Agent、打断执行和释放运行资源。
- [worker/cloud](../../worker/cloud/) 读 `Worker.runClaim`，再读一次认领的生命周期、输入处理、输出处理。公共认领信息只保留一份；输入和输出各自拥有可变结果。
- [cloud/worker/agent_builder.go](../../cloud/worker/agent_builder.go) 读 workspace 和 AgentThread 装配。`threadSpec.Profile` 是线程配置唯一来源，workspace 返回真实工作目录后只更新这一处；每轮动态选择模型/能力仍在原时机执行。

## Coordinator 怎么读

先读 [Coordinator](../../coordinator/coordinator.go) 的公开方法，再看对应存储操作。`Coordinator` 的定义与方法在同一个文件；输入输出字段在 [requests.go](../../coordinator/requests.go) 和 [domain.go](../../coordinator/domain.go)，连接参数与默认值在 [config.go](../../coordinator/config.go)。

| 动作 | 方法 | 完成意味着什么 |
| --- | --- | --- |
| 提交输入 | `SubmitInput` | 输入已入队，不代表模型已执行 |
| 获取执行权 | `ScanRunnableThreads` → `ClaimThread` → `RenewThreadLease` | Worker 拿到有期限的线程租约 |
| 交付输入 | `ReadPendingInputs` → `ConfirmInputDelivery` | 输入已交给运行时，不代表线程结束 |
| 交付输出 | `PublishEvents` → `SubscribeSession` / `ListEvents` | 事件实时送达或持久化供查询 |
| 结束本次认领 | `ReleaseThread` | 根据待处理输入和状态释放租约，线程仍可继续使用 |
| 恢复阻塞 | `ResumeFromBlock` | 恢复输入优先入队，重新安排执行 |
| 请求取消 | `RequestInputCancel` | 撤销范围内的待处理输入，并入队控制消息，由 Worker 中断执行 |
| 关闭线程 | `RequestThreadClose` → `ConfirmThreadClosed` | 前者请求关闭，后者在 Worker 清理完成后确认终态 |

HTTP 路径、事件格式、控制消息字段和数据库结构没有变化；简化的是进程内 Go 接口。`ReleaseThread` 自己查询待处理输入，不再接收无效的 `HasPending`。旧的内部批量消息查询、用户线程分页、队列诊断接口与生成式 CRUD 已移除，当前 HTTP/CLI 使用的线程列表、timeline 和控制能力仍保留。

继续往下读时：输入队列直接返回已校验的消息；事件落库显式传入数据库或事务，不复制 `EventLog` 再递归调用；内部 `OpenSubscription` 统一新建和恢复订阅，订阅循环仅在出口区分队列失效与异常失败。事件发布保持原始顺序，不另建位置和推送标记数组。

## 不能当作冗余删除的边界

- **输入与输出并发处理**：等待模型执行时仍需接收停止/新输入；收尾先停止、再等待两个处理协程，不能改成串行。
- **执行结束与事件排空**：模型结束不代表所有事件已送出。`eventsDrained` 防止下一次执行抢先开始。
- **取消时的两次唤醒**：两次读取来源、metadata 更新和失败容错不同；都在 `Coordinator.RequestInputCancel` 中，不能合并为一次唤醒。
- **配置快照与动态回调**：建图配置保留快照，按轮选择配置的回调保留动态时机；不能统一为启动时求值。
- **产品 Session 与执行 Thread**：前者拥有用户组织/展示信息，后者拥有调度与执行状态。
- **权限和路径校验**：公开适配器即使短，也可能负责身份、类型、结果或安全验证，不能仅因转发调用就删除。

## 验证

在仓库根目录运行：

```bash
go test ./...
go build -mod=readonly ./...
go test -race ./deepagent/core ./deepagent/core/agentthread ./deepagent/coordinator/... ./deepagent/worker/... ./deepagent/cloud/worker/... ./deepagent/runtime/local/...
node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/*.test.mjs
```

模块测试不能替代真实页面的发送、审批、停止后继续和刷新恢复验证。没有改变存储格式，不需要数据迁移；代码回退应连同共享适配目录及其导入一起回退。
