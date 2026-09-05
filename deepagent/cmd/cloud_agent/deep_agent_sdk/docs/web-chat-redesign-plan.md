# Web 对话体验重构方案

## 背景

当前 `deep_agent_sdk` Web UI 已经能跑通基础链路：创建 session、发送消息、展示 agent 事件、切换 main/child thread、审批、plan input、stop running。但它还不是一个真正可展示、可长期使用的对话产品。

核心问题不是样式细节，而是 UI 数据模型仍然按 runtime event log 直接渲染：

- 左侧栏只有扁平 session 列表，没有按工作目录聚合。
- 用户输入没有被渲染成对话 bubble，用户发出去的话在 timeline 里不可见。
- agent 输出和工具事件偏 raw event/JSON，用户需要理解 `TURN_STARTED`、`TOOL_CALL_FINISHED`、`wait_message` payload。
- main/child thread 已经能跑通，但协作关系没有被产品化表达。
- 当前 session 缺少明确的工作目录字段，worker 只能从 runtime 默认路径推导工作目录。

这次重构目标是把 UI 的中心从“事件查看器”改成“面向用户的 agent chat”。

## 核心收敛

这里不把 project 做成独立业务实体。所谓 project，本质只是 session 的一个聚合属性，对应 worker 上的一个工作目录。

统一规则：

```text
workspace_root/{uid}/{project_name}
```

例如 worker 配置：

```text
workspace_root = /path/to/agent
```

用户 `user_a` 的 `cuda_demo` 项目目录就是：

```text
/path/to/agent/user_a/cuda_demo
```

这带来几个约束：

- 前端不传任意绝对路径，只传 `project_name`。
- 后端根据 `workspace_root + uid + project_name` 计算 `project_path`。
- `project_path` 固化到 `t_agent_session`，作为 session 的聚合字段和后续 AC thread 的 cwd 来源。
- `ListProjects` 不查 project 表，只从 session 表按 `project_name/project_path` 聚合。
- 空 project 第一阶段不持久化；用户点击 `+` 后只是进入空白对话态，首条消息真正创建 session 时才落库。

这个设计的重点是简单：project 不是一个需要管理生命周期的实体，只是“这个 session 属于哪个工作目录”。

## 目标体验

页面结构应是：

```text
Workspace
  -> Project(project_name, project_path)
      -> Session(chat)
          -> Thread(main/child)
              -> Message / Tool activity / Pending input
```

用户进入页面后：

1. 左侧看到 project 列表，project 名就是 `project_name`，通常等于目录名。
2. 选中 project 后，左侧展示该 project 下的 session 列表。
3. 点击 `New chat` 只进入空白对话态，不立刻创建 session。
4. 用户在空白对话态发送第一条消息时，先创建 session，并绑定当前 `project_name`。
5. 后端把 `project_name` 解析成 `project_path`，并用它创建 AC thread 的 cwd。
6. 中间区域按正常 chat 展示：
   - 用户 bubble：用户输入。
   - agent bubble：assistant 回复。
   - 工具调用：折叠过程卡片。
   - 审批、plan input：待处理卡片。
7. 输入框上方展示当前 session 的 threads/main-child 协作条，可切换子 agent；如果子 agent 需要审批或输入，要有明显状态。

## 非目标

- 不把 project 做成 SDK 核心概念。Project 是 `cmd/cloud_agent` 参考业务的 UI 聚合概念。
- 不新增 `t_agent_project`。
- 不让 `agentworker` 理解 project/session UI。
- 不让前端/API 接收任意用户绝对路径。
- 不重写 AC 的 thread/message/event 协议。
- 不在前端继续暴露 raw `AgentStreamEventType` 作为主视觉。
- 不把 `project_name/project_path` 塞进 `metadata_json`。

## 后端改造总览

后端改造分为四层：

