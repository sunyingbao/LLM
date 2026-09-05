import { toActivities } from "./activity.js";
import { projectExecution } from "./execution.js";

export function initialState() {
  return {
    catalog: {
      projects: [],
      tasksByProject: new Map(),
      selectedProject: "",
      selectedTaskID: "",
      query: "",
      expandedProjects: new Set(),
    },
    task: {
      view: null,
      selectedThreadID: "",
      events: [],
      eventIDs: new Set(),
      pendingUserMessages: new Map(),
      pendingReplies: new Map(),
      requestedRunStates: new Map(),
      activities: [],
      pending: null,
      runState: "idle",
      contextUsage: null,
      submitError: "",
      draft: "",
      scrollRequest: null,
      planMode: false,
    },
    inspector: {
      tab: "changes",
      collapsed: false,
      changesLoading: false,
      changes: [],
      selectedChangePath: "",
      selectedPath: "",
      diff: null,
      diffLoading: false,
      fileTree: new Map(),
      filePreview: null,
      annotations: [],
      commentTarget: null,
    },
    transport: {
      queueID: "",
      cursor: "",
      connected: false,
    },
    ui: {
      sidebarOpen: false,
      error: "",
    },
  };
}

export function reduce(state = initialState(), action = {}) {
  switch (action.type) {
    case "catalog/projectsLoaded":
      return {
        ...state,
        catalog: { ...state.catalog, projects: action.projects || [] },
      };
    case "catalog/projectSelected":
      if (state.catalog.selectedProject === action.projectName) return state;
      return {
        ...state,
        catalog: { ...state.catalog, selectedProject: action.projectName, selectedTaskID: "" },
        task: emptyTask(),
        inspector: emptyFiles(state.inspector),
      };
    case "catalog/tasksLoaded":
      return withProjectTasks(state, action.projectName, action.tasks || []);
    case "catalog/queryChanged":
      if (state.catalog.query === action.query) return state;
      return { ...state, catalog: { ...state.catalog, query: action.query } };
    case "catalog/projectExpanded":
      return withExpandedProject(state, action.projectName, action.expanded);
    case "catalog/projectClosed":
      return withoutProject(state, action.projectName);
    case "task/new":
      return {
        ...state,
        catalog: { ...state.catalog, selectedTaskID: "" },
        task: emptyTask(),
        inspector: emptyFiles(state.inspector),
      };
    case "task/selected":
      if (state.catalog.selectedTaskID === String(action.taskID) && state.task.view) return state;
      return {
        ...state,
        catalog: { ...state.catalog, selectedTaskID: String(action.taskID) },
        task: emptyTask(),
        inspector: emptyFiles(state.inspector),
      };
    case "task/loaded":
      if (state.catalog.selectedTaskID !== String(action.taskID)) return state;
      return withExecution(state, {
        view: action.view,
        selectedThreadID: mainThreadID(action.view),
      });
    case "task/created":
      return addCreatedTask(state, action.projectName, action.view);
    case "task/threadSelected":
      if (state.task.selectedThreadID === String(action.threadID)) return state;
      return withExecution(state, { selectedThreadID: String(action.threadID) });
    case "task/renamed":
      return renameTask(state, action.taskID, action.title);
    case "task/archived":
      return archiveTask(state, action.taskID, action.projectName);
    case "task/restored":
      return restoreTask(state, action.task, action.projectName);
    case "ui/undoOffered":
      return { ...state, ui: { ...state.ui, undo: action.undo } };
    case "ui/undoCleared":
      if (!state.ui.undo) return state;
      return { ...state, ui: { ...state.ui, undo: null } };
    case "ui/error":
      return { ...state, ui: { ...state.ui, error: action.error || "" } };
    case "conversation/draftChanged":
      if (state.task.draft === action.draft) return state;
      return withTask(state, { draft: action.draft });
    case "conversation/submitFailed":
      return withTask(state, { submitError: action.error || "", draft: action.draft ?? state.task.draft });
    case "conversation/userOptimistic":
      return addOptimisticUser(state, action.message, action.content);
    case "conversation/submitted":
      return requestRunState(withTask(state, { submitError: "", draft: "", planMode: false }), "running");
    case "plan/toggled":
      return withTask(state, { planMode: !state.task.planMode });
    case "pending/submitting":
      return recordPendingReply(state, action.eventID, { submitting: true, error: "" });
    case "pending/failed":
      return recordPendingReply(state, action.eventID, { submitting: false, error: action.error || "" });
    case "pending/completed":
      return recordPendingReply(state, action.eventID, { completed: true });
    case "stop/failed":
      return requestRunState(state, action.previousState || "running");
    case "transport/statusChanged":
      return {
        ...state,
        transport: { ...state.transport, connected: action.status === "connected", status: action.status },
      };
    case "transport/cursorChanged":
      return { ...state, transport: { ...state.transport, cursor: action.cursor || "" } };
    case "inspector/tabSelected":
      if (state.inspector.tab === action.tab) return state;
      return { ...state, inspector: { ...state.inspector, tab: action.tab } };
    case "inspector/collapsedChanged":
      if (state.inspector.collapsed === action.collapsed) return state;
      return { ...state, inspector: { ...state.inspector, collapsed: action.collapsed } };
    case "inspector/directoryLoading":
      if (state.catalog.selectedTaskID !== String(action.taskID)) return state;
      return withDirectory(state, action.path, { loading: true, loaded: false, expanded: true, files: [] });
    case "inspector/directoryLoaded":
      if (state.catalog.selectedTaskID !== String(action.taskID)) return state;
      return withDirectory(state, action.path, { loading: false, loaded: true, expanded: true, files: action.files || [] });
    case "inspector/directoryToggled":
      return toggleDirectory(state, action.path);
    case "inspector/changesLoading":
      if (state.catalog.selectedTaskID !== String(action.taskID)) return state;
      return { ...state, inspector: { ...state.inspector, selectedChangePath: "", changesLoading: true, diff: null, diffLoading: false } };
    case "inspector/changesLoaded":
      if (state.catalog.selectedTaskID !== String(action.taskID)) return state;
      return {
        ...state,
        inspector: {
          ...state.inspector,
          changesLoading: false,
          changes: (action.changes || []).map((change) => ({
            path: String(change.path || ""),
            status: String(change.status || ""),
            additions: Number.isFinite(Number(change.additions)) ? Number(change.additions) : 0,
            deletions: Number.isFinite(Number(change.deletions)) ? Number(change.deletions) : 0,
          })),
          selectedChangePath: "",
          diff: null,
          diffLoading: false,
        },
      };
    case "inspector/changesFailed":
      if (state.catalog.selectedTaskID !== String(action.taskID)) return state;
      return { ...state, inspector: { ...state.inspector, changesLoading: false } };
    case "inspector/changeSelected":
      return {
        ...state,
        inspector: {
          ...state.inspector,
          selectedChangePath: String(action.path || ""),
          selectedPath: "",
          diffLoading: true,
          diff: { path: String(action.path || ""), patch: "", truncated: false },
        },
      };
    case "inspector/diffLoaded":
      if (state.catalog.selectedTaskID !== String(action.taskID) || state.inspector.selectedChangePath !== action.path) return state;
      return {
        ...state,
        inspector: {
          ...state.inspector,
          diffLoading: false,
          diff: {
            path: String(action.path || ""),
            patch: String(action.patch || ""),
            truncated: Boolean(action.truncated),
          },
        },
      };
    case "inspector/diffFailed":
      if (state.catalog.selectedTaskID !== String(action.taskID) || state.inspector.selectedChangePath !== action.path) return state;
      return { ...state, inspector: { ...state.inspector, diffLoading: false } };
    case "inspector/commentStarted":
      return {
        ...state,
        inspector: {
          ...state.inspector,
          commentTarget: { path: String(action.path || ""), line: Number(action.line) },
        },
      };
    case "inspector/commentCancelled":
      if (!state.inspector.commentTarget) return state;
      return { ...state, inspector: { ...state.inspector, commentTarget: null } };
    case "inspector/annotationAdded":
      return {
        ...state,
        inspector: {
          ...state.inspector,
          annotations: [...state.inspector.annotations, action.annotation],
          commentTarget: null,
        },
      };
    case "inspector/annotationsCleared":
      if (!state.inspector.annotations.length && !state.inspector.commentTarget) return state;
      return { ...state, inspector: { ...state.inspector, annotations: [], commentTarget: null } };
    case "inspector/fileSelected":
      return {
        ...state,
        inspector: {
          ...state.inspector,
          selectedPath: action.file?.path || "",
          selectedChangePath: "",
          filePreview: action.file || null,
          diff: null,
          diffLoading: false,
        },
      };
    case "inspector/fileContentLoaded":
      if (state.catalog.selectedTaskID !== String(action.taskID) || state.inspector.selectedPath !== action.path) return state;
      return { ...state, inspector: { ...state.inspector, filePreview: { ...state.inspector.filePreview, content: action.content } } };
    case "inspector/changesCleared":
    case "inspector/filesCleared":
      return { ...state, inspector: emptyFiles(state.inspector) };
    case "ui/sidebarChanged":
      return { ...state, ui: { ...state.ui, sidebarOpen: action.open } };
    case "timeline/received":
      return receiveTimeline(state, action.events);
    case "stop/requested":
      if (state.task.runState === "stopping") return state;
      return requestRunState(state, "stopping");
    default:
      return state;
  }
}

