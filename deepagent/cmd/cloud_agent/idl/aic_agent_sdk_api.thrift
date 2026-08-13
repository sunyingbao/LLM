namespace go aic_agent_sdk_api
namespace py aic_agent_sdk_api
namespace java com.bytedance.aic_agent_sdk.api

include "base.thrift"
include "aic_agent_sdk_common.thrift"

struct CreateSessionHTTPRequest {
  4: optional string title,
  5: optional string project_name,
}

struct ListSessionsHTTPRequest {
  3: optional aic_agent_sdk_common.AgentSessionStatus status,
  4: optional string cursor,
  5: optional i32 limit,
  6: optional string project_name,
}

struct ListProjectsHTTPRequest {
}

struct GetSessionHTTPRequest {
  1: i64 session_id (go.tag = 'json:"session_id,string"', agw.cli_conv="str"),
  // include_threads=true 时返回该 session 下的 thread 摘要。
  // 默认只返回 session 元信息。
  3: optional bool include_threads,
}

struct UpdateSessionHTTPRequest {
  1: i64 session_id (go.tag = 'json:"session_id,string"', agw.cli_conv="str"),
  4: optional string title,
  6: optional aic_agent_sdk_common.AgentSessionStatus status,
}

struct CloseSessionHTTPRequest {
  1: i64 session_id (go.tag = 'json:"session_id,string"', agw.cli_conv="str"),
  4: optional string reason,
}

struct CloseProjectHTTPRequest {
  1: string project_name,
  2: optional string reason,
}

struct StopRunningHTTPRequest {
  1: i64 session_id (go.tag = 'json:"session_id,string"', agw.cli_conv="str"),
  4: optional string reason,
}

struct SubmitInputHTTPRequest {
  1: i64 session_id (go.tag = 'json:"session_id,string"', agw.cli_conv="str"),
  4: optional i64 thread_id (go.tag = 'json:"thread_id,string,omitempty"', agw.cli_conv="str"),
  5: optional string content,
  6: optional aic_agent_sdk_common.ResumeRef resume_ref,
  7: optional aic_agent_sdk_common.ApprovalInput approval,
  // 不填表示普通输入；IMPL_PLAN 表示用户确认开始执行已有 plan；
  // COMPACT_CONTEXT 表示手动压缩当前 thread 上下文，不进入模型历史。
  8: optional aic_agent_sdk_common.InputMode mode,
}

struct ListTimelineHTTPRequest {
  1: i64 session_id (go.tag = 'json:"session_id,string"', agw.cli_conv="str"),
  4: optional i64 thread_id (go.tag = 'json:"thread_id,string,omitempty"', agw.cli_conv="str"),
  5: optional string turn_id,
  6: optional string cursor,
  7: optional i32 limit,
  8: optional bool backward,
}

struct SubscribeTimelineHTTPRequest {
  1: i64 session_id (go.tag = 'json:"session_id,string"', agw.cli_conv="str"),
  4: optional i64 thread_id (go.tag = 'json:"thread_id,string,omitempty"', agw.cli_conv="str"),
  5: optional string turn_id,
  6: optional string recover_queue_id,
  7: optional string after_event_id,
}

// TimelineEvent 是 aic_agent_sdk_api 对前端暴露的 AC event envelope。
// event_type 是 AC event header；payload 是 worker 写入 AC eventlog 的原始
// timeline JSON。payload 本身只包含 xxxEventPayload，不重复 event header。
struct TimelineEvent {
  1: string event_id,
  2: optional i64 session_id (go.tag = 'json:"session_id,string,omitempty"', agw.cli_conv="str"),
  3: optional i64 thread_id (go.tag = 'json:"thread_id,string,omitempty"', agw.cli_conv="str"),
  4: optional string turn_id,
  5: optional i64 created_at_ms,
  // HTTP reference service 当前用自定义响应结构输出 payload JSON object；
  // thrift IDL 无法表达任意 JSON object，这里保留 string 作为描述性字段。
  6: string payload,
  7: optional string event_type,
}

struct ListTimelineHTTPResponse {
  1: optional list<TimelineEvent> events,
  2: aic_agent_sdk_common.PageInfo page_info,
  255: base.BaseResp BaseResp,
}

struct SubscribeTimelineHTTPResponse {
  1: optional string queue_id,
  2: optional TimelineEvent event,
  255: base.BaseResp BaseResp,
}

struct CreateSessionHTTPResponse {
  1: optional aic_agent_sdk_common.AgentSessionView session_view,
  255: base.BaseResp BaseResp,
}

struct ListSessionsHTTPResponse {
  1: optional list<aic_agent_sdk_common.AgentSession> sessions,
  2: aic_agent_sdk_common.PageInfo page_info,
  255: base.BaseResp BaseResp,
}

struct ListProjectsHTTPResponse {
  1: optional list<aic_agent_sdk_common.SessionProject> projects,
  255: base.BaseResp BaseResp,
}

