# Mailbox 模块设计

`mailbox` 是 Agent Coordinator 的通信和任务驱动核心。

## 1. 这个包是什么

`mailbox` 是持久化消息系统，用于：

- 用户向 Agent 投递任务
- 系统向 Agent 投递控制消息
- Agent 向 Agent 发送协作消息
- Agent 消费、认领、确认消息

## 2. 这个包不是什么

`mailbox` 不是：

- Agent 执行器
- EventLog
- 注册中心
- UI 推流系统

它只负责消息生命周期，不负责执行消息内容。

## 3. 负责什么

`mailbox` 负责：

- 写入消息
- 查询消息
- 消费或订阅消息
- claim 消息
- ack / nack 消息
- 超时重试
- 记录消息投递和处理状态

## 4. 不负责什么

`mailbox` 不负责：

- 理解业务消息 payload
- 判断 Agent 内部是否真的在推理
- 保存 Agent 输出事件
- 替代 EventLog

## 5. 关键语义

### 5.1 Message

消息是 Agent Coordinator 的输入和通信载体。

基础字段应至少包含：

- `MessageID`
- `ThreadID`
- `Sender`
- `Target`
- `CreatedAt`
- `Payload`
- `Metadata`

具体 payload 由业务方解释。

### 5.2 Claim

claim 表示某个 Agent 实例认领了一条消息。

claim 不等于业务执行一定成功，只表示控制面观测到：

- 某个实例承诺处理这条消息
- 这条消息在 claim 有效期内不应被其他实例重复处理

### 5.3 Ack / Nack

Agent 执行完成后应对消息进行确认：

- `ack`
  - 消息处理完成
- `nack`
  - 消息处理失败，允许重试或进入失败状态

### 5.4 Timeout / Retry

如果 Agent claim 消息后未续约或未确认，mailbox 可以重新释放消息或进入失败状态。

## 6. 当前阶段结论

后台长时运行的驱动力来自 mailbox，而不是本框架内部 worker。

业务 Agent 通过消费 mailbox 获得任务，通过写入 mailbox 与其他 Agent 通信。
