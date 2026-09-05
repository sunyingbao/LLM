# Codex Workspace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把现有 `deep_agent_sdk/webui` 改造成已确认设计图中的 Codex 风格 DeepAgent 开发工作区，并完成真实功能、自动化测试和逐页视觉验收。

**Architecture:** 保留现有 Session、Timeline、SSE、文件和输入协议，前端改为无生产构建依赖的 ES Modules。单一 Store 保存页面事实，Timeline events 统一折叠成 Activity，features 连接 API 和组件；后端只新增只读 Git Changes/Diff 接口，Terminal 直接投影已有 Tool Activity。

**Tech Stack:** Go 1.25、Hertz、Go `embed.FS`、原生 HTML/CSS/JavaScript ES Modules、Node `node:test`、SSE。

**Spec:** `deepagent/cmd/cloud_agent/deep_agent_sdk/docs/codex-workspace-design.md`

## Global Constraints

- 不创建新 worktree；所有改动发生在当前 `feat/arch` 工作区。
- 保留工作区中与本计划无关的已有修改，不回滚、不覆盖、不混入阶段提交。
- 生产前端不引入 React、Vite、npm runtime package 或额外构建步骤。
- 新增或修改的 Go 函数使用命名返回值，并显式 `return`；生成代码和外部接口签名除外。
- 不修改 DeepAgent ReAct loop、AgentThread、Worker event payload、AC mailbox/eventlog 和 Session 数据库结构。
- 前端只展示 Project、Task、Activity；Session 和主 Thread 不成为一级 UI 概念。
- `runState` 只有 `idle | running | waiting_approval | waiting_input | stopping | error`。
- 视觉基准固定为 `docs/codex-workspace-main.png` 和设计规格第 5 节的 token。
- 每个任务只暂存并提交该任务列出的文件。

---

### Task 1: 建立单一 Store 和 Activity 模型

**Files:**
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/store.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/reducer.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/activity.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/selectors.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/activity.test.mjs`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/reducer.test.mjs`

**Interfaces:**
- Consumes: API 已规范化的 Timeline event object。
- Produces: `createStore(reduce, initialState)`、`reduce(state, action)`、`toActivities(events)`、`selectRunState(state)`、`selectPendingActivity(state)`。

- [ ] **Step 1: 写 Activity 折叠失败测试**

```js
import assert from "node:assert/strict";
import test from "node:test";
import { toActivities } from "../static/state/activity.js";

test("folds assistant and tool deltas into one activity each", () => {
  const activities = toActivities([
    { event_id: "1", event_type: "TURN_STARTED", thread_id: "t1", turn_id: "r1", message: { parts: [{ type: "text", text: "inspect" }] } },
    { event_id: "2", event_type: "ASSISTANT_DELTA", thread_id: "t1", turn_id: "r1", assistant_delta: { delta: "I will " } },
    { event_id: "3", event_type: "ASSISTANT_DELTA", thread_id: "t1", turn_id: "r1", assistant_delta: { delta: "inspect." } },
    { event_id: "4", event_type: "TOOL_CALL_STARTED", thread_id: "t1", turn_id: "r1", tool_call: { tool_call_id: "c1", tool_name: "exec_command", arguments_json: "{\"cmd\":\"pwd\"}" } },
    { event_id: "5", event_type: "TOOL_CALL_OUTPUT_DELTA", thread_id: "t1", turn_id: "r1", tool_call: { tool_call_id: "c1", output_delta: "/repo" } },
    { event_id: "6", event_type: "TOOL_CALL_FINISHED", thread_id: "t1", turn_id: "r1", tool_call: { tool_call_id: "c1", status: "completed", result_json: "{\"exit_code\":0}" } },
  ]);

  assert.deepEqual(activities.map((item) => item.kind), ["user", "assistant", "tool"]);
  assert.equal(activities[1].content, "I will inspect.");
  assert.equal(activities[2].output, "/repo");
  assert.equal(activities[2].status, "completed");
});
```

- [ ] **Step 2: 运行测试并确认缺少模块**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/activity.test.mjs`

Expected: FAIL with `ERR_MODULE_NOT_FOUND` for `static/state/activity.js`.

- [ ] **Step 3: 实现稳定的 Activity 折叠**

```js
export function toActivities(events) {
  const activities = [];
  const tools = new Map();
  let assistant = null;
  for (const event of events) {
    switch (event.event_type) {
      case "TURN_STARTED":
        assistant = null;
        appendUser(activities, event);
        break;
      case "ASSISTANT_DELTA":
        assistant = appendAssistantDelta(activities, assistant, event);
        break;
      case "ASSISTANT_MESSAGE":
        assistant = finishAssistant(activities, assistant, event);
        break;
      case "TOOL_CALL_STARTED":
      case "TOOL_CALL_OUTPUT_DELTA":
      case "TOOL_CALL_FINISHED":
        assistant = null;
        mergeTool(activities, tools, event);
        break;
      default:
        assistant = null;
        appendControl(activities, event);
    }
  }
  return activities;
}
```

Activity ID 使用后端 `event_id`；流式 assistant 使用 `assistant:${thread_id}:${turn_id}`，tool 使用 `tool:${thread_id}:${turn_id}:${tool_call_id}`。最终 `ASSISTANT_MESSAGE` 替换对应流式内容，不增加第二条回复。

- [ ] **Step 4: 写 Reducer 状态转换失败测试**

```js
test("pending approval is the single run state", () => {
  const running = reduce(initialState(), {
    type: "timeline/received",
    events: [{ event_id: "1", event_type: "TURN_STARTED", thread_id: "t1", turn_id: "r1" }],
  });
  const waiting = reduce(running, {
    type: "timeline/received",
    events: [{ event_id: "2", event_type: "APPROVAL_REQUIRED", thread_id: "t1", turn_id: "r1", approval: { tool_name: "execute" } }],
  });
  assert.equal(running.task.runState, "running");
  assert.equal(waiting.task.runState, "waiting_approval");
  assert.equal(waiting.task.pending.id, "2");
});