struct GetSessionHTTPResponse {
  1: optional aic_agent_sdk_common.AgentSessionView session_view,
  255: base.BaseResp BaseResp,
}

struct UpdateSessionHTTPResponse {
  1: optional aic_agent_sdk_common.AgentSessionView session_view,
  255: base.BaseResp BaseResp,
}

struct CloseSessionHTTPResponse {
  1: optional aic_agent_sdk_common.AgentSessionView session_view,
  255: base.BaseResp BaseResp,
}

struct CloseProjectHTTPResponse {
  1: optional aic_agent_sdk_common.SessionProject project,
  2: optional list<i64> closed_session_ids,
  3: optional i64 closed_session_count,
  4: optional i64 closed_thread_count,
  255: base.BaseResp BaseResp,
}

struct SubmitInputHTTPResponse {
  1: optional aic_agent_sdk_common.MessageRef message,
  2: optional aic_agent_sdk_common.AgentSessionView session_view,
  255: base.BaseResp BaseResp,
}

struct StopRunningHTTPResponse {
  1: optional aic_agent_sdk_common.AgentSessionView session_view,
  2: optional list<aic_agent_sdk_common.MessageRef> control_messages,
  255: base.BaseResp BaseResp,
}

// AICAgentSDKAPIService 是浏览器/前端入口。
//
// 目标 PSM：ad.creative.aic_agent_sdk_api。
//
// HTTP 层负责登录态解析，把 uid 注入到 ad.creative.aic_agent_sdk_session；
// 这里的 IDL 故意不出现 uid，避免前端伪造用户身份。
service AICAgentSDKAPIService {
  // 创建一个 agent session，用于空白会话或预创建会话目录项。
  CreateSessionHTTPResponse CreateSession(1: CreateSessionHTTPRequest req) (api.post="/ad/aic_agent_sdk/create_session"),

  // 获取左侧栏 session 列表。
  ListSessionsHTTPResponse ListSessions(1: ListSessionsHTTPRequest req) (api.post="/ad/aic_agent_sdk/list_sessions"),

  // 获取左侧栏 project 列表。project 是 session 的工作目录聚合视图，不是独立实体。
  ListProjectsHTTPResponse ListProjects(1: ListProjectsHTTPRequest req) (api.post="/ad/aic_agent_sdk/list_projects"),

  // 获取 session 详情首屏数据，包括 session 和 threads。
  GetSessionHTTPResponse GetSession(1: GetSessionHTTPRequest req) (api.post="/ad/aic_agent_sdk/get_session"),

  // 更新 session 标题、归档状态等目录属性。
  UpdateSessionHTTPResponse UpdateSession(1: UpdateSessionHTTPRequest req) (api.post="/ad/aic_agent_sdk/update_session"),

  // 关闭或归档一个 session。
  CloseSessionHTTPResponse CloseSession(1: CloseSessionHTTPRequest req) (api.post="/ad/aic_agent_sdk/close_session"),

  // 从侧边栏移除一个 project：关闭该 project 下 active sessions，并保留
  // workspace、timeline、history、checkpoint 和 memory。
  CloseProjectHTTPResponse CloseProject(1: CloseProjectHTTPRequest req) (api.post="/ad/aic_agent_sdk/close_project"),

  // 给 session 提交用户输入或控制指令；普通输入没有 main thread 时由后端按需创建。
  SubmitInputHTTPResponse SubmitInput(1: SubmitInputHTTPRequest req) (api.post="/ad/aic_agent_sdk/submit_input"),

  // 停止 session 当前正在执行的 turn。
  StopRunningHTTPResponse StopRunning(1: StopRunningHTTPRequest req) (api.post="/ad/aic_agent_sdk/stop_running"),

  // 查询 session timeline 历史事件，用于刷新恢复。
  //
  // aic_agent_sdk_api 直接请求 Agent Coordinator ListEvents/ListTurnEvents，并把 AC Event.Payload
  // 作为 AIC Agent SDK demo 自有 JSON event 协议透传给前端；不经过 aic_agent_sdk_session RPC。
  ListTimelineHTTPResponse ListTimeline(1: ListTimelineHTTPRequest req) (api.post="/ad/aic_agent_sdk/list_timeline"),

  // 订阅 session timeline 实时事件；HTTP 实现通常会把 AC server stream 转成 SSE。
  //
  // aic_agent_sdk_api 直接请求 Agent Coordinator SubscribeSession，并把 AC Event.Payload
  // 作为 AIC Agent SDK demo 自有 JSON event 协议透传给前端；不经过 aic_agent_sdk_session RPC。
  SubscribeTimelineHTTPResponse SubscribeTimeline(1: SubscribeTimelineHTTPRequest req) (api.post="/ad/aic_agent_sdk/subscribe_timeline", streaming.mode="server"),
}
