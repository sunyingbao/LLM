# Agent Thread 接入指南

<callout emoji="🧠" background-color="light-blue">

Agent Thread 是单体 Agent 的运行内核。它把一次次输入组织成连续对话，负责上下文、事件、pending input、HITL 中断与恢复。
</callout>

## 它解决什么

当业务想把 Agent 做成连续对话，而不是一次输入、一次输出时，很快会遇到几个问题：

- 每轮都要自己拼历史。
- 工具消息、assistant 消息、用户新输入要自己写回上下文。
- 模型上下文窗口超限后要自己压缩。
- HITL 中断后要自己恢复被暂停的执行。
- 运行中的新输入要自己决定进不进入当前轮。
- 前端想实时观察过程，需要一套事件流。

Agent Thread 就是为这些问题准备的。

它不替你做 session、权限、渲染、消息队列、事件存储，也不依赖 Agent Coordinator。

在 SDK 内部，每个 Turn 会由一个单次执行体完成模型调用、工具调用和 ReAct loop。默认实现使用 `DeepAgent`；一次执行里的 model、tool、middleware、backend/sandbox 语义见 [DeepAgent 接入指南](../deepagent/index.md)。

接入 Agent Thread 时，业务首先要理解的是 Thread 如何管理多轮运行。

## 它负责什么，不负责什么

Agent Thread 负责单体 Agent 的多轮 runtime：

- 从历史恢复模型可见上下文。
- 接收新输入并决定是否启动新 Turn 或进入 pending input。
- 输出 runtime event，让外层系统观察模型、工具、HITL 和上下文压缩过程。
- 在 HITL 或 checkpoint 场景下恢复被暂停的 Turn。

Agent Thread 不负责：

- 服务端 scan、claim、lease、ack、append event、release。
- 产品 session、权限、UI、渲染和资产模型。
- 事件最终如何持久化、推流或回放。
- Agent Coordinator 的 thread state、message id 或 event envelope。

## 运行模型

```plaintext
Thread
  ├─ Context Manager
  │   ├─ history store
  │   ├─ context window
  │   ├─ compaction strategy
  │   └─ token usage
  ├─ Event stream
  └─ Turn
      ├─ input messages
      ├─ single-turn runner
      ├─ tool calls
      ├─ HITL / checkpoint
      └─ runtime events
```

几个概念只需要这样理解：

- `Thread`：一段连续 Agent 对话的 runtime 载体。
- `Turn`：Agent 消费一组输入并产生结果的一次运行。
- `Message`：进入 runtime 或由模型 / 工具产生的消息，复用 Eino `schema.Message`。
- `Event`：runtime 对外输出的运行事实，不是 UI 文案。
- `Context Manager`：维护本轮模型可见上下文。

Turn 不是 message。一条 message 可以启动一个 Turn；running Turn 中追加的多条 message，也可能被同一个 Turn 消费。

## 最小接入路径

### 1. 创建 Thread

下面的例子使用 SDK 内置的 DeepAgent。`TurnConfig` 配置每个 Turn 内部的执行；Thread 负责组织输入、上下文、事件和恢复流程。

构造 Thread 时传入基础 Turn 配置；确实需要按请求变化时，再使用
`TurnConfigProvider` 生成本次运行配置。

```go
events := make(chan agentthread.Event, 128)

sandboxBackend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
    RootDir:     workDir,
    VirtualMode: true,
})
// 这里用本地文件系统模拟一台 Agent 可以操纵的电脑。生产环境也可以替换成
// Docker、AI Infra、ByteSandbox 或业务自己的 SandboxBackend。
baseTurnConfig := &agentthread.TurnConfig{
    Agent: deepagents.Config{
        Model:            chatModel,
        MaxSteps:         100,
        MaxModelCalls:    30,
        FilesystemConfig: &deepagents.FilesystemConfig{WorkDir: workDir},
        Backend:          sandboxBackend,
        CheckpointStore:  checkpointStore,
        HITLConfig:       hitlConfig,
    },
}

thread := agentthread.New(
    threadID,
    baseTurnConfig,
    events,                                    // agent 运行事件总线,业务从这里获得运行状态,事件的通知
    agentthread.ThreadOptions{
        HistoryStore:       historyStore,      // 上下文历史存储后端，需要业务提供
        ContextWindow:      contextWindow,     // 模型上下文窗口大小
        CompactionStrategy: compactionStrategy, // 上下文压缩策略
        TokenCounter:       tokenCounter,      // token 计算器，用于预估 token 消耗
    },
)
```