test("run state follows lifecycle events", () => {
  assertRunState("idle", []);
  assertRunState("running", [{ event_type: "TURN_STARTED" }]);
  assertRunState("waiting_input", [{ event_type: "INTERRUPT_REQUIRED" }]);
  assertRunState("error", [{ event_type: "TURN_FAILED" }]);
  assertRunState("idle", [{ event_type: "TURN_FINISHED" }]);
});
```

- [ ] **Step 5: 实现 Store、Reducer 和 Selector**

```js
export function createStore(reduce, initial) {
  let state = initial;
  const listeners = new Set();
  return {
    getState: () => state,
    dispatch(action) {
      const next = reduce(state, action);
      if (next === state) return;
      state = next;
      for (const listener of listeners) listener(state);
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}
```

`timeline/received` 先按 `event_id` 去重，再调用 `toActivities`，最后由最新 lifecycle/pending event 计算一次 `runState`。`stop/requested` 单独进入 `stopping`，新的 lifecycle event 再覆盖它。Reducer 不读 DOM、localStorage 或网络。

- [ ] **Step 6: 运行 State 测试**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/activity.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/reducer.test.mjs`

Expected: PASS.

- [ ] **Step 7: 提交 State 基础**

```bash
git add deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/activity.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/reducer.test.mjs
git commit -m "refactor: add WebUI state and activity model"
```

---

### Task 2: 拆分 API client 和 Timeline transport

**Files:**
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/api/client.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/api/sessions.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/api/timeline.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/api/workspace.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/client.test.mjs`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/timeline_channel.test.mjs`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/api.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/stream_controller.test.mjs`

**Interfaces:**
- Consumes: `fetch`, `AbortSignal`, current JSON/SSE HTTP contract。
- Produces: `createAPI({ basePath, fetchFn, onUnauthenticated })` and `createTimelineChannel({ api, onEvents, loadQueue, saveQueue })`。

- [ ] **Step 1: 写 JSON client 失败测试**

```js
test("normalizes BaseResp and keeps the BOE auth header", async () => {
  const calls = [];
  const api = createAPI({
    basePath: "/ad/deep_agent_sdk",
    fetchFn: async (url, init) => {
      calls.push({ url, init });
      return new Response(JSON.stringify({ projects: [], BaseResp: { StatusCode: 0 } }), { status: 200 });
    },
    onUnauthenticated: () => assert.fail("unexpected redirect"),
  });
  const result = await api.listProjects();
  assert.deepEqual(result.projects, []);
  assert.equal(calls[0].url, "/ad/deep_agent_sdk/list_projects");
  assert.equal(calls[0].init.headers["X-Deep-Agent-SDK-Test-UID"], "1234");
});
```

- [ ] **Step 2: 运行 client 测试并确认缺少模块**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/client.test.mjs`

Expected: FAIL with `ERR_MODULE_NOT_FOUND` for `static/api/client.js`.

- [ ] **Step 3: 实现可注入的 JSON client 和领域 API**

```js
export function createJSONRequest({ basePath, fetchFn, onUnauthenticated }) {
  return async function request(name, body = {}, options = {}) {
    const response = await fetchFn(`${basePath}/${name}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Deep-Agent-SDK-Test-UID": "1234" },
      body: JSON.stringify(body),
      signal: options.signal,
    });
    const payload = await response.json().catch(() => ({}));
    assertSuccess(response, payload, onUnauthenticated);
    return payload;
  };
}
```

`sessions.js` 只声明 Task 请求，`workspace.js` 只声明 files/changes/diff，`timeline.js` 保留当前 payload compatibility mapping 和 SSE parser。

- [ ] **Step 4: 写 Timeline 恢复失败测试**

```js
test("discards events from a previously selected task", async () => {
  const received = [];
  const channels = [];
  const channel = createTimelineChannel({
    api: {
      subscribeTimeline: async ({ sessionID, recoverQueueID, onQueue, onEvent, signal }) => {
        channels.push({ sessionID, recoverQueueID });
        onQueue(`queue-${sessionID}`);
        await new Promise((done) => signal.addEventListener("abort", done, { once: true }));
        onEvent({ event_id: `late-${sessionID}` });
      },
    },
    loadQueue: () => "saved-queue",
    saveQueue: () => {},
    onEvents: (taskID, events) => received.push({ taskID, events }),
  });
  channel.follow("task-1");
  channel.follow("task-2");
  await new Promise((done) => queueMicrotask(done));
  assert.equal(channels[0].recoverQueueID, "saved-queue");
  assert.equal(received.some((item) => item.taskID === "task-1"), false);
});
```

- [ ] **Step 5: 实现单连接 Timeline channel**

```js
export function createTimelineChannel(config) {
  let active = null;
  return {
    follow(taskID) {
      active?.controller.abort();
      const controller = new AbortController();
      const token = Symbol(taskID);
      active = { taskID, token, controller };
      follow(config, active).catch((error) => {
        if (active?.token === token && error.name !== "AbortError") config.onError?.(taskID, error);
      });
    },
    close() {
      active?.controller.abort();
      active = null;
    },
  };
}
```

重连沿用 `recover_queue_id`；队列失效时 `listTimeline(cursor)` 补事件后开启新订阅。旧 task token 的事件和错误全部丢弃。

- [ ] **Step 6: 让旧 `api.js` 临时转发到新 client**

```js
import { createAPI } from "./api/client.js";

