import { api } from "./api.js?v=ui-clean-20260614b";
import { bindElements, render } from "./render.js?v=ui-clean-20260614b";
import { EventType, InputMode, appendLocalUserMessage, createState, currentSession, currentThread, mergeEvents, normalizeID, resetTimeline } from "./state.js?v=ui-clean-20260614b";
import { createStreamController } from "./stream_controller.js?v=ui-clean-20260614b";
import { bindTap, createSubmitGate } from "./submit_gate.js?v=ui-clean-20260614b";

const state = createState();
const el = bindElements();
const app = { state, el };
const submitGate = createSubmitGate();
const streamEventQueue = [];
const POLL_TIMEOUT_MS = 10000;
let streamFlushScheduled = false;
let streamFlushing = false;
const pollInFlight = new Map();
const stream = createStreamController({
  loadQueue: loadStreamQueue,
  saveQueue: saveStreamQueue,
  clearQueue: clearStreamQueue,
  openStream: api.subscribeTimeline,
  onStatus: setStatus,
  onEvent: (event) => {
    enqueueStreamEvent(event);
  },
});

boot();

async function boot() {
  wireEvents();
  loadUserInfo();
  await refreshProjects();
}

async function loadUserInfo() {
  state.userInfo = await api.getUserInfo();
  render(app);
}

function wireEvents() {
  byID("newSessionBtn").addEventListener("click", () => newSession());
  el.newProjectBtn.addEventListener("click", () => newProject());
  byID("refreshSessionsBtn").addEventListener("click", () => refreshSessions());
  el.toggleFilesBtn.addEventListener("click", () => {
    state.filesCollapsed = !state.filesCollapsed;
    el.shell?.classList.toggle("files-collapsed", state.filesCollapsed);
    el.filesPanel?.setAttribute("aria-expanded", state.filesCollapsed ? "false" : "true");
    el.toggleFilesBtn.textContent = state.filesCollapsed ? "Files" : "Hide";
    el.toggleFilesBtn.title = state.filesCollapsed ? "Expand files" : "Collapse files";
    el.toggleFilesBtn.setAttribute("aria-label", state.filesCollapsed ? "Expand files" : "Collapse files");
    render(app);
  });
  el.refreshFilesBtn.addEventListener("click", () => refreshFileTree());
  byID("openNavBtn").addEventListener("click", () => toggleNav(true));
  byID("closeNavBtn").addEventListener("click", () => toggleNav(false));
  byID("scrim").addEventListener("click", () => toggleNav(false));
  el.sessionList.addEventListener("click", (event) => {
    const remove = event.target.closest("[data-project-remove]");
    if (remove) {
      event.preventDefault();
      event.stopPropagation();
      removeProject(remove.dataset.projectRemove);
      return;
    }
    const project = event.target.closest("[data-project-name]");
    if (project) {
      selectProject(project.dataset.projectName);
      return;
    }
    const row = event.target.closest("[data-session-id]");
    if (row) selectSession(row.dataset.sessionId);
  });
  el.threadStrip.addEventListener("click", (event) => {
    const row = event.target.closest("[data-thread-id]");
    if (row) selectThread(row.dataset.threadId);
  });
  el.fileTree.addEventListener("click", (event) => {
    const row = event.target.closest("[data-file-path]");
    if (row) selectFile(row.dataset.filePath, row.dataset.fileDir === "true");
  });
  bindTap(el.sendButton, () => sendMessage());
  el.contextRing.addEventListener("click", () => compactContext());
  el.stopBtn.addEventListener("click", () => stopRunning());
  el.planButton.addEventListener("click", () => {
    state.planMode = !state.planMode;
    render(app);
  });
  const handlePendingClick = (event) => {
    const feedbackButton = event.target.closest("[data-approval-feedback-toggle]");
    if (feedbackButton) {
      const card = feedbackButton.closest("[data-pending-card]");
      const form = card?.querySelector("[data-approval-feedback-event]");
      form?.classList.toggle("hidden");
      form?.querySelector("textarea")?.focus();
      return;
    }
    const button = event.target.closest("[data-approve-event]");
    if (button) submitApproval(button.dataset.approveEvent, button.dataset.approved === "true", button.closest("[data-pending-card]"));
  };
  const handlePendingSubmit = (event) => {
    const approvalForm = event.target.closest("[data-approval-feedback-event]");
    if (approvalForm) {
      event.preventDefault();
      submitApprovalFeedback(approvalForm);
      return;
    }
    const interruptForm = event.target.closest("[data-interrupt-event]");
    if (interruptForm) {
      event.preventDefault();
      submitInterruptInput(interruptForm);
      return;
    }
    const form = event.target.closest("[data-plan-input-event]");
    if (!form) return;
    event.preventDefault();
    submitPlanInput(form);
  };
  el.timeline.addEventListener("click", handlePendingClick);
  el.pendingBar.addEventListener("click", handlePendingClick);
  el.timeline.addEventListener("submit", handlePendingSubmit);
  el.pendingBar.addEventListener("submit", handlePendingSubmit);
  el.messageInput.addEventListener("keydown", (event) => {
    if (event.isComposing) return;
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      sendMessage();
    }
  });
  el.messageInput.addEventListener("input", resizeComposer);
}