配置分两类：

- `TurnConfig`：包含一个 `deepagents.Config`，外加计划事件、动态中间件和运行完成回调等 Thread 边界能力。
- `ThreadOptions`：Thread 如何管理上下文；也可以通过 `ContextManager` 字段替换内置上下文管理器。

### 2. 初始化

```go
if err := thread.Init(ctx); err != nil {
    return err
}
```

`Init` 会从 `HistoryStore` 恢复历史。没有 `HistoryStore` 时，Thread 只能作为内存 runtime 使用。

### 3. 消费事件

```go
go func() {
    for ev := range events {
        handleEvent(ctx, ev)
    }
}()
```

事件流要持续消费。LLM token、工具调用、HITL 请求、上下文压缩、Turn 结束都会通过事件暴露。

### 4. 提交输入

```go
result, err := thread.SubmitInput(
	ctx,
	schema.UserMessage(content),
	agentthread.WithTurnConfigProvider(func(ctx context.Context, req agentthread.TurnStartRequest) (*agentthread.TurnConfig, error) {
		cfg := baseTurnConfig.Clone()
		// 这里可以根据 req.TurnID、req.Input、业务 metadata 或用户 profile
        // 生成本次 turn 的模型、工具、middleware、预算和 HITL 配置。
        return cfg, nil
    }),
)
if err != nil {
    return err
}

if result.Started {
    go result.TurnHandle.Wait(ctx)
}
```

语义：

- 没有 active Turn 时，启动新 Turn。
- 已有 active Turn 时，输入进入 pending input。
- `SubmitInput` 成功只表示 Thread 已接收输入，不表示模型已经完成回复。
- `SubmitInputResult.TurnHandle.Wait` 只等待当前 turn，不会误等后续 turn。

### 5. 恢复中断

```go
handle, err := thread.ResumeTurn(ctx, turnID, agentthread.ResumeTurnOptions{
    CheckpointID:       checkpointID,
    ResumeInterruptIDs: []string{interruptID},
    ResumeData:         resumeData,
	ConfigProvider: func(ctx context.Context, req agentthread.TurnStartRequest) (*agentthread.TurnConfig, error) {
		cfg := baseTurnConfig.Clone()
		// ResumeTurn 和 SubmitInput 共享同一类配置提供器。业务可以用
		// req.Resume 或 turnID 查询自己的业务级 turn profile。
        return cfg, nil
    },
})
if err != nil {
    return err
}

err = handle.Wait(ctx)
```

`ResumeTurn` 只用于恢复已有 Turn。它不会并入 active Turn，active Turn 存在时新的 resume 会被拒绝。

## Context Manager

上下文管理不是把历史 append 到数组。

Agent Thread 需要一套组件来保证：

- 本轮输入、工具结果、assistant 输出会写入历史。
- 下一次模型请求能看到正确上下文。
- 进程重启后能从历史恢复。
- 上下文窗口超限时能压缩。
- 模型返回 usage 后能修正 token 统计。
- HITL resume 时上下文不和中断前脱节。

所以 SDK 把 Context Manager 定义成接口：

```go
type ContextManager interface {
    ReloadHistory(ctx context.Context) error
    AddHistory(ctx context.Context, turnID string, msg ...*schema.Message) error
    History(ctx context.Context) []*schema.Message
    ContextUsage() agentthread.ContextUsageSnapshot
    RecordModelUsage(ctx context.Context, usage *model.TokenUsage)
}
```