export const api = createAPI({
  basePath: "/ad/deep_agent_sdk",
  fetchFn: window.fetch.bind(window),
  onUnauthenticated: redirectToLogin,
});
```

保留旧页面可运行，直到 Task 9 删除 compatibility facade。

- [ ] **Step 7: 运行 API 和 transport 测试**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/client.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/timeline_channel.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/stream_controller.test.mjs`

Expected: PASS.

- [ ] **Step 8: 提交 API 和 transport**

```bash
git add deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/api deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/api.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/client.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/timeline_channel.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/stream_controller.test.mjs
git commit -m "refactor: split WebUI API and timeline transport"
```

---

### Task 3: 建立 App Shell 和视觉系统

**Files:**
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/index.html`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/app.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/app_shell.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/sidebar.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/task_workspace.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/inspector.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles/index.css`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles/tokens.css`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles/base.css`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles/layout.css`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles/components.css`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/auth_redirect_test.go`

**Interfaces:**
- Consumes: `store.subscribe`, feature actions。
- Produces: `mountApp(root, { store, actions })` and DOM regions `[data-sidebar]`, `[data-workspace]`, `[data-inspector]`。

- [ ] **Step 1: 写嵌套静态资源失败测试**

```go
func TestCodexWorkspaceStaticShell(t *testing.T) {
	for _, name := range []string{
		"static/index.html",
		"static/components/app_shell.js",
		"static/styles/tokens.css",
		"static/styles/layout.css",
	} {
		if _, err := Static.ReadFile(name); err != nil {
			t.Fatalf("Static.ReadFile(%q) error = %v", name, err)
		}
	}
}
```

- [ ] **Step 2: 运行 Go 测试并确认资源缺失**

Run: `go test ./deepagent/cmd/cloud_agent/deep_agent_sdk/webui -run TestCodexWorkspaceStaticShell -count=1`

Expected: FAIL reading `static/components/app_shell.js`.

- [ ] **Step 3: 创建语义化 HTML shell**

```html
<div id="app" class="app-shell">
  <aside class="sidebar" data-sidebar aria-label="Projects and tasks"></aside>
  <main class="task-workspace" data-workspace></main>
  <aside class="inspector" data-inspector aria-label="Workspace inspector"></aside>
</div>
<script type="module" src="./app.js"></script>
```

`index.html` 只保留 mount points、metadata、favicon 和 `/static/styles/index.css`，不放业务 markup。

- [ ] **Step 4: 实现 Design token 和三栏布局**

```css
@import "./tokens.css";
@import "./base.css";
@import "./layout.css";
@import "./components.css";

.app-shell {
  display: grid;
  grid-template-columns: var(--sidebar-width) minmax(0, 1fr) var(--inspector-width);
  height: 100dvh;
  overflow: hidden;
}
```

`tokens.css` 使用规格第 5 节的确切值。这个阶段只实现布局、字体、边框、基础按钮和空态，不实现业务卡片。

- [ ] **Step 5: 实现只负责装配的 App Shell**

```js
export function mountApp(root, { store, actions }) {
  const sidebar = createSidebar(root.querySelector("[data-sidebar]"), actions);
  const workspace = createTaskWorkspace(root.querySelector("[data-workspace]"), actions);
  const inspector = createInspector(root.querySelector("[data-inspector]"), actions);
  const render = (state) => {
    sidebar.render(state);
    workspace.render(state);
    inspector.render(state);
  };
  return {
    start() {
      store.subscribe(render);
      render(store.getState());
    },
  };
}
```

- [ ] **Step 6: 运行静态资源和 JS 语法测试**

Run:

```bash
go test ./deepagent/cmd/cloud_agent/deep_agent_sdk/webui -count=1
node --check deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/app.js
node --check deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/app_shell.js
```

Expected: PASS.

- [ ] **Step 7: 提交 App Shell**

```bash
git add deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/index.html deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/app.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles deepagent/cmd/cloud_agent/deep_agent_sdk/webui/auth_redirect_test.go
git commit -m "feat: add Codex-style WebUI shell"
```

---

### Task 4: 实现 Project、Task 和 Sidebar 生命周期

**Files:**
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/index.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/tasks.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/sidebar.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/task_workspace.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/reducer.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/selectors.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/tasks.test.mjs`

