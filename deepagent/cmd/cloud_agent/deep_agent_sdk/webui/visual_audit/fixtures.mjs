import { readFile } from "node:fs/promises";
import { extname, resolve, sep } from "node:path";

const mockOrigin = "http://deepagent.local";
const desktop = { name: "desktop", width: 1536, height: 1024 };

const task = {
  session_id: "task-1",
  project_name: "LLM",
  title: "Build a Codex-style frontend",
  preview: "Implement and verify the DeepAgent workspace",
  status: 1,
};

const view = {
  session: task,
  threads: [
    { thread_id: "thread-1", role: 1, title: "DeepAgent", status: 3 },
    { thread_id: "thread-2", role: 2, title: "Code reviewer", status: 2 },
  ],
};

const commonEvents = [
  event("1", "TURN_STARTED", { message_id: "message-1", parts: [{ type: "text", text: "Create a Codex Desktop style workspace for DeepAgent." }] }),
];

export const visualScenarios = [
  {
    name: "01-empty-task",
    viewport: desktop,
    fixture: { empty: true },
    steps: [
      exists("[data-sidebar]"),
      exists("[data-workspace]"),
      exists("[data-inspector]"),
      text("[data-run-status]", "Idle"),
      style("body", { fontFamily: "-apple-system", fontSize: "13px" }),
    ],
  },
  {
    name: "02-running-tool",
    viewport: desktop,
    fixture: {
      events: [
        ...commonEvents,
        event("2", "ASSISTANT_MESSAGE", { parts: [{ type: "text", text: "I’ll inspect the current WebUI and its runtime contract." }] }),
        event("3", "TOOL_CALL_STARTED", { tool_call_id: "call-1", tool_name: "exec_command", arguments_json: JSON.stringify({ cmd: "go test ./deepagent/..." }) }),
        event("4", "TOOL_CALL_OUTPUT_DELTA", { tool_call_id: "call-1", output_delta: "ok  eino-cli/deepagent/core\n" }),
      ],
    },
    steps: [
      hidden("[data-empty-task]"),
      text("[data-run-status]", "Running"),
      text("[data-activity-timeline]", "exec_command"),
      click(".tool-activity summary"),
      text(".tool-body", "go test ./deepagent/..."),
    ],
  },
  {
    name: "03-plan-waiting",
    viewport: desktop,
    fixture: {
      events: [
        ...commonEvents,
        event("2", "PLAN_UPDATED", {
          explanation: "Implement the workspace in verifiable slices.",
          plan: [
            { step: "Build the shell", status: "completed" },
            { step: "Connect runtime events", status: "in_progress" },
            { step: "Run visual checks", status: "pending" },
          ],
        }),
        event("3", "PLAN_INPUT_REQUIRED", {
          questions: [{ header: "Scope", question: "Which surface should be completed next?", options: [{ label: "Inspector", description: "Finish files and changes." }, { label: "Timeline", description: "Finish activity states." }] }],
        }),
      ],
    },
    steps: [
      text("[data-run-status]", "Waiting Input"),
      text(".pending-card", "Plan input required"),
      check(".plan-input-form input[type='radio']"),
      fill(".plan-input-form textarea", "Complete the Inspector first."),
      click(".plan-input-form button[type='submit']"),
      text(".pending-card.handled", "Handled"),
    ],
  },
  {
    name: "04-approval-waiting",
    viewport: desktop,
    fixture: {
      events: [
        ...commonEvents,
        event("2", "APPROVAL_REQUIRED", { tool_name: "exec_command", arguments_json: JSON.stringify({ cmd: "git commit" }), interrupt_id: "approval-1" }),
      ],
    },
    steps: [
      text("[data-run-status]", "Waiting Approval"),
      text(".pending-card", "Approval required"),
      click("[data-approval='approve']"),
      text(".pending-card.handled", "Handled"),
    ],
  },
  {
    name: "05-files-preview",
    viewport: desktop,
    fixture: { events: finishedEvents() },
    steps: [
      click("[data-inspector-tab='files']"),
      click("[data-file-path='README.md']"),
      text(".file-preview", "DeepAgent workspace"),
    ],
  },
  {
    name: "06-terminal-output",
    viewport: desktop,
    fixture: {
      events: [
        ...commonEvents,
        event("2", "TOOL_CALL_STARTED", { tool_call_id: "call-1", tool_name: "exec_command", arguments_json: JSON.stringify({ cmd: "go test ./..." }) }),
        event("3", "TOOL_CALL_OUTPUT_DELTA", { tool_call_id: "call-1", output_delta: "ok  eino-cli/deepagent\n" }),
        event("4", "TOOL_CALL_FINISHED", { tool_call_id: "call-1", tool_name: "exec_command", status: "completed", result_json: JSON.stringify({ exit_code: 0 }) }),
        event("5", "TURN_FINISHED", {}),
      ],
    },
    steps: [click("[data-inspector-tab='terminal']"), text(".terminal-entry", "go test ./..."), text(".terminal-output", "ok  eino-cli/deepagent")],
  },
  {
    name: "07-changes-diff-comment",
    viewport: desktop,
    fixture: { events: finishedEvents() },
    steps: [
      click("[data-change-path='webui/static/app.js']"),
      click("[data-diff-comment-line='12']"),
      fill("[data-diff-comment-form] textarea", "Keep this transition explicit."),
      click("[data-save-diff-comment]"),
      text(".diff-annotation", "Keep this transition explicit."),
    ],
  },
  {
    name: "08-error-state",
    viewport: desktop,
    fixture: { events: [...commonEvents, event("2", "ERROR", { message: "The model request failed. Check the endpoint and retry." })] },
    steps: [text("[data-run-status]", "Error"), text(".system-activity.error", "The model request failed")],
  },
  {
    name: "09-archived-task",
    viewport: desktop,
    fixture: { events: finishedEvents() },
    steps: [
      click("[data-task-menu]"),
      fill("[data-header-rename-input]", "Codex frontend ready"),
      click("[data-header-rename-save]"),
      text("[data-task-id='task-1']", "Codex frontend ready"),
      click("[data-task-menu]"),
      click("[data-header-archive]"),
      text("[data-sidebar-footer]", "Archived Codex frontend ready"),
      absent("[data-task-id='task-1']"),
    ],
  },
  {
    name: "10-inspector-collapsed-1179x900",
    viewport: { name: "tablet", width: 1179, height: 900 },
    fixture: { events: finishedEvents() },
    steps: [
      attribute("[data-inspector]", "data-collapsed", "true"),
      width("[data-inspector]", 48),
      click("[data-collapse-inspector]"),
      attribute("[data-inspector]", "data-collapsed", "false"),
      press("[data-inspector-tab='changes']", "ArrowRight"),
      ariaTab("files"),
    ],
  },
  {
    name: "11-drawers-899x900",
    viewport: { name: "mobile", width: 899, height: 900 },
    fixture: { events: finishedEvents() },
    steps: [
      attribute("[data-sidebar]", "data-open", "false"),
      attribute("[data-inspector]", "data-collapsed", "true"),
      click("[data-open-sidebar]"),
      attribute("[data-sidebar]", "data-open", "true"),
      press("body", "Escape"),
      attribute("[data-sidebar]", "data-open", "false"),
      click("[data-open-inspector]"),
      attribute("[data-inspector]", "data-collapsed", "false"),
    ],
  },
];