它只负责“模型可见上下文”，不负责业务 session、消息投递、权限、渲染或事件推流。

### 内置实现做了什么

常规业务不需要实现这个接口。

`New` 会在 `ThreadOptions.ContextManager` 为空时，根据其余选项构造内置的 `MemoryContextManager`；传入自定义 `ContextManager` 时则直接使用它。

运行时它会：

1. `thread.Init` 时从 `HistoryStore` 恢复历史。
2. 模型请求前，把本轮输入、工具结果和 pending input 写入历史。
3. 模型请求前，把历史拼进本次请求。
4. 模型响应后，把 assistant 消息写入历史。
5. 根据模型 usage 或本地 `TokenCounter` 跟踪上下文用量。
6. 达到压缩条件时调用 `CompactionStrategy`。
7. 压缩后写入 compact record，并替换内存上下文。

业务要提供的依赖：

- `HistoryStore`：生产场景建议提供；否则无法跨进程恢复历史。
- `ContextWindow`：模型上下文窗口；不传时按模型名尝试查默认值。
- `TokenCounter`：本地 token 估算；模型 usage 不可用时需要。
- `CompactionStrategy`：需要长对话压缩时提供。

### 什么时候自定义

大多数业务不用自定义 Context Manager。

只有默认策略无法满足时再自定义，例如：

- 历史不是 append/list 存储模型。
- 上下文需要按权限、资产或消息类型过滤。
- 上下文来自外部长期记忆、检索系统或业务知识库。
- 压缩和恢复必须绑定业务自己的摘要服务。

自定义时要保证 `History()` 返回的就是模型真正可见的上下文，并且 `ReloadHistory` 能在进程重启后重建它。

## 上下文压缩

压缩是长对话能持续运行的关键能力。

模型上下文窗口有限，不能无限塞历史；直接删除旧消息又会丢任务背景。所以压缩要把旧历史变成一个可恢复的摘要锚点。

### 压缩前后的历史长什么样

可以把历史理解成一条 append-only 的记录流：

```plaintext
m1, m2, m3, m4, m5, m6 ...
```

当上下文过长时，压缩策略会把一段旧历史压成一个 compact record，同时返回一份新的内存上下文：

```plaintext
HistoryStore:
  m1, m2, m3, m4, compact(summary of m1..m4), m5, m6

Memory context after compact:
  summary, m5, m6
```

注意两点：

- compact record 会写入 `HistoryStore`，业务不需要单独再存一份。
- 内存上下文会被 `CompactionResult.Rebuilt` 替换，它应该是“下一次模型请求可直接使用”的消息列表。

旧消息是否还留在底层存储，取决于 `HistoryStore` 的实现；但恢复时，内置 Context Manager 会倒序读取历史，遇到最近的 compact record 后停止，再用 compact record 和它之后的消息重建上下文。

SDK 用 `CompactionStrategy` 描述压缩策略：

```go
type CompactionStrategy interface {
    ID() string
    Compact(ctx context.Context, current []*agentthread.Message) (*agentthread.CompactionResult, error)
    Resume(ctx context.Context, compact *agentthread.CompactRecord, postCompactMessages []*agentthread.Message) (*agentthread.ResumeResult, error)
}
```

`Compact` 的输入是当前模型可见上下文。

它需要返回两类结果：

- `CompactRecord`：要持久化的压缩锚点，通常是一条 summary message，加上策略 id 和策略私有 payload。
- `Rebuilt`：压缩后新的内存上下文，通常是 summary message 加最近未压缩消息。

`Resume` 只在恢复历史时使用。比如进程重启后，`thread.Init` 调用 `ReloadHistory`，如果读到了 compact record，就会调用 `Resume(compact, postCompactMessages)`。

`Resume` 返回的 rebuilt history 会直接成为当前模型可见上下文。

生产环境建议同时实现 `AutoCompactLimiter`：

```go
type AutoCompactLimiter interface {
    AutoCompactTokenLimit() int64
}
```

