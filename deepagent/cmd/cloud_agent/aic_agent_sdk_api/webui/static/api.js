const API_PREFIX = "/ad/aic_agent_sdk";
const LOGIN_PATH = "/oidc/login";
const BOE_TEST_AUTH_HEADER = "X-AIC-Agent-SDK-Test-UID";

export async function post(name, body = {}, options = {}) {
  const response = await fetch(`${API_PREFIX}/${name}`, {
    method: "POST",
    headers: aicAgentSDKHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify(body),
    signal: options.signal,
  });
  const payload = await response.json().catch(() => ({}));
  const baseResp = payload.BaseResp || payload.base_resp || {};
  if (!response.ok || Number(baseResp.StatusCode || baseResp.status_code || 0) !== 0) {
    if (isUnauthenticated(response, baseResp)) {
      redirectToLogin();
    }
    const message =
      baseResp.StatusMessage ||
      baseResp.status_message ||
      payload.error ||
      response.statusText ||
      `${name} failed`;
    throw new Error(message);
  }
  return payload;
}

export const api = {
  ping: () => post("ping"),
  getUserInfo: () => getUserInfo(),
  createSession: ({ title = "", projectName }) => {
    const body = { project_name: projectName };
    if (title) body.title = title;
    return post("create_session", body);
  },
  listProjects: () => post("list_projects"),
  closeProject: (projectName) => post("close_project", { project_name: projectName, reason: "user_remove_project" }),
  listSessions: (projectName) => {
    const body = { limit: 100 };
    if (projectName) body.project_name = projectName;
    return post("list_sessions", body);
  },
  getSession: (sessionID) => post("get_session", { session_id: String(sessionID), include_threads: true }),
  closeSession: (sessionID) => post("close_session", { session_id: String(sessionID), reason: "user_close" }),
  stopRunning: (sessionID) => post("stop_running", { session_id: String(sessionID), reason: "user_stop" }),
  submitInput: ({ sessionID, threadID, content, mode, resumeRef, approval, interrupt }) => {
    const body = { session_id: String(sessionID) };
    if (threadID) body.thread_id = String(threadID);
    if (content) body.content = content;
    if (mode) body.mode = mode;
    if (resumeRef) body.resume_ref = resumeRef;
    if (approval) body.approval = approval;
    if (interrupt) body.interrupt = interrupt;
    return post("submit_input", body);
  },
  listTimeline: ({ sessionID, threadID, cursor, limit = 200, backward = false, signal }) => {
    const body = { session_id: String(sessionID), limit };
    if (threadID) body.thread_id = String(threadID);
    if (cursor) body.cursor = String(cursor);
    if (backward) body.backward = true;
    return post("list_timeline", body, { signal }).then(normalizeTimelineResponse);
  },
  listFiles: ({ sessionID, path = "." }) => post("list_files", { session_id: String(sessionID), path }),
  subscribeTimeline: ({ sessionID, recoverQueueID, signal, onQueue, onEvent, onError }) => {
    return streamPost("subscribe_timeline", {
      body: {
        session_id: String(sessionID),
        ...(recoverQueueID ? { recover_queue_id: recoverQueueID } : {}),
      },
      signal,
      onMessage: ({ name, payload }) => {
        const baseResp = payload.BaseResp || payload.base_resp || {};
        if (Number(baseResp.StatusCode || baseResp.status_code || 0) !== 0) {
          const message = baseResp.StatusMessage || baseResp.status_message || "stream error";
          const error = new Error(message);
          error.retryStream = false;
          onError?.(error);
          throw error;
        }
        if (payload.queue_id) {
          onQueue?.(payload.queue_id);
          return;
        }
        const event = parseTimelineEvent(payload.event || payload.Event);
        if (event) {
          onEvent?.(event);
        }
      },
    });
  },
};

async function getUserInfo() {
  try {
    const response = await fetch("/userinfo", {
      method: "GET",
      credentials: "include",
      headers: { Accept: "application/json" },
    });
    if (!response.ok) return null;
    const payload = await response.json().catch(() => null);
    return normalizeUserInfo(payload);
  } catch (error) {
    return null;
  }
}

function normalizeUserInfo(payload) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return null;
  const candidates = [payload.data, payload.Data, payload.user, payload.User, payload];
  for (const candidate of candidates) {
    if (!candidate || typeof candidate !== "object" || Array.isArray(candidate)) continue;
    const email = firstString(candidate, ["email", "Email"]);
    const userName = firstString(candidate, ["username", "user_name", "UserName", "name", "Name", "full_name", "FullName"]);
    const avatarURL = safeImageURL(firstString(candidate, ["picture", "Picture", "avatar", "Avatar", "avatar_url", "AvatarURL", "picture_url", "PictureURL"]));
    const employeeNumber = firstString(candidate, ["employee_number", "EmployeeNumber", "employee_id", "EmployeeID"]);
    if (email || userName || avatarURL || employeeNumber) {
      return { email, userName, avatarURL, employeeNumber };
    }
  }
  return null;
}

function firstString(obj, keys) {
  for (const key of keys) {
    const value = obj[key];
    if (value === undefined || value === null) continue;
    const text = String(value).trim();
    if (text) return text;
  }
  return "";
}

function safeImageURL(value) {
  if (!value) return "";
  try {
    const parsed = new URL(value, window.location.origin);
    if (parsed.protocol === "http:" || parsed.protocol === "https:") return parsed.href;
  } catch (error) {
    return "";
  }
  return "";
}