**Interfaces:**
- Consumes: `api.listProjects`、`api.listTasks`、`api.getTask`、`api.updateTask`、`api.closeProject`。
- Produces: `createTaskFeature({ api, store })` with `loadCatalog`、`newTask`、`selectProject`、`selectTask`、`renameTask`、`archiveTask`、`restoreTask`、`closeProject`。

- [ ] **Step 1: 写 Task 快速切换和搜索失败测试**

```js
test("ignores a task load that finishes after another task was selected", async () => {
  const pending = new Map();
  const feature = createTaskFeature({
    api: { getTask: (taskID) => new Promise((done) => pending.set(taskID, done)) },
    store,
  });
  const first = feature.selectTask("1");
  const second = feature.selectTask("2");
  pending.get("2")({ session_view: { session: { session_id: "2" } } });
  pending.get("1")({ session_view: { session: { session_id: "1" } } });
  await Promise.all([first, second]);
  assert.equal(store.getState().catalog.selectedTaskID, "2");
  assert.equal(store.getState().task.view.session.session_id, "2");
});

test("filters task title and preview case-insensitively", () => {
  assert.deepEqual(filterTasks(tasks, "runtime").map((task) => task.session_id), ["2"]);
});
```

- [ ] **Step 2: 运行测试并确认 feature 缺失**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/tasks.test.mjs`

Expected: FAIL with `ERR_MODULE_NOT_FOUND` for `static/features/tasks.js`.

- [ ] **Step 3: 实现 token 化 Task 加载**

```js
export function createTaskFeature({ api, store }) {
  let loadToken = 0;
  async function selectTask(taskID) {
    const token = ++loadToken;
    store.dispatch({ type: "task/selected", taskID });
    const view = await api.getTask(taskID);
    if (token !== loadToken) return;
    store.dispatch({ type: "task/loaded", taskID, view: view.session_view });
  }
  return { selectTask, loadCatalog, newTask, selectProject, renameTask, archiveTask };
}
```

`renameTask` 调 `update_session{title}`；`archiveTask` 调 `update_session{status:2}`；撤销时 `restoreTask` 调 `update_session{status:1}`；`closeProject` 复用现有项目关闭接口。`New task` 只调用 `newTask` 进入空白态，不调用 CreateSession。

- [ ] **Step 4: 实现 Sidebar DOM 和键盘行为**

```js
export function createSidebar(root, actions) {
  bindSidebarEvents(root, actions);
  return {
    render(state) {
      root.replaceChildren(sidebarView(state));
    },
  };
}
```

Sidebar 使用事件委托，只绑定一次 click/keydown listener。Task menu 支持 Enter/Space 打开、Escape 关闭；Rename 使用内联 input，Archive 显示可撤销提示并从 active list 移除。Project 展开状态由 feature 写入 `localStorage`，启动时读回后通过 action 进入 Store。

- [ ] **Step 5: 运行 Task 和 State 测试**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/tasks.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/reducer.test.mjs`

Expected: PASS.

- [ ] **Step 6: 提交 Task 生命周期**

```bash
git add deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/index.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/tasks.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/sidebar.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/task_workspace.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/tasks.test.mjs
git commit -m "feat: add WebUI project and task navigation"
```

---

### Task 5: 实现对话、Composer 和 Pending 闭环

**Files:**
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/conversation.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/timeline.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/approvals.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/plans.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/tools.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/activity_timeline.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/composer.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/task_workspace.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/index.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/reducer.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/conversation.test.mjs`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/pending.test.mjs`

**Interfaces:**
- Consumes: Task feature、Timeline channel、`submit_input`、`stop_running`。
- Produces: `submitMessage`、`stopTask`、`submitApproval`、`submitPlanInput`、`submitInterrupt`、`compactContext`、`agentSummaries` and renderable Activity nodes。

- [ ] **Step 1: 写空白 Task 首次提交失败测试**

```js
test("preserves a created session when the first submit fails", async () => {
  let creates = 0;
  const feature = createConversationFeature({
    api: {
      createTask: async () => {
        creates += 1;
        return { session_view: { session: { session_id: "9" } } };
      },
      submitInput: async () => { throw new Error("submit failed"); },
    },
    store,
  });
  await assert.rejects(feature.submitMessage("inspect the repo"), /submit failed/);
  assert.equal(store.getState().catalog.selectedTaskID, "9");
  assert.equal(creates, 1);
  assert.equal(store.getState().task.submitError, "submit failed");
});
```

- [ ] **Step 2: 写 Pending 防重复失败测试**

