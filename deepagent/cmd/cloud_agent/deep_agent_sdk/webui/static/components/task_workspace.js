import { agentSummaries } from "../features/tools.js";
import { createActivityTimeline } from "./activity_timeline.js";
import { createComposer } from "./composer.js";

export function createTaskWorkspace(root, actions) {
  if (!root) throw new Error("workspace root is missing");
  root.innerHTML = `
    <header class="task-header">
      <button class="icon-button mobile-nav-button sidebar-nav-button" type="button" data-open-sidebar aria-label="Open projects and tasks">☰</button>
      <div class="task-heading">
        <h1 data-task-title>New task</h1>
        <p data-task-meta>Choose a project and describe what you want to build.</p>
      </div>
      <span class="run-status" data-run-status>Idle</span>
      <button class="icon-button mobile-nav-button inspector-nav-button" type="button" data-open-inspector aria-label="Open task inspector">▱</button>
      <div class="task-menu-wrap">
        <button class="quiet-button" type="button" data-task-menu aria-label="Task actions" aria-haspopup="menu" aria-expanded="false">•••</button>
        <div class="task-menu-popover" data-task-menu-popover role="menu" hidden>
          <label>
            <span>Task title</span>
            <input type="text" data-header-rename-input />
          </label>
          <button type="button" data-header-rename-save>Rename</button>
          <button type="button" data-header-archive>Archive</button>
        </div>
      </div>
    </header>
    <section class="activity-scroll" data-activity-scroll>
      <div class="empty-task" data-empty-task>
        <div class="empty-task-mark">D</div>
        <h2>What should DeepAgent work on?</h2>
        <p>Ask it to inspect code, implement a change, or explain the repository.</p>
      </div>
      <div class="activity-timeline" data-activity-timeline></div>
    </section>
    <footer class="composer-region">
      <div class="agent-strip" data-agent-strip aria-label="Agents"></div>
      <div class="pending-slot" data-pending-slot></div>
      <div class="composer-shell">
        <textarea rows="1" data-composer placeholder="Message DeepAgent"></textarea>
        <div class="composer-actions">
          <div class="composer-options">
            <button class="composer-option" type="button" data-plan-mode>Plan</button>
            <button class="composer-option" type="button" data-compact>Compact</button>
          </div>
          <button class="send-button" type="button" data-send aria-label="Send message">↑</button>
        </div>
      </div>
      <div class="composer-status" data-composer-status></div>
    </footer>
  `;
  let state = null;
  root.addEventListener("click", (event) => {
    if (event.target.closest("[data-open-sidebar]")) actions.toggleSidebar?.(true);
    if (event.target.closest("[data-open-inspector]")) actions.toggleInspector?.(false);
    if (event.target.closest("[data-task-menu]")) toggleTaskMenu(root);
    if (event.target.closest("[data-header-rename-save]")) {
      const taskID = state?.catalog.selectedTaskID;
      const title = root.querySelector("[data-header-rename-input]").value;
      if (taskID) run(actions.renameTask?.(taskID, title));
      closeTaskMenu(root);
    }
    if (event.target.closest("[data-header-archive]")) {
      const taskID = state?.catalog.selectedTaskID;
      if (taskID) run(actions.archiveTask?.(taskID));
      closeTaskMenu(root);
    }
    const agent = event.target.closest("[data-agent-id]");
    if (agent) actions.selectAgent?.(agent.dataset.agentId);
  });
  root.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || root.querySelector("[data-task-menu-popover]").hidden) return;
    event.preventDefault();
    event.stopPropagation();
    closeTaskMenu(root);
    root.querySelector("[data-task-menu]").focus();
  });
  const timeline = createActivityTimeline(root.querySelector("[data-activity-timeline]"), actions);
  const composer = createComposer(root.querySelector(".composer-region"), actions);

  return {
    render(nextState) {
      state = nextState;
      const session = state.task.view?.session;
      root.querySelector("[data-task-title]").textContent = session?.title || "New task";
      root.querySelector("[data-task-meta]").textContent = session
        ? state.catalog.selectedProject || "Project"
        : "Choose a project and describe what you want to build.";
      const menuButton = root.querySelector("[data-task-menu]");
      menuButton.hidden = !session;
      const titleInput = root.querySelector("[data-header-rename-input]");
      if (root.querySelector("[data-task-menu-popover]").hidden) titleInput.value = session?.title || "";
      root.querySelector("[data-empty-task]").hidden = Boolean(session);
      renderAgents(root.querySelector("[data-agent-strip]"), state);
      const runState = state.task.runState;
      root.querySelector("[data-run-status]").textContent = statusLabel(runState);
      root.querySelector("[data-run-status]").dataset.state = runState;
      timeline.render(state);
      composer.render(state);
    },
  };
}

function toggleTaskMenu(root) {
  const menu = root.querySelector("[data-task-menu-popover]");
  const button = root.querySelector("[data-task-menu]");
  menu.hidden = !menu.hidden;
  button.setAttribute("aria-expanded", String(!menu.hidden));
  if (!menu.hidden) root.querySelector("[data-header-rename-input]").focus();
}

function closeTaskMenu(root) {
  root.querySelector("[data-task-menu-popover]").hidden = true;
  root.querySelector("[data-task-menu]").setAttribute("aria-expanded", "false");
}

function renderAgents(root, state) {
  const agents = agentSummaries(state.task.view);
  root.replaceChildren(...agents.map((agent) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "agent-button";
    button.dataset.agentId = agent.id;
    button.dataset.status = agent.status;
    button.dataset.selected = String(agent.id === state.task.selectedThreadID);
    button.textContent = agent.title;
    button.setAttribute("aria-pressed", String(agent.id === state.task.selectedThreadID));
    return button;
  }));
}

function statusLabel(state) {
  return state.split("_").map((part) => `${part.slice(0, 1).toUpperCase()}${part.slice(1)}`).join(" ");
}

function run(promise) {
  promise?.catch(() => {});
}