async function refreshProjects() {
  try {
    el.serviceState.textContent = "Online";
    const resp = await api.listProjects();
    const remoteProjects = resp.projects || [];
    state.projects = mergeLocalProjects(remoteProjects);
    if (state.selectedProjectName && !state.projects.some((project) => project.project_name === state.selectedProjectName)) {
      state.selectedProjectName = "";
    }
    if (!state.selectedProjectName) {
      state.selectedProjectName = state.projects[0]?.project_name || "";
    }
    if (!state.selectedProjectName) {
      addLocalProject("default");
      state.projects = mergeLocalProjects([]);
      state.selectedProjectName = "default";
    }
    await refreshSessions();
  } catch (error) {
    el.serviceState.textContent = "Offline";
    setStatus(error.message, true);
    render(app);
  }
}

async function refreshSessions() {
  if (!state.selectedProjectName) {
    state.sessions = [];
    render(app);
    return;
  }
  const projectName = state.selectedProjectName;
  try {
    el.serviceState.textContent = "Online";
    const resp = await api.listSessions(projectName);
    if (projectName !== state.selectedProjectName) return;
    state.sessions = resp.sessions || [];
    if (!state.selectedSessionID && state.sessions.length) {
      await selectSession(normalizeID(state.sessions[0].session_id), { silent: true });
      return;
    }
    render(app);
  } catch (error) {
    el.serviceState.textContent = "Offline";
    setStatus(error.message, true);
    render(app);
  }
}

async function selectProject(projectName) {
  if (!projectName || projectName === state.selectedProjectName) return;
  stopPolling();
  stopStream();
  state.selectedProjectName = projectName;
  state.selectedSessionID = "";
  state.selectedThreadID = "";
  state.sessionView = null;
  resetFiles();
  resetTimeline(state);
  await refreshSessions();
  toggleNav(false);
}

async function newProject() {
  const raw = window.prompt("Project name");
  if (raw === null) return;
  const name = raw.trim();
  if (!name) return;
  if (name.includes("/") || name.includes("\\") || name === "." || name === "..") {
    setStatus("Project name must be a single path segment.", true);
    return;
  }
  addLocalProject(name);
  state.projects = mergeLocalProjects(state.projects);
  await selectProject(name);
}

async function removeProject(projectName) {
  if (!projectName) return;
  const project = state.projects.find((item) => item.project_name === projectName);
  const isSelected = projectName === state.selectedProjectName;
  if (isLocalOnlyProject(project)) {
    removeLocalProject(projectName);
    state.projects = mergeLocalProjects(state.projects.filter((item) => item.project_name !== projectName));
    if (isSelected) resetProjectSelection();
    await refreshProjects();
    return;
  }
  const ok = window.confirm(`Remove project "${projectName}" from the sidebar?`);
  if (!ok) return;
  stopPolling();
  stopStream();
  setStatus("Removing project...");
  try {
    await api.closeProject(projectName);
    removeLocalProject(projectName);
    if (isSelected) resetProjectSelection();
    await refreshProjects();
    setStatus("Project removed.");
  } catch (error) {
    setStatus(error.message, true);
    render(app);
  }
}

