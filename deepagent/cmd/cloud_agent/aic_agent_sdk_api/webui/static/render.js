import { EventType, currentSession, currentThread, normalizeID } from "./state.js?v=ui-clean-20260614b";
import { buildTimelineModel, pendingResolution } from "./timeline_model.js?v=ui-clean-20260614b";

const statusName = {
  1: "idle",
  2: "ready",
  3: "running",
  4: "blocked",
  5: "closing",
  6: "closed",
};

export function bindElements() {
  return {
    shell: document.querySelector(".shell"),
    serviceState: byID("serviceState"),
    sessionList: byID("sessionList"),
    newProjectBtn: byID("newProjectBtn"),
    sessionTitle: byID("sessionTitle"),
    sessionMeta: byID("sessionMeta"),
    threadStrip: byID("threadStrip"),
    timeline: byID("timeline"),
    pendingBar: byID("pendingBar"),
    messageInput: byID("messageInput"),
    sendButton: byID("sendButton"),
    planButton: byID("planButton"),
    contextRing: byID("contextRing"),
    statusLine: byID("statusLine"),
    runState: byID("runState"),
    userInfo: byID("userInfo"),
    stopBtn: byID("stopBtn"),
    sidebar: byID("sidebar"),
    scrim: byID("scrim"),
    filesPanel: byID("filesPanel"),
    filesMeta: byID("filesMeta"),
    fileTree: byID("fileTree"),
    filePreview: byID("filePreview"),
    toggleFilesBtn: byID("toggleFilesBtn"),
    refreshFilesBtn: byID("refreshFilesBtn"),
  };
}

export function render(app) {
  const model = buildTimelineModel(app.state);
  const view = { ...app, model };
  renderSessions(view);
  renderHeader(view);
  renderThreads(view);
  renderTimeline(view);
  renderComposer(view);
  renderFiles(view);
}

function renderSessions({ state, el }) {
  if (!state.projects.length) {
    el.sessionList.innerHTML = `<div class="nav-empty">Create a project to start.</div>`;
    return;
  }
  const nodes = [];
  for (const project of state.projects) {
    const activeProject = project.project_name === state.selectedProjectName;
    const projectRow = document.createElement("div");
    projectRow.className = `project-row${activeProject ? " active" : ""}`;
    projectRow.innerHTML = `
      <button type="button" class="project-select" data-project-name="${escapeHTML(project.project_name)}">
        <span class="project-title">${escapeHTML(project.project_name)}</span>
        <span class="project-meta">${Number(project.session_count || 0)} chats</span>
      </button>
      <button type="button" class="project-remove" data-project-remove="${escapeHTML(project.project_name)}" title="Remove project" aria-label="Remove project">×</button>
    `;
    nodes.push(projectRow);
    if (!activeProject) continue;
    if (!state.sessions.length) {
      const empty = document.createElement("div");
      empty.className = "project-empty";
      empty.textContent = "No chats in this project.";
      nodes.push(empty);
      continue;
    }
    for (const session of state.sessions) {
      const button = document.createElement("button");
      button.className = "session-row";
      if (normalizeID(session.session_id) === normalizeID(state.selectedSessionID)) button.classList.add("active");
      button.type = "button";
      button.dataset.sessionId = normalizeID(session.session_id);
      button.innerHTML = `
        <span class="session-title">${escapeHTML(session.title || "Untitled chat")}</span>
        <span class="session-preview">${escapeHTML(session.last_message_preview || "")}</span>
      `;
      nodes.push(button);
    }
  }
  el.sessionList.replaceChildren(...nodes);
}

function renderHeader({ state, el, model }) {
  const session = currentSession(state);
  const thread = currentThread(state);
  const runtime = model.runtime;
  const title = session?.title || (state.selectedSessionID ? "Untitled session" : "New session");
  el.sessionTitle.textContent = title;
  el.sessionMeta.textContent = session
    ? `${session.project_name || state.selectedProjectName || "project"} · session ${normalizeID(session.session_id)}${thread ? ` · thread ${normalizeID(thread.thread_id)}` : ""}`
    : state.selectedProjectName
      ? `${state.selectedProjectName} · send a message to create a chat.`
      : "Choose or create a project first.";
  el.runState.textContent = runtime.label;
  el.runState.className = `run-pill ${runtime.className}`;
  renderUserInfo(state, el);
  el.stopBtn.disabled = !session;
}

function renderUserInfo(state, el) {
  const user = state.userInfo;
  if (!user) {
    el.userInfo.classList.add("hidden");
    el.userInfo.replaceChildren();
    return;
  }
  const label = user.email || user.userName || user.employeeNumber || "User";
  const avatar = document.createElement("span");
  avatar.className = "user-avatar";
  avatar.textContent = userInitial(label);
  if (user.avatarURL) {
    const img = document.createElement("img");
    img.alt = "";
    img.addEventListener("load", () => {
      avatar.textContent = "";
      avatar.appendChild(img);
    });
    img.addEventListener("error", () => {
      avatar.textContent = userInitial(label);
    });
    img.src = user.avatarURL;
  }
  const email = document.createElement("span");
  email.className = "user-email";
  email.textContent = label;
  el.userInfo.title = label;
  el.userInfo.classList.remove("hidden");
  el.userInfo.replaceChildren(avatar, email);
}

