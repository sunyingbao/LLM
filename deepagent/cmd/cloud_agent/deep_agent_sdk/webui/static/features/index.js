import { createPendingFeature } from "./approvals.js";
import { createConversationFeature } from "./conversation.js";
import { createFilesFeature } from "./files.js";
import { createChangesFeature } from "./changes.js";
import { createTaskFeature } from "./tasks.js";
import { createTimelineFeature } from "./timeline.js";
import { readStorage, writeStorage } from "../state/storage.js";

export function createFeatures({ api, store, storage }) {
  const timeline = createTimelineFeature({ api, store, storage });
  const files = createFilesFeature({ api, store });
  const conversation = createConversationFeature({ api, store, timeline });
  const changes = createChangesFeature({ api, store, submitMessage: conversation.submitMessage });
  const tasks = createTaskFeature({
    api,
    store,
    storage,
    onTaskLoaded: (view) => Promise.all([timeline.openTask(view), files.openTask(view), changes.openTask(view)]),
    onTaskCleared: () => {
      timeline.close();
      files.clear();
      changes.clear();
    },
  });
  const pending = createPendingFeature({ api, store, timeline });
  const layout = layoutActions(store, storage);
  return {
    actions: { ...tasks, ...conversation, ...pending, ...files, ...changes, ...layout },
    async start() {
      layout.restore();
      await tasks.loadCatalog();
    },
    stop() {
      timeline.close();
      files.clear();
      changes.clear();
    },
  };
}

function layoutActions(store, storage = globalThis.localStorage) {
  function selectInspectorTab(tab) {
    store.dispatch({ type: "inspector/tabSelected", tab });
    if (store.getState().inspector.collapsed) toggleInspector(false);
  }

  function toggleInspector(collapsed) {
    const next = typeof collapsed === "boolean" ? collapsed : !store.getState().inspector.collapsed;
    store.dispatch({ type: "inspector/collapsedChanged", collapsed: next });
    writeStorage(storage, "deepagent.inspectorCollapsed", String(next));
  }

  function toggleSidebar(open) {
    const next = typeof open === "boolean" ? open : !store.getState().ui.sidebarOpen;
    store.dispatch({ type: "ui/sidebarChanged", open: next });
  }

  function restore() {
    const stored = readStorage(storage, "deepagent.inspectorCollapsed") || null;
    const collapsed = defaultInspectorCollapsed(globalThis.innerWidth, stored);
    store.dispatch({ type: "inspector/collapsedChanged", collapsed });
  }

  return { selectInspectorTab, toggleInspector, toggleSidebar, restore };
}

export function defaultInspectorCollapsed(viewportWidth, stored) {
  if (stored === "true" || stored === "false") return stored === "true";
  return Number(viewportWidth) <= 1179;
}
