# Langfuse 自托管部署

[Langfuse](https://langfuse.com) 是一个开源的 LLM 可观测性平台，用于追踪 LLM 调用和工具执行。

## 快速开始

### 1. 启动服务

```bash
cd docker/langfuse
sudo docker compose up -d
```

### 2. 访问 Langfuse

打开浏览器访问 http://localhost:13000

首次访问需要注册账户并创建项目。

## 服务组件

| 服务 | 端口 | 说明 |
|------|------|------|
| langfuse-web | 13000 | Langfuse Web UI 和 API |
| db | 5432 | PostgreSQL 数据库 |

## 常用命令

```bash
# 启动
sudo docker compose up -d

# 查看日志
sudo docker compose logs -f langfuse-web

# 停止
sudo docker compose down

# 完全清理（包括数据）
sudo docker compose down -v
```

## 与 DeepAgent CLI 集成

### API 密钥

内网测试环境配置：

```bash
export LANGFUSE_HOST=http://localhost:13000
export LANGFUSE_PUBLIC_KEY=pk-lf-c0c2c05c-c9f9-488c-a7a4-ea2e6f78c13c
export LANGFUSE_SECRET_KEY=sk-lf-60bbab35-1daa-4f8e-8e7b-fd8e99ca54ff
```

### 运行 DeepAgent CLI

配置环境变量后，CLI 会自动启用 Langfuse 追踪：

```bash
# 设置 Langfuse 配置
export LANGFUSE_HOST=http://localhost:13000
export LANGFUSE_PUBLIC_KEY=pk-lf-c0c2c05c-c9f9-488c-a7a4-ea2e6f78c13c
export LANGFUSE_SECRET_KEY=sk-lf-60bbab35-1daa-4f8e-8e7b-fd8e99ca54ff

# 设置模型配置
export ARK_MODEL=ep-20251210192323-m6rs6
export ARK_API_KEY=69bb0d4a-a06a-4d11-a9b2-a57b5062be35

# 运行 CLI
go run ./cmd/deepagent -workdir=.
```

### 代码集成

```go
import (
    "eino-cli/deepagent/core"
    "eino-cli/deepagent/core/tracing/langfuse"
)

// 方式 1: 使用环境变量（推荐）
agent, err := deepagents.New(ctx,
    deepagents.WithModel(model),
    deepagents.WithLangfuseTracing(), // 自动读取环境变量
)

// 方式 2: 显式配置
agent, err := deepagents.New(ctx,
    deepagents.WithModel(model),
    deepagents.WithLangfuseTracingConfig(&langfuse.TracingConfig{
        PublicKey: "pk-lf-c0c2c05c-c9f9-488c-a7a4-ea2e6f78c13c",
        SecretKey: "sk-lf-60bbab35-1daa-4f8e-8e7b-fd8e99ca54ff",
        Host:      "http://localhost:13000",
        SessionID: "my-session",
        UserID:    "user-123",
        Tags:      []string{"test", "demo"},
    }),
)
```

## 查看 Traces

1. 访问 http://localhost:13000
2. 登录后进入项目
3. 在 Traces 页面查看所有追踪记录
4. 点击单条 trace 查看详细的 LLM 调用和工具执行信息

## 故障排查

### 服务无法启动

```bash
# 查看详细日志
sudo docker compose logs langfuse-web

# 检查数据库连接
sudo docker compose exec db pg_isready -U postgres
```

### curl 返回 503

如果本机有 HTTP 代理，需要绕过：

```bash
curl --noproxy localhost http://localhost:13000
```

## 生产环境部署

详见 [Langfuse 官方文档](https://langfuse.com/self-hosting)。