function emptyTask() {
  return initialState().task;
}

function withProjectTasks(state, projectName, tasks) {
  const tasksByProject = new Map(state.catalog.tasksByProject);
  tasksByProject.set(projectName, tasks);
  return { ...state, catalog: { ...state.catalog, tasksByProject } };
}

function withDirectory(state, path, branch) {
  const fileTree = new Map(state.inspector.fileTree);
  fileTree.set(path, { path, ...branch });
  return { ...state, inspector: { ...state.inspector, fileTree } };
}

function toggleDirectory(state, path) {
  const current = state.inspector.fileTree.get(path);
  if (!current) return state;
  const fileTree = new Map(state.inspector.fileTree);
  fileTree.set(path, { ...current, expanded: !current.expanded });
  return { ...state, inspector: { ...state.inspector, fileTree } };
}

function emptyFiles(inspector) {
  return {
    ...inspector,
    changes: [],
    selectedChangePath: "",
    selectedPath: "",
    changesLoading: false,
    diff: null,
    diffLoading: false,
    fileTree: new Map(),
    filePreview: null,
    annotations: [],
    commentTarget: null,
  };
}

function addCreatedTask(state, projectName, view) {
  const task = view?.session;
  if (!task) return state;
  const id = String(task.session_id);
  const tasks = state.catalog.tasksByProject.get(projectName) || [];
  const tasksByProject = new Map(state.catalog.tasksByProject);
  tasksByProject.set(projectName, [task, ...tasks.filter((item) => String(item.session_id) !== id)]);
  const next = {
    ...state,
    catalog: { ...state.catalog, selectedProject: projectName, selectedTaskID: id, tasksByProject },
    task: {
      ...emptyTask(),
      view,
      selectedThreadID: mainThreadID(view),
    },
    inspector: emptyFiles(state.inspector),
  };
  return withExecution(next, {});
}