```js
test("submits a pending approval once and restores it after failure", async () => {
  let calls = 0;
  const pending = createPendingFeature({
    submit: async () => {
      calls += 1;
      throw new Error("resume failed");
    },
    store,
  });
  await Promise.allSettled([
    pending.approve("event-1", true),
    pending.approve("event-1", true),
  ]);
  assert.equal(calls, 1);
  assert.equal(store.getState().task.pending.id, "event-1");
  assert.equal(store.getState().task.runState, "waiting_approval");
});
```

- [ ] **Step 3: 运行测试并确认功能模块缺失**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/conversation.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/pending.test.mjs`

Expected: FAIL with `ERR_MODULE_NOT_FOUND`.

- [ ] **Step 4: 实现 Conversation 和 submission gate**

```js
export function createSubmissionGate() {
  const active = new Set();
  return async function once(key, submit) {
    if (active.has(key)) return;
    active.add(key);
    try {
      return await submit();
    } finally {
      active.delete(key);
    }
  };
}
```

首次提交顺序固定为 `create_session -> optimistic user activity -> submit_input`。失败后保留已创建 Task 和原输入，用户重试时复用同一 Session。`stopTask` 先 dispatch `stop/requested` 再调用 `stop_running`；`compactContext` 复用 `submit_input{compact:true}`，不创建另一条请求协议。

- [ ] **Step 5: 实现 Timeline、Activity 和 Composer DOM**

```js
export function createActivityTimeline(root, actions) {
  return {
    render({ task }) {
      patchKeyedActivities(root, task.activities, (activity) => activityNode(activity, actions));
      keepReadingPosition(root, task.scrollRequest);
    },
  };
}
```

Assistant 使用无气泡文本；tool/thinking/plan 默认折叠；pending card 不能被运行 indicator 覆盖。Task 的 `threads` 按 role/title 投影成 AgentStrip，但选择 Child Thread 只过滤当前 Task 的 Activity，不改变一级导航。Composer 处理 IME，Enter 提交、Shift+Enter 换行，发送中显示 Stop。

- [ ] **Step 6: 运行 Conversation、Pending、Activity 测试**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/conversation.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/pending.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/activity.test.mjs`

Expected: PASS.

- [ ] **Step 7: 提交对话主链路**

```bash
git add deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/activity_timeline.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/composer.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/task_workspace.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/reducer.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/conversation.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/pending.test.mjs
git commit -m "feat: add WebUI conversation and pending flows"
```

---

### Task 6: 实现 Files 和 Terminal Inspector

**Files:**
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/files.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/terminal.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/index.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/inspector.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/reducer.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/selectors.js`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/files.test.mjs`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/terminal.test.mjs`

**Interfaces:**
- Consumes: `api.listFiles`、`api.fileURL`、Tool Activity。
- Produces: `selectFile`、`toggleDirectory`、`refreshFiles`、`terminalEntries(activities)`。

- [ ] **Step 1: 写文件迟到响应和 Terminal 投影失败测试**

```js
test("does not attach a file response to a different task", async () => {
  const request = deferred();
  const feature = createFilesFeature({ api: { listFiles: () => request.promise }, store });
  const load = feature.loadDirectory("task-1", ".");
  store.dispatch({ type: "task/selected", taskID: "task-2" });
  request.done({ files: [{ path: "main.go" }] });
  await load;
  assert.equal(store.getState().inspector.fileTree.size, 0);
});

test("projects command tools into terminal entries", () => {
  const entries = terminalEntries([
    { kind: "tool", id: "c1", toolName: "exec_command", arguments: { cmd: "go test ./..." }, output: "ok", status: "completed", exitCode: 0 },
    { kind: "tool", id: "c2", toolName: "read_file", status: "completed" },
  ]);
  assert.deepEqual(entries.map((entry) => entry.id), ["c1"]);
  assert.equal(entries[0].command, "go test ./...");
});
```

- [ ] **Step 2: 运行测试并确认模块缺失**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/files.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/terminal.test.mjs`

Expected: FAIL with `ERR_MODULE_NOT_FOUND`.

- [ ] **Step 3: 实现 Files feature 和预览规则**

```js
export function fileURL(sessionID, path) {
  const query = new URLSearchParams({ session_id: String(sessionID), path });
  return `/ad/deep_agent_sdk/file?${query}`;
}
```

目录节点按 path 存储 `loaded/loading/expanded/files`。文本文件使用 `/file` 读取；图片、音视频使用相同 URL；超过预览限制时显示下载入口，不把二进制塞进 Store。

- [ ] **Step 4: 实现 Terminal Activity 投影**

```js
export function terminalEntries(activities) {
  return activities
    .filter((item) => item.kind === "tool" && isCommandTool(item.toolName))
    .map((item) => ({
      id: item.id,
      command: commandFrom(item),
      output: item.output || "",
      status: item.status,
      exitCode: exitCodeFrom(item),
    }));
}
```

Terminal 只读，不创建新的执行请求。运行中的 output delta 追加到同一个 entry。

- [ ] **Step 5: 实现 Inspector tabs 和折叠状态**