function userInitial(label) {
  const text = String(label || "").trim();
  return text ? text.slice(0, 1).toUpperCase() : "?";
}

function renderThreads({ state, el }) {
  const threads = (state.sessionView?.threads || []).filter((thread) => Number(thread.status || 0) !== 6);
  if (threads.length <= 1) {
    el.threadStrip.classList.add("hidden");
    el.threadStrip.replaceChildren();
    return;
  }
  el.threadStrip.classList.remove("hidden");
  el.threadStrip.replaceChildren(
    ...threads.map((thread) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "thread-chip";
      if (normalizeID(thread.thread_id) === normalizeID(state.selectedThreadID)) button.classList.add("active");
      button.dataset.threadId = normalizeID(thread.thread_id);
      const role = thread.role === 1 ? "main" : "child";
      const status = statusName[Number(thread.status || 0)] || "";
      button.innerHTML = `
        <span>${escapeHTML(`${role} · ${thread.title || normalizeID(thread.thread_id)}`)}</span>
        ${status ? `<small>${escapeHTML(status)}</small>` : ""}
      `;
      return button;
    })
  );
}

function renderTimeline({ state, el, model }) {
  const shouldScroll = state.forceTimelineScroll || isTimelineNearBottom(el.timeline);
  if (!state.events.length) {
    if (model.runtime.active || state.stopRequested) {
      el.timeline.innerHTML = `
        <div class="empty running-empty">
          <h3>${state.stopRequested ? "Stopping run" : "Waiting for agent"}</h3>
          <p>${state.stopRequested ? "The stop request was sent. Waiting for the thread to settle." : "The message was accepted. Waiting for the first runtime event."}</p>
        </div>`;
      scrollTimelineIfNeeded(el.timeline, state, shouldScroll);
      return;
    }
    el.timeline.innerHTML = `
      <div class="empty">
        <h3>${state.selectedSessionID ? "No events yet" : "Start a conversation"}</h3>
        <p>${state.selectedSessionID ? "Send a message to start this chat." : state.selectedProjectName ? "Type below to create a chat in this project." : "Choose or create a project on the left."}</p>
      </div>`;
    scrollTimelineIfNeeded(el.timeline, state, shouldScroll);
    return;
  }
  const renderState = { ...state };
  const nodes = model.items.map((item) => itemNode(item, renderState)).filter(Boolean);
  el.timeline.replaceChildren(...nodes);
  scrollTimelineIfNeeded(el.timeline, state, shouldScroll);
}

function renderComposer({ state, el, model }) {
  el.planButton.classList.toggle("active", state.planMode);
  const waiting = model.pending;
  el.pendingBar.replaceChildren();
  if (waiting) {
    el.pendingBar.appendChild(pendingActionNode(waiting, state));
  }
  const usage = state.contextUsage;
  const canCompact = Boolean(state.selectedSessionID && currentThread(state));
  el.contextRing.disabled = !canCompact;
  if (!usage) {
    el.contextRing.style.setProperty("--usage", "0deg");
    el.contextRing.title = canCompact ? "Compact context" : "Context usage unavailable";
    el.contextRing.querySelector("span").textContent = "";
  } else {
    const ratio = Number(usage.ratio || (usage.max_tokens ? usage.used_tokens / usage.max_tokens : 0));
    const degrees = Math.max(0, Math.min(1, ratio)) * 360;
    el.contextRing.style.setProperty("--usage", `${degrees}deg`);
    const label = `${Math.round(ratio * 100)}%`;
    el.contextRing.querySelector("span").textContent = label;
    const tokenText = `${usage.used_tokens || 0}${usage.max_tokens ? ` / ${usage.max_tokens}` : ""} tokens`;
    el.contextRing.title = canCompact ? `${tokenText}. Click to compact context.` : tokenText;
  }
}

function itemNode(item, state) {
  switch (item.type) {
    case "user":
      return bubble("user", item.content || "");
    case "assistant":
      return bubble("assistant", item.content || "", item.streaming ? "streaming" : "", state, item.thinkingContent || "");
    case "tool":
      return toolNode(item.tool, state);
    case "approval":
      return approvalNode(item.event, item);
    case "interrupt":
      return interruptNode(item.event, item);
    case "plan":
      return planNode(item.event);
    case "plan_input":
      return planInputNode(item.event, item);
    case "system":
      return systemNode(item.text, item.kind || "");
    case "running":
      return agentRunningNode(item.writing);
    default:
      return null;
  }
}

function isTimelineNearBottom(node) {
  if (!node) return true;
  const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
  return distance < 96;
}