export function visualBaseURL(useMock) {
  return useMock ? mockOrigin : process.env.BASE_URL || "http://127.0.0.1:8080";
}

export async function installMockApp(context, fixture, staticRoot) {
  await context.route(`${mockOrigin}/**`, async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/" || url.pathname === "/index.html") {
      await fulfillFile(route, resolve(staticRoot, "index.html"));
      return;
    }
    if (url.pathname === "/favicon.svg") {
      await fulfillFile(route, resolve(staticRoot, "favicon.svg"));
      return;
    }
    if (url.pathname.startsWith("/static/")) {
      const relativePath = decodeURIComponent(url.pathname.slice("/static/".length));
      const filePath = resolve(staticRoot, relativePath);
      if (!filePath.startsWith(`${resolve(staticRoot)}${sep}`)) {
        await route.fulfill({ status: 403, body: "Forbidden" });
        return;
      }
      await fulfillFile(route, filePath);
      return;
    }
    if (url.pathname === "/userinfo") {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ user_name: "Codex", email: "codex@example.com" }) });
      return;
    }
    if (url.pathname === "/ad/deep_agent_sdk/file") {
      await route.fulfill({ status: 200, contentType: "text/markdown", body: "# DeepAgent workspace\n\nA Codex-style frontend for the DeepAgent runtime.\n" });
      return;
    }
    if (url.pathname.startsWith("/ad/deep_agent_sdk/")) {
      await fulfillAPI(route, url.pathname.split("/").pop(), fixture);
      return;
    }
    await route.fulfill({ status: 404, body: "Not found" });
  });
}