async function streamPost(name, { body, signal, onMessage }) {
  const response = await fetch(`${API_PREFIX}/${name}`, {
    method: "POST",
    headers: aicAgentSDKHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify(body || {}),
    signal,
  });
  if (!response.ok || !response.body) {
    if (response.status === 401) {
      redirectToLogin();
    }
    throw new Error(response.statusText || `${name} failed`);
  }

  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffered = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffered += value;
    let index = buffered.indexOf("\n\n");
    while (index >= 0) {
      const raw = buffered.slice(0, index);
      buffered = buffered.slice(index + 2);
      const emitted = emitSSE(raw, onMessage);
      if (emitted) await yieldToBrowser();
      index = buffered.indexOf("\n\n");
    }
  }
}

function aicAgentSDKHeaders(extra = {}) {
  return {
    ...extra,
    [BOE_TEST_AUTH_HEADER]: "1234",
  };
}

function normalizeTimelineResponse(payload) {
  return {
    ...payload,
    events: (payload.events || payload.Events || []).map(parseTimelineEvent).filter(Boolean),
  };
}

function isUnauthenticated(response, baseResp) {
  return response.status === 401 || Number(baseResp.StatusCode || baseResp.status_code || 0) === 401;
}

function redirectToLogin() {
  if (typeof window === "undefined" || !window.location) return;
  const next = `${window.location.pathname || "/"}${window.location.search || ""}${window.location.hash || ""}`;
  window.location.assign(`${LOGIN_PATH}?next=${encodeURIComponent(next)}`);
}

function parseTimelineEvent(item) {
  if (!item) return null;
  if (item.event_json) return parseLegacyTimelineEvent(item.event_json);

  const payload = normalizeTimelinePayload(item.payload);
  const eventType = normalizeTimelineField(item.event_type, "");
  const shapedPayload = shapeTimelinePayload(eventType, payload);
  return {
    ...shapedPayload,
    event_id: normalizeTimelineField(item.event_id, ""),
    event_type: eventType,
    session_id: normalizeTimelineField(item.session_id, ""),
    thread_id: normalizeTimelineField(item.thread_id, ""),
    turn_id: normalizeTimelineField(item.turn_id, ""),
    created_at_ms: Number(item.created_at_ms || 0),
    __ac_event: {
      event_id: normalizeTimelineField(item.event_id, ""),
      session_id: normalizeTimelineField(item.session_id, ""),
      thread_id: normalizeTimelineField(item.thread_id, ""),
      turn_id: normalizeTimelineField(item.turn_id, ""),
      created_at_ms: Number(item.created_at_ms || 0),
    },
  };
}

function shapeTimelinePayload(eventType, payload) {
  switch (eventType) {
    case "TURN_STARTED":
    case "ASSISTANT_MESSAGE":
      return {
        message: payload,
        context_usage: payload.context_usage,
        consumed_message_ids: payload.consumed_message_ids || [],
      };
    case "ASSISTANT_DELTA":
      return { assistant_delta: payload };
    case "TOOL_CALL_STARTED":
    case "TOOL_CALL_OUTPUT_DELTA":
    case "TOOL_CALL_FINISHED":
      return { tool_call: payload, context_usage: payload.context_usage };
    case "APPROVAL_REQUIRED":
      return { approval: payload, consumed_message_ids: payload.consumed_message_ids || [] };
    case "INTERRUPT_REQUIRED":
      return { interrupt_required: payload, consumed_message_ids: payload.consumed_message_ids || [] };
    case "PLAN_UPDATED":
      return { plan_updated: payload, context_usage: payload.context_usage };
    case "PLAN_INPUT_REQUIRED":
      return { plan_input_required: payload, consumed_message_ids: payload.consumed_message_ids || [] };
    case "CONTEXT_COMPACTED":
      return { context_compacted: payload, context_usage: payload.context_usage };
    case "ERROR":
    case "TURN_INTERRUPTED":
      return { error: payload, context_usage: payload.context_usage, consumed_message_ids: payload.consumed_message_ids || [] };
    case "TURN_FINISHED":
      return { turn_finished: payload, context_usage: payload.context_usage, consumed_message_ids: payload.consumed_message_ids || [] };
    case "COMPACT_INTERRUPTED":
      return { compact_interrupted: payload };
    default:
      return { payload };
  }
}

function parseLegacyTimelineEvent(eventJSON) {
  try {
    return JSON.parse(eventJSON);
  } catch (error) {
    return timelinePayloadError(error);
  }
}

function normalizeTimelinePayload(payload) {
  if (!payload) return {};
  if (typeof payload === "object" && !Array.isArray(payload)) return payload;
  if (typeof payload === "string") {
    try {
      const parsed = JSON.parse(payload);
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) return parsed;
    } catch (error) {
      return timelinePayloadError(error);
    }
  }
  return {
    event_type: "ERROR",
    error: {
      message: "Unsupported timeline payload.",
      detail: String(payload),
    },
  };
}

function timelinePayloadError(error) {
  return {
    event_type: "ERROR",
    error: {
      message: "Failed to parse timeline event.",
      detail: String(error?.message || error),
    },
  };
}

function normalizeTimelineField(primary, fallback) {
  if (primary === undefined || primary === null || primary === "") return fallback;
  return String(primary);
}

function emitSSE(raw, onMessage) {
  let name = "message";
  const data = [];
  for (const line of raw.split(/\r?\n/)) {
    if (line.startsWith("event:")) {
      name = line.slice("event:".length).trim();
    } else if (line.startsWith("data:")) {
      data.push(line.slice("data:".length).trimStart());
    }
  }
  if (!data.length) return false;
  onMessage?.({ name, payload: JSON.parse(data.join("\n")) });
  return true;
}

function yieldToBrowser() {
  return new Promise((resolve) => setTimeout(resolve, 0));
}