function scrollTimelineIfNeeded(node, state, shouldScroll) {
  if (!node) return;
  if (shouldScroll) node.scrollTop = node.scrollHeight;
  state.forceTimelineScroll = false;
}

function bubble(kind, content, modifier = "", state = null, thinkingContent = "") {
  const node = document.createElement("article");
  node.className = `message ${kind}${modifier ? ` ${modifier}` : ""}`;
  if (kind === "assistant") {
    node.classList.add("markdown");
    node.innerHTML = `${renderThinkingContent(thinkingContent)}${renderMarkdown(content || " ")}${renderAssetGallery(extractAssetPaths(content), state)}`;
  } else {
    node.textContent = content || " ";
  }
  return node;
}

function renderThinkingContent(content) {
  if (!String(content || "").trim()) return "";
  return `
    <details class="thinking-content">
      <summary>Thinking</summary>
      <div>${escapeHTML(content)}</div>
    </details>
  `;
}

function renderFiles({ state, el }) {
  el.shell?.classList.toggle("files-collapsed", Boolean(state.filesCollapsed));
  el.filesPanel?.setAttribute("aria-expanded", state.filesCollapsed ? "false" : "true");
  if (el.toggleFilesBtn) {
    el.toggleFilesBtn.textContent = state.filesCollapsed ? "Files" : "Hide";
    el.toggleFilesBtn.title = state.filesCollapsed ? "Expand files" : "Collapse files";
    el.toggleFilesBtn.setAttribute("aria-label", state.filesCollapsed ? "Expand files" : "Collapse files");
  }
  const session = currentSession(state);
  el.refreshFilesBtn.disabled = !session;
  if (!session) {
    el.filesMeta.textContent = "Select a session";
    el.fileTree.innerHTML = `<div class="files-empty">No project selected.</div>`;
    el.filePreview.classList.add("hidden");
    el.filePreview.replaceChildren();
    return;
  }
  el.filesMeta.textContent = session.project_name || "Project files";
  renderFilePreview(state, el);
  const root = state.fileTree.get(".") || { path: ".", files: [], loading: false, loaded: false, expanded: true };
  if (!root.loaded && !root.loading) {
    el.fileTree.innerHTML = `<div class="files-empty">Loading files...</div>`;
    return;
  }
  if (root.loading) {
    el.fileTree.innerHTML = `<div class="files-empty">Loading files...</div>`;
    return;
  }
  if (!root.files.length) {
    el.fileTree.innerHTML = `<div class="files-empty">No files yet.</div>`;
    return;
  }
  el.fileTree.replaceChildren(...root.files.map((file) => fileNode(file, state, 0)));
}

function renderFilePreview(state, el) {
  const preview = state.filePreview;
  if (!preview) {
    el.filePreview.classList.add("hidden");
    el.filePreview.replaceChildren();
    return;
  }
  el.filePreview.classList.remove("hidden");
  const url = assetURL(state, preview.path);
  const media = preview.media_type || "";
  const title = escapeHTML(preview.name || preview.path);
  if (media.startsWith("image/")) {
    el.filePreview.innerHTML = `<div class="file-preview-title">${title}</div><img src="${escapeHTML(url)}" alt="${title}">`;
    return;
  }
  if (media.startsWith("video/")) {
    el.filePreview.innerHTML = `<div class="file-preview-title">${title}</div><video src="${escapeHTML(url)}" controls></video>`;
    return;
  }
  if (media.startsWith("audio/")) {
    el.filePreview.innerHTML = `<div class="file-preview-title">${title}</div><audio src="${escapeHTML(url)}" controls></audio>`;
    return;
  }
  el.filePreview.innerHTML = `<div class="file-preview-title">${title}</div><a href="${escapeHTML(url)}" target="_blank" rel="noreferrer">Open file</a>`;
}

function fileNode(file, state, depth) {
  const row = document.createElement("div");
  row.className = "file-node-wrap";
  const button = document.createElement("button");
  button.type = "button";
  button.className = `file-node${state.selectedFilePath === file.path ? " active" : ""}${file.is_dir ? " dir" : ""}`;
  button.dataset.filePath = file.path;
  button.dataset.fileDir = file.is_dir ? "true" : "false";
  button.title = file.path || file.name || "";
  button.style.paddingLeft = `${8 + depth * 16}px`;
  button.innerHTML = `
    <span class="file-icon">${fileIcon(file)}</span>
    <span class="file-name">${escapeHTML(file.name)}</span>
    ${file.is_dir ? "" : `<span class="file-size">${escapeHTML(formatBytes(file.size || 0))}</span>`}
  `;
  row.appendChild(button);
  if (file.is_dir) {
    const branch = state.fileTree.get(file.path);
    if (branch?.expanded) {
      const children = document.createElement("div");
      children.className = "file-children";
      if (branch.loading) {
        children.innerHTML = `<div class="files-empty nested">Loading...</div>`;
      } else if (branch.loaded && branch.files.length) {
        children.replaceChildren(...branch.files.map((child) => fileNode(child, state, depth + 1)));
      } else if (branch.loaded) {
        children.innerHTML = `<div class="files-empty nested">Empty</div>`;
      }
      row.appendChild(children);
    }
  }
  return row;
}