function resetProjectSelection() {
  state.selectedProjectName = "";
  state.selectedSessionID = "";
  state.selectedThreadID = "";
  state.sessionView = null;
  resetFiles();
  resetTimeline(state);
}

async function newSession() {
  if (!state.selectedProjectName) {
    newProject();
    return;
  }
  stopPolling();
  stopStream();
  state.selectedSessionID = "";
  state.selectedThreadID = "";
  state.sessionView = null;
  resetFiles();
  resetTimeline(state);
  render(app);
  toggleNav(false);
  el.messageInput.focus();
}

async function selectSession(sessionID, options = {}) {
  stopPolling();
  stopStream();
  const selectedSessionID = normalizeID(sessionID);
  state.selectedSessionID = selectedSessionID;
  state.selectedThreadID = "";
  resetFiles();
  resetTimeline(state);
  if (!options.silent) setStatus("Loading session...");
  try {
    const resp = await api.getSession(selectedSessionID);
    if (selectedSessionID !== normalizeID(state.selectedSessionID)) return;
    state.sessionView = resp.session_view || null;
    const thread = currentThread(state);
    state.selectedThreadID = normalizeID(thread?.thread_id);
    await loadTimeline();
    await refreshFileTree();
    startStream();
    startPolling();
    setStatus("");
    toggleNav(false);
  } catch (error) {
    setStatus(error.message, true);
  }
  render(app);
}

async function refreshCurrentSessionView() {
  const sessionID = state.selectedSessionID;
  if (!sessionID) return;
  const resp = await api.getSession(sessionID);
  if (normalizeID(sessionID) !== normalizeID(state.selectedSessionID)) return;
  state.sessionView = resp.session_view || state.sessionView;
  const thread = currentThread(state);
  state.selectedThreadID = normalizeID(thread?.thread_id);
}

async function selectThread(threadID) {
  stopPolling();
  stopStream();
  state.selectedThreadID = normalizeID(threadID);
  resetTimeline(state);
  await loadTimeline();
  startStream();
  startPolling();
  render(app);
}

async function loadTimeline() {
  if (!state.selectedSessionID) return;
  const resp = await api.listTimeline({
    sessionID: state.selectedSessionID,
    threadID: state.selectedThreadID,
    limit: 120,
    backward: true,
  });
  const events = resp.events || [];
  ingestTimelineEvents(events, resp.page_info, { replaceCursor: true });
}

async function pollTimeline() {
  const sessionID = state.selectedSessionID;
  if (!sessionID) return;
  const threadID = state.selectedThreadID;
  const cursor = state.nextCursor;
  const pollKey = `${normalizeID(sessionID)}:${normalizeID(threadID)}`;
  if (pollInFlight.has(pollKey)) return;
  const controller = new AbortController();
  const timeoutID = window.setTimeout(() => controller.abort(), POLL_TIMEOUT_MS);
  pollInFlight.set(pollKey, controller);
  try {
    const resp = await api.listTimeline({
      sessionID,
      threadID,
      cursor,
      limit: 100,
      signal: controller.signal,
    });
    if (sessionID !== state.selectedSessionID || threadID !== state.selectedThreadID) return;
    const events = resp.events || [];
    const changed = ingestTimelineEvents(events, resp.page_info);
    if (changed) {
      await refreshCurrentSessionView();
      refreshFileTree({ preserveExpanded: true }).catch(() => {});
      render(app);
    }
  } catch (error) {
    if (error.name === "AbortError") return;
    setStatus(error.message, true);
  } finally {
    window.clearTimeout(timeoutID);
    if (pollInFlight.get(pollKey) === controller) pollInFlight.delete(pollKey);
  }
}