这样内置 Context Manager 会按 token limit 判断是否压缩，避免每次 `AddHistory` 都尝试压缩。

### 压缩什么时候触发

内置 Context Manager 会在 `AddHistory` 之后判断是否压缩。

这意味着用户输入、工具结果、assistant 输出写入历史后，都可能触发压缩。

上下文用量的来源有两类：

- 模型返回的 token usage：更可信，作为最近一次成功模型请求的基线。
- 本地 `TokenCounter`：用于估算模型请求后新增的用户消息、工具消息等。

业务不需要自己在每轮判断是否压缩；只需要提供合适的 `CompactionStrategy` 和压缩阈值。

### 压缩执行边界

`CompactionStrategy.Compact` 可以调用模型生成摘要。模型调用过程中仍然可能触发 token usage callback，进而调用 `ContextManager.RecordModelUsage`。

因此内置 `MemoryContextManager` 不会在持有 history 锁时执行 `CompactionStrategy.Compact`。它会先复制当前模型可见上下文，释放锁后执行压缩，最后在提交压缩结果前确认 history 没有被新的输入或输出修改过。

如果压缩期间 history 发生变化，当前压缩结果会被丢弃，下一次模型请求边界再重新判断是否需要压缩。这样可以避免用旧快照覆盖新消息，也避免压缩策略内部模型调用和 context manager 自身回调形成死锁。

### 一个好的压缩策略要保证什么

压缩策略不是随便写一段摘要。

它至少要保证：

- summary 保留任务目标、关键约束、重要结论和未完成事项。
- `Rebuilt` 能直接作为后续模型上下文使用。
- `Resume` 能把 compact record 和压缩点之后的消息重新拼成同样语义的上下文。
- 策略 id 稳定，后续版本能识别自己写下的 compact record。
- 压缩不能破坏 HITL resume 所需的上下文一致性。

## HITL 与 Checkpoint

HITL 是 Agent runtime 执行过程中的中断机制。

在默认 DeepAgent 执行中，HITL 发生在工具调用前：当模型准备调用敏感工具时，执行体可以暂停当前执行，并等待外部给出 resume data。底层 checkpoint / resume 机制见 [DeepAgent 接入指南](../deepagent/index.md)。

Agent Thread 在这一层要解决的是：把这次中断变成可观察的 runtime event，并让业务后续可以调用 `ResumeTurn` 回到被暂停的 Turn。

### 业务会看到什么

当发生 HITL 时，Thread 会输出事件。

常见事件：

- `approve_requested`：单个审批或 review/edit 请求。
- `followup_requested`：需要用户补充信息。
- `interrupt_batch_requested`：一次执行里有多个 interrupt，需要业务批量处理。

这些事件里会带上恢复所需的关键信息：

- `turnID`：事件所属 Turn。
- `checkpointID`：恢复被暂停执行需要的 checkpoint。
- `interruptID`：本次中断点 id。
- 业务展示所需的信息，例如工具名、工具参数、追问问题。

代码里的事件字段名是 `TurnID`，在这篇文档里等价理解为 turn id。

业务拿到事件后，不应该只把它当成 UI 提示。它同时是后续 `ResumeTurn` 的输入来源。

这里需要区分两类存储：

- `HistoryStore`：恢复对话上下文，让 Agent 知道之前聊过什么。
- `CheckpointStore`：恢复被中断的执行，让工具调用从中断点继续。

如果要支持 HITL resume，通常两者都需要。

### 业务要怎么恢复

审批完成后，业务构造 `ResumeData`，再调用 `ResumeTurn`。

最常见的 approve 场景：

```go
resumeData := map[string]any{
    interruptID: &deeptools.ApprovalResult{
        Approved: true,
    },
}

handle, err := thread.ResumeTurn(ctx, turnID, agentthread.ResumeTurnOptions{
    CheckpointID:       checkpointID,
    ResumeInterruptIDs: []string{interruptID},
    ResumeData:         resumeData,
})
```

拒绝审批时：