function toolNode(tool, state) {
  if ((tool.tool_name || "").toLowerCase() === "exec_command") {
    return execToolNode(tool, state);
  }
  const parsedArgs = parseJSON(tool.arguments_json);
  const node = document.createElement("article");
  node.className = "tool-event";
  const detail = toolDetail(tool);
  const assets = extractAssetsFromTool(tool);
  node.innerHTML = `
    <div class="tool-head">
      <span>${escapeHTML(toolTitle(tool, parsedArgs))}</span>
      <span>${escapeHTML(toolStatus(tool.status))}</span>
    </div>
    ${toolSubtitle(tool, parsedArgs) ? `<div class="tool-meta">${escapeHTML(toolSubtitle(tool, parsedArgs))}</div>` : ""}
    ${renderAssetGallery(assets, state)}
    ${detail ? collapsedDetail(detail, detailTitle(tool)) : ""}
  `;
  return node;
}

function execToolNode(tool, state) {
  const parsedArgs = parseJSON(tool.arguments_json);
  const parsedResult = parseJSON(tool.result_json);
  const command = execCommandText(parsedArgs, tool);
  const rejection = humanRejectedToolResult(tool);
  const output = rejection ? rejection.feedback || rejection.raw : execOutputText(parsedResult, tool);
  const failed = !rejection && (Number(tool.status) === 3 || Boolean(tool.error_message) || Number(parsedResult?.exit_code || 0) !== 0);
  const assets = extractAssetsFromTool(tool);
  const summary = rejection ? "Rejected by human." : execSummary(command, parsedResult, output, assets);
  const node = document.createElement("article");
  node.className = `command-event${failed ? " failed" : ""}${rejection ? " rejected" : ""}`;
  node.innerHTML = `
    <div class="command-line">
      <span class="command-dot"></span>
      <span class="command-text">${escapeHTML(execTitle(command, tool, rejection))}</span>
      <span class="command-status">${escapeHTML(execStatus(tool, parsedResult, rejection))}</span>
    </div>
    ${failed && parsedArgs?.workdir ? `<div class="tool-meta">cwd ${escapeHTML(parsedArgs.workdir)}</div>` : ""}
    ${summary ? `<div class="command-summary">${escapeHTML(summary)}</div>` : ""}
    ${renderAssetGallery(assets, state)}
    ${output ? collapsedDetail(formatExecOutput(output), rejection ? "Human feedback" : failed ? "Error output" : "Command output") : ""}
  `;
  return node;
}

function approvalNode(event, item) {
  const approval = event.approval || {};
  const resolved = item.resolved || null;
  const active = Boolean(item.active);
  const node = document.createElement("article");
  node.className = `pending-card approval-card${resolved || !active ? " resolved" : ""}`;
  node.dataset.pendingCard = "approval";
  node.innerHTML = `
    <div class="pending-head">
      <div>
        <strong>${approvalTitle(resolved, active)}</strong>
        <p>${escapeHTML(approval.tool_name || "tool call")}</p>
      </div>
      <div class="pending-actions">
        ${resolved || !active
          ? `<span class="approval-state">${approvalStateLabel(resolved, active)}</span>`
          : `
            <button type="button" data-approve-event="${escapeHTML(event.__stable_id)}" data-approved="true">Approve</button>
            <button type="button" data-approval-feedback-toggle="${escapeHTML(event.__stable_id)}">Tell agent...</button>
            <button type="button" class="secondary-action" data-approve-event="${escapeHTML(event.__stable_id)}" data-approved="false">Reject</button>
          `}
      </div>
    </div>
    ${!resolved && active ? `
      <label class="approval-reuse">
        <input type="checkbox" data-allow-in-session checked>
        <span>Approve similar commands in this session</span>
      </label>
    ` : ""}
    ${!resolved && active ? `
      <form class="approval-feedback hidden" data-approval-feedback-event="${escapeHTML(event.__stable_id)}">
        <textarea rows="2" placeholder="Tell the agent what to do differently"></textarea>
        <button type="submit">Send instruction</button>
      </form>
    ` : ""}
    ${approval.arguments_json ? `<pre>${escapeHTML(formatApprovalArguments(approval.arguments_json))}</pre>` : ""}
  `;
  return node;
}

function planNode(event) {
  const items = event.plan_updated?.items || [];
  const completed = items.filter((item) => normalizedPlanStatus(item.status) === "completed").length;
  const total = items.length;
  const node = document.createElement("article");
  node.className = "plan-card";
  node.innerHTML = `
    <div class="plan-head">
      <strong>Plan</strong>
      ${total ? `<span>${completed}/${total}</span>` : ""}
    </div>
    ${event.plan_updated?.explanation ? `<p class="plan-note">${escapeHTML(event.plan_updated.explanation)}</p>` : ""}
    <ol class="plan-list">
      ${items.map((item) => planItemHTML(item)).join("")}
    </ol>
  `;
  return node;
}

