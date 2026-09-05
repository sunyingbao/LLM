import assert from "node:assert/strict";
import test from "node:test";

import { createAPI } from "../static/api/client.js";

test("uses the API prefix, BOE identity header, and product task names", async () => {
  const calls = [];
  const api = createAPI({
    basePath: "/ad/deep_agent_sdk",
    fetchFn: async (url, init) => {
      calls.push({ url, init });
      return response({ projects: [], BaseResp: { StatusCode: 0 } });
    },
  });

  const result = await api.listProjects();

  assert.deepEqual(result.projects, []);
  assert.equal(calls[0].url, "/ad/deep_agent_sdk/list_projects");
  assert.equal(calls[0].init.headers["X-Deep-Agent-SDK-Test-UID"], "1234");
  assert.equal(calls[0].init.method, "POST");
});

test("maps task operations onto the existing session contract", async () => {
  const calls = [];
  const api = createAPI({
    fetchFn: async (url, init) => {
      calls.push({ url, body: JSON.parse(init.body) });
      return response({ BaseResp: { StatusCode: 0 } });
    },
  });

  await api.listTasks("project-a");
  await api.updateTask("42", { title: "Clear title", status: 2 });
  await api.stopTask("42");
  await api.submitInput({ sessionID: "42", compact: true });

  assert.deepEqual(calls, [
    { url: "/ad/deep_agent_sdk/list_sessions", body: { project_name: "project-a", status: 1, limit: 100 } },
    { url: "/ad/deep_agent_sdk/update_session", body: { session_id: "42", title: "Clear title", status: 2 } },
    { url: "/ad/deep_agent_sdk/stop_running", body: { session_id: "42", reason: "user_stop" } },
    { url: "/ad/deep_agent_sdk/submit_input", body: { session_id: "42", mode: 2 } },
  ]);
});

test("reports authentication failures through the injected boundary", async () => {
  let authenticationFailures = 0;
  const api = createAPI({
    fetchFn: async () => response({ BaseResp: { StatusCode: 401, StatusMessage: "sign in" } }, 401),
    onUnauthenticated: () => {
      authenticationFailures += 1;
    },
  });

  await assert.rejects(api.listProjects(), /sign in/);
  assert.equal(authenticationFailures, 1);
});

function response(payload, status = 200) {
  return new Response(JSON.stringify(payload), {
    status,
    statusText: status === 200 ? "OK" : "Unauthorized",
    headers: { "Content-Type": "application/json" },
  });
}