async function refreshFileTree(options = {}) {
  if (!state.selectedSessionID) {
    state.fileTree = new Map();
    state.filePreview = null;
    render(app);
    return;
  }
  if (!options.preserveExpanded) {
    state.fileTree = new Map();
    state.selectedFilePath = "";
    state.filePreview = null;
  }
  await loadFiles(".");
  render(app);
}

async function selectFile(filePath, isDir) {
  if (!filePath) return;
  if (isDir) {
    const current = state.fileTree.get(filePath) || { path: filePath, files: [], loaded: false, loading: false, expanded: false };
    current.expanded = !current.expanded;
    state.fileTree.set(filePath, current);
    render(app);
    if (current.expanded && !current.loaded) {
      await loadFiles(filePath);
      render(app);
    }
    return;
  }
  const file = findFileInfo(filePath);
  state.selectedFilePath = filePath;
  state.filePreview = file || { path: filePath, name: filePath };
  render(app);
}

async function loadFiles(filePath) {
  const path = normalizeFilePath(filePath);
  const existing = state.fileTree.get(path) || { path, files: [], loaded: false, loading: false, expanded: true };
  existing.loading = true;
  if (path === ".") existing.expanded = true;
  state.fileTree.set(path, existing);
  render(app);
  const resp = await api.listFiles({ sessionID: state.selectedSessionID, path });
  state.fileTree.set(path, {
    path,
    files: resp.files || [],
    loaded: true,
    loading: false,
    expanded: existing.expanded,
  });
  return resp.files || [];
}

function findFileInfo(filePath) {
  for (const branch of state.fileTree.values()) {
    const found = (branch.files || []).find((file) => file.path === filePath);
    if (found) return found;
  }
  return null;
}

function normalizeFilePath(path) {
  const value = String(path || ".").replaceAll("\\", "/").replace(/^\/+/, "");
  return value === "" ? "." : value;
}

function resetFiles() {
  state.fileTree = new Map();
  state.selectedFilePath = "";
  state.filePreview = null;
}

async function sendMessage() {
  const content = el.messageInput.value.trim();
  if (!content) return;
  await submitGate.run("message", async () => {
    el.sendButton.disabled = true;
    try {
      let sessionID = state.selectedSessionID;
      if (!sessionID) {
        const created = await api.createSession({ title: content.slice(0, 80), projectName: state.selectedProjectName });
        const session = created.session_view?.session;
        sessionID = normalizeID(session?.session_id);
        state.selectedSessionID = sessionID;
        state.sessionView = created.session_view || null;
        await refreshProjects();
        state.selectedSessionID = sessionID;
      }
      const thread = currentThread(state);
      const resp = await api.submitInput({
        sessionID,
        threadID: normalizeID(thread?.thread_id),
        content,
        mode: state.planMode ? InputMode.IMPL_PLAN : undefined,
      });
      state.sessionView = resp.session_view || state.sessionView;
      state.selectedThreadID = normalizeID(resp.message?.thread_id || state.selectedThreadID || currentThread(state)?.thread_id);
      el.messageInput.value = "";
      resizeComposer();
      appendLocalUserMessage(state, resp.message, content);
      state.running = true;
      state.stopRequested = false;
      state.planMode = false;
      state.forceTimelineScroll = true;
      setStatus("");
      render(app);
      await loadTimeline();
      refreshFileTree().catch((error) => setStatus(error.message, true));
      startStream();
      startPolling(true);
      await refreshSessions();
    } catch (error) {
      setStatus(error.message, true);
    } finally {
      el.sendButton.disabled = false;
      render(app);
    }
  });
}

async function compactContext() {
  const sessionID = state.selectedSessionID;
  const thread = currentThread(state);
  const threadID = normalizeID(thread?.thread_id);
  if (!sessionID || !threadID) {
    setStatus("Choose a running thread before compacting context.", true);
    return;
  }

  el.contextRing.disabled = true;
  try {
    await api.submitInput({
      sessionID,
      threadID,
      mode: InputMode.COMPACT_CONTEXT,
    });
    setStatus("Compacting context...");
    startPolling(true);
  } catch (error) {
    setStatus(error.message, true);
  } finally {
    el.contextRing.disabled = false;
    render(app);
  }
}

