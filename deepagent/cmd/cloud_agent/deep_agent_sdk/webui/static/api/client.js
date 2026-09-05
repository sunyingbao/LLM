import { createSessionAPI } from "./sessions.js";
import { createTimelineAPI } from "./timeline.js";
import { createWorkspaceAPI } from "./workspace.js";

const defaultBasePath = "/ad/deep_agent_sdk";
const identityHeader = "X-Deep-Agent-SDK-Test-UID";

export function createAPI({
  basePath = defaultBasePath,
  fetchFn = globalThis.fetch?.bind(globalThis),
  onUnauthenticated = defaultAuthenticationFailure,
} = {}) {
  if (!fetchFn) throw new Error("fetch is unavailable");
  const request = createJSONRequest({ basePath, fetchFn, onUnauthenticated });
  const streamRequest = createStreamRequest({ basePath, fetchFn, onUnauthenticated });
  const sessions = createSessionAPI(request, fetchFn);
  const timeline = createTimelineAPI(request, streamRequest);
  const workspace = createWorkspaceAPI(request, basePath);
  const api = { ...sessions, ...timeline, ...workspace };
  return {
    ...api,
    createSession: api.createTask,
    getSession: api.getTask,
    closeSession: api.closeTask,
    stopRunning: api.stopTask,
  };
}

export function createJSONRequest({ basePath = defaultBasePath, fetchFn, onUnauthenticated }) {
  return async function request(name, body = {}, options = {}) {
    const response = await fetchFn(`${basePath}/${name}`, {
      method: "POST",
      headers: headers({ "Content-Type": "application/json" }),
      body: JSON.stringify(body),
      signal: options.signal,
    });
    const payload = await response.json().catch(() => ({}));
    assertSuccess(name, response, payload, onUnauthenticated);
    return payload;
  };
}

export function createStreamRequest({ basePath = defaultBasePath, fetchFn, onUnauthenticated }) {
  return async function streamRequest(name, { body, signal, onMessage }) {
    const response = await fetchFn(`${basePath}/${name}`, {
      method: "POST",
      headers: headers({ "Content-Type": "application/json" }),
      body: JSON.stringify(body || {}),
      signal,
    });
    if (!response.ok || !response.body) {
      if (response.status === 401) onUnauthenticated?.();
      throw new Error(response.statusText || `${name} failed`);
    }
    const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
    let buffered = "";
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      buffered += chunk.value;
      let boundary = buffered.indexOf("\n\n");
      while (boundary >= 0) {
        const raw = buffered.slice(0, boundary);
        buffered = buffered.slice(boundary + 2);
        if (emitSSE(raw, onMessage)) await new Promise((done) => setTimeout(done, 0));
        boundary = buffered.indexOf("\n\n");
      }
    }
  };
}

function assertSuccess(name, response, payload, onUnauthenticated) {
  const base = payload.BaseResp || payload.base_resp || {};
  const code = Number(base.StatusCode || base.status_code || 0);
  if (response.ok && code === 0) return;
  if (response.status === 401 || code === 401) onUnauthenticated?.();
  throw new Error(base.StatusMessage || base.status_message || payload.error || response.statusText || `${name} failed`);
}

function headers(extra = {}) {
  return { ...extra, [identityHeader]: "1234" };
}

function emitSSE(raw, onMessage) {
  let name = "message";
  const data = [];
  for (const line of raw.split(/\r?\n/)) {
    if (line.startsWith("event:")) name = line.slice(6).trim();
    if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
  }
  if (!data.length) return false;
  onMessage?.({ name, payload: JSON.parse(data.join("\n")) });
  return true;
}

function defaultAuthenticationFailure() {
  if (!globalThis.window?.location) return;
  const location = globalThis.window.location;
  const next = `${location.pathname || "/"}${location.search || ""}${location.hash || ""}`;
  location.assign(`/oidc/login?next=${encodeURIComponent(next)}`);
}