Tabs 使用 `role="tablist"`、`role="tab"`、`aria-selected`。折叠只改变 Inspector layout state；当前 Files/Terminal 数据保持不变。折叠状态由 feature 写入 `localStorage`，启动时读回并 dispatch `inspector/collapsedChanged`。

- [ ] **Step 6: 运行 Files、Terminal 和 Reducer 测试**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/files.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/terminal.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/reducer.test.mjs`

Expected: PASS.

- [ ] **Step 7: 提交 Files 和 Terminal**

```bash
git add deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/files.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/terminal.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/index.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/inspector.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/files.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/terminal.test.mjs
git commit -m "feat: add WebUI files and terminal inspector"
```

---

### Task 7: 新增只读 Git Changes 和 Diff API

**Files:**
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/service/changes/changes.go`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/service/changes/changes_test.go`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/biz/handler/changes.go`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/biz/handler/changes_test.go`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/router.go`

**Interfaces:**
- Consumes: authenticated Session、`cloudbackend.Open`、`backends.SandboxBackend.ExecuteCommand`。
- Produces: `changes.List(ctx, uid, req)`、`changes.Diff(ctx, uid, req)`、POST `list_changes`、POST `get_diff`。

- [ ] **Step 1: 写 Git workspace 失败测试**

```go
func TestCollectReturnsTrackedAndUntrackedChanges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	backend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     root,
		VirtualMode: true,
	})
	mustRunCommand(t, ctx, backend, root, "git init && git config user.email test@example.com && git config user.name test")
	mustWriteFile(t, filepath.Join(root, "tracked.txt"), "old\n")
	mustRunCommand(t, ctx, backend, root, "git add tracked.txt && git commit -m initial")
	mustWriteFile(t, filepath.Join(root, "tracked.txt"), "new\n")
	mustWriteFile(t, filepath.Join(root, "new.txt"), "added\n")

	items, err := Collect(ctx, backend, root)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if got := changePaths(items); !slices.Equal(got, []string{"new.txt", "tracked.txt"}) {
		t.Fatalf("paths = %v", got)
	}
}
```

- [ ] **Step 2: 写 Diff 路径拒绝失败测试**

```go
func TestCollectDiffRejectsParentPath(t *testing.T) {
	_, err := CollectDiff(context.Background(), nil, "/workspace", "../secret")
	if err == nil {
		t.Fatal("CollectDiff() error = nil")
	}
}
```

- [ ] **Step 3: 运行测试并确认 package 缺失**

Run: `go test ./deepagent/cmd/cloud_agent/deep_agent_sdk/service/changes -count=1`

Expected: FAIL because the package does not exist.

- [ ] **Step 4: 实现固定 Git 查询和输出限制**

```go
func Collect(ctx context.Context, backend backends.SandboxBackend, workDir string) (changes []ChangeInfo, err error) {
	status, err := backend.ExecuteCommand(ctx, backends.CommandRequest{
		Command:        "git status --porcelain=v1 -z --untracked-files=all",
		WorkDir:        workDir,
		Timeout:        5 * time.Second,
		MaxOutputBytes: maxStatusBytes,
	})
	if err != nil {
		return nil, err
	}
	if status.ExitCode != 0 && isNotGitRepository(status.Output) {
		return []ChangeInfo{}, nil
	}
	if status.ExitCode != 0 {
		return nil, fmt.Errorf("git status failed: %s", strings.TrimSpace(status.Output))
	}
	return collectStats(ctx, backend, workDir, parseStatus(status.Output))
}
```

`CollectDiff` 先调用 `cleanPath`，tracked file 使用固定 `git diff --no-ext-diff --unified=3 HEAD -- <quoted-path>`；untracked file 读取内容后生成 `/dev/null -> path` patch。Diff 最大 2 MiB，截断时设置 `Truncated=true`。

- [ ] **Step 5: 实现 Session workspace 包装和 handlers**

```go
func List(ctx context.Context, uid int64, req *ListRequest) (resp *ListResponse, err error) {
	workspace, err := openSessionWorkspace(ctx, uid, req.SessionID)
	if err != nil {
		return nil, err
	}
	items, err := Collect(ctx, workspace.Backend, workspace.WorkDir)
	if err != nil {
		return nil, common.Internal("collect workspace changes", err)
	}
	return &ListResponse{Changes: items}, nil
}
```

Handler 沿用 `currentUID`、`binding.BindAndValidate`、`commonBaseResp`。Router 增加：

```go
r.POST("/ad/deep_agent_sdk/list_changes", handler.ListChanges)
r.POST("/ad/deep_agent_sdk/get_diff", handler.GetDiff)
```

- [ ] **Step 6: 运行 Changes service 和 handler 测试**

Run: `go test ./deepagent/cmd/cloud_agent/deep_agent_sdk/service/changes ./deepagent/cmd/cloud_agent/deep_agent_sdk/biz/handler -run 'Test(Collect|ListChanges|GetDiff)' -count=1`

Expected: PASS.

- [ ] **Step 7: 运行 API package 回归**

Run: `go test ./deepagent/cmd/cloud_agent/deep_agent_sdk/... -count=1`

Expected: PASS.

- [ ] **Step 8: 提交 Changes API**

