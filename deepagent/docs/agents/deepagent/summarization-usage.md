# 上下文压缩（Summarization）使用文档

## 概述

上下文压缩中间件用于在对话历史过长时自动进行摘要压缩，防止超出模型的上下文窗口限制。当消息量达到触发条件时，中间件会将旧消息通过 LLM 生成摘要，仅保留摘要和最近的消息。

## 快速开始

### 最简用法

使用 `WithSummarization()` 即可启用，默认使用主模型生成摘要：

```go
agent, err := deepagents.New(ctx, &deepagents.Config{
    Model: chatModel,
}, deepagents.WithSummarization())
```

默认行为：
- 当消息总 token 数 >= 8000 时触发摘要
- 保留最近 5 条消息
- 使用主模型生成摘要

### 指定摘要模型

使用更小/更便宜的模型来生成摘要：

```go
agent, err := deepagents.New(ctx, &deepagents.Config{
    Model: mainModel,
}, deepagents.WithSummarizationModel(cheapModel))
```

### 自定义配置

```go
agent, err := deepagents.New(ctx, &deepagents.Config{
    Model: mainModel,
}, deepagents.WithSummarizationConfig(&summarization.SummarizationConfig{
    Model:               cheapModel,
    ModelMaxInputTokens: 128000,
    Trigger:             summarization.NewFractionTriggerConfig(0.8),
    Keep:                summarization.NewFractionTriggerConfig(0.1),
    ToolCompressSettings: &summarization.ToolCompressConfig{
        Trigger:            summarization.NewFractionTriggerConfig(0.5),
        KeepBudgetFraction: 0.2,
    },
}))
```

## 触发策略

通过 `Trigger` 字段配置何时触发摘要，支持三种策略：

| 策略 | 构造函数 | 示例 | 说明 |
|------|----------|------|------|
| 容量百分比 | `NewFractionTriggerConfig(0.8)` | 当前 token 达到模型容量的 80% 时触发 | 适合动态适配不同模型 |
| 固定 token 数 | `NewTokensTriggerConfig(8000)` | 当前 token >= 8000 时触发 | 适合精确控制 |
| 消息数量 | `NewMessagesTriggerConfig(20)` | 消息数 >= 20 时触发 | 最简单直观 |

**容量百分比**需要配合 `ModelMaxInputTokens` 使用（默认 128000）。

## 保留策略

通过 `Keep` 字段配置摘要后保留多少最近的消息，策略类型与触发策略一致：

```go
// 保留最近 10 条消息
Keep: summarization.NewMessagesTriggerConfig(10)

// 保留占模型容量 10% 的最近消息
Keep: summarization.NewFractionTriggerConfig(0.1)

// 保留最近 2000 token 的消息
Keep: summarization.NewTokensTriggerConfig(2000)
```

## 工具内容压缩

Agent 的工具调用参数和结果可能很长。可以单独配置工具内容压缩的触发阈值和保留预算：

```go
ToolCompressSettings: &summarization.ToolCompressConfig{
    Trigger:            summarization.NewFractionTriggerConfig(0.5),
    KeepBudgetFraction: 0.2,
}
```

内置工具使用 `BuiltinToolCompressRules`，自定义工具可通过 `CustomToolCompressRules` 配置规则。

## 自定义 Token 计数器

默认使用 `字符数 / 4` 作为粗略估计。可以提供更精确的计数器：

```go
// 使用自定义字符比例
TokenCounter: summarization.CreateTikTokenCounter(3) // 每 3 字符算 1 token

// 完全自定义
TokenCounter: func(messages []*schema.Message) int {
    // 调用 tiktoken 等精确计数
    return count
}
```

## 自定义摘要提示词

```go
SummaryPrompt: `请对以下对话进行摘要，重点保留：
1. 用户的核心需求
2. 已完成的操作
3. 当前进展

对话历史：
%s

摘要：`
```

注意：提示词中必须包含一个 `%s` 占位符，用于插入对话内容。

## 工作原理

```
消息列表 → 检查触发条件 → 未触发 → 返回原始消息
                          → 已触发 → 分离为 [待摘要] + [保留]
                                    → 压缩工具内容（可选）
                                    → LLM 生成摘要
                                    → 返回 [摘要消息] + [保留消息]
```

摘要结果通过 system prompt 注入到后续对话中，格式为：

```
## 对话历史摘要
以下是之前对话的摘要（原始 N 条消息已压缩）：

{摘要内容}

请基于此摘要继续对话。
```

## SummarizationConfig 完整字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `Model` | `model.ChatModel` | 主模型 | 用于生成摘要的模型 |
| `ModelMaxInputTokens` | `int` | 128000 | 模型最大输入 token（fraction 策略使用） |
| `Trigger` | `*TriggerConfig` | token >= 8000 | 触发条件 |
| `Keep` | `*TriggerConfig` | 最近 5 条 | 保留策略 |
| `ToolCompressSettings` | `*ToolCompressConfig` | 默认工具压缩策略 | 工具参数和结果压缩配置 |
| `SummaryPrompt` | `string` | 内置中文提示词 | 自定义摘要提示词 |
| `TokenCounter` | `func([]*schema.Message) int` | 字符数/4 | 自定义 token 计数器 |
| `OnSummarize` | `OnSummarizeFunc` | nil | 压缩状态持久化回调 |
| `MaxTokens` | `int` | 8000 | **已废弃**，使用 `Trigger` |
| `KeepLastN` | `int` | 5 | **已废弃**，使用 `Keep` |

## 注意事项

- 如果未配置 `Model`，摘要不会执行（静默跳过）
- 摘要失败时返回原始消息，不会导致 Agent 报错
- 中间件是线程安全的
- 摘要缓存可通过 `GetSummary()` 获取，`ClearSummary()` 清除