function withExpandedProject(state, projectName, expanded) {
  const expandedProjects = new Set(state.catalog.expandedProjects);
  if (expanded) expandedProjects.add(projectName);
  else expandedProjects.delete(projectName);
  return { ...state, catalog: { ...state.catalog, expandedProjects } };
}

function withoutProject(state, projectName) {
  const projects = state.catalog.projects.filter((project) => project.project_name !== projectName);
  const tasksByProject = new Map(state.catalog.tasksByProject);
  tasksByProject.delete(projectName);
  const selected = state.catalog.selectedProject === projectName;
  return {
    ...state,
    catalog: {
      ...state.catalog,
      projects,
      tasksByProject,
      selectedProject: selected ? projects[0]?.project_name || "" : state.catalog.selectedProject,
      selectedTaskID: selected ? "" : state.catalog.selectedTaskID,
    },
    task: selected ? emptyTask() : state.task,
  };
}

function renameTask(state, taskID, title) {
  const id = String(taskID);
  const tasksByProject = new Map();
  for (const [project, tasks] of state.catalog.tasksByProject) {
    tasksByProject.set(project, tasks.map((task) => String(task.session_id) === id ? { ...task, title } : task));
  }
  const view = state.task.view && String(state.task.view.session?.session_id) === id
    ? { ...state.task.view, session: { ...state.task.view.session, title } }
    : state.task.view;
  return {
    ...state,
    catalog: { ...state.catalog, tasksByProject },
    task: { ...state.task, view },
  };
}

function archiveTask(state, taskID, projectName) {
  const id = String(taskID);
  const tasks = state.catalog.tasksByProject.get(projectName) || [];
  const tasksByProject = new Map(state.catalog.tasksByProject);
  tasksByProject.set(projectName, tasks.filter((task) => String(task.session_id) !== id));
  const selected = state.catalog.selectedTaskID === id;
  return {
    ...state,
    catalog: { ...state.catalog, tasksByProject, selectedTaskID: selected ? "" : state.catalog.selectedTaskID },
    task: selected ? emptyTask() : state.task,
  };
}

function restoreTask(state, task, projectName) {
  if (!task) return state;
  const tasks = state.catalog.tasksByProject.get(projectName) || [];
  const id = String(task.session_id);
  if (tasks.some((item) => String(item.session_id) === id)) return state;
  const tasksByProject = new Map(state.catalog.tasksByProject);
  tasksByProject.set(projectName, [task, ...tasks]);
  return { ...state, catalog: { ...state.catalog, tasksByProject } };
}

function mainThreadID(view) {
  const threads = view?.threads || [];
  const thread = threads.find((item) => Number(item.role || 0) === 1) || threads[0];
  return thread?.thread_id === undefined || thread?.thread_id === null ? "" : String(thread.thread_id);
}

