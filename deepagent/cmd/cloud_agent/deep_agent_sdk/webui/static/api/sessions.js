export function createSessionAPI(request, fetchFn) {
  return {
    ping: () => request("ping"),
    getUserInfo: () => getUserInfo(fetchFn),
    createTask: ({ title = "", projectName }) => {
      const body = { project_name: projectName };
      if (title) body.title = title;
      return request("create_session", body);
    },
    listProjects: () => request("list_projects"),
    closeProject: (projectName) => request("close_project", {
      project_name: projectName,
      reason: "user_remove_project",
    }),
    listTasks: (projectName) => request("list_sessions", {
      ...(projectName ? { project_name: projectName } : {}),
      status: 1,
      limit: 100,
    }),
    listSessions: (projectName) => request("list_sessions", {
      ...(projectName ? { project_name: projectName } : {}),
      limit: 100,
    }),
    getTask: (sessionID) => request("get_session", {
      session_id: String(sessionID),
      include_threads: true,
    }),
    updateTask: (sessionID, patch) => request("update_session", {
      session_id: String(sessionID),
      ...patch,
    }),
    closeTask: (sessionID) => request("close_session", {
      session_id: String(sessionID),
      reason: "user_close",
    }),
    stopTask: (sessionID) => request("stop_running", {
      session_id: String(sessionID),
      reason: "user_stop",
    }),
    submitInput: (input) => request("submit_input", submitBody(input)),
  };
}

function submitBody({ sessionID, threadID, content, mode, resumeRef, approval, interrupt, compact }) {
  const body = { session_id: String(sessionID) };
  if (threadID) body.thread_id = String(threadID);
  if (content) body.content = content;
  if (mode) body.mode = mode;
  if (!mode && compact) body.mode = 2;
  if (resumeRef) body.resume_ref = resumeRef;
  if (approval) body.approval = approval;
  if (interrupt) body.interrupt = interrupt;
  return body;
}

async function getUserInfo(fetchFn) {
  try {
    const response = await fetchFn("/userinfo", {
      method: "GET",
      credentials: "include",
      headers: { Accept: "application/json" },
    });
    if (!response.ok) return null;
    return normalizeUserInfo(await response.json().catch(() => null));
  } catch {
    return null;
  }
}

function normalizeUserInfo(payload) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return null;
  const candidates = [payload.data, payload.Data, payload.user, payload.User, payload];
  for (const candidate of candidates) {
    if (!candidate || typeof candidate !== "object" || Array.isArray(candidate)) continue;
    const email = firstText(candidate, ["email", "Email"]);
    const userName = firstText(candidate, ["username", "user_name", "UserName", "name", "Name", "full_name", "FullName"]);
    const avatarURL = safeImageURL(firstText(candidate, ["picture", "Picture", "avatar", "Avatar", "avatar_url", "AvatarURL", "picture_url", "PictureURL"]));
    const employeeNumber = firstText(candidate, ["employee_number", "EmployeeNumber", "employee_id", "EmployeeID"]);
    if (email || userName || avatarURL || employeeNumber) {
      return { email, userName, avatarURL, employeeNumber };
    }
  }
  return null;
}

function firstText(value, keys) {
  for (const key of keys) {
    if (value[key] === undefined || value[key] === null) continue;
    const text = String(value[key]).trim();
    if (text) return text;
  }
  return "";
}

function safeImageURL(value) {
  if (!value) return "";
  try {
    const origin = globalThis.location?.origin || "http://localhost";
    const url = new URL(value, origin);
    return url.protocol === "http:" || url.protocol === "https:" ? url.href : "";
  } catch {
    return "";
  }
}