function mergeLocalProjects(remoteProjects) {
  const merged = new Map();
  for (const project of remoteProjects || []) {
    if (project?.project_name) merged.set(project.project_name, project);
  }
  for (const name of loadLocalProjects()) {
    if (!merged.has(name)) {
      merged.set(name, { project_name: name, project_path: "", session_count: 0 });
    }
  }
  return [...merged.values()].sort((a, b) => {
    const at = Number(a.last_active_at_ms || 0);
    const bt = Number(b.last_active_at_ms || 0);
    if (at !== bt) return bt - at;
    return String(a.project_name).localeCompare(String(b.project_name));
  });
}

function addLocalProject(projectName) {
  const projects = new Set(loadLocalProjects());
  projects.add(projectName);
  window.localStorage.setItem("aic_agent_sdk.projects", JSON.stringify([...projects]));
}

function removeLocalProject(projectName) {
  const projects = new Set(loadLocalProjects());
  projects.delete(projectName);
  window.localStorage.setItem("aic_agent_sdk.projects", JSON.stringify([...projects]));
}

function isLocalOnlyProject(project) {
  return Boolean(project) && Number(project.session_count || 0) === 0 && !project.project_path;
}

function loadLocalProjects() {
  try {
    const parsed = JSON.parse(window.localStorage.getItem("aic_agent_sdk.projects") || "[]");
    return Array.isArray(parsed) ? parsed.filter(Boolean).map(String) : [];
  } catch (error) {
    return [];
  }
}

async function submitApproval(eventID, approved, card, reason = "") {
  const event = state.events.find((item) => item.__stable_id === eventID);
  if (!event?.approval) return;
  await submitGate.run(`approval:${eventID}`, async () => {
    const allowInSession = approved && Boolean(card?.querySelector("[data-allow-in-session]")?.checked);
    state.resolvedPendingInputs.set(eventID, { kind: "approval", approved, submitting: true });
    render(app);
    try {
      await api.submitInput({
        sessionID: state.selectedSessionID,
        threadID: event.thread_id,
        resumeRef: {
          turn_id: event.turn_id || "",
          checkpoint_id: event.approval.checkpoint_id || "",
          interrupt_id: event.approval.interrupt_id || "",
        },
        approval: {
          approved,
          reason,
          allow_in_session: allowInSession,
          tool_name: event.approval.tool_name || "",
          arguments_json: event.approval.arguments_json || "",
        },
      });
      state.resolvedPendingInputs.set(eventID, { kind: "approval", approved });
      state.running = true;
      state.stopRequested = false;
      state.forceTimelineScroll = true;
      startStream();
      startPolling(true);
      setStatus("");
      render(app);
    } catch (error) {
      state.resolvedPendingInputs.delete(eventID);
      setStatus(error.message, true);
      render(app);
    }
  });
}

async function submitApprovalFeedback(form) {
  const eventID = form.dataset.approvalFeedbackEvent;
  const reason = form.querySelector("textarea")?.value.trim() || "";
  if (!reason) {
    setStatus("Tell the agent what to do differently.", true);
    return;
  }
  const submitButton = form.querySelector("button[type='submit']");
  if (submitButton) submitButton.disabled = true;
  try {
    await submitApproval(eventID, false, form.closest("[data-pending-card]"), reason);
  } finally {
    if (submitButton) submitButton.disabled = false;
  }
}