| 层 | 改造点 | 原因 |
| --- | --- | --- |
| 配置 | 增加统一 `workspace_root` | 后端需要从 `uid/project_name` 计算标准工作目录 |
| IDL/common | 给 `AgentSession` 增加 `project_name/project_path`，新增派生视图 `SessionProject` | 前端左侧栏、worker cwd 都需要稳定字段 |
| session RPC | `CreateSession/ListSessions/GetSession/ListProjects` 支持 project 维度 | session 目录服务负责用户视角的会话索引 |
| deep_agent_sdk | `CreateSession` 接收 `project_name`，创建 AC thread 时使用 session 的 `project_path` | HTTP 层负责前端体验聚合，不让前端伪造 uid/path |
| deep_agent_sdk_worker | 使用 AC thread profile cwd 作为工具工作目录 | worker 只关心最终 cwd，不关心 project UI |

## 配置设计

新增配置项：

```yaml
workspace:
  root: /path/to/agent
```

本地测试可以配置为：

```yaml
workspace:
  root: /home/wudi.hust/deepagent_workspace
```

路径生成规则：

```go
projectPath := filepath.Join(workspaceRoot, uidString, projectName)
```

其中：

- `uidString` 使用登录态 uid 的字符串形式，例如 `1234`。
- `project_name` 只能是单段目录名，不能包含 `/`、`\`、`..`，不能为空。
- `project_path` 是后端计算结果，不由前端传入。
- worker 启动 thread 前负责 `MkdirAll(project_path)`。

配置放置建议：

- `deep_agent_sdk` 需要知道 `workspace.root`，用于创建 session 时计算 `project_path`。
- `deep_agent_sdk_worker` 也保留 `workspace.root`，用于没有 profile cwd 的 fallback 和路径约束。
- `deep_agent_sdk_session` 不需要理解 root 规则，只存 API 传入的 `project_name/project_path`。

这样 session RPC 不感知 worker 的部署细节，worker 也不感知前端 project 交互。

## 数据模型

### `t_agent_session` DDL

当前表：

```sql
t_agent_session(
  session_id,
  uid,
  title,
  status,
  main_thread_id,
  last_message_preview,
  last_active_at_ms,
  created_at_ms,
  updated_at_ms,
  closed_at_ms,
  metadata_json
)
```

建议新增字段：

```sql
ALTER TABLE t_agent_session
  ADD COLUMN project_name VARCHAR(256) NOT NULL DEFAULT '' COMMENT 'project directory name under user workspace' AFTER uid,
  ADD COLUMN project_path VARCHAR(1024) NOT NULL DEFAULT '' COMMENT 'resolved worker cwd for this session' AFTER project_name,
  ADD KEY idx_uid_project_active (uid, project_name, status, last_active_at_ms);
```

说明：

- `session_id` 继续使用 idgen 生成的 `BIGINT UNSIGNED` 主键，不使用自增。
- `project_name` 是用户选择的项目名，也是左侧栏展示名。
- `project_path` 是后端按 `workspace_root/{uid}/{project_name}` 计算出的工作目录。
- `metadata_json` 不用于 project/workdir 主链路。
- 不需要 `project_key`。因为查询主维度是 `project_name`，不是任意长路径。

### 为什么同时存 `project_name` 和 `project_path`

只存 `project_name`，后续如果 `workspace.root` 变更，历史 session 的工作目录会被重新解释。

只存 `project_path`，UI 又需要反推目录名，并且后续如果展示名规则变化会不清晰。

所以第一版同时存：

- `project_name`：用户视角和列表聚合字段。
- `project_path`：运行视角和 AC thread cwd 字段。

## IDL 改造

### `deep_agent_sdk_common.thrift`

`AgentSession` 增加：

```thrift
struct AgentSession {
  ...
  14: optional string project_name,
  15: optional string project_path,
}
```

新增派生视图：

```thrift
struct SessionProject {
  1: string project_name,
  2: string project_path,
  3: optional i64 session_count,
  4: optional i64 last_active_at_ms,
}
```

注意：`SessionProject` 不是一张表，也不是独立实体，只是 `t_agent_session` 的聚合结果。

### 历史 Session RPC 设计（已由进程内 SessionService 替代）

以下是原始协议设计记录；内部 RPC IDL 和生成代码已删除，当前字段通过 SessionService 参数和 HTTP 模型传递。

`CreateSessionRequest` 增加：

```thrift
struct CreateSessionRequest {
  1: i64 uid,
  5: optional string title,
  6: optional string project_name,
  7: optional string project_path,
  255: base.Base Base,
}
```

这里 `project_path` 由 `deep_agent_sdk` 计算后传给 session RPC，不允许前端直接传。

`ListSessionsRequest` 增加：

```thrift
struct ListSessionsRequest {
  1: i64 uid,
  4: optional deep_agent_sdk_common.AgentSessionStatus status,
  5: optional deep_agent_sdk_common.PageCursor page,
  6: optional string project_name,
  255: base.Base Base,
}
```

新增：

```thrift
struct ListProjectsRequest {
  1: i64 uid,
  2: optional deep_agent_sdk_common.AgentSessionStatus status,
  255: base.Base Base,
}