```go
reason := "用户拒绝执行该命令"
resumeData := map[string]any{
    interruptID: &deeptools.ApprovalResult{
        Approved:         false,
        DisapproveReason: &reason,
    },
}
```

恢复后，runtime 会回到被暂停的工具调用处：

- 如果审批通过，工具继续执行。
- 如果审批拒绝，工具 wrapper 会把拒绝结果作为工具结果返回给模型。
- 如果是 follow-up，用户回答会作为工具结果返回给模型。
- 如果是 review/edit，业务可以返回编辑后的工具参数。

所以 HITL 不是“暂停后重新开一轮对话”，而是“从原来的中断点继续本次执行”。

### 端到端流程

一次 HITL 通常是：

1. 模型触发敏感工具。
2. HITL 策略要求审批。
3. Thread 输出 approve / follow-up / interrupt batch 事件。
4. 外部系统持久化事件并展示 UI。
5. 用户审批或拒绝。
6. 业务用事件里的 `turnID`、`checkpointID`、`interruptID` 构造 `ResumeTurn`。
7. 被暂停的执行从 checkpoint 继续。

如果进程在 HITL 中断后重启，通常流程是：重新创建 Thread，调用 `Init` 恢复历史，然后根据外部保存的 HITL 事件和用户决策调用 `ResumeTurn`。`HistoryStore` 负责恢复上下文，`CheckpointStore` 负责恢复被暂停的执行。

### 中断期间还能不能继续输入

Agent Thread 只负责 runtime 语义。

当一个 Turn 因 HITL 暂停时，业务通常应该把它视为等待外部决策：先处理 resume，再继续推进当前 Turn。

如果业务接入了 Agent Worker，把 thread 标记为 blocked 是合理做法；如果只使用 Agent Thread，也应该在自己的业务状态里记录“等待审批”，避免继续把普通用户输入误投到一个等待恢复的 Turn。

## 和 Agent Worker 的关系

Agent Thread 是 runtime kernel。

Agent Worker 是 CloudAgent 背后的底层服务端 worker 框架。

典型关系：

```plaintext
Agent Worker
  - pull coordinator message
  - ack / append event / release / block

业务 adapter
  - 把 coordinator message 转成 schema.Message
  - 调用 Agent Thread SubmitInput / ResumeTurn
  - 把 Agent Thread event 转成 coordinator event

Agent Thread
  - 管理上下文
  - 执行 Turn
  - 输出 runtime event
```

`agentthread` 不依赖 Agent Coordinator，也不应该理解 coordinator message id。

## 接入清单

接入 Agent Thread 前，业务至少要准备：

- chat model。
- thread id 和 turn id 生成策略。
- history store。
- checkpoint store。
- 事件消费逻辑。
- HITL 交互逻辑。
- 如果有长对话，准备 context window、token counter 和 compaction strategy。
- 外层 session / message / user 关联逻辑。

## 常见误解

- Turn 不是 message。一条 message 可以启动一个 Turn，running Turn 中追加的多条 message 也可能被同一个 Turn 消费。
- `SubmitInput` 成功只表示 Thread 已接收输入，不表示模型已经完成回复。
- `SubmitInputResult.TurnHandle.Wait` 只等待当前 turn，不会误等后续 turn。
- `HistoryStore` 恢复的是对话上下文；`CheckpointStore` 恢复的是被中断的执行点，两者不要混用。
- `agentthread` 不依赖 Agent Coordinator，也不应该理解 coordinator message id、event id 或 lease。

## 下一步

- 想把这个多轮 runtime 接成服务端 Agent：优先看 [`CloudAgent 接入指南`](../cloudagent/index.md)。
- 确认要自定义底层 worker host contract：再看 [`Agent Worker 底层机制说明`](../agentworker/index.md)。
- 想理解每个 Turn 内部的模型、工具、middleware 和 backend：继续看 [`DeepAgent 接入指南`](../deepagent/index.md)。
- 想了解工具、Sandbox、Skill、Memory 等能力：继续看 [`内置工具说明`](../deepagent/builtin-tools.md)。
