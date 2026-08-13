namespace go aic_agent_sdk_session
namespace py aic_agent_sdk_session
namespace java com.bytedance.aic_agent_sdk.session

include "base.thrift"
include "aic_agent_sdk_common.thrift"

struct CreateSessionRequest {
  1: i64 uid,
  5: optional string title,
  6: optional string project_name,
  7: optional string project_path,
  255: base.Base Base,
}

struct CreateSessionResponse {
  1: optional aic_agent_sdk_common.AgentSessionView session_view,
  255: base.BaseResp BaseResp,
}

struct ListSessionsRequest {
  1: i64 uid,
  4: optional aic_agent_sdk_common.AgentSessionStatus status,
  5: optional aic_agent_sdk_common.PageCursor page,
  6: optional string project_name,
  255: base.Base Base,
}

struct ListSessionsResponse {
  1: optional list<aic_agent_sdk_common.AgentSession> sessions,
  2: aic_agent_sdk_common.PageInfo page_info,
  255: base.BaseResp BaseResp,
}

struct GetSessionRequest {
  1: i64 uid,
  3: i64 session_id,
  // include_threads=true 时返回该 session 下的 thread 摘要。
  // 默认不打包 threads，避免列表/轻量鉴权路径被 AC 查询拖慢。
  4: optional bool include_threads,
  255: base.Base Base,
}

struct GetSessionResponse {
  1: optional aic_agent_sdk_common.AgentSessionView session_view,
  255: base.BaseResp BaseResp,
}

struct UpdateSessionRequest {
  1: i64 uid,
  3: i64 session_id,
  5: optional string title,
  7: optional aic_agent_sdk_common.AgentSessionStatus status,
  255: base.Base Base,
}

struct UpdateSessionResponse {
  1: optional aic_agent_sdk_common.AgentSessionView session_view,
  255: base.BaseResp BaseResp,
}

struct CloseSessionRequest {
  1: i64 uid,
  3: i64 session_id,
  5: optional string reason,
  255: base.Base Base,
}

struct CloseSessionResponse {
  1: optional aic_agent_sdk_common.AgentSessionView session_view,
  255: base.BaseResp BaseResp,
}

struct CloseProjectRequest {
  1: i64 uid,
  2: string project_name,
  3: optional string reason,
  255: base.Base Base,
}

struct CloseProjectResponse {
  1: optional aic_agent_sdk_common.SessionProject project,
  2: optional list<i64> closed_session_ids,
  3: optional i64 closed_session_count,
  4: optional i64 closed_thread_count,
  255: base.BaseResp BaseResp,
}

struct BindMainThreadRequest {
  1: i64 uid,
  3: i64 session_id,
  4: i64 main_thread_id,
  255: base.Base Base,
}

struct BindMainThreadResponse {
  1: optional aic_agent_sdk_common.AgentSessionView session_view,
  255: base.BaseResp BaseResp,
}

struct TouchSessionRequest {
  1: i64 uid,
  3: i64 session_id,
  4: optional string last_message_preview,
  // 当 session title 为空时，可以用首条用户消息生成标题。
  // 如果 CreateSession 已传 title，则实现不应被该字段覆盖。
  5: optional string title_if_empty,
  6: optional i64 last_active_at_ms,
  255: base.Base Base,
}

struct TouchSessionResponse {
  1: optional aic_agent_sdk_common.AgentSessionView session_view,
  255: base.BaseResp BaseResp,
}

struct ListProjectsRequest {
  1: i64 uid,
  2: optional aic_agent_sdk_common.AgentSessionStatus status,
  255: base.Base Base,
}

struct ListProjectsResponse {
  1: optional list<aic_agent_sdk_common.SessionProject> projects,
  255: base.BaseResp BaseResp,
}

// AICAgentSDKSessionService 是 AIC Agent SDK 的 session 控制面。
//
// 目标 PSM：ad.creative.aic_agent_sdk_session。
//
// 这个服务不暴露 Agent Coordinator 的 Thread/Message/EventLog 原语；
// 它只围绕用户视角的 agent_session 提供目录和交互能力。
// uid 由上游 HTTP/业务登录态解析后传入，本服务不负责登录态认证。
service AICAgentSDKSessionService {
  // 创建一个 agent_session 目录项。
  //
  // 这是产品会话创建，不是 AC CreateThread。实现可以只写 agent_session 表；
  // main thread 可在首次用户输入时由 API 按需创建并回绑。
  CreateSessionResponse CreateSession(1: CreateSessionRequest req),

  // 列出用户的 agent_session，用于左侧栏。
  //
  // 返回的是会话目录数据，不读取完整 timeline，也不创建/唤醒任何 AC thread。
  ListSessionsResponse ListSessions(1: ListSessionsRequest req),

  // 获取一个 session 的聚合视图。
  //
  // 用于打开会话详情页；返回 session 和该 session 下的 thread 摘要。
  // timeline、pending human input、context usage 都来自 aic_agent_sdk_api 直接读取 AC event。
  GetSessionResponse GetSession(1: GetSessionRequest req),

  // 更新 session 的目录属性。
  //
  // 例如标题、归档状态。
  // 这不是 thread runtime 状态更新。
  UpdateSessionResponse UpdateSession(1: UpdateSessionRequest req),

  // 关闭或归档一个 session。
  //
  // 这是用户视角的会话结束动作。实现需要关闭该 session 下的 main/child threads，
  // 但接口不暴露 CloseThread 这种 AC 原语。
  CloseSessionResponse CloseSession(1: CloseSessionRequest req),

  // 关闭一个 project 下的 active sessions，并让该 project 从 active project
  // 侧边栏聚合视图中消失。
  //
  // Project 不是独立实体，本接口不会删除 workspace、eventlog、history、
  // checkpoint 或 memory；它只处理 uid + project_name 范围内的 active
  // session 目录态，并关闭这些 session 下的 AC threads。
  CloseProjectResponse CloseProject(1: CloseProjectRequest req),

  // 绑定 session 的 main thread。
  //
  // API 首次提交用户输入时可以直接调用 AC CreateThread，然后用该接口把
  // main_thread_id 写回 agent_session。该接口不创建 thread，也不发送消息。
  BindMainThreadResponse BindMainThread(1: BindMainThreadRequest req),

  // 更新 session 左侧栏展示所需的轻量活跃信息。
  //
  // 用于 SubmitInput 成功后的 last_message_preview / last_active_at_ms 回写；
  // title_if_empty 用于“创建时未传 title，则使用首条用户消息生成标题”的场景。
  TouchSessionResponse TouchSession(1: TouchSessionRequest req),

  // 按 session 表聚合用户已有 project，用于左侧栏。
  //
  // 该接口不创建 project，也不扫描 worker 文件系统；空 project 第一阶段不持久化。
  ListProjectsResponse ListProjects(1: ListProjectsRequest req),

}
