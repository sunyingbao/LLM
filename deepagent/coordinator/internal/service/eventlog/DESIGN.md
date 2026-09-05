# EventLog 模块设计

`eventlog` 是 Agent Coordinator 的输出事实源。

## 1. 这个包是什么

`eventlog` 保存 Agent 执行过程中上报的事件。

它面向：

- UI 渲染
- 控制台观察
- 调试回放
- 审计
- 故障诊断

## 2. 这个包不是什么

`eventlog` 不是：

- mailbox
- Agent 执行器
- 业务状态存储
- 唯一实时推流实现

实时 stream 可以存在，但应视为 EventLog 的派生消费通道。

## 3. 负责什么

`eventlog` 负责：

- append event
- 按 Thread 查询事件
- 按游标查询事件
- 支持回放
- 支持实时消费的派生接口

## 4. 不负责什么

`eventlog` 不负责：

- 驱动 Agent 执行
- 修改 mailbox 状态
- 判断业务任务是否成功
- 替代业务方 Agent 内部日志

## 5. Event 语义

Event 是 Agent Coordinator 的统一输出载体。

第一版 Event 建议保持可扩展：

- `EventID`
- `ThreadID`
- `MessageID`
- `SourceAgent`
- `Type`
- `CreatedAt`
- `Payload`
- `Metadata`

`Type` 可以表达：

- token
- tool_start
- tool_end
- message
- error
- hitl_requested
- status

但 Coordinator 不应过早绑定业务方全部事件枚举。

## 6. 写入路径

Agent 输出推荐路径：

```text
Agent 服务
  -> Protocol AppendEvent
  -> EventLog
  -> UI / 控制台 / 实时派生流
```

如果实时流丢失，消费方应能通过 EventLog 游标补偿。

## 7. 当前阶段结论

EventLog 是输出事实源。任何 UI、控制台或实时推流能力都应围绕 EventLog 构建，而不是直接绑定业务 Agent 的进程内流。