function receiveTimeline(state, incoming) {
  let events = [...state.task.events];
  let arrival = events.reduce((next, event) => Math.max(next, Number(event.__arrival || 0) + 1), 0);
  const eventIDs = new Set(state.task.eventIDs);
  const pendingUserMessages = new Map(state.task.pendingUserMessages);
  const requestedRunStates = new Map(state.task.requestedRunStates);
  let changed = false;
  for (const event of incoming || []) {
    for (const messageID of eventMessageIDs(event)) {
      const localID = pendingUserMessages.get(messageID);
      if (!localID) continue;
      pendingUserMessages.delete(messageID);
      eventIDs.delete(localID);
      events = events.filter((item) => item.__stable_id !== localID);
      changed = true;
    }
    const id = stableEventID(event, arrival);
    if (eventIDs.has(id)) continue;
    eventIDs.add(id);
    if (["TURN_STARTED", "TURN_FINISHED", "TURN_INTERRUPTED", "ERROR", "APPROVAL_REQUIRED", "PLAN_INPUT_REQUIRED", "INTERRUPT_REQUIRED"].includes(event.event_type)) {
      const threadID = String(event.thread_id || "");
      for (const key of [threadID, ""]) {
        const request = requestedRunStates.get(key);
        if (request && (!request.threadID || request.threadID === threadID) &&
            (event.event_type === "TURN_STARTED" || !request.turnID || request.turnID === String(event.turn_id || ""))) {
          requestedRunStates.delete(key);
        }
      }
    }
    events.push({ ...event, __stable_id: id, __arrival: arrival++ });
    changed = true;
  }
  if (!changed) return state;
  events.sort(compareEvents);
  return withExecution(state, {
    events,
    eventIDs,
    pendingUserMessages,
    activities: toActivities(events),
    requestedRunStates,
  });
}

function addOptimisticUser(state, message, content) {
  const messageID = message?.message_id === undefined || message?.message_id === null ? "" : String(message.message_id);
  if (!messageID || !content) return state;
  const id = `local_user:${messageID}`;
  if (state.task.eventIDs.has(id)) return state;
  const event = {
    __stable_id: id,
    __arrival: state.task.events.length,
    __local: true,
    event_id: id,
    event_type: "TURN_STARTED",
    session_id: state.catalog.selectedTaskID,
    thread_id: String(message.thread_id || state.task.selectedThreadID || ""),
    created_at_ms: Date.now(),
    message: { ...message, parts: [{ type: "text", text: content }] },
  };
  const events = [...state.task.events, event];
  const eventIDs = new Set(state.task.eventIDs).add(id);
  const pendingUserMessages = new Map(state.task.pendingUserMessages).set(messageID, id);
  return withTask(state, {
    events,
    eventIDs,
    pendingUserMessages,
    activities: toActivities(events),
  });
}

function eventMessageIDs(event) {
  const ids = [];
  if (event?.message?.message_id !== undefined && event?.message?.message_id !== null) {
    ids.push(String(event.message.message_id));
  }
  for (const id of event?.consumed_message_ids || []) ids.push(String(id));
  return ids;
}

function withExecution(state, changes) {
  const task = { ...state.task, ...changes };
  return { ...state, task: { ...task, ...projectExecution(task) } };
}

function recordPendingReply(state, eventID, reply) {
  if (!state.task.events.some((event) => event.__stable_id === eventID)) return state;
  const pendingReplies = new Map(state.task.pendingReplies).set(eventID, reply);
  return withExecution(state, { pendingReplies });
}

function requestRunState(state, status) {
  const selected = state.task.selectedThreadID;
  const start = state.task.events.findLast((event) => !event.__local && event.event_type === "TURN_STARTED" && (!selected || String(event.thread_id) === selected));
  const requestedRunStates = new Map(state.task.requestedRunStates).set(selected, {
    state: status, threadID: selected || String(start?.thread_id || ""), turnID: String(start?.turn_id || ""),
  });
  return withExecution(state, { requestedRunStates });
}

function stableEventID(event, arrival) {
  const id = event?.event_id === undefined || event?.event_id === null ? "" : String(event.event_id);
  if (id && id !== "0") return id;
  if (["ASSISTANT_DELTA", "TOOL_CALL_OUTPUT_DELTA"].includes(event?.event_type)) return `live:${arrival}`;
  return `${event?.thread_id || ""}:${event?.turn_id || ""}:${event?.created_at_ms || ""}:${event?.event_type || ""}`;
}

function compareEvents(left, right) {
  const time = Number(left.created_at_ms || 0) - Number(right.created_at_ms || 0);
  if (time !== 0) return time;
  return Number(left.__arrival || 0) - Number(right.__arrival || 0);
}

function withTask(state, task) {
  return { ...state, task: { ...state.task, ...task } };
}