function planInputNode(event, item) {
  const questions = event.plan_input_required?.questions || [];
  const node = document.createElement("article");
  const resolved = item.resolved || null;
  const active = Boolean(item.active);
  node.className = `pending-card plan-input-card${resolved || !active ? " resolved" : ""}`;
  const questionNodes = questions.map((question, questionIndex) => {
    const name = `plan-${event.__stable_id}-${question.id || questionIndex}`;
    const options = (question.options || []).map((option, optionIndex) => {
      const label = option.label || `Option ${optionIndex + 1}`;
      const description = option.description || "";
      const value = [question.header || "", label, description].filter(Boolean).join(" - ");
      return `
        <label class="plan-option">
          <input type="radio" name="${escapeHTML(name)}" value="${escapeHTML(value)}">
          <span>
            <strong>${escapeHTML(label)}</strong>
            ${description ? `<small>${escapeHTML(description)}</small>` : ""}
          </span>
        </label>
      `;
    }).join("");
    return `
      <div class="plan-question">
        ${question.header ? `<div class="plan-question-header">${escapeHTML(question.header)}</div>` : ""}
        <p>${escapeHTML(question.question || "The agent needs your input.")}</p>
        ${options ? `<div class="plan-options">${options}</div>` : ""}
      </div>
    `;
  }).join("");
  if (resolved || !active) {
    node.innerHTML = `
      <div class="pending-head">
        <div>
          <strong>${resolved?.submitting ? "Submitting answer" : "Plan input answered"}</strong>
          <p>The agent has already continued past this question.</p>
        </div>
        <span class="approval-state">${resolved?.submitting ? "Submitting" : "Answered"}</span>
      </div>
      ${questionNodes || ""}
    `;
    return node;
  }
  node.innerHTML = `
    <form data-plan-input-event="${escapeHTML(event.__stable_id)}">
      <div class="plan-input-topline">
        <span>Asking questions</span>
        <strong>Plan input required</strong>
      </div>
      ${questionNodes || `<p>The agent needs your input.</p>`}
      <textarea class="plan-input-text" rows="3" placeholder="Add your answer"></textarea>
      <div class="pending-actions">
        <button type="submit">Send answer</button>
      </div>
    </form>
  `;
  return node;
}

function interruptNode(event, item) {
  const interrupt = event.interrupt_required || {};
  if ((interrupt.kind || "") !== "follow_up") {
    return systemNode(`Interrupt required: ${interrupt.kind || interrupt.info_type || "custom"}`);
  }
  const info = interrupt.info || {};
  const questions = Array.isArray(info.questions) ? info.questions : [];
  const resolved = item.resolved || null;
  const active = Boolean(item.active);
  const node = document.createElement("article");
  node.className = `pending-card plan-input-card${resolved || !active ? " resolved" : ""}`;
  const questionNodes = questions.map((question, index) => `
    <div class="plan-question">
      <p>${escapeHTML(question || `Question ${index + 1}`)}</p>
    </div>
  `).join("");
  if (resolved || !active) {
    node.innerHTML = `
      <div class="pending-head">
        <div>
          <strong>${resolved?.submitting ? "Submitting answer" : "Follow-up answered"}</strong>
          <p>The agent has already continued past this question.</p>
        </div>
        <span class="approval-state">${resolved?.submitting ? "Submitting" : "Answered"}</span>
      </div>
      ${questionNodes || ""}
    `;
    return node;
  }
  node.innerHTML = `
    <form data-interrupt-event="${escapeHTML(event.__stable_id)}">
      <div class="plan-input-topline">
        <span>Asking questions</span>
        <strong>Follow-up required</strong>
      </div>
      ${questionNodes || `<p>The agent needs more information.</p>`}
      <textarea class="plan-input-text" rows="3" placeholder="Add your answer"></textarea>
      <div class="pending-actions">
        <button type="submit">Send answer</button>
      </div>
    </form>
  `;
  return node;
}

function systemNode(text, kind = "") {
  const node = document.createElement("div");
  node.className = `system-event ${kind}`;
  node.textContent = text;
  return node;
}

function agentRunningNode(writing) {
  const node = document.createElement("div");
  node.className = "agent-running";
  node.innerHTML = `
    <span class="running-dot"></span>
    <span>${writing ? "Agent is writing" : "Agent is running"}</span>
  `;
  return node;
}

function pendingActionNode(event, state) {
  if (event.event_type === EventType.APPROVAL_REQUIRED) {
    return approvalNode(event, { active: true, resolved: pendingResolution(event, state) });
  }
  if (event.event_type === EventType.INTERRUPT_REQUIRED) {
    return interruptNode(event, { active: true, resolved: pendingResolution(event, state) });
  }
  return planInputNode(event, { active: true, resolved: pendingResolution(event, state) });
}

function toolStatus(status) {
  return { 1: "running", 2: "finished", 3: "failed" }[Number(status)] || "running";
}