```bash
git add deepagent/cmd/cloud_agent/deep_agent_sdk/service/changes deepagent/cmd/cloud_agent/deep_agent_sdk/biz/handler/changes.go deepagent/cmd/cloud_agent/deep_agent_sdk/biz/handler/changes_test.go deepagent/cmd/cloud_agent/deep_agent_sdk/router.go
git commit -m "feat: expose read-only workspace changes"
```

---

### Task 8: 实现 Changes、Diff 和行级评论

**Files:**
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/changes.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/conversation.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/index.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/inspector.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/reducer.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles/components.css`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/changes.test.mjs`

**Interfaces:**
- Consumes: `api.listChanges`、`api.getDiff`、Conversation `submitMessage`。
- Produces: `loadChanges`、`selectChangedFile`、`addAnnotation`、`submitReview`、`parseUnifiedDiff(patch)`、`formatReviewMessage(annotations, note)`。

- [ ] **Step 1: 写 Diff parser 和评论格式失败测试**

```js
test("parses diff lines and formats stable review input", () => {
  const file = parseUnifiedDiff([
    "@@ -10,2 +10,2 @@",
    "-const oldName = true;",
    "+const clearName = true;",
  ].join("\n"));
  assert.equal(file.hunks[0].lines[1].newLine, 10);

  const message = formatReviewMessage([{
    path: "static/app.js",
    startLine: 10,
    endLine: 10,
    comment: "Keep this name explicit.",
  }], "Please update the diff.");
  assert.match(message, /File: static\/app\.js/);
  assert.match(message, /Lines: 10/);
  assert.match(message, /Keep this name explicit\./);
});
```

- [ ] **Step 2: 运行测试并确认 Changes feature 缺失**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/changes.test.mjs`

Expected: FAIL with `ERR_MODULE_NOT_FOUND`.

- [ ] **Step 3: 实现 Unified Diff parser**

```js
export function parseUnifiedDiff(patch) {
  const file = { hunks: [] };
  let hunk = null;
  let oldLine = 0;
  let newLine = 0;
  for (const text of String(patch || "").split("\n")) {
    const header = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(text);
    if (header) {
      oldLine = Number(header[1]);
      newLine = Number(header[2]);
      hunk = { header: text, lines: [] };
      file.hunks.push(hunk);
      continue;
    }
    if (!hunk) continue;
    hunk.lines.push(diffLine(text, oldLine, newLine));
    if (!text.startsWith("+")) oldLine += 1;
    if (!text.startsWith("-")) newLine += 1;
  }
  return file;
}
```

- [ ] **Step 4: 实现 Task token 化 Changes 加载**

`loadChanges` 和 `selectChangedFile` 捕获 `selectedTaskID`；响应回来时 Task 已变化就丢弃。Changes 在 `TOOL_CALL_FINISHED` 且工具可能修改文件时刷新，最多每 500ms 合并一次。

- [ ] **Step 5: 实现 Diff DOM 和评论提交**

```js
export function formatReviewMessage(annotations, note = "") {
  const blocks = annotations.map((item) => [
    "Review comment:",
    `- File: ${item.path}`,
    `- Lines: ${lineRange(item)}`,
    `- Comment: ${item.comment.trim()}`,
  ].join("\n"));
  return [...blocks, note.trim()].filter(Boolean).join("\n\n");
}
```

Diff 行使用真实 `<code>` 文本。点击新增/上下文行显示评论输入；`submitReview` 进入现有 `conversation.submitMessage`，成功后清空当前 Task 的 annotations。

- [ ] **Step 6: 运行 Changes 前端测试**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/changes.test.mjs deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/conversation.test.mjs`

Expected: PASS.

- [ ] **Step 7: 提交 Changes UI**

```bash
git add deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/changes.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/conversation.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/index.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/inspector.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state/reducer.js deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles/components.css deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/changes.test.mjs
git commit -m "feat: add WebUI diff review workflow"
```

### Task 9: 收口入口、可访问性、全链路联调与视觉验收