struct ListProjectsResponse {
  1: optional list<deep_agent_sdk_common.SessionProject> projects,
  255: base.BaseResp BaseResp,
}
```

`DeepAgentSDKSessionService` 增加：

```thrift
ListProjectsResponse ListProjects(1: ListProjectsRequest req),
```

不提供 `CreateProject` RPC。第一阶段 project 是 session 目录的派生视图。

### `deep_agent_sdk.thrift`

`CreateSessionHTTPRequest` 增加：

```thrift
struct CreateSessionHTTPRequest {
  4: optional string title,
  5: optional string project_name,
}
```

`ListSessionsHTTPRequest` 增加：

```thrift
struct ListSessionsHTTPRequest {
  3: optional deep_agent_sdk_common.AgentSessionStatus status,
  4: optional string cursor,
  5: optional i32 limit,
  6: optional string project_name,
}
```

新增：

```thrift
struct ListProjectsHTTPRequest {}

struct ListProjectsHTTPResponse {
  1: optional list<deep_agent_sdk_common.SessionProject> projects,
  255: base.BaseResp BaseResp,
}
```

`DeepAgentSDKAPIService` 增加：

```thrift
ListProjectsHTTPResponse ListProjects(1: ListProjectsHTTPRequest req) (api.post="/ad/deep_agent_sdk/list_projects"),
```

不提供 `CreateProject` HTTP 接口。用户创建 project 的动作，第一阶段只是创建一个本地空白 chat 状态；首条消息创建 session 后才真正持久化。

## session RPC 实现改造

### DAL model

`cmd/cloud_agent/deep_agent_sdk_session/dal/session/model.go`

`Session` 增加：

```go
ProjectName string
ProjectPath string
```

`ListFilter` 增加：

```go
ProjectName string
```

新增聚合结果：

```go
type SessionProject struct {
    ProjectName string
    ProjectPath string
    SessionCount int64
    LastActiveAtMS int64
}
```

### Store

`sessionColumns` 增加 `project_name, project_path`。

`Create` 插入 `project_name/project_path`。

`List` 支持 `project_name` 过滤：

```go
if filter.ProjectName != "" {
    where = append(where, "project_name = ?")
    args = append(args, filter.ProjectName)
}
```

新增 `ListProjects(ctx, uid, status)`：

```sql
SELECT project_name,
       MIN(project_path) AS project_path,
       COUNT(*) AS session_count,
       MAX(last_active_at_ms) AS last_active_at_ms
FROM t_agent_session
WHERE uid = ?
  AND status <> CLOSED
  AND project_name <> ''
GROUP BY project_name
ORDER BY MAX(last_active_at_ms) DESC, project_name ASC
```

如果 `status` 入参传入具体状态，则额外加 `status = ?`。

### Service

`CreateSession`：

- 校验 `project_name/project_path` 必填。
- 不在 session RPC 内重新拼 path，避免多个服务重复 workspace 规则。
- 创建 session 时写入 project 字段。

`ListSessions`：

- 按 `uid/status/project_name/page` 查询。

`GetSession`：

- 返回 `AgentSession.project_name/project_path`。

`ListProjects`：

- 返回从 session 表聚合出的 `SessionProject` 列表。

## deep_agent_sdk 实现改造

### 配置

`deep_agent_sdk` 配置增加：

```yaml
workspace:
  root: /path/to/agent
