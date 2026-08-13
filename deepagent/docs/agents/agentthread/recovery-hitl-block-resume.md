# 恢复、HITL 与 Block/Resume

<callout emoji="⏸️" background-color="light-blue">

恢复不是一个单点能力。Agent runtime、worker runtime 和业务产品协议里都有恢复语义，接入时要分清它们分别恢复什么。
</callout>

## 三种恢复

| 层级 | 解决的问题 | 典型机制 |
| --- | --- | --- |
| DeepAgent | 图执行被中断后如何继续 | checkpoint、interrupt、resume data |
| Agent Thread | 多轮上下文和当前 turn 如何恢复 | history store、checkpoint store、`ResumeTurn` |
| Agent Worker | 服务端任务如何重新被推进 | block、resume、lease、message、event |

这三层可以配合，但不能混为一谈。

## HITL 是 runtime 问题

HITL 发生在 Agent 执行过程中。

当模型准备调用敏感工具时，runtime 可以中断执行，并输出审批事件。业务收集用户选择后，再把审批结果作为 resume data 交回 runtime。

在 DeepAgent / Agent Thread 层，关键数据是：

- checkpoint id。
- interrupt id。
- resume data。
- 被中断的 turn。

这些数据描述的是“如何恢复这次模型和工具执行”。

## Block/Resume 是服务端状态问题

当 Agent 被 HITL 中断后，服务端 worker 不应该一直占着执行权空等用户审批。

更合理的方式是：

1. runtime 输出 HITL 事件。
2. worker 把 thread release 到 blocked 状态。
3. 用户审批后，业务向 Agent Coordinator 写入 resume message。
4. thread 重新变成 ready。
5. worker 再次 claim，并调用 runtime resume。

这里的 block/resume 描述的是“服务端任务什么时候可运行”。

它不等于 DeepAgent 的 graph resume。

## History 和 Checkpoint

History 负责上下文重建。

Checkpoint 负责图执行恢复。

简单理解：

- history 让 Agent 知道之前聊过什么。
- checkpoint 让中断的 graph run 能从中断点继续。

如果只配置 history，没有 checkpoint，HITL 后无法恢复图执行。

如果只配置 checkpoint，没有 history，跨进程恢复时上下文可能不完整。

## 事件设计

业务应该把中断和恢复过程显式写成事件。

推荐至少包含：

- turn start。
- approve requested / interrupt requested。
- turn blocked。
- resume received。
- turn resumed。
- turn end 或 turn error。

前端展示和 `wait_message` 这类工具，都应该依赖这些事件事实，而不是依赖某个进程内状态。

## 接入建议

如果业务只使用 DeepAgent：

- 使用 `WithCheckpointStore`。
- 运行时传入 `WithCheckpointID`。
- 审批后使用 `WithResumeData` 或 `WithResume`。

如果业务使用 Agent Thread：

- 配置 history store。
- 配置 checkpoint store。
- 通过事件识别 HITL 请求。
- 审批后调用 `ResumeTurn`。

如果业务使用 Agent Worker：

- HITL 事件需要转换成 coordinator event。
- thread 需要进入 blocked 状态。
- 审批结果应该通过 message 恢复执行。
- terminal event 需要记录本轮消费了哪些输入，方便 wait 逻辑定位结果。

## 常见错误

- 只做 UI 审批弹窗，但没有 checkpoint，导致无法恢复。
- 把 block/resume 当成 graph resume，混淆服务端状态和 runtime 状态。
- 把 checkpoint id 暴露成产品协议主键。
- 在 worker 进程内保存唯一审批状态，重启后无法继续。
- 没有把中断、恢复、完成写成事件，导致前端和工具只能猜状态。
