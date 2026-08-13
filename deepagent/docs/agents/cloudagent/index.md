# CloudAgent 接入指南

<callout background-color="light-blue">

CloudAgent 是本仓库推荐的服务端 Agent 接入层。它已经接近一个可复用的服务端 Agent 运行框架：默认使用 DeepAgent 执行每个 turn，用 Agent Thread 管理多轮运行，并把这些能力接到 Agent Coordinator 的服务端运行机制上。
</callout>

## 它解决什么

当业务要把 Agent 做成一个服务端产品时，通常不应该从底层 worker contract 开始。更常见的问题是：

- Agent 要能持续运行，而不是绑在一次 HTTP 请求里。
- 用户断线后，运行过程和结果还能继续保存和回放。
- 新输入、HITL 审批、任务取消、多 Agent 协作都要进入同一套运行机制。
- 业务希望主要配置模型、prompt、tools、skills、history、checkpoint、approval 和 workdir，而不是自己实现底层 worker 协议。

CloudAgent 就是为这个默认路径准备的。它把 DeepAgent、Agent Thread、工具能力、history、checkpoint、approval、多 Agent 协作和服务端 worker 机制组装在一起，让业务可以在一个接近成品的 Agent 运行框架上接入自己的模型、工具和产品层。

CloudAgent 不替业务定义 HTTP API、登录态、session 目录、WebUI 或资产模型。这些仍然属于业务产品层。

## 先给结论

大多数服务端 Agent 接入都应该从这里开始：

```go
import cloudagentworker "eino-cli/deepagent/cloud/worker"
```

如果业务已经有自己的 HTTP/TUI 接入层，但希望复用 CloudAgent 的 submit、timeline、stop 等运行时编排，可以使用：

```go
import cloudagentapi "eino-cli/deepagent/cloud/api"
```

如果业务已经有自己的 runtime，并且确实要直接接 Agent Coordinator 的 scan / claim / mailbox / eventlog / lease 机制，才考虑更低层的：

```go
import "eino-cli/deepagent/worker/cloud"
```

不要把下面这些路径当成 SDK public API：

| 路径 | 定位 |
| --- | --- |
| `cmd/cloud_agent/aic_agent_sdk_worker` | CloudAgent worker 的参考服务 wiring |
| `cmd/cloud_agent/aic_agent_sdk_api` | HTTP/WebUI reference service |
| `cmd/cloud_agent/aic_agent_sdk_session` | 默认 session directory 服务 |
| `cmd/cloud_agent/idl` | reference service 的 IDL |

这些 `cmd/cloud_agent/*` 代码可以参考、部署或 fork，但不承诺作为稳定 library 入口。业务接入时优先复用 `cloudagent/worker` 和 `cloudagent/api`，不要从 `cmd` 目录反向提炼 SDK 合同。

reference service 的 HTTP API 与 Session 模型由
`cmd/cloud_agent/generate_local_code.sh` 直接生成到各自子模块；它们不要求业务方
创建或依赖 API/Session 专用 Overpass。只有跨接入方共享的 Coordinator 协议继续
使用统一发布的 Coordinator Overpass 依赖。

## 分层关系

```text
业务服务 / 产品接入层
  - HTTP API、鉴权、权限、WebUI、session 目录、project/workspace
  - 参考实现：cmd/cloud_agent/aic_agent_sdk_api + aic_agent_sdk_session

cloudagent/worker
  - 推荐的服务端 Agent worker SDK 面
  - 组装 DeepAgent thread、模型、prompt、tools、skills、history、checkpoint、approval
  - 默认接入 Agent Coordinator worker host

cloudagent/api
  - 不绑定 Hertz 的 CloudAgent API use case
  - 提供 CreateThread、Submit、ListTimeline、SubscribeTimeline、StopRunning
  - 依赖 Agent Coordinator 抽象接口，不依赖 aic_agent_sdk_session

agentworker/cloud
  - 底层 Agent Coordinator worker host
  - scan、claim、pull、ack、append event、renew lease、release、close

agentworker
  - host 与业务 runtime 之间的底层 contract

deepagents / deepagents/agentthread
  - Agent 执行内核、上下文、HITL、checkpoint、middleware
```