```

新增一个很小的 resolver，职责只做三件事：

```go
type WorkspaceResolver struct {
    Root string
}

func (r WorkspaceResolver) Resolve(uid int64, projectName string) (string, error)
```

校验规则：

- `projectName` trim 后不能为空。
- 不允许 `/`、`\`、`..`。
- 可以限制最大长度，例如 128。
- 返回 `filepath.Join(root, strconv.FormatInt(uid, 10), projectName)`。

这个 resolver 是 `deep_agent_sdk` 内部业务代码，不放进 SDK。

### Handler/Service 结构

新增或调整：

```text
service/project/
  project.go          # ListProjects
service/session/
  session.go          # Create/List/Get/Update/Close session directory
service/input/
  input.go            # SubmitInput
```

新增 handler：

```text
biz/handler/list_projects.go
```

router 增加：

```go
r.POST("/ad/deep_agent_sdk/list_projects", handler.ListProjects)
```

### ListProjects

流程：

1. `currentUID(c)` 获取 uid。
2. 调 `deep_agent_sdk_session.ListProjects(uid, status=ACTIVE)`。
3. 返回 `deep_agent_sdk_common.SessionProject` 列表。

第一阶段不扫描 worker 文件系统。没有 session 的空目录不会出现在左侧栏里。

### CreateSession

流程：

1. 获取 uid。
2. 校验 `project_name` 必填。
3. 用 `WorkspaceResolver.Resolve(uid, project_name)` 得到 `project_path`。
4. 调 `deep_agent_sdk_session.CreateSession(uid, title, project_name, project_path)`。
5. 返回 session view。

`project_path` 不从 HTTP request 读取。

### ListSessions

流程：

1. 获取 uid。
2. `project_name` 如果传入，则按 project 过滤。
3. 调 `deep_agent_sdk_session.ListSessions`。

### SubmitInput

当前 `SubmitInput` 要求 `session_id` 必填。为了支持 `New chat -> 首条消息创建 session`，第一阶段选择最简单的两步方案：

1. 如果前端没有 active session，先调用 `CreateSession(project_name, title=首条消息 preview)`。
2. 再调用 `SubmitInput(session_id, content)`。

这个方案的优点：

- `SubmitInput` 语义不变，只对已有 session 提交输入。
- `project_name -> project_path` 只发生在 `CreateSession`。
- 后端状态容易理解。

代价：

- CreateSession 成功但 SubmitInput 失败时可能出现空 session。

这个代价可以接受。它比把 `SubmitInput.session_id` 改 optional 并在内部隐式创建 session 更清晰。

### CreateThread 时传 workdir

当前 `resolveThread` 创建 main thread 时只设置：

```go
Profile: &ac.ThreadProfile{
    Role: threadRoleMain,
}
```

需要改为：

```go
Profile: &ac.ThreadProfile{
    Role: threadRoleMain,
    Cwd:  view.Session.GetProjectPath(),
}
```

注意：

- `project_path` 来自 session 视图，不来自 HTTP request。
- 如果 session 没有 `project_path`，返回明确错误，避免 worker 落到不可预期的默认目录。
- child thread 的 cwd 由当前 thread 继承。main thread 的 profile cwd 正确后，child thread 继续在同一项目目录下运行。

## worker workdir 改造

`deep_agent_sdk_worker` 配置也保留：

```yaml
workspace:
  root: /path/to/agent
```

worker 的 workdir 规则：

1. 如果 AC thread profile `Cwd` 非空，使用它作为该 thread 的 workdir。
2. 如果 `Cwd` 为空，才 fallback 到 `workspace.root/{uid}/{session_id}`。
3. 创建 thread runtime 前执行 `MkdirAll(workdir)`。

这样主链路是：

```text
project_name
  -> deep_agent_sdk Resolve(uid, project_name)
  -> project_path
  -> t_agent_session.project_path
  -> deep_agent_sdk CreateThread Profile.Cwd
  -> deep_agent_sdk_worker threadWorkDir
  -> execute/filesystem tools workdir
