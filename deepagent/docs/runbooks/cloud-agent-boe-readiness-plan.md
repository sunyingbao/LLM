# DeepAgent BOE 部署准备清单

本文是部署前的核对清单，不代表 BOE 已验收。当前 [main.go](../../cmd/cloud_agent/main.go)
在一个 `cloud_agent` 进程内组装 HTTP API、Session、Worker 和 Coordinator。
内部模块不需要独立 RPC 服务、PSM、cluster 或额外监听端口。

## 配置与外部依赖

- 使用仓库根 Go module 构建 `./deepagent/cmd/cloud_agent`。发布物应包含所选
  YAML 引用的 prompt、skills 和其他必要文件。
- 通过 `-conf` 或 `AGENT_WORKER_CONF` 指定部署 YAML。仓库当前只有
  [worker.local.yml](../../cmd/cloud_agent/conf/worker.local.yml) 本地模板，
  不能把未提供的远端模板当作已完成的 BOE 配置。
- Worker YAML 定义 MySQL DSN、Redis 地址/认证/DB、namespace、模型、backend、
  并发、租约、checkpoint、日志及可选 memory/Fornax。只有显式写出的
  `${NAME}` 才读取环境变量；密钥由部署平台注入，不写入仓库。
- MySQL/Redis 地址、账号和权限由实际部署环境提供。当前 Coordinator 使用
  MySQL DSN 和 Redis 直连配置，不应填写已不存在的内部服务发现配置。
- HTTP 监听由 `DEEP_AGENT_SDK_API_ADDRESS` 指定；认证使用
  `DEEP_AGENT_SDK_API_AUTH_MODE`。BOE 应验证真实身份链路，不沿用本地默认用户。
- 单进程入口将 Worker 的 MySQL 和 namespace 传给 Session，将 namespace/backend
  传给 API；Session 表名/ID 配置和 API 其他配置仍由各自 loader 读取。按需使用
  `DEEP_AGENT_SDK_SESSION_CONF`、`DEEP_AGENT_SDK_API_CONF` 选择配置文件。
- 外部 AIInfra 沙箱依赖仍是外部服务，例如 `pippit.sandbox.gateway`。
  BOE 使用 `biz_type: test` 时应核实平台授权和配套 `biz_id/workdir` 约定；
  外部平台字段不能因内部模块改名而随意替换。
- 网关需支持 `/ad/deep_agent_sdk` HTTP API 和 SSE 长连接。确认超时、代理缓冲、
  请求上下文和环境标识传递符合目标 BOE 环境要求。

启动代码及配置边界参考 [bootstrap/run.go](../../cloud/worker/bootstrap/run.go)、
[Session 初始化](../../cmd/cloud_agent/deep_agent_sdk_session/main.go) 和
[API 配置](../../cmd/cloud_agent/deep_agent_sdk/service/config/config.go)。

## 数据与 ID

| 数据 | 来源 | 部署前检查 |
| --- | --- | --- |
| namespace、thread、mailbox、event log | [Coordinator SQL](../../coordinator/sql) | 表结构、读写权限及 namespace 注册 |
| Session 目录 | [Session SQL](../../cmd/cloud_agent/deep_agent_sdk_session/sql) | 用户/session/project 字段及索引 |
| history | [Worker SQL](../../cloud/worker/sql) | message ID、thread sequence、索引和历史兼容 |
| thread reference | [Worker SQL](../../cloud/worker/sql) | 按 `features.thread_refs.enabled` 决定是否启用 |
| memory | [Memory SQL](../../core/memory/gorm_store/sql) | 按 `memory.enabled` 决定是否需要四张 memory 表 |
| mailbox、session stream、history sequence、Redis checkpoint | Redis | 权限、命名空间隔离、容量及恢复行为 |

发布前对已有数据库执行 schema diff，制定保留数据的迁移。不要把本地
`dev.py init-db` 当成 BOE 通用迁移工具；它还会创建部分本轮未启用能力的表。

分别检查 Session、Worker 和 Coordinator 的 ID 来源。Session/Worker 支持配置
ID namespace；空值会落到本地生成器。当前 [Coordinator 构造](../../coordinator/coordinator.go)
仍使用本地 ID 生成器，不能仅凭 Worker 的 ID 配置就宣称多副本具有全局唯一 ID。
在启用多副本前完成这一项验证和部署方案确认。

## 发布前验收

先在受控测试环境运行代码检查：

```bash
go test ./deepagent/coordinator/... ./deepagent/cloud/... ./deepagent/worker/...
go test ./deepagent/cmd/cloud_agent/...
```

然后逐项验证目标 BOE 环境：

1. 单进程可启动；MySQL、Redis、模型和所启用的外部沙箱连通。
2. HTTP 认证、Session 创建与归属校验正常，输入能进入正确 namespace 的 Worker。
3. 模型 turn 完成，history、event log 和工作区文件副作用可核对。
4. 停止、关闭、租约失效和重新认领的行为符合预期；日志能按 session/thread/turn 关联。
5. SSE 与历史 timeline 一致；断线恢复应单独验证，不仅依赖现有同名 E2E case。
6. 开启 memory、thread reference、Fornax 时，对相应存储、权限和调用链补充验收。
7. 网关超时、进程退出、请求失败率和存储错误指标具备可观察结果。

测试方法见 [Worker E2E](deepagent-worker-e2e.md)。部署 profile 只描述目标 API、
测试身份和能力，不代替模型和 Worker 配置。

## 回滚与运维边界

发布前保存上一个可用构建及对应配置，明确 schema 迁移的回退条件。出现持续的
认证失败、ID 冲突、丢失历史或模型主链路不可用时，按发布平台流程回滚进程版本，
保留数据库、Redis 和日志用于定位；不要通过清库掩盖错误。

已有进程、GoLand 后端和未知端口不能盲目终止。先确认进程所属服务、工作区及管理
方式，再执行对应的发布或停止操作。BOE 服务创建、资源申请、访问权限和发布审批
由目标环境负责人确认，本清单不提供或虚构外部 PSM。