function toolDetail(tool) {
  const raw = tool.output_delta || tool.result_json || tool.error_message || tool.arguments_json || "";
  if (!raw) return "";
  if (tool.error_message) return tool.error_message;
  try {
    const parsed = JSON.parse(raw);
    if (parsed?.reason) return parsed.reason;
    if (parsed?.errmsg) return parsed.errmsg;
    if (Object.prototype.hasOwnProperty.call(parsed, "data")) return meaningfulData(parsed.data);
    if (parsed?.command || parsed?.workdir || parsed?.output !== undefined) return commandResultDetail(parsed);
    if (parsed?.exit_code !== undefined && parsed?.stderr) return `exit ${parsed.exit_code}\n${parsed.stderr}`;
    if (parsed?.stdout || parsed?.stderr) return [parsed.stdout, parsed.stderr].filter(Boolean).join("\n");
    return JSON.stringify(parsed, null, 2);
  } catch (error) {
    return raw;
  }
}

function detailTitle(tool) {
  if ((tool.tool_name || "").toLowerCase() === "activate_skill") return "Skill instructions";
  return "Details";
}

function collapsedDetail(text, title = "Details") {
  const value = String(text || "").trimEnd();
  if (!value) return "";
  return `
    <details class="tool-details">
      <summary>${escapeHTML(title)}</summary>
      <pre>${escapeHTML(value)}</pre>
    </details>
  `;
}

function execSummary(command, result, output, assets) {
  if (assets.length) return `Saved ${assets.length} asset${assets.length > 1 ? "s" : ""}.`;
  if (!result) return "";
  if (result.denied) return result.reason ? `Denied: ${result.reason}` : "Denied by policy.";
  if (Number(result.exit_code || 0) !== 0) return "";
  const text = String(output || "").trim();
  if (!text) return "";
  const parsedOutput = parseJSON(text);
  if (parsedOutput?.submit_id && parsedOutput?.gen_status) {
    return `Task ${parsedOutput.submit_id}: ${parsedOutput.gen_status}.`;
  }
  if (/^ETCD: use bagent to access ETCD\s*$/m.test(text) && text.split("\n").length <= 2) {
    return "";
  }
  if (command.startsWith("file ")) return "Verified file type.";
  if (command.startsWith("ls ")) return "Listed files.";
  return "";
}

function meaningfulData(data) {
  if (data === null || data === undefined) return "";
  if (typeof data === "string") return data.trim();
  if (Array.isArray(data) && data.length === 0) return "";
  if (typeof data === "object" && Object.keys(data).length === 0) return "";
  return JSON.stringify(data, null, 2);
}

function toolTitle(tool, args) {
  if ((tool.tool_name || "").toLowerCase() === "activate_skill") {
    return `Activated ${args?.name || "skill"}`;
  }
  const name = toolDisplayName(tool.tool_name || "tool");
  const target = toolTarget(args);
  return target ? `${name} ${target}` : name;
}

function toolSubtitle(tool, args) {
  if (tool.error_message) return tool.error_message;
  if (args?.path && tool.tool_name !== "ls") return args.path;
  return "";
}

function toolTarget(args) {
  if (!args) return "";
  return args.path || args.pattern || args.query || "";
}

function execTitle(command, tool, rejection = null) {
  if (rejection) return command ? `Rejected ${command}` : "Rejected command";
  if (command) return `${Number(tool.status) === 1 ? "Running" : "Ran"} ${command}`;
  return Number(tool.status) === 1 ? "Running command" : "Ran command";
}

function execStatus(tool, result, rejection = null) {
  if (rejection) return "rejected";
  if (result?.denied) return "denied";
  if (tool.error_message) return "failed";
  if (Number(tool.status) === 1) return "running";
  if (Number(tool.status) === 3) return "failed";
  if (result?.exit_code !== undefined) return Number(result.exit_code) === 0 ? "ok" : `exit ${result.exit_code}`;
  return toolStatus(tool.status);
}

function execCommandText(args, tool) {
  if (Array.isArray(args?.command)) {
    if (args.command.length >= 3 && args.command[0].endsWith("bash") && args.command[1] === "-lc") {
      return args.command.slice(2).join(" ");
    }
    return args.command.join(" ");
  }
  return args?.command || args?.cmd || tool.command || "";
}

function execOutputText(result, tool) {
  if (tool.error_message) return tool.error_message;
  if (tool.output_delta) return tool.output_delta;
  if (!result) return tool.result_json || "";
  if (result.denied) return `Denied by policy: ${result.reason || "command denied"}`;
  const output = result.output || [result.stdout, result.stderr].filter(Boolean).join("\n");
  if (output) return output;
  if (result.reason || result.errmsg) return result.reason || result.errmsg;
  if (Number(result.exit_code || 0) !== 0) return JSON.stringify(result, null, 2);
  return "";
}

