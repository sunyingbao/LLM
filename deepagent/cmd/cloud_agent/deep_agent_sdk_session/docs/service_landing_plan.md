# DeepAgent Session 职责与调用关系

`deep_agent_sdk_session` 是用户会话目录的进程内服务。它由
[CloudAgent 入口](../../main.go) 与 HTTP API、Worker、Coordinator 一起初始化，
不单独监听端口，不通过内部 RPC 或服务发现调用。

## 初始化与依赖

[sessionapp.New](../main.go) 打开 MySQL、创建 Session store、配置 ID 生成器，
并将宿主传入的 Coordinator 包装为 Session 所需的窄接口，最后返回
`*session.Service` 和数据库关闭函数。

```text
cmd/cloud_agent/main.go
  ├── Coordinator：thread / mailbox / event log
  ├── Session Service：MySQL 会话目录 + 共享 Coordinator
  ├── HTTP API：通过 service/deps 取得上述实例
  └── Worker：执行 thread，写 history 和事件
```

[HTTP Session 适配](../../deep_agent_sdk/service/session/session.go) 直接调用本地
Service，并在 HTTP 边界转换对外结构和错误。提交输入、停止运行、timeline 查询与
订阅由 `deepagent/cloud/api` 编排；Session 不参与 Worker 执行循环。

主要代码：

| 位置 | 职责 |
| --- | --- |
| [service/session/service.go](../service/session/service.go) | 会话和项目目录业务操作 |
| [service/session/view.go](../service/session/view.go) | 本地 Session / Thread view |
| [dal/session](../dal/session) | MySQL model、查询、条件更新 |
| [infra/ac/client.go](../infra/ac/client.go) | 通过共享 Coordinator 列出或关闭 Session 的 threads |
| [infra/idgen](../infra/idgen) | Session ID 生成 |
| [config/config.go](../config/config.go) | 表名、连接、namespace、ID 配置 |

## 数据与边界

表结构以 [t_agent_session.sql](../sql/t_agent_session.sql) 为准。Session 保存
`session_id`、`uid`、`project_name/project_path`、标题、目录状态、
`main_thread_id`、最后消息摘要和时间字段。

- `session_id` 是会话目录 ID，不等于 Coordinator 的 `thread_id`。
- 创建会话要求有效用户、项目名和项目路径；初始状态为 ACTIVE，可以暂时没有 main thread。
- `project_path` 记录服务端确定的项目位置；Sandbox 构造、工具执行与路径安全策略由 API/backend/Worker 各自实现，Session 不创建另一套执行环境。
- Session 的 ACTIVE / ARCHIVED / CLOSED 是目录状态，不能替代 thread 的运行状态。
- thread 摘要按需从 Coordinator 读取；完整 timeline、mailbox、history 和 checkpoint 不复制进 Session 表。
- 用户身份由 HTTP 接入层解析后作为参数传入；本地服务及 store 使用用户与会话标识限制归属，内部 Go 调用不构成新的身份来源。

## 当前操作

| 方法 | 行为 |
| --- | --- |
| `CreateSession` | 生成 ID 并创建目录项，不创建或启动 thread |
| `ListSessions` | 按用户及可选状态/项目条件分页查询目录 |
| `ListProjects` | 从用户的 Session 目录聚合项目 |
| `GetSession` | 查询目录；`includeThreads=true` 时附加 Coordinator thread 摘要 |
| `UpdateSession` | 更新标题及目录状态；CLOSED 必须通过 CloseSession 设置 |
| `BindMainThread` | 通过 store 条件更新绑定主 thread，不创建 thread 或发送消息 |
| `TouchSession` | 更新摘要、活跃时间及空标题，不代表消息已被 Worker 消费 |
| `CloseSession` | 先请求关闭关联 threads，成功后将 Session 目录标为 CLOSED |
| `CloseProject` | 请求关闭项目内会话的 threads，再关闭相应目录项；当前拒绝一次关闭超过 100 个活跃会话 |

关闭 thread 的请求可能需要 Worker 后续处理控制消息；Session 目录关闭不等于
模型和工具已同步停止。多 thread 关闭遇到错误会返回，之前已成功的关闭请求不会
自动撤销。Coordinator 事件、Worker history、checkpoint 和工作区文件也不会因
关闭目录而自动删除。

## 配置与验证

单进程入口使用 Worker YAML 的 MySQL 和 namespace 初始化 Session。Session 表名、
读取超时、ID namespace 等配置由本模块 loader 提供；可用
`DEEP_AGENT_SDK_SESSION_CONF` 选择配置文件。不存在本地 Session 服务专属的 PSM、
cluster 或直连端口。

ID namespace 非空时使用配置的 ID 服务；为空时使用本地生成器。部署环境应核验
ID 唯一性、MySQL 权限和 schema，不把本地默认值直接当作部署结论。

从仓库根目录验证：

```bash
go test ./deepagent/cmd/cloud_agent/deep_agent_sdk_session/...
go test ./deepagent/cmd/cloud_agent/deep_agent_sdk/service/session/...
```

完整进程启动见[本地运行说明](../../../../docs/runbooks/cloud-agent-codex-local-runbook.md)，
发布前检查见[BOE 准备清单](../../../../docs/runbooks/cloud-agent-boe-readiness-plan.md)。