**Files:**
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/index.html`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/app.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/app_shell.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/sidebar.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/components/inspector.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/features/index.js`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles/base.css`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles/layout.css`
- Modify: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles/components.css`
- Create: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/accessibility.test.mjs`
- Delete after replacement is proven: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/api.js`
- Delete after replacement is proven: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/render.js`
- Delete after replacement is proven: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/state.js`
- Delete after replacement is proven: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/timeline_model.js`
- Delete after replacement is proven: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/stream_controller.js`
- Delete after replacement is proven: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/submit_gate.js`
- Delete after replacement is proven: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static/styles.css`
- Delete after replacement is proven: `deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/stream_controller.test.mjs`

**Interfaces:**
- Consumes: Tasks 1-8 产出的 Store、API、Features、Components 和样式。
- Produces: 唯一应用入口、完整键盘行为、响应式布局、无旧模块引用的最终 WebUI。

- [ ] **Step 1: 写结构与键盘行为失败测试**

```js
test("workspace exposes labelled regions and keyboard navigation", async () => {
  const view = createAppShell(document);
  assert.equal(view.sidebar.getAttribute("aria-label"), "Projects and tasks");
  assert.equal(view.main.getAttribute("aria-label"), "Task conversation");
  assert.equal(view.inspector.getAttribute("aria-label"), "Task inspector");

  view.inspectorTabs[0].focus();
  view.inspectorTabs[0].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight" }));
  assert.equal(document.activeElement, view.inspectorTabs[1]);
});
```

- [ ] **Step 2: 运行测试并确认可访问性行为尚未实现**

Run: `node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/accessibility.test.mjs`

Expected: FAIL because labelled regions or keyboard tab navigation is missing.

- [ ] **Step 3: 实现最终键盘与响应式规则**

- `>= 1180px`：左侧 256px，中间自适应，右侧 360px。
- `900px-1179px`：右侧收成 48px inspector rail，点击后覆盖展开。
- `< 900px`：左侧和右侧都成为抽屉，中间对话保持全宽。
- `Escape` 依次关闭评论输入、右侧抽屉、左侧抽屉。
- Inspector tabs 支持 `ArrowLeft`、`ArrowRight`、`Home`、`End`。
- 所有 icon-only button 都有 `aria-label`；运行、失败、等待审批不能只靠颜色区分。
- `prefers-reduced-motion: reduce` 时关闭非必要动画。

- [ ] **Step 4: 切换唯一入口并删除已被替代的旧模块**

先执行：

```bash
grep -R 'api.js\|render.js\|state.js\|timeline_model.js\|stream_controller.js\|submit_gate.js\|styles.css' deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static --include='*.js' --include='*.html'
```

Expected: no imports from the new entry path. Only then delete the seven legacy production files and the replaced stream controller test, and remove any legacy `<script>` or stylesheet reference from `index.html`.

- [ ] **Step 5: 运行全部前端自动化测试**

Run:

```bash
node --test deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static_tests/*.test.mjs
```

Expected: PASS.

- [ ] **Step 6: 运行 Go 回归测试**

Run:

```bash
go test ./deepagent/cmd/cloud_agent/deep_agent_sdk/... -count=1
go test ./deepagent/cloud/... ./deepagent/worker/... -count=1
go test ./deepagent/core/... ./deepagent/runtime/... ./deepagent/host/... -count=1
```

Expected: PASS. If a command fails because the local toolchain or sandbox cannot write its cache, rerun with a task-specific writable `GOCACHE` and record both commands and outputs.

- [ ] **Step 7: 启动真实服务并保存十一张视觉基线**

按现有 runbook 启动 API 与 WebUI。前九张使用 `1536x1024`，后两张使用文件名标注的尺寸：

1. `01-empty-task.png`
2. `02-running-tool.png`
3. `03-plan-waiting.png`
4. `04-approval-waiting.png`
5. `05-files-preview.png`
6. `06-terminal-output.png`
7. `07-changes-diff-comment.png`
8. `08-error-state.png`
9. `09-archived-task.png`
10. `10-inspector-collapsed-1179x900.png`
11. `11-drawers-899x900.png`

桌面图逐项对照 `codex-workspace-main.png`：三栏比例、12/13/14px 字号层级、8px 圆角、1px 分隔线、选中态、composer 高度、Inspector tabs 和底部状态区。两张响应式图分别确认 Inspector rail 和双抽屉行为。

- [ ] **Step 8: 完成真实交互回归**

在同一 Task 中依次验证：发送消息、流式输出、工具展开、Stop、继续输入、Plan 提交、Approval 同意/拒绝、Compact、文件查看、Terminal 投影、Changes 刷新、Diff 评论提交、Task 重命名、Task 归档、项目关闭。每一项同时确认浏览器无未处理异常、SSE 重连不重复 Activity、切换 Task 后无旧响应串入。

- [ ] **Step 9: 做最终静态检查**

Run:

```bash
git diff --check
grep -R 'api.js\|render.js\|state.js\|timeline_model.js\|stream_controller.js\|submit_gate.js\|styles.css' deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static --include='*.js' --include='*.html'
```

Expected: both commands produce no errors and the grep produces no legacy references.

- [ ] **Step 10: 提交收口改动**

```bash
git add deepagent/cmd/cloud_agent/deep_agent_sdk/webui
git commit -m "refactor: complete Codex-style WebUI architecture"
```

## Completion Audit

完成标准不是“页面能打开”，而是以下证据全部存在：

- Tasks 1-2：Store、Activity、API、SSE 生命周期测试通过。
- Tasks 3-6：三栏框架、Task 生命周期、Conversation、Plan、Approval、Files、Terminal 测试通过。
- Tasks 7-8：Git Changes/Diff 后端安全边界和前端评论链路测试通过。
- Task 9：全部前端测试、相关 Go 测试、十一张视觉基线和完整交互回归通过。
- 最终源码只有一个 WebUI 入口，不再引用七个旧生产模块，替代测试也只覆盖新实现。
- 未修改 ReAct、AgentThread、worker payload、Access Control 或 session 持久化语义。
- 每个提交只包含本方案对应文件；用户原有工作区改动没有被覆盖或混入。