async function fulfillAPI(route, name, fixture) {
  const success = { BaseResp: { StatusCode: 0, StatusMessage: "" } };
  let payload = success;
  switch (name) {
    case "list_projects":
      payload = { ...success, projects: [{ project_name: "LLM", local: true }] };
      break;
    case "list_sessions":
      payload = { ...success, sessions: fixture.empty ? [] : [task] };
      break;
    case "get_session":
      payload = { ...success, session_view: view };
      break;
    case "list_timeline":
      payload = { ...success, events: fixture.events || [], next_cursor: "cursor-1" };
      break;
    case "list_files":
      payload = { ...success, files: [{ path: "README.md", name: "README.md", is_dir: false, media_type: "text/markdown", size: 72 }, { path: "deepagent", name: "deepagent", is_dir: true, media_type: "inode/directory", size: 0 }] };
      break;
    case "list_changes":
      payload = { ...success, changes: [{ path: "webui/static/app.js", status: "modified", additions: 28, deletions: 12 }, { path: "webui/static/styles/components.css", status: "modified", additions: 46, deletions: 8 }] };
      break;
    case "get_diff":
      payload = { ...success, path: "webui/static/app.js", patch: "@@ -10,3 +10,4 @@ function start() {\n const store = createStore();\n-oldStart();\n+startFeatures();\n+renderWorkspace();\n return store;", truncated: false };
      break;
    case "subscribe_timeline":
      await route.fulfill({ status: 200, contentType: "text/event-stream", body: `data: ${JSON.stringify({ ...success, queue_id: "mock-queue" })}\n\n` });
      return;
    case "submit_input":
      payload = { ...success, message: { message_id: "submitted-1", thread_id: "thread-1" } };
      break;
    default:
      payload = success;
      break;
  }
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(payload) });
}

async function fulfillFile(route, filePath) {
  try {
    const body = await readFile(filePath);
    await route.fulfill({ status: 200, contentType: mimeType(filePath), body });
  } catch {
    await route.fulfill({ status: 404, body: "Not found" });
  }
}

function event(id, eventType, payload) {
  return { event_id: id, event_type: eventType, session_id: "task-1", thread_id: "thread-1", turn_id: "run-1", created_at_ms: Number(id) * 1000, payload };
}

function finishedEvents() {
  return [...commonEvents, event("2", "ASSISTANT_MESSAGE", { parts: [{ type: "text", text: "The workspace implementation is ready for review." }] }), event("3", "TURN_FINISHED", {})];
}

function mimeType(filePath) {
  return ({ ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml" })[extname(filePath)] || "application/octet-stream";
}

function exists(selector) { return { kind: "assertExists", selector }; }
function absent(selector) { return { kind: "assertAbsent", selector }; }
function hidden(selector) { return { kind: "assertHidden", selector }; }
function text(selector, value) { return { kind: "assertTextIncludes", selector, value }; }
function attribute(selector, key, value) { return { kind: "assertAttribute", selector, key, value }; }
function style(selector, props) { return { kind: "assertStyle", selector, props }; }
function click(selector) { return { kind: "click", selector }; }
function hover(selector) { return { kind: "hover", selector }; }
function fill(selector, value) { return { kind: "fill", selector, value }; }
function check(selector) { return { kind: "check", selector }; }
function press(selector, key) { return { kind: "press", selector, key }; }
function width(selector, value) { return { kind: "assertWidth", selector, value }; }
function ariaTab(selectedTab) { return { kind: "assertAriaTab", selectedTab }; }