这几层不要互相替代：

- `agentworker` 不理解 DeepAgent prompt、工具和事件 payload。
- `cloudagent/worker` 不负责 HTTP、登录态、session 列表和 WebUI。
- `cloudagent/api` 不负责登录态、session 目录、main thread 绑定和 Hertz response。
- `aic_agent_sdk_api` 不实现 worker scan/claim，也不理解 worker timeline payload schema；它只把产品 HTTP/session 语义适配到 `cloudagent/api`。
- `aic_agent_sdk_session` 是产品会话目录，不是 Agent runtime 状态机。

## 什么时候用 cloudagent/worker

使用 `cloudagent/worker` 的前提是业务接受默认 CloudAgent 运行模型：

- 一个 AC thread 对应一个 DeepAgent thread runtime。
- role、cwd 等运行 profile 来自 Agent Coordinator thread profile。
- worker 负责持续扫描并推进 runnable thread。
- runtime 通过 AC eventlog 输出 timeline event payload。
- history 和 checkpoint 由接入方提供持久化实现。

适用场景：

- 希望直接复用接近成品的服务端 Agent 运行框架，而不是自己实现 `agentworker.AgentThread`。
- 需要模型、工具、skills、HITL、自动 compact、子任务协作等默认能力。
- 可以接受通过 `Config` 和 `Deps` 注入模型、存储、工具和协作依赖。

不适用场景：

- 业务 runtime 不是 DeepAgent。
- 事件协议、输入协议、生命周期都由业务完全自定义。
- 业务只需要 Agent Coordinator host，不需要默认 thread builder。

这些场景才应考虑直接使用 `agentworker/cloud`。如果只是想获得服务端 Agent 的默认运行能力，不建议绕过 CloudAgent。

## 最小接入面

普通业务优先使用 `cloudagent/worker/bootstrap`。它把配置加载、模型/Fornax、MCP、MySQL/Redis、history/checkpoint、IDGen、memory、thread refs、AC client 和 Worker 启动统一封装，业务入口通常只剩：

```go
return bootstrap.Run(ctx, bootstrap.Options{
    Args:  args,
    Tools: businessTools,
})
```

本地和远端使用同一条代码路径，分别选择 `worker.local.yml` 和 `worker.remote.yml`。只有确实需要自行组装基础设施的接入方，才直接使用 `cloudagent/worker` 的两个底层对象：

Worker 启动后不再从远端配置中心、通用环境变量或额外 CLI flag 合并行为配置。`-conf` / `AGENT_WORKER_CONF` 只选择一个 YAML；模型、Fornax、存储、AC、runtime 和日志等行为都由该文件声明。YAML 可以用 `${NAME}` 引用部署系统注入的密钥，但只有显式写出的占位符才会读取环境变量。

| 对象 | 作用 |
| --- | --- |
| `Config` | 描述 worker/runtime 行为：namespace、coordinator、并发、workdir、skills、roles、models、prompt |
| `Deps` | 注入外部系统：history store、checkpoint store、thread refs、approval store、workdir resolver |

必填项：

- `Config.Host.Namespace`
- `Config.Host.Coordinator.PSM`，除非 `Deps.CoordinatorClient` 已提供
- `Config.Turn.Models`
- `Config.Turn.Roles`
- `Deps.HistoryStore`
- `Deps.CheckpointStore`

## 配置边界

当前 `cloudagent/worker` 的 public API 仍然以 `Config` 和 `Deps` 暴露接入面。接入和维护时不要把它理解成一份平铺的启动配置，而要按生命周期分成三类：

