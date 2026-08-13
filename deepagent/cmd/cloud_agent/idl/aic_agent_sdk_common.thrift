namespace go aic_agent_sdk_common
namespace py aic_agent_sdk_common
namespace java com.bytedance.aic_agent_sdk.common

enum AgentSessionStatus {
  ACTIVE = 1,
  ARCHIVED = 2,
  CLOSED = 3,
}

enum AgentThreadStatus {
  IDLE = 1,
  READY = 2,
  RUNNING = 3,
  BLOCKED = 4,
  CLOSING = 5,
  CLOSED = 6,
}

enum AgentThreadRole {
  MAIN = 1,
  CHILD = 2,
}

enum InputMode {
  IMPL_PLAN = 1,
  COMPACT_CONTEXT = 2,
}

struct AgentSession {
  1: i64 session_id (go.tag = 'json:"session_id,string"'),
  2: i64 uid (go.tag = 'json:"uid,string"'),
  5: optional string title,
  6: optional i64 main_thread_id (go.tag = 'json:"main_thread_id,string,omitempty"'),
  7: optional string project_name,
  8: optional string last_message_preview,
  9: optional i64 last_active_at_ms,
  10: AgentSessionStatus status,
  11: optional string project_path,
  12: optional i64 created_at_ms,
  13: optional i64 updated_at_ms,
}

// SessionProject 是从 t_agent_session 聚合出的 project 视图。
//
// 它不是独立实体，也不对应 project 表；project 本质只是 session 的工作目录聚合属性。
struct SessionProject {
  1: string project_name,
  2: string project_path,
  3: optional i64 session_count,
  4: optional i64 last_active_at_ms,
}

struct AgentThread {
  1: i64 thread_id (go.tag = 'json:"thread_id,string"'),
  2: string ac_namespace,
  3: i64 session_id (go.tag = 'json:"session_id,string"'),
  4: i64 uid (go.tag = 'json:"uid,string"'),
  6: optional string title,
  7: AgentThreadStatus status,
  8: optional string status_reason,
  9: optional AgentThreadRole role,
  10: optional string parent_thread_id,
  11: optional string root_thread_id,
  14: optional i64 created_at_ms,
  15: optional i64 updated_at_ms,
}

struct MessageRef {
  1: i64 thread_id (go.tag = 'json:"thread_id,string"'),
  2: string message_id,
}

struct ResumeRef {
  1: string turn_id,
  2: string checkpoint_id,
  3: string interrupt_id,
}

struct ApprovalInput {
  1: bool approved,
  2: optional string reason,
  3: optional bool allow_in_session,
  // 用户响应的是哪个工具审批。前端从 timeline event_json 的 approval payload 原样带回，
  // worker 用它写入 session 级审批复用规则。
  4: optional string tool_name,
  5: optional string arguments_json,
}


struct AgentSessionView {
  1: AgentSession session,
  2: optional list<AgentThread> threads,
}

struct PageCursor {
  1: optional string cursor,
  2: optional i32 limit,
}

struct PageInfo {
  1: optional string next_cursor,
  2: bool has_more,
}
