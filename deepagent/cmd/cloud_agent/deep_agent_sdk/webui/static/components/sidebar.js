import { filterTasks } from "../state/selectors.js";

export function createSidebar(root, actions) {
  if (!root) throw new Error("sidebar root is missing");
  root.innerHTML = `
    <div class="sidebar-header">
      <div class="product-mark" aria-label="DeepAgent home">D</div>
      <button class="icon-button sidebar-nav-button" type="button" data-collapse-sidebar aria-label="Close projects and tasks">×</button>
    </div>
    <button class="new-task-button" type="button" data-new-task>
      <span aria-hidden="true">＋</span><span>New task</span>
    </button>
    <label class="task-search">
      <span class="sr-only">Search tasks</span>
      <span aria-hidden="true">⌕</span>
      <input type="search" data-task-search placeholder="Search tasks" autocomplete="off" />
    </label>
    <nav class="project-list" data-project-list aria-label="Projects"></nav>
    <div class="sidebar-footer" data-sidebar-footer></div>
  `;
  let state = null;
  root.addEventListener("click", (event) => {
    if (event.target.closest("[data-new-task]")) actions.newTask?.();
    if (event.target.closest("[data-collapse-sidebar]")) actions.toggleSidebar?.();
    const projectToggle = event.target.closest("[data-project-toggle]");
    if (projectToggle) actions.toggleProject?.(projectToggle.dataset.projectToggle);
    const projectSelect = event.target.closest("[data-project-select]");
    if (projectSelect) actions.selectProject?.(projectSelect.dataset.projectSelect);
    const projectClose = event.target.closest("[data-project-close]");
    if (projectClose) actions.closeProject?.(projectClose.dataset.projectClose);
    const task = event.target.closest("[data-task-id]");
    if (task && !event.target.closest("[data-task-action]")) actions.selectTask?.(task.dataset.taskId);
    const rename = event.target.closest("[data-task-rename]");
    if (rename) beginRename(rename.closest("[data-task-id]"), rename.dataset.taskRename, rename.dataset.taskTitle);
    const archive = event.target.closest("[data-task-archive]");
    if (archive) actions.archiveTask?.(archive.dataset.taskArchive);
    const restore = event.target.closest("[data-task-restore]");
    if (restore) actions.restoreTask?.(restore.dataset.taskRestore, restore.dataset.projectName);
  });
  root.querySelector("[data-task-search]").addEventListener("input", (event) => {
    actions.searchTasks?.(event.target.value);
  });
  root.addEventListener("keydown", (event) => {
    if (event.target.closest?.("[data-task-rename-input]")) return;
    const task = event.target.closest?.("[data-task-id]");
    if (task && (event.key === "Enter" || event.key === " ")) {
      event.preventDefault();
      actions.selectTask?.(task.dataset.taskId);
      return;
    }
    if (event.key === "Escape") actions.toggleSidebar?.(false);
  });

  function render(nextState) {
    state = nextState;
    root.dataset.open = String(Boolean(state.ui.sidebarOpen));
    const list = root.querySelector("[data-project-list]");
    list.replaceChildren(...state.catalog.projects.map((project) => projectNode(project, state)));
    renderFooter(root.querySelector("[data-sidebar-footer]"), state.ui);
  }

  function beginRename(row, taskID, currentTitle) {
    const content = row?.querySelector(".task-row-content");
    if (!content) return;
    const input = document.createElement("input");
    input.type = "text";
    input.className = "task-rename-input";
    input.dataset.taskRenameInput = "true";
    input.value = currentTitle || "";
    input.setAttribute("aria-label", "Task title");
    content.replaceChildren(input);
    input.focus();
    input.select();
    let submitted = false;
    input.addEventListener("keydown", (event) => {
      event.stopPropagation();
      if (event.key === "Escape") {
        event.preventDefault();
        render(state);
        return;
      }
      if (event.key !== "Enter") return;
      event.preventDefault();
      submitted = true;
      Promise.resolve(actions.renameTask?.(taskID, input.value)).catch(() => render(state));
    });
    input.addEventListener("blur", () => {
      if (!submitted) render(state);
    }, { once: true });
  }

  return { render };
}

function projectNode(project, state) {
  const name = String(project.project_name || "");
  const section = document.createElement("section");
  section.className = "project-group";
  const expanded = state.catalog.expandedProjects.has(name);
  section.append(projectHeader(name, expanded, state.catalog.selectedProject === name));
  if (!expanded) return section;
  const tasks = filterTasks(state.catalog.tasksByProject.get(name) || [], state.catalog.query);
  const list = document.createElement("div");
  list.className = "task-list";
  if (!tasks.length) list.append(emptyTasks());
  else list.append(...tasks.map((task) => taskNode(task, state.catalog.selectedTaskID)));
  section.append(list);
  return section;
}

function projectHeader(name, expanded, selected) {
  const row = document.createElement("div");
  row.className = `project-row${selected ? " selected" : ""}`;
  const toggle = button(expanded ? "⌄" : "›", "Toggle project");
  toggle.dataset.projectToggle = name;
  toggle.className = "project-toggle";
  const select = button(name || "Project", `Open ${name}`);
  select.dataset.projectSelect = name;
  select.className = "project-name";
  const close = button("×", `Close ${name}`);
  close.dataset.projectClose = name;
  close.className = "project-close";
  row.append(toggle, select, close);
  return row;
}

function taskNode(task, selectedTaskID) {
  const id = String(task.session_id);
  const row = document.createElement("div");
  row.className = `task-row${id === String(selectedTaskID) ? " selected" : ""}`;
  row.dataset.taskId = id;
  row.tabIndex = 0;
  const content = document.createElement("div");
  content.className = "task-row-content";
  const title = document.createElement("span");
  title.className = "task-title";
  title.textContent = task.title || "Untitled task";
  const preview = document.createElement("span");
  preview.className = "task-preview";
  preview.textContent = task.preview || task.summary || "";
  content.append(title, preview);
  const actions = document.createElement("div");
  actions.className = "task-row-actions";
  const rename = button("✎", "Rename task");
  rename.dataset.taskAction = "rename";
  rename.dataset.taskRename = id;
  rename.dataset.taskTitle = task.title || "";
  const archive = button("−", "Archive task");
  archive.dataset.taskAction = "archive";
  archive.dataset.taskArchive = id;
  actions.append(rename, archive);
  row.append(content, actions);
  return row;
}

function emptyTasks() {
  const empty = document.createElement("p");
  empty.className = "task-list-empty";
  empty.textContent = "No tasks";
  return empty;
}

function renderFooter(root, ui) {
  root.replaceChildren();
  if (!ui.undo) return;
  const label = document.createElement("span");
  label.textContent = ui.undo.label;
  const restore = button("Undo", "Restore archived task");
  restore.dataset.taskRestore = ui.undo.taskID;
  restore.dataset.projectName = ui.undo.projectName;
  root.append(label, restore);
}

function button(text, label) {
  const element = document.createElement("button");
  element.type = "button";
  element.textContent = text;
  element.setAttribute("aria-label", label);
  return element;
}