async function submitPlanInput(form) {
  const eventID = form.dataset.planInputEvent;
  const event = state.events.find((item) => item.__stable_id === eventID);
  if (!event?.plan_input_required) return;
  const content = planInputContent(form);
  if (!content) {
    setStatus("Plan input cannot be empty.", true);
    return;
  }
  await submitGate.run(`plan:${eventID}`, async () => {
    const submitButton = form.querySelector("button[type='submit']");
    if (submitButton) submitButton.disabled = true;
    state.resolvedPendingInputs.set(eventID, { kind: "plan_input", submitting: true });
    render(app);
    try {
      await api.submitInput({
        sessionID: state.selectedSessionID,
        threadID: event.thread_id,
        content,
        resumeRef: {
          turn_id: event.turn_id || "",
          checkpoint_id: event.plan_input_required.checkpoint_id || "",
          interrupt_id: event.plan_input_required.interrupt_id || "",
        },
      });
      state.resolvedPendingInputs.set(eventID, { kind: "plan_input" });
      state.running = true;
      state.stopRequested = false;
      state.forceTimelineScroll = true;
      startStream();
      startPolling(true);
      setStatus("");
      render(app);
    } catch (error) {
      state.resolvedPendingInputs.delete(eventID);
      setStatus(error.message, true);
      render(app);
    } finally {
      if (submitButton) submitButton.disabled = false;
    }
  });
}

async function submitInterruptInput(form) {
  const eventID = form.dataset.interruptEvent;
  const event = state.events.find((item) => item.__stable_id === eventID);
  if (!event?.interrupt_required) return;
  const answer = form.querySelector("textarea")?.value.trim() || "";
  if (!answer) {
    setStatus("Follow-up answer cannot be empty.", true);
    return;
  }
  await submitGate.run(`interrupt:${eventID}`, async () => {
    const submitButton = form.querySelector("button[type='submit']");
    if (submitButton) submitButton.disabled = true;
    state.resolvedPendingInputs.set(eventID, { kind: "interrupt", submitting: true });
    render(app);
    try {
      await api.submitInput({
        sessionID: state.selectedSessionID,
        threadID: event.thread_id,
        content: answer,
        resumeRef: {
          turn_id: event.turn_id || "",
          checkpoint_id: event.interrupt_required.checkpoint_id || "",
          interrupt_id: event.interrupt_required.interrupt_id || "",
        },
        interrupt: {
          kind: event.interrupt_required.kind || "",
          info_type: event.interrupt_required.info_type || "",
          data: { user_answer: answer },
        },
      });
      state.resolvedPendingInputs.set(eventID, { kind: "interrupt" });
      state.running = true;
      state.stopRequested = false;
      state.forceTimelineScroll = true;
      startStream();
      startPolling(true);
      setStatus("");
      render(app);
    } catch (error) {
      state.resolvedPendingInputs.delete(eventID);
      setStatus(error.message, true);
      render(app);
    } finally {
      if (submitButton) submitButton.disabled = false;
    }
  });
}

function planInputContent(form) {
  const selected = [...form.querySelectorAll("input[type='radio']:checked")].map((input) => input.value).filter(Boolean);
  const text = form.querySelector("textarea")?.value.trim() || "";
  return [...selected, text].filter(Boolean).join("\n\n").trim();
}

async function stopRunning() {
  const session = currentSession(state);
  if (!session) return;
  try {
    await api.stopRunning(normalizeID(session.session_id));
    state.running = false;
    state.stopRequested = true;
    await loadTimeline();
    startPolling(true);
    render(app);
  } catch (error) {
    setStatus(error.message, true);
  }
}

function startPolling(immediate = false) {
  stopPolling();
  if (immediate) pollTimeline();
  state.pollTimer = window.setInterval(pollTimeline, 3000);
}

function stopPolling() {
  if (state.pollTimer) window.clearInterval(state.pollTimer);
  state.pollTimer = null;
  for (const controller of pollInFlight.values()) {
    controller.abort();
  }
  pollInFlight.clear();
}

function startStream() {
  if (!state.selectedSessionID) return;
  stream.open(state.selectedSessionID);
}

function stopStream() {
  stream.close();
}

function enqueueStreamEvent(event) {
  if (!event) return;
  streamEventQueue.push(event);
  scheduleStreamFlush();
}

function scheduleStreamFlush() {
  if (streamFlushScheduled) return;
  streamFlushScheduled = true;
  const schedule = window.requestAnimationFrame || ((fn) => window.setTimeout(fn, 16));
  schedule(() => {
    flushStreamEvents().catch((error) => setStatus(error.message, true));
  });
}

