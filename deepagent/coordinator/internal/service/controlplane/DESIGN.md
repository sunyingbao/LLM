# ControlPlane 模块设计

`controlplane` 是 Agent Coordinator 的控制面入口。

它合并承载两个子领域：

- Agent 注册与寻址
- 逻辑 Thread 控制

这两个概念可以区分，但当前不拆成两个一级包。原因是它们的调用面、服务形态和演进路径高度耦合，过早拆分会增加胶水和边界成本。

## 1. 这个包是什么

`controlplane` 面向用户、系统和业务 Agent 提供统一管理入口。

它管理的是：

- Agent 定义、实例、能力和心跳
- 逻辑 Thread 的创建、查询、关闭和状态
- 向 mailbox 投递消息的控制面入口

## 2. 这个包不是什么

`controlplane` 不是：

- Agent 执行器
- Scheduler
- Mailbox 的底层实现
- EventLog 的底层实现
- 业务 Agent 内部状态存储

它不直接运行 Agent，也不假设 Agent 如何实现自己的上下文和推理循环。

## 3. 负责什么

`controlplane` 负责：

- 注册 Agent 类型和能力
- 注册 Agent 服务实例
- 接收 Agent 实例 heartbeat
- 查询可处理某类消息或任务的 Agent
- 创建逻辑 Thread
- 查询逻辑 Thread
- 关闭逻辑 Thread
- 挂起或恢复逻辑 Thread
- 向 Thread 的 mailbox 投递输入
- 查询 Thread 的消息状态和事件状态

## 4. 不负责什么

`controlplane` 不负责：

- 执行业务任务
- 保存 mailbox 的完整消息生命周期
- 保存 Agent 输出事件
- 判断业务 Agent 内部是否真的在推理
- 直接写入业务 Agent 内部状态

## 5. 状态边界

`controlplane` 可以维护控制面可观测状态，例如：

- Agent 是否在线
- Agent 最近 heartbeat 时间
- Agent 能力描述
- 逻辑 Thread 是否 active / blocked / closed

这些状态描述的是控制面视角，不描述业务 Agent 内部执行细节。

如果需要表达某条消息正在被某个 Agent 实例处理，应使用 mailbox 的 claim / lease 语义。

## 6. 当前阶段结论

`controlplane` 是上层系统使用 Agent Coordinator 的入口。

它把用户或系统请求转成 mailbox 消息和逻辑 Thread 状态变化，但不直接参与 Agent 执行。