```

worker 不需要知道 project 列表、session 左侧栏、标题、展示名。

## 前端状态模型改造

当前前端 state：

```js
sessions
selectedSessionID
selectedThreadID
sessionView
events
```

需要改为：

```js
projects
selectedProjectName
sessionsByProject
selectedSessionID
selectedThreadID
sessionView
rawEvents
timelineItems
pendingItems
contextUsageByThread
```

### Project/Session 左侧栏

左侧结构：

```text
DeepAgent
New chat
Search

cuda_demo
  session A
  session B

deep_agent_sdk
  session C

+ Project
```

交互：

- 选择 project：清空当前 session，展示该 project 下 sessions。
- New chat：保留 selectedProjectName，清空 selectedSessionID，右侧进入空白 chat。
- 发送首条消息：
  - 若无 selectedSessionID：`CreateSession(project_name, title preview)`，再 `SubmitInput`。
  - 若已有 selectedSessionID：直接 `SubmitInput`。
- 创建 project：只输入 `project_name`，不是输入完整路径。
- Project 展示名直接使用 `project_name`。

### Timeline folding

不要直接 `events.map(eventNode)`。

新增一个 fold 层：

```js
function buildTimelineItems(events) -> TimelineItem[]
```

目标 UI item：

```ts
type TimelineItem =
  | { kind: "user_message", messageID, content, createdAt }
  | { kind: "assistant_message", content, streaming, createdAt }
  | { kind: "tool_call", toolCallID, toolName, status, summary, detail, createdAt }
  | { kind: "approval", ... }
  | { kind: "plan", ... }
  | { kind: "plan_input", ... }
  | { kind: "system", level, text }
```

映射规则：

- `TURN_STARTED`：
  - 如果 `event.message.content` 非空，渲染为 user bubble。
  - 不再默认渲染 “Turn started”。
- `ASSISTANT_DELTA`：
  - 追加到当前 streaming assistant bubble。
- `ASSISTANT_MESSAGE`：
  - 写入/替换最终 assistant bubble。
- `TOOL_CALL_STARTED/OUTPUT_DELTA/FINISHED`：
  - 按 `tool_call_id` 合并成一个 tool card。
  - 默认展示 tool name、状态、简短 summary。
  - args/result JSON 默认折叠。
- `APPROVAL_REQUIRED`：
  - 渲染 pending card。
  - 同步到 thread 协作条 “needs approval”。
- `PLAN_UPDATED`：
  - 渲染 plan card。
- `PLAN_INPUT_REQUIRED`：
  - 渲染 plan input card。
- `TURN_FINISHED`：
  - 不默认渲染，除非 debug 模式。
- `TURN_INTERRUPTED/ERROR`：
  - 渲染轻量 system notice。

### Tool card 展示

建议初版：

```text
exec_command  completed
$ pwd
cwd: /path
exit: 0