| 层级 | 解决的问题 | 典型字段 / 依赖 |
| --- | --- | --- |
| worker host | worker 进程如何连接 AC、扫描和运行 thread | `HostConfig.Namespace`、`Coordinator`、`Concurrency`、`ScanLimit`、`LeaseMS`、`IdleTimeout` |
| thread profile | 某个 thread 被 claim 后，如何作为长期运行的 Agent thread 存在 | role/model 选择、`WorkDir`、`Backend`、`HistoryStore`、`CheckpointStore`、context compaction、多 Agent 能力 |
| turn profile | 某一轮 ReAct loop 如何运行 | `ChatModel`、prompt/middleware/tools、HITL policy、`MaxSteps`、`MaxModelCalls`、plan mode / read-only mode |

这三个层级的变化频率不同：

- worker host 通常在进程启动时确定。
- thread profile 应能根据 AC thread metadata、role、用户、实验策略或业务配置在 thread 构建时解析。
- turn profile 应能根据本轮输入类型、运行模式、计划执行模式、动态模型策略或工具策略在 turn 开始时解析。

因此，新增配置或扩展点时先判断它属于哪一层：

- 不要把 prompt、tools、model budget 这类 turn 行为写进 worker host 配置语义。
- 不要把 history、checkpoint、backend/workdir 这类 thread 生命周期资源做成每个 turn 都重新决定的临时状态。
- 部署级行为配置只从启动时选中的 YAML 读取；thread metadata 和本轮 mode 仍可参与 SDK 内部的 thread/turn profile 解析，但不能暗中切换部署模型、Fornax 或基础设施配置。
- 不要用 `Config`、`RuntimeConfig`、`Provider`、`Factory` 这类泛名逃避语义判断。新字段或新函数应该能从名字看出它是在解析 thread profile，还是构建 turn runner config。

底层稳定接入口是 `Config` / `Deps`。其中 `HostConfig`、`ThreadConfig` 与 `TurnConfig` 已按生命周期拆分；后续新增字段或扩展点仍应遵守上面的配置边界，避免重新把不同生命周期的配置混合到同一层。

典型启动形态：

```go
cfg := cloudagentworker.Config{
    Host: cloudagentworker.HostConfig{
        Namespace:   "my_agent",
        Concurrency: 8,
        Coordinator: cloudagentworker.CoordinatorConfig{
            PSM: "ad.creative.aic_agent_coordinator",
        },
    },
    Thread: cloudagentworker.ThreadConfig{
        WorkDir: "/data/my_agent/workdirs",
    },
    Turn: cloudagentworker.TurnConfig{
        Prompt: cloudagentworker.PromptConfig{Text: basePrompt},
        Models: map[string]cloudagentworker.ModelProfile{
            "default": {
                ChatModel: chatModel,
                ModelName: "doubao",
            },
        },
        Roles: map[string]cloudagentworker.RolePreset{
            cloudagentworker.DefaultRoleID: {
                Model:          cloudagentworker.ModelPolicy{Default: "default"},
                ApprovalPolicy: cloudagentworker.ApprovalPolicyNormal,
            },
        },
        Defaults: cloudagentworker.TurnDefaults{
            Capabilities: cloudagentworker.TurnCapabilities{
                Skills: cloudagentworker.SkillConfig{Sources: []string{"/data/my_agent/skills"}},
                Tools:  extraTools,
            },
            Budget: cloudagentworker.TurnBudget{
                MaxSteps:      100, // 可选；单个 turn 的最大图运行步数，0 表示使用 DeepAgent 默认值
                MaxModelCalls: 30,  // 可选；单个逻辑 turn 的最大模型调用次数，HITL resume 后继续消耗剩余额度
            },
        },
    },
}

deps := cloudagentworker.Deps{
    HistoryStore:    historyStoreProvider,
    CheckpointStore: checkpointStoreProvider,
    Approvals:       cloudagentworker.NewApprovalStore(),
}

if err := cloudagentworker.Run(ctx, cfg, deps); err != nil {
    return err
}
```

完整服务 wiring 可参考：

```text
cmd/cloud_agent/aic_agent_sdk_worker
```

