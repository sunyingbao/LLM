import { readStorage, writeStorage } from "../state/storage.js";

export function createTaskFeature({ api, store, storage = globalThis.localStorage, onTaskLoaded, onTaskCleared }) {
  let loadToken = 0;
  const archived = new Map();

  async function loadCatalog() {
    const payload = await api.listProjects();
    const projects = normalizeProjects(payload.projects);
    store.dispatch({ type: "catalog/projectsLoaded", projects });
    restoreExpandedProjects(projects);
    const current = store.getState().catalog.selectedProject;
    const selected = projects.some((project) => project.project_name === current)
      ? current
      : projects[0]?.project_name || "";
    if (selected) await selectProject(selected);
  }

  async function selectProject(projectName) {
    const token = ++loadToken;
    store.dispatch({ type: "catalog/projectSelected", projectName });
    store.dispatch({ type: "catalog/projectExpanded", projectName, expanded: true });
    saveExpandedProjects();
    const payload = await api.listTasks(projectName);
    if (token !== loadToken || store.getState().catalog.selectedProject !== projectName) return;
    const tasks = payload.sessions || [];
    store.dispatch({ type: "catalog/tasksLoaded", projectName, tasks });
    const selected = store.getState().catalog.selectedTaskID;
    const first = tasks.find((task) => String(task.session_id) === selected) || tasks[0];
    if (first) await selectTask(first.session_id);
  }

  function newTask() {
    loadToken += 1;
    onTaskCleared?.();
    store.dispatch({ type: "task/new" });
  }

  async function selectTask(taskID) {
    const id = String(taskID);
    const token = ++loadToken;
    store.dispatch({ type: "task/selected", taskID: id });
    try {
      const payload = await api.getTask(id);
      if (token !== loadToken || store.getState().catalog.selectedTaskID !== id) return;
      store.dispatch({ type: "task/loaded", taskID: id, view: payload.session_view });
      await onTaskLoaded?.(payload.session_view);
    } catch (error) {
      if (token === loadToken) store.dispatch({ type: "ui/error", error: error.message });
      throw error;
    }
  }

  async function renameTask(taskID, title) {
    const nextTitle = String(title || "").trim();
    if (!nextTitle) return false;
    await api.updateTask(String(taskID), { title: nextTitle });
    store.dispatch({ type: "task/renamed", taskID: String(taskID), title: nextTitle });
    return true;
  }

  async function archiveTask(taskID) {
    const id = String(taskID);
    const { projectName, task } = findTask(id);
    if (!task) return false;
    await api.updateTask(id, { status: 2 });
    archived.set(id, { projectName, task });
    store.dispatch({ type: "task/archived", taskID: id, projectName });
    if (!store.getState().catalog.selectedTaskID) onTaskCleared?.();
    store.dispatch({ type: "ui/undoOffered", undo: { kind: "archive", taskID: id, projectName, label: `Archived ${task.title || "task"}` } });
    return true;
  }

  async function restoreTask(taskID, projectName = "") {
    const id = String(taskID);
    const saved = archived.get(id);
    const targetProject = projectName || saved?.projectName;
    if (!targetProject || !saved?.task) return false;
    await api.updateTask(id, { status: 1 });
    store.dispatch({ type: "task/restored", task: saved.task, projectName: targetProject });
    store.dispatch({ type: "ui/undoCleared" });
    archived.delete(id);
    return true;
  }

  async function closeProject(projectName) {
    await api.closeProject(projectName);
    store.dispatch({ type: "catalog/projectClosed", projectName });
  }

  function searchTasks(query) {
    store.dispatch({ type: "catalog/queryChanged", query: String(query || "") });
  }

  function toggleProject(projectName) {
    const expanded = !store.getState().catalog.expandedProjects.has(projectName);
    store.dispatch({ type: "catalog/projectExpanded", projectName, expanded });
    saveExpandedProjects();
  }

  function findTask(taskID) {
    for (const [projectName, tasks] of store.getState().catalog.tasksByProject) {
      const task = tasks.find((item) => String(item.session_id) === taskID);
      if (task) return { projectName, task };
    }
    return { projectName: "", task: null };
  }

  function restoreExpandedProjects(projects) {
    const saved = readExpanded(storage);
    for (const project of projects) {
      if (saved.has(project.project_name)) {
        store.dispatch({ type: "catalog/projectExpanded", projectName: project.project_name, expanded: true });
      }
    }
  }

  function saveExpandedProjects() {
    writeStorage(storage, "deepagent.expandedProjects", JSON.stringify([...store.getState().catalog.expandedProjects]));
  }

  return {
    loadCatalog,
    newTask,
    selectProject,
    selectTask,
    renameTask,
    archiveTask,
    restoreTask,
    closeProject,
    searchTasks,
    toggleProject,
  };
}

function normalizeProjects(projects) {
  return projects?.length ? projects : [{ project_name: "default", local: true }];
}

function readExpanded(storage) {
  try {
    const value = JSON.parse(readStorage(storage, "deepagent.expandedProjects") || "[]");
    return new Set(Array.isArray(value) ? value.map(String) : []);
  } catch {
    return new Set();
  }
}
