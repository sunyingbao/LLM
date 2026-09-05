import { createInspector } from "./inspector.js";
import { createSidebar } from "./sidebar.js";
import { createTaskWorkspace } from "./task_workspace.js";

export function mountApp(root, { store, actions }) {
  if (!root) throw new Error("app root is missing");
  const sidebar = createSidebar(root.querySelector("[data-sidebar]"), actions);
  const workspace = createTaskWorkspace(root.querySelector("[data-workspace]"), actions);
  const inspector = createInspector(root.querySelector("[data-inspector]"), actions);
  let unsubscribe = null;

  function closeDrawer(event) {
    if (event.key !== "Escape") return;
    const state = store.getState();
    if (!state.inspector.collapsed) {
      actions.toggleInspector?.(true);
      return;
    }
    if (state.ui.sidebarOpen) actions.toggleSidebar?.(false);
  }

  function render(state) {
    sidebar.render(state);
    workspace.render(state);
    inspector.render(state);
  }

  return {
    start() {
      if (unsubscribe) return;
      unsubscribe = store.subscribe(render);
      root.ownerDocument.addEventListener("keydown", closeDrawer);
      render(store.getState());
    },
    stop() {
      unsubscribe?.();
      unsubscribe = null;
      root.ownerDocument.removeEventListener("keydown", closeDrawer);
    },
  };
}