## 协议边界

CloudAgent worker 和上层 API 之间只共享必要协议，不共享产品服务实现。

| 协议 | 位置 | 说明 |
| --- | --- | --- |
| 用户输入 / resume / compact payload | `cloudagent/protocol/input` | API 写入 AC message，worker 读取并解释 |
| worker timeline payload | `cloudagent/protocol/event` | worker 写入 AC event payload；只定义 payload，不定义 AC event header |
| timeline wire shape | `cloudagent/protocol/timeline` | API/TUI/WebUI 看到的 `AC event envelope + payload` |
| API use case | `cloudagent/api` | 不绑定 Hertz/session 的 submit、timeline、stop 编排 |
| host message / event contract | `agentworker` | worker host 与 runtime 的最小 Go contract |

`aic_agent_sdk_api` 对 timeline 的职责是输出：

```text
AC event envelope + opaque worker payload
```

也就是 `event_id/session_id/thread_id/turn_id/created_at_ms/event_type` 来自 AC event，`payload` 是 worker 写入的原始 JSON。payload 本身是 `MessageEventPayload`、`AssistantDeltaEventPayload`、`ToolCallEventPayload`、`ApprovalRequiredEventPayload`、`PlanUpdatedEventPayload`、`PlanInputRequiredEventPayload`、`CompactStartedEventPayload`、`ContextCompactedEventPayload`、`ErrorEventPayload` 等结构之一，不再重复携带 event header。

## 输入归因与输出观察

CloudAgent 的一条 input message 会触发一个 Agent Thread turn。turn 运行时会持续输出 timeline event。接入方经常需要把这些 event 关联回产品侧对象，例如前端消息、业务 history、资源 ID 或 trace。

推荐做法是：通过 `cloudagent/api.AgentAPI.Submit` 发送用户输入时，把产品侧关联信息放进 `SubmitRequest.Metadata`。

```go
_, err := agentAPI.Submit(ctx, cloudagentapi.SubmitRequest{
    UserID:   uid,
    ThreadID: threadID,
    Input:    &cloudagentapi.SubmitInput{Parts: parts},
    Metadata: map[string]string{
        "frontend_message_id": frontendMessageID,
        "history_id":          historyID,
        "resource_id":         resourceID,
        "trace_id":            traceID,
    },
})
```

这份 metadata 会沿着下面的链路传播：

```text
cloudagent/api.SubmitRequest.Metadata
  -> AC message metadata
  -> agentworker.Message.Metadata
  -> agentthread.SubmitInput WithInputMeta
  -> agentthread.Event.ConsumedInputsMeta
  -> cloudagent/protocol/event payload.consumed_inputs_meta
```

最终 timeline event payload 中会出现：

```json
{
  "consumed_message_ids": ["178xxx"],
  "consumed_inputs_meta": [
    {
      "frontend_message_id": "msg_123",
      "history_id": "his_456",
      "resource_id": "res_789",
      "trace_id": "trace_abc"
    }
  ]
}
```

语义规则：

- `consumed_inputs_meta[i]` 对应第 `i` 个 consumed input。
- 如果一个 turn 消费了多个 input，会按消费顺序输出多个 meta。
- 如果 input 没有 metadata，对应位置可能为空；如果整个 turn 都没有 input meta，payload 会省略 `consumed_inputs_meta`。
- CloudAgent API 当前对外的 metadata 类型是 `map[string]string`。如果需要复杂结构，建议业务先编码成稳定字符串字段，不要把业务对象直接塞进 runtime。

如果业务需要在 worker 内部旁路观察输出，可以配置 `cloudagent/worker.Deps.ThreadOutputObserver`：