function humanRejectedToolResult(tool) {
  const raw = String(tool?.result_json || "").trim();
  if (!raw.startsWith("Human rejected tool ")) return null;
  const feedbackMatch = raw.match(/Human feedback:\s*(.*?)\.\s*Treat this feedback/s);
  return {
    raw,
    feedback: feedbackMatch?.[1]?.trim() || "",
  };
}

function formatExecOutput(output) {
  const text = String(output || "").trimEnd();
  if (!text) return "";
  return text.split("\n").map((line, index) => `${index === 0 ? "└ " : "  "}${line}`).join("\n");
}

function formatApprovalArguments(raw) {
  const parsed = parseJSON(raw);
  if (!parsed) return raw;
  if (parsed.cmd || parsed.command) return execCommandText(parsed, {});
  return JSON.stringify(parsed, null, 2);
}

function approvalTitle(resolved, active) {
  if (resolved?.submitting) return "Submitting approval";
  if (resolved) return resolved.approved ? "Approval submitted" : "Rejection submitted";
  return active ? "Approval required" : "Approval handled";
}

function approvalStateLabel(resolved, active) {
  if (resolved?.submitting) return "Submitting";
  if (resolved) return resolved.approved ? "Approved" : "Rejected";
  return active ? "Pending" : "Handled";
}

function parseJSON(raw) {
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch (error) {
    return null;
  }
}

function toolDisplayName(name) {
  switch (name) {
    case "ls":
      return "ls";
    case "read_file":
      return "Read file";
    case "write_file":
      return "Write file";
    case "edit_file":
      return "Edit file";
    case "apply_patch":
      return "Apply patch";
    case "grep":
      return "Search";
    case "glob":
      return "Find files";
    case "close_task":
      return "Close task";
    case "spawn_task":
      return "Spawn task";
    case "send_message":
      return "Send message";
    case "wait_message":
      return "Wait for task";
    default:
      return name;
  }
}

function planItemHTML(item) {
  const status = normalizedPlanStatus(item.status);
  return `
    <li class="plan-item ${escapeHTML(status)}">
      <span class="plan-marker">${planMarker(status)}</span>
      <span class="plan-text">${escapeHTML(item.content || "")}</span>
      <span class="plan-status">${escapeHTML(planStatusLabel(status))}</span>
    </li>
  `;
}

function normalizedPlanStatus(status) {
  switch (String(status || "").toLowerCase()) {
    case "completed":
      return "completed";
    case "in_progress":
    case "in-progress":
    case "running":
      return "in-progress";
    default:
      return "pending";
  }
}

function planMarker(status) {
  if (status === "completed") return "✓";
  if (status === "in-progress") return "…";
  return "";
}

function planStatusLabel(status) {
  if (status === "completed") return "done";
  if (status === "in-progress") return "doing";
  return "todo";
}

function commandResultDetail(result) {
  const lines = [];
  if (Array.isArray(result.command) && result.command.length) {
    lines.push(`$ ${result.command.join(" ")}`);
  }
  if (result.workdir) lines.push(`cwd: ${result.workdir}`);
  if (result.exit_code !== undefined) lines.push(`exit: ${result.exit_code}`);
  const output = result.output || result.stdout || result.stderr || "";
  if (output) lines.push("", output.trimEnd());
  return lines.join("\n");
}

function byID(id) {
  return document.getElementById(id);
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function renderMarkdown(content) {
  const lines = String(content ?? "").replace(/\r\n/g, "\n").split("\n");
  const blocks = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) {
      i++;
      continue;
    }

    const fence = line.match(/^```(\w+)?\s*$/);
    if (fence) {
      const code = [];
      i++;
      while (i < lines.length && !/^```\s*$/.test(lines[i])) {
        code.push(lines[i]);
        i++;
      }
      if (i < lines.length) i++;
      blocks.push(`<pre><code>${escapeHTML(code.join("\n"))}</code></pre>`);
      continue;
    }

    const heading = line.match(/^(#{1,6})\s+(.+)$/);
    if (heading) {
      const level = Math.min(4, heading[1].length + 1);
      blocks.push(`<h${level}>${renderInlineMarkdown(heading[2])}</h${level}>`);
      i++;
      continue;
    }

    if (/^>\s?/.test(line)) {
      const quote = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) {
        quote.push(lines[i].replace(/^>\s?/, ""));
        i++;
      }
      blocks.push(`<blockquote>${renderInlineMarkdown(quote.join("\n")).replace(/\n/g, "<br>")}</blockquote>`);
      continue;
    }

    if (/^\s*[-*+]\s+/.test(line)) {
      const items = [];
      while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*[-*+]\s+/, ""));
        i++;
      }
      blocks.push(`<ul>${items.map((item) => `<li>${renderInlineMarkdown(item)}</li>`).join("")}</ul>`);
      continue;
    }

    if (/^\s*\d+[.)]\s+/.test(line)) {
      const items = [];
      while (i < lines.length && /^\s*\d+[.)]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*\d+[.)]\s+/, ""));
        i++;
      }
      blocks.push(`<ol>${items.map((item) => `<li>${renderInlineMarkdown(item)}</li>`).join("")}</ol>`);
      continue;
    }

    const paragraph = [];
    while (i < lines.length && lines[i].trim() && !isMarkdownBlockStart(lines[i])) {
      paragraph.push(lines[i]);
      i++;
    }
    const text = paragraph.join("\n");
    const rendered = renderInlineMarkdown(text).replace(/\n/g, "<br>");
    blocks.push(`<p>${rendered}</p>`);
  }
  return blocks.join("");
}