[Details]
```

`spawn_task`：

```text
Started child agent
target: alice
initial message: ...
[Open thread]
```

`wait_message`：

```text
Waiting for alice
completed: CHILD_OK
```

`close_task`：

```text
Closed child agent
state: completed
```

### Thread 协作条

位置：composer 上方。

内容：

```text
main     idle
alice    running
bob      needs approval
```

数据来源：

- `GetSession(include_threads=true)` 返回 threads。
- 当前 thread timeline events 推导 open turn。
- 对非当前 thread 的 running/blocked 状态，短期可以从 AC thread status + 最近事件综合判断。

第一阶段不追求完美实时状态，但必须支持：

- 点击 child thread 切换 timeline。
- child thread 有 pending approval/plan input 时显示明显 badge。
- main agent wait child 时，用户能切过去处理 child pending。

## 分阶段落地

### Phase 1：后端 project/workdir 主链路

改动：

- IDL：
  - `deep_agent_sdk_common.AgentSession` 增加 `project_name/project_path`。
  - `deep_agent_sdk_common.SessionProject`。
  - `deep_agent_sdk_session.CreateSession/ListSessions/ListProjects`。
  - `deep_agent_sdk.CreateSession/ListSessions/ListProjects`。
- Overpass：
  - 生成 `deep_agent_sdk_session` kitex。
  - 生成 `deep_agent_sdk` hertz。
  - 更新 `deep_agent_sdk/deep_agent_sdk_session/deep_agent_sdk_worker` 中公共结构依赖。
- DDL：
  - `t_agent_session` 增加 project 字段和索引。
- session RPC：
  - DAL/model/store/service 支持 project。
- deep_agent_sdk：
  - 增加 workspace resolver。
  - ListProjects。
  - CreateSession 只接收 `project_name`，内部计算 `project_path`。
  - ListSessions 按 `project_name` 过滤。
  - CreateThread Profile.Cwd 使用 session.project_path。
- worker：
  - 验证 `ThreadProfile.Cwd` 优先级和 `MkdirAll`。

验证：

- 创建两个 project name，各自发送一条消息。
- ListProjects 返回两个 project。
- ListSessions(project_name=A) 只返回 A 下 session。
- agent 执行 `pwd`，输出应为 `workspace_root/{uid}/{project_name}`。

### Phase 2：左侧栏重构

改动：

- 前端新增 project state。
- 左侧改为 Project -> Session。
- New chat 只创建本地空白态。
- 首条消息触发 CreateSession + SubmitInput。
- Project 创建只输入 `project_name`。

验证：

- 切 project 后 session 列表隔离。
- New chat 不发消息不落 session。
- 首条消息后 session 出现在当前 project 下。

### Phase 3：Timeline folding

改动：

- 新增 `timeline_model.js` 或类似模块，把 raw events fold 为 UI items。
- user bubble、assistant bubble、tool card、pending card、system notice 分开渲染。
- raw event JSON 只在 debug/details 中出现。

验证：

- 用户输入可见。
- assistant 流式输出合并。
- `exec_command` 不裸 JSON。
- `spawn_task/wait_message` 以协作卡片展示。
- 刷新后 UI 还原一致。

### Phase 4：Thread/Pending 协作体验

改动：

- composer 上方 thread switcher 重新设计。
- child pending approval/plan input badge。
- pending card 能直接审批/回复。
- stop running 状态和 main/child 多线程状态统一。

验证：

- 子 agent 触发审批，主 agent 等待时，用户能发现并切到 child 处理。
- Stop session 时 main/child 都能中断，刷新后状态正确。

## 风险和取舍

### Project 是否单独建表

第一阶段不建。因为当前 project 的唯一持久意义是 session 的 `project_name/project_path`。如果未来需要空 project、项目重命名、项目归档、协作权限，再新增 `t_agent_project`。

### 是否允许用户输入完整路径

不允许。完整路径由后端生成：

```text
workspace_root/{uid}/{project_name}
```

这比让用户传任意 path 更简单，也避免路径穿越、跨用户目录、线上 sandbox 映射等问题提前进入 API。

### API 和 worker 都有 workspace root 会不会重复

这是可以接受的部署约束：

- API 用它计算 session 的 `project_path`。
- worker 用它做 fallback 和约束。

主链路仍以 session 上固化的 `project_path` 为准。只要部署配置一致，行为就是确定的。

### 事件 folding 会不会丢调试信息

不会。raw events 仍然保留在 state，可以在 tool details/debug 模式展示。只是默认 UI 不再让用户读 raw event。

### 是否需要重做 SubscribeTimeline

短期 polling 可以继续工作，但最终应该走 `SubscribeTimeline` SSE。重构 timeline folding 时要让输入是统一的 `AgentStreamEvent[]`，无论来源是 `ListTimeline` 还是 `SubscribeTimeline`。

## 建议先确认的问题

1. `workspace.root` 本地测试是否就用 `/home/wudi.hust/deepagent_workspace`。
2. 用户目录名是否直接用 uid，例如 `/path/to/agent/1234/cuda_demo`，还是继续沿用之前的 `{uid}_wk`。我的建议是直接 uid，少一个历史兼容规则。
3. `project_name` 是否只允许单段目录名。我的建议是只允许单段，先不支持用户输入多级相对路径。