```go
deps := cloudagentworker.Deps{
    ThreadOutputObserver: func(ctx context.Context, obs cloudagentworker.ThreadOutputObservation) {
        if obs.Item.Event == nil {
            return
        }

        var attr struct {
            ConsumedMessageIDs []string            `json:"consumed_message_ids"`
            ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta"`
        }
        if err := json.Unmarshal(obs.Item.Event.Payload, &attr); err != nil {
            return
        }

        // 这里适合写日志、metrics、trace、旁路索引或轻量状态同步。
    },
}
```

`ThreadOutputObserver` 是只读旁路：

- 它观察的是已经成功写给 worker host 的 `ThreadOutputItem`。
- 传入 observer 的 item 是防御拷贝，修改它不会影响真实输出。
- observer 有独立队列，队列满会丢 observation 并打 warning，不阻塞主输出链路。
- observer panic 会被 recover；执行超过 1s 会打 slow log。
- 不要把它当作可靠事件消费或前端渲染链路。前端展示仍然应该走 `ListTimeline` / `SubscribeTimeline`。

给 Codex 或维护者快速定位时，从这些代码入口开始：

| 目的 | 代码入口 |
| --- | --- |
| API 提交 input metadata | `cloudagent/api/types.go` 的 `SubmitRequest.Metadata`，以及 `cloudagent/api/agent_api.go` 的 `Submit` |
| AC message 到 worker message | `agentworker/cloud/adapter.go` 的 `messageFromAC` |
| worker message metadata 进入 Agent Thread | `cloudagent/worker/thread/thread_execution.go` 的 `postUserInput` |
| thread 保存 input meta | `deepagents/agentthread/thread.go` 的 `WithInputMeta`、`activeRun.consumedInputsMeta` |
| event payload 输出归因字段 | `cloudagent/protocol/event/event.go` 的 `consumed_inputs_meta` 字段，以及 `cloudagent/worker/thread/event_mapper.go` |
| worker 内部旁路观察输出 | `cloudagent/worker/config.go` 的 `Deps.ThreadOutputObserver`，以及 `cloudagent/worker/thread/output_observer.go` |

## cloudagent/api 和 aic_agent_sdk_api 的边界

`cloudagent/api.AgentAPI` 抽的是 CloudAgent 运行时 use case，不是 HTTP 框架：

| 方法 | 负责 | 不负责 |
| --- | --- | --- |
| `CreateThread` | 创建 AC thread，编码初始 input payload，记录 parent metadata | 创建/绑定产品 session |
| `Submit` | 编码 input/resume/compact message，选择 SendMessage 或 ResumeFromBlock | 自动寻找 main thread |
| `ListTimeline` | 基于 thread/turn/session 查询并归一化 timeline wire shape | WebUI 渲染、session 权限 |
| `SubscribeTimeline` | 单向订阅 AC session stream，输出 queue/event frame | 双向通信、浏览器 SSE 格式 |
| `StopRunning` | 选择显式 thread 或 session 下 RUNNING/BLOCKED thread；RUNNING 走 CancelInput，approval-blocked 走 cancel-turn resume | 关闭 session 或归档 |

`aic_agent_sdk_api` 保留业务接入层职责：Hertz handler、登录态、session RequireView、main thread 找/建/绑定、TouchSession、BaseResp、SSE 编码和 WebUI 静态资源。

## session 为什么不在 cloudagent 里

`aic_agent_sdk_session` 是产品接入层，不是 Agent runtime 复用层。

`aic_agent_sdk_api` 负责：

- HTTP/Hertz handler。
- 登录态到 uid。
- workspace/project 解析。
- 调 session 服务做 session directory。
- 调 `cloudagent/api.AgentAPI` 创建 thread、发送 message、cancel、list/subscribe event。
- 给 WebUI 输出 SSE 和静态资源。

`aic_agent_sdk_session` 负责：

- 用户视角 session 目录。
- title、project、last preview、main thread 绑定。
- session 列表、project 聚合、关闭/归档。

这些能力很容易被业务定制：鉴权、权限、目录模型、project 组织、WebUI、标题生成、分享协作、搜索排序都可能不同。把它们抽成 public lib 会把产品语义过早固化。当前推荐方式是：

- 需要默认体验：直接部署或参考 `cmd/cloud_agent/aic_agent_sdk_api` / `aic_agent_sdk_session`。
- 需要业务定制：实现自己的 API/session，但复用 `cloudagent/api`、`cloudagent/worker` 和 `cloudagent/protocol/*`。
- 需要完全自定义 runtime：直接用 `agentworker/cloud`。

## 扩展点

优先通过这些入口扩展：

| 需求 | 扩展点 |
| --- | --- |
| 换模型或多模型 | `Config.Turn.Models`、`RolePreset.Model`、`TurnProfileResolver` |
| 角色 prompt / 权限 | `Config.Turn.Roles`、`RolePreset.Prompt`、`RolePreset.ApprovalPolicy` |
| 注入业务工具 | 高阶入口用 `bootstrap.Options.Tools`；底层入口用 `Config.Turn.Defaults.Capabilities.Tools` |
| 自定义 history | `Deps.HistoryStore` |
| 自定义 checkpoint | `Deps.CheckpointStore` |
| 自定义 workdir | `Deps.WorkDirResolver` |
| 子任务命名 | `Deps.ThreadRefs` |
| approval 复用 | `Deps.Approvals` |
| 等待子任务事件 | `Deps.MessageWaitObserver` |

不要为了复用内部实现去 import `cloudagent/worker/thread` 或修改 builder 内部文件。若这些扩展点不够，先讨论是否应该新增 `Config` / `Deps` 字段；只有业务确实需要完全控制 runtime 时，才退到底层 `agentworker/cloud`。

## 常见误解

- 不要把 `cmd/cloud_agent` 当成 SDK public API。它是 reference implementation，用来验证 SDK 边界和提供 dogfood 服务。
- `cloudagent/worker` 不包含业务 HTTP API、WebUI、session 目录或权限模型；这些属于业务产品层。
- `cloudagent/api` 是不绑定 Hertz/session 的运行时 use case，不负责 main thread 查找、登录态、BaseResp 或 SSE 编码。
- timeline payload 是 worker 写入的业务 payload；AC event envelope 承载 event id、type、thread id、turn id 和时间。
- 如果业务不接受默认 DeepAgent thread builder，不要强行改 `cloudagent/worker/thread` 内部实现，应先讨论新增扩展点；只有确实要完全自定义 runtime 时，才退到底层 `agentworker/cloud`。

## 验证

改 `cloudagent/worker`、`agentworker/cloud` 或 `aic_agent_sdk_worker` wiring 后，至少执行：

```bash
go test ./cloudagent/protocol/... ./cloudagent/worker/thread ./cloudagent/worker ./agentworker/...

(cd cmd/cloud_agent/aic_agent_sdk_worker && go test ./...)
```

改 `aic_agent_sdk_api` 和 shared protocol 后，执行：

```bash
(cd cmd/cloud_agent/aic_agent_sdk_api && go test ./...)
node --check cmd/cloud_agent/aic_agent_sdk_api/webui/static/api.js
```

真实链路验证参考：

- [`../../testing/cloud-agent-test-suite.md`](../../testing/cloud-agent-test-suite.md)
- [`../../runbooks/cloud-agent-codex-local-runbook.md`](../../runbooks/cloud-agent-codex-local-runbook.md)

上线前不要只看单测。至少需要在本地生产等价链路里跑通 `dev.py start/status/smoke`、API contract smoke，以及一次真实 worker E2E。

## 下一步

- 确认要自定义底层 worker host contract：再看 [`Agent Worker 底层机制说明`](../agentworker/index.md)。
- 想理解默认 worker 里每个 turn 的多轮 runtime：继续看 [`Agent Thread 接入指南`](../agentthread/index.md)。
- 想理解模型、工具、middleware 和 backend：继续看 [`DeepAgent 接入指南`](../deepagent/index.md)。
- 只想复用工具、Sandbox、Skill、Memory 等能力：继续看 [`内置工具说明`](../deepagent/builtin-tools.md)。