function isMarkdownBlockStart(line) {
  return /^```/.test(line) || /^(#{1,6})\s+/.test(line) || /^>\s?/.test(line) || /^\s*[-*+]\s+/.test(line) || /^\s*\d+[.)]\s+/.test(line);
}

function renderInlineMarkdown(value) {
  return String(value ?? "").split(/(`[^`]*`)/g).map((part) => {
    if (part.startsWith("`") && part.endsWith("`")) {
      return `<code>${escapeHTML(part.slice(1, -1))}</code>`;
    }
    return escapeHTML(part)
      .replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>')
      .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
      .replace(/(^|[^*])\*([^*\n]+)\*/g, "$1<em>$2</em>");
  }).join("");
}

function extractAssetsFromTool(tool) {
  const result = parseJSON(tool.result_json);
  const failed = Number(tool.status) === 3 || Boolean(tool.error_message) || Number(result?.exit_code || 0) !== 0;
  if (failed) return [];
  const paths = [];
  collectAssetPaths(result, paths);
  collectAssetPaths(tool.output_delta, paths);
  return uniqueAssetPaths(paths);
}

function collectAssetPaths(value, out) {
  if (value === null || value === undefined) return;
  if (typeof value === "string") {
    out.push(...extractAssetPaths(value));
    const parsed = parseJSON(value.trim());
    if (parsed && parsed !== value) collectAssetPaths(parsed, out);
    return;
  }
  if (Array.isArray(value)) {
    for (const item of value) collectAssetPaths(item, out);
    return;
  }
  if (typeof value === "object") {
    for (const item of Object.values(value)) collectAssetPaths(item, out);
  }
}

function uniqueAssetPaths(paths) {
  const seen = new Set();
  const out = [];
  for (const path of paths || []) {
    if (!path || seen.has(path)) continue;
    seen.add(path);
    out.push(path);
  }
  return out;
}

function renderAssetGallery(paths, state) {
  if (!state?.selectedSessionID) return "";
  const seen = new Set();
  const nodes = [];
  for (const path of paths || []) {
    if (seen.has(path)) continue;
    if (state.renderedAssetPaths?.has(path)) continue;
    seen.add(path);
    state.renderedAssetPaths?.add(path);
    const url = assetURL(state, path);
    const lower = path.toLowerCase();
    if (/\.(png|jpe?g|webp|gif|bmp|svg)$/.test(lower)) {
      nodes.push(`
        <figure class="asset-preview">
          <img src="${escapeHTML(url)}" alt="${escapeHTML(path)}" loading="lazy" decoding="async">
          <figcaption>${escapeHTML(path)}</figcaption>
        </figure>
      `);
    } else if (/\.(mp4|webm|mov|m4v)$/.test(lower)) {
      nodes.push(`
        <figure class="asset-preview">
          <video src="${escapeHTML(url)}" controls></video>
          <figcaption>${escapeHTML(path)}</figcaption>
        </figure>
      `);
    } else if (/\.(mp3|wav|m4a|aac|ogg)$/.test(lower)) {
      nodes.push(`
        <figure class="asset-preview">
          <audio src="${escapeHTML(url)}" controls></audio>
          <figcaption>${escapeHTML(path)}</figcaption>
        </figure>
      `);
    }
  }
  if (!nodes.length) return "";
  return `<div class="asset-gallery">${nodes.join("")}</div>`;
}

function extractAssetPaths(text) {
  const matches = String(text || "").matchAll(/(?:^|[\s("'`])((?:\.\/)?assets\/[^\s)"'`<>]+\.(?:png|jpe?g|webp|gif|bmp|svg|mp4|webm|mov|m4v|mp3|wav|m4a|aac|ogg))/gi);
  return [...matches]
    .map((match) => match[1].replace(/^\.\//, "").replace(/[.,;:!?]+$/, ""))
    .filter((path) => !/[*?\[\]{}]/.test(path));
}

function assetURL(state, filePath) {
  const params = new URLSearchParams({
    session_id: normalizeID(state.selectedSessionID),
    path: filePath,
  });
  return `/ad/aic_agent_sdk/file?${params.toString()}`;
}

function fileIcon(file) {
  if (file.is_dir) return "▸";
  const media = file.media_type || "";
  if (media.startsWith("image/")) return "▧";
  if (media.startsWith("video/")) return "▶";
  if (media.startsWith("audio/")) return "♪";
  return "•";
}

function formatBytes(size) {
  const n = Number(size || 0);
  if (!n) return "";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