async function flushStreamEvents() {
  streamFlushScheduled = false;
  if (streamFlushing) {
    scheduleStreamFlush();
    return;
  }
  streamFlushing = true;
  const events = streamEventQueue.splice(0, streamEventQueue.length);
  try {
    if (!events.length) return;
    const firstThreadID = normalizeID(events.find((event) => event?.thread_id)?.thread_id);
    if (!state.selectedThreadID && firstThreadID) {
      state.selectedThreadID = firstThreadID;
    }
    const changed = ingestTimelineEvents(events);
    if (changed) render(app);
    if (events.some(shouldRefreshSessionView)) {
      await refreshCurrentSessionView();
      render(app);
    }
  } finally {
    streamFlushing = false;
    if (streamEventQueue.length) scheduleStreamFlush();
  }
}

function ingestTimelineEvents(events, pageInfo = null, options = {}) {
  const changed = mergeEvents(state, events || []);
  if (options.replaceCursor) {
    state.nextCursor = pageInfo?.next_cursor || "";
  } else if (pageInfo?.next_cursor) {
    state.nextCursor = pageInfo.next_cursor;
  }
  if (events?.length) {
    clearOptimisticStopOnRuntimeEvents(events);
    updateStatusFromRuntimeEvents(events);
  }
  return changed;
}

function clearOptimisticStopOnRuntimeEvents(events) {
  for (const event of events || []) {
    switch (event.event_type) {
      case EventType.TURN_FINISHED:
      case EventType.TURN_INTERRUPTED:
      case EventType.ERROR:
      case EventType.APPROVAL_REQUIRED:
      case EventType.INTERRUPT_REQUIRED:
      case EventType.PLAN_INPUT_REQUIRED:
        state.stopRequested = false;
        return;
      default:
        break;
    }
  }
}

function shouldRefreshSessionView(event) {
  switch (event.event_type) {
    case EventType.TURN_STARTED:
    case EventType.TURN_FINISHED:
    case EventType.TURN_INTERRUPTED:
    case EventType.ERROR:
    case EventType.APPROVAL_REQUIRED:
    case EventType.INTERRUPT_REQUIRED:
    case EventType.PLAN_INPUT_REQUIRED:
      return true;
    case EventType.TOOL_CALL_FINISHED:
      return ["close_task", "spawn_task"].includes((event.tool_call?.tool_name || "").toLowerCase());
    default:
      return false;
  }
}

function updateStatusFromRuntimeEvents(events) {
  for (const event of events || []) {
    switch (event.event_type) {
      case EventType.CONTEXT_COMPACTED:
        setStatus("");
        return;
      case EventType.ERROR:
        setStatus(event.error?.message || "Runtime error", true);
        return;
      default:
        break;
    }
  }
}

function resizeComposer() {
  el.messageInput.style.height = "auto";
  el.messageInput.style.height = `${Math.min(180, el.messageInput.scrollHeight)}px`;
}

function setStatus(text, error = false) {
  el.statusLine.textContent = text || "";
  el.statusLine.className = error ? "status-line error" : "status-line";
}

function toggleNav(open) {
  document.body.classList.toggle("nav-open", open);
}

function byID(id) {
  return document.getElementById(id);
}

function loadStreamQueue(sessionID) {
  try {
    return window.sessionStorage.getItem(`aic_agent_sdk.stream_queue.${sessionID}`) || "";
  } catch (error) {
    return "";
  }
}

function saveStreamQueue(sessionID, queueID) {
  try {
    if (queueID) window.sessionStorage.setItem(`aic_agent_sdk.stream_queue.${sessionID}`, queueID);
  } catch (error) {
    // sessionStorage may be unavailable in private modes.
  }
}

function clearStreamQueue(sessionID) {
  try {
    window.sessionStorage.removeItem(`aic_agent_sdk.stream_queue.${sessionID}`);
  } catch (error) {
    // sessionStorage may be unavailable in private modes.
  }
}
